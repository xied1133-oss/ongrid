package packetcapture

import (
	"bytes"
	"context"
	"crypto/ed25519"
	"crypto/hmac"
	"crypto/sha256"
	"crypto/tls"
	"crypto/x509"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"encoding/pem"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"strconv"
	"strings"
	"time"

	model "github.com/ongridio/ongrid/internal/manager/model/packetcapture"
	"github.com/ongridio/ongrid/internal/pkg/errs"
)

const parserTokenTTL = 5 * time.Minute

type ParserClientConfig struct {
	URL             string
	ArtifactBaseURL string
	TokenSecret     string
	PrivateKeyFile  string
	Timeout         time.Duration
	MaxPackets      uint64
	MaxBytes        uint64
	IncludeHex      bool
	CAFile          string
}

// ParserClient calls the private pcap-parser service and persists its
// Wireshark-style packet dissection unchanged. It deliberately does not
// generate summaries or reports.
type ParserClient struct {
	endpoint        string
	artifactBaseURL string
	tokenSecret     []byte
	privateKey      ed25519.PrivateKey
	httpClient      *http.Client
	maxPackets      uint64
	maxBytes        uint64
	includeHex      bool
	now             func() time.Time
}

func NewParserClient(cfg ParserClientConfig) (*ParserClient, error) {
	root := strings.TrimRight(strings.TrimSpace(cfg.URL), "/")
	if root == "" {
		return nil, errs.ErrNotWiredYet
	}
	if _, err := url.ParseRequestURI(root); err != nil {
		return nil, fmt.Errorf("%w: packet parser url", errs.ErrInvalid)
	}
	artifactBaseURL := strings.TrimRight(strings.TrimSpace(cfg.ArtifactBaseURL), "/")
	if artifactBaseURL == "" {
		return nil, fmt.Errorf("%w: packet parser artifact base url required", errs.ErrInvalid)
	}
	if _, err := url.ParseRequestURI(artifactBaseURL); err != nil {
		return nil, fmt.Errorf("%w: packet parser artifact base url", errs.ErrInvalid)
	}
	secret := strings.TrimSpace(cfg.TokenSecret)
	if secret == "" {
		return nil, fmt.Errorf("%w: packet parser token secret required", errs.ErrInvalid)
	}
	privateKey, err := loadParserPrivateKey(strings.TrimSpace(cfg.PrivateKeyFile))
	if err != nil {
		return nil, err
	}
	timeout := cfg.Timeout
	if timeout <= 0 {
		timeout = 2 * time.Minute
	}
	maxPackets := cfg.MaxPackets
	if maxPackets == 0 {
		maxPackets = 1000
	}
	maxBytes := cfg.MaxBytes
	if maxBytes == 0 {
		maxBytes = 64 << 20
	}
	httpClient, err := newParserHTTPClient(cfg, timeout)
	if err != nil {
		return nil, err
	}
	return &ParserClient{
		endpoint:        root + "/api/v1/pcap-artifacts:parse",
		artifactBaseURL: artifactBaseURL,
		tokenSecret:     []byte(secret),
		privateKey:      privateKey,
		httpClient:      httpClient,
		maxPackets:      maxPackets,
		maxBytes:        maxBytes,
		includeHex:      cfg.IncludeHex,
		now:             time.Now,
	}, nil
}

func newParserHTTPClient(cfg ParserClientConfig, timeout time.Duration) (*http.Client, error) {
	tlsConfig := &tls.Config{MinVersion: tls.VersionTLS13}
	if caFile := strings.TrimSpace(cfg.CAFile); caFile != "" {
		caPEM, err := os.ReadFile(caFile)
		if err != nil {
			return nil, fmt.Errorf("packet parser: read ca file: %w", err)
		}
		pool := x509.NewCertPool()
		if !pool.AppendCertsFromPEM(caPEM) {
			return nil, fmt.Errorf("%w: packet parser ca file contains no certificates", errs.ErrInvalid)
		}
		tlsConfig.RootCAs = pool
	}
	return &http.Client{
		Timeout: timeout,
		Transport: &http.Transport{
			Proxy:           http.ProxyFromEnvironment,
			TLSClientConfig: tlsConfig,
		},
	}, nil
}

func (c *ParserClient) Parse(ctx context.Context, capture *model.Capture, raw RawObject) (ParsedArtifact, error) {
	if c == nil || c.httpClient == nil {
		return ParsedArtifact{}, errs.ErrNotWiredYet
	}
	if capture == nil || capture.ID == 0 || raw.SizeBytes == 0 || raw.SHA256Hex == "" {
		return ParsedArtifact{}, fmt.Errorf("%w: packet parser input required", errs.ErrInvalid)
	}
	artifactID := fmt.Sprintf("pcap-%d", capture.ID)
	if strings.TrimSpace(capture.ArtifactID) != "" {
		artifactID = strings.TrimSpace(capture.ArtifactID)
	}
	expiresAt := c.now().UTC().Add(parserTokenTTL)
	downloadToken := c.signDownloadToken(capture.ID, raw.SHA256Hex, expiresAt)
	requestID := fmt.Sprintf("parse-%d-%d", capture.ID, c.now().UnixNano())
	parseRequest := parserRequest{
		RequestID:  requestID,
		IncludeHex: c.includeHex,
		Audit: parserAudit{
			TaskID:     artifactID,
			TenantID:   "default",
			OperatorID: strconv.FormatUint(capture.CreatedBy, 10),
		},
		Artifact: parserArtifact{
			ArtifactID:        artifactID,
			DownloadURI:       fmt.Sprintf("%s/api/internal/packet-captures/%d/download", c.artifactBaseURL, capture.ID),
			AccessToken:       downloadToken,
			ExpiresAt:         expiresAt.Format(time.RFC3339Nano),
			ExpectedSizeBytes: raw.SizeBytes,
			SHA256Hex:         raw.SHA256Hex,
		},
		Limits: parserLimits{
			MaxPackets: minUint64(c.maxPackets, nonzeroUint64(capture.MaxPackets, c.maxPackets)),
			MaxBytes:   parserByteLimit(c.maxBytes, nonzeroUint64(capture.MaxBytes, c.maxBytes), raw.SizeBytes),
		},
	}
	managerToken, err := c.signManagerRequest(parseRequest, expiresAt)
	if err != nil {
		return ParsedArtifact{}, err
	}
	parseRequest.ManagerRequestToken = managerToken

	body, err := json.Marshal(parseRequest)
	if err != nil {
		return ParsedArtifact{}, fmt.Errorf("packet parser: marshal request: %w", err)
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.endpoint, bytes.NewReader(body))
	if err != nil {
		return ParsedArtifact{}, fmt.Errorf("packet parser: create request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := c.httpClient.Do(req)
	if err != nil {
		return ParsedArtifact{}, fmt.Errorf("packet parser: call: %w", err)
	}
	defer func() {
		if closeErr := resp.Body.Close(); closeErr != nil {
			// Body close failure after response consumption is not actionable
			// for the caller; the parser response itself is validated below.
		}
	}()
	data, err := io.ReadAll(io.LimitReader(resp.Body, 64<<20))
	if err != nil {
		return ParsedArtifact{}, fmt.Errorf("packet parser: read response: %w", err)
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return ParsedArtifact{}, fmt.Errorf("packet parser: status %d: %s", resp.StatusCode, strings.TrimSpace(string(data)))
	}
	parsed, err := decodeParserResponse(data)
	if err != nil {
		return ParsedArtifact{}, err
	}
	if parsed.ArtifactID == "" {
		parsed.ArtifactID = artifactID
	}
	return parsed, nil
}

func (c *ParserClient) signDownloadToken(captureID uint64, sha256Hex string, expiresAt time.Time) string {
	payload := fmt.Sprintf("pcap-download:%d:%s:%d", captureID, strings.ToLower(sha256Hex), expiresAt.Unix())
	mac := hmac.New(sha256.New, c.tokenSecret)
	_, _ = mac.Write([]byte(payload))
	return base64.RawURLEncoding.EncodeToString([]byte(payload)) + "." + base64.RawURLEncoding.EncodeToString(mac.Sum(nil))
}

func (c *ParserClient) VerifyDownloadToken(token string, captureID uint64, sha256Hex string, now time.Time) error {
	parts := strings.Split(token, ".")
	if len(parts) != 2 {
		return errs.ErrUnauthorized
	}
	payloadData, err := base64.RawURLEncoding.DecodeString(parts[0])
	if err != nil {
		return errs.ErrUnauthorized
	}
	gotSig, err := base64.RawURLEncoding.DecodeString(parts[1])
	if err != nil {
		return errs.ErrUnauthorized
	}
	payload := string(payloadData)
	fields := strings.Split(payload, ":")
	if len(fields) != 4 || fields[0] != "pcap-download" {
		return errs.ErrUnauthorized
	}
	if fields[1] != strconv.FormatUint(captureID, 10) || !strings.EqualFold(fields[2], sha256Hex) {
		return errs.ErrUnauthorized
	}
	exp, err := strconv.ParseInt(fields[3], 10, 64)
	if err != nil || exp <= now.Unix() {
		return errs.ErrUnauthorized
	}
	mac := hmac.New(sha256.New, c.tokenSecret)
	_, _ = mac.Write(payloadData)
	if !hmac.Equal(gotSig, mac.Sum(nil)) {
		return errs.ErrUnauthorized
	}
	return nil
}

func (c *ParserClient) signManagerRequest(request parserRequest, expiresAt time.Time) (string, error) {
	request.ManagerRequestToken = ""
	digest, err := parserRequestDigest(request, expiresAt)
	if err != nil {
		return "", err
	}
	header, err := json.Marshal(signedParserHeader{Algorithm: "EdDSA", Type: "JWT"})
	if err != nil {
		return "", fmt.Errorf("packet parser: marshal jwt header: %w", err)
	}
	claims, err := json.Marshal(signedParserClaims{
		Audience:      "pcap-parser",
		ExpiresAt:     expiresAt.Unix(),
		IssuedAt:      c.now().Unix(),
		RequestSHA256: digest,
		IncludeHex:    request.IncludeHex,
	})
	if err != nil {
		return "", fmt.Errorf("packet parser: marshal jwt claims: %w", err)
	}
	encodedHeader := base64.RawURLEncoding.EncodeToString(header)
	encodedClaims := base64.RawURLEncoding.EncodeToString(claims)
	signingInput := encodedHeader + "." + encodedClaims
	signature := ed25519.Sign(c.privateKey, []byte(signingInput))
	return signingInput + "." + base64.RawURLEncoding.EncodeToString(signature), nil
}

type parserRequest struct {
	RequestID           string         `json:"request_id"`
	IncludeHex          bool           `json:"include_hex"`
	Audit               parserAudit    `json:"audit"`
	Artifact            parserArtifact `json:"artifact"`
	Limits              parserLimits   `json:"limits"`
	ManagerRequestToken string         `json:"manager_request_token"`
}

type parserAudit struct {
	TaskID     string `json:"task_id"`
	TenantID   string `json:"tenant_id"`
	OperatorID string `json:"operator_id"`
}

type parserArtifact struct {
	ArtifactID        string `json:"artifact_id"`
	DownloadURI       string `json:"download_uri"`
	AccessToken       string `json:"access_token"`
	ExpiresAt         string `json:"expires_at"`
	ExpectedSizeBytes uint64 `json:"expected_size_bytes"`
	SHA256Hex         string `json:"sha256_hex"`
}

type parserLimits struct {
	MaxPackets uint64 `json:"max_packets"`
	MaxBytes   uint64 `json:"max_bytes"`
}

type signedParserHeader struct {
	Algorithm string `json:"alg"`
	Type      string `json:"typ"`
}

type signedParserClaims struct {
	Audience      string `json:"aud"`
	ExpiresAt     int64  `json:"exp"`
	IssuedAt      int64  `json:"iat"`
	RequestSHA256 string `json:"request_sha256"`
	IncludeHex    bool   `json:"include_hex"`
}

type parserEnvelope struct {
	Code    int             `json:"code"`
	Message string          `json:"message"`
	Data    json.RawMessage `json:"data"`
}

func parserRequestDigest(request parserRequest, expiresAt time.Time) (string, error) {
	capabilityDigest := sha256.Sum256([]byte(request.Artifact.AccessToken))
	payload := struct {
		RequestID         string
		Audit             digestAudit
		ArtifactID        string
		DownloadURI       string
		CapabilitySHA256  string
		ExpiresAtUnixNano int64
		ExpectedSizeBytes uint64
		ArtifactSHA256    string
		Limits            digestLimits
		IncludeHex        bool
	}{
		RequestID:         request.RequestID,
		Audit:             digestAudit{TaskID: request.Audit.TaskID, TenantID: request.Audit.TenantID, OperatorID: request.Audit.OperatorID},
		ArtifactID:        request.Artifact.ArtifactID,
		DownloadURI:       request.Artifact.DownloadURI,
		CapabilitySHA256:  hex.EncodeToString(capabilityDigest[:]),
		ExpiresAtUnixNano: expiresAt.UnixNano(),
		ExpectedSizeBytes: request.Artifact.ExpectedSizeBytes,
		ArtifactSHA256:    strings.ToLower(request.Artifact.SHA256Hex),
		Limits:            digestLimits{MaxPackets: request.Limits.MaxPackets, MaxBytes: request.Limits.MaxBytes},
		IncludeHex:        request.IncludeHex,
	}
	encoded, err := json.Marshal(payload)
	if err != nil {
		return "", fmt.Errorf("packet parser: encode signed request: %w", err)
	}
	digest := sha256.Sum256(encoded)
	return hex.EncodeToString(digest[:]), nil
}

type digestAudit struct {
	TaskID     string
	TenantID   string
	OperatorID string
}

type digestLimits struct {
	MaxPackets uint64
	MaxBytes   uint64
}

func loadParserPrivateKey(path string) (ed25519.PrivateKey, error) {
	if path == "" {
		return nil, fmt.Errorf("%w: packet parser manager private key required", errs.ErrInvalid)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("packet parser: read manager private key: %w", err)
	}
	block, _ := pem.Decode(data)
	if block == nil {
		return nil, fmt.Errorf("%w: packet parser private key pem", errs.ErrInvalid)
	}
	key, err := x509.ParsePKCS8PrivateKey(block.Bytes)
	if err != nil {
		return nil, fmt.Errorf("packet parser: parse manager private key: %w", err)
	}
	privateKey, ok := key.(ed25519.PrivateKey)
	if !ok {
		return nil, fmt.Errorf("%w: packet parser private key must be Ed25519", errs.ErrInvalid)
	}
	return privateKey, nil
}

func decodeParserResponse(data []byte) (ParsedArtifact, error) {
	var envelope parserEnvelope
	if err := json.Unmarshal(data, &envelope); err == nil && envelope.Data != nil {
		if envelope.Code != 0 {
			return ParsedArtifact{}, fmt.Errorf("packet parser: %s", envelope.Message)
		}
		data = envelope.Data
	}
	var raw struct {
		RequestID string           `json:"request_id"`
		Summary   map[string]any   `json:"summary"`
		Packets   []map[string]any `json:"packets"`
		Meta      map[string]any   `json:"meta"`
	}
	if err := json.Unmarshal(data, &raw); err != nil {
		return ParsedArtifact{}, fmt.Errorf("packet parser: decode response: %w", err)
	}
	if raw.Packets == nil && raw.Summary == nil {
		return ParsedArtifact{}, errors.New("packet parser: response missing packet dissection")
	}
	meta := raw.Meta
	if meta == nil {
		meta = map[string]any{}
	}
	if raw.RequestID != "" {
		meta["request_id"] = raw.RequestID
	}
	return ParsedArtifact{Summary: raw.Summary, Packets: raw.Packets, Meta: meta}, nil
}

func minUint64(a, b uint64) uint64 {
	if a < b {
		return a
	}
	return b
}

func parserByteLimit(configuredMax, captureMax, rawSize uint64) uint64 {
	limit := minUint64(configuredMax, captureMax)
	if rawSize > limit {
		return rawSize
	}
	return limit
}

func nonzeroUint64(value, fallback uint64) uint64 {
	if value == 0 {
		return fallback
	}
	return value
}
