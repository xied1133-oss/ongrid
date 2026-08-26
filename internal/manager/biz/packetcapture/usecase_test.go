package packetcapture

import (
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"crypto/rsa"
	"crypto/sha256"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"encoding/pem"
	"errors"
	"math/big"
	"os"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/google/uuid"

	model "github.com/ongridio/ongrid/internal/manager/model/packetcapture"
	"github.com/ongridio/ongrid/internal/pkg/errs"
	"github.com/ongridio/ongrid/internal/pkg/tunnel"
)

type fakeRepo struct {
	nextID uint64
	byID   map[uint64]*model.Capture
	byKey  map[string]*model.Capture
}

type lockedRepo struct {
	mu          sync.Mutex
	repo        *fakeRepo
	deleteCalls int
}

func newLockedRepo() *lockedRepo { return &lockedRepo{repo: newFakeRepo()} }

func (r *lockedRepo) Create(ctx context.Context, capture *model.Capture) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.repo.Create(ctx, capture)
}
func (r *lockedRepo) Get(ctx context.Context, id uint64) (*model.Capture, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.repo.Get(ctx, id)
}
func (r *lockedRepo) GetByArtifactID(ctx context.Context, artifactID string) (*model.Capture, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.repo.GetByArtifactID(ctx, artifactID)
}
func (r *lockedRepo) GetByIdempotencyKey(ctx context.Context, key string) (*model.Capture, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.repo.GetByIdempotencyKey(ctx, key)
}
func (r *lockedRepo) List(ctx context.Context, filter ListFilter) ([]*model.Capture, int64, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.repo.List(ctx, filter)
}
func (r *lockedRepo) Delete(ctx context.Context, id uint64) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.deleteCalls++
	return r.repo.Delete(ctx, id)
}
func (r *lockedRepo) Transition(ctx context.Context, id uint64, from []string, to string, fields map[string]any) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.repo.Transition(ctx, id, from, to, fields)
}
func (r *lockedRepo) SetRawObject(ctx context.Context, id uint64, objectKey, sha256Hex string, sizeBytes uint64) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.repo.SetRawObject(ctx, id, objectKey, sha256Hex, sizeBytes)
}
func (r *lockedRepo) SetParsedArtifact(ctx context.Context, id uint64, artifactID, parsedJSON string) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.repo.SetParsedArtifact(ctx, id, artifactID, parsedJSON)
}

func (r *lockedRepo) deleted() int {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.deleteCalls
}

func (r *fakeRepo) GetByArtifactID(_ context.Context, artifactID string) (*model.Capture, error) {
	for _, capture := range r.byID {
		if capture.ArtifactID == artifactID && capture.State == model.StateReady && capture.ParsedJSON != "" {
			clone := *capture
			return &clone, nil
		}
	}
	return nil, errs.ErrNotFound
}

func TestParserClientDownloadTokenAuthorizesExactCapture(t *testing.T) {
	_, privateKey, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatalf("GenerateKey: %v", err)
	}
	der, err := x509.MarshalPKCS8PrivateKey(privateKey)
	if err != nil {
		t.Fatalf("MarshalPKCS8PrivateKey: %v", err)
	}
	keyPath := t.TempDir() + "/manager-key.pem"
	if err := os.WriteFile(keyPath, pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: der}), 0o600); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}
	client, err := NewParserClient(ParserClientConfig{
		URL:             "http://parser:8080",
		ArtifactBaseURL: "https://manager.internal",
		TokenSecret:     "test-secret",
		PrivateKeyFile:  keyPath,
	})
	if err != nil {
		t.Fatalf("NewParserClient: %v", err)
	}
	now := time.Date(2026, 8, 12, 9, 0, 0, 0, time.UTC)
	client.now = func() time.Time { return now }
	token := client.signDownloadToken(12, strings.Repeat("a", 64), now.Add(time.Minute))
	if err := client.VerifyDownloadToken(token, 12, strings.Repeat("a", 64), now); err != nil {
		t.Fatalf("VerifyDownloadToken: %v", err)
	}
	if err := client.VerifyDownloadToken(token, 13, strings.Repeat("a", 64), now); !errors.Is(err, errs.ErrUnauthorized) {
		t.Fatalf("VerifyDownloadToken wrong capture = %v, want unauthorized", err)
	}
	if err := client.VerifyDownloadToken(token, 12, strings.Repeat("b", 64), now); !errors.Is(err, errs.ErrUnauthorized) {
		t.Fatalf("VerifyDownloadToken wrong sha = %v, want unauthorized", err)
	}
	if err := client.VerifyDownloadToken(token, 12, strings.Repeat("a", 64), now.Add(2*time.Minute)); !errors.Is(err, errs.ErrUnauthorized) {
		t.Fatalf("VerifyDownloadToken expired = %v, want unauthorized", err)
	}
}

func TestParserClientAcceptsServerCA(t *testing.T) {
	dir := t.TempDir()
	keyPath := writeTestEd25519PrivateKey(t)
	_, _, caFile := writeTestTLSFiles(t, dir)
	client, err := NewParserClient(ParserClientConfig{
		URL:             "https://parser:8080",
		ArtifactBaseURL: "https://manager.internal",
		TokenSecret:     "test-secret",
		PrivateKeyFile:  keyPath,
		CAFile:          caFile,
	})
	if err != nil {
		t.Fatalf("NewParserClient: %v", err)
	}
	if client.httpClient == nil {
		t.Fatal("httpClient is nil")
	}
}

func TestParserByteLimitCoversRawObjectSize(t *testing.T) {
	if got := parserByteLimit(64<<20, 100, 23608); got != 23608 {
		t.Fatalf("parserByteLimit raw larger = %d, want 23608", got)
	}
	if got := parserByteLimit(64<<20, 10<<20, 23608); got != 10<<20 {
		t.Fatalf("parserByteLimit capture larger = %d, want %d", got, 10<<20)
	}
}

func writeTestEd25519PrivateKey(t *testing.T) string {
	t.Helper()
	_, privateKey, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatalf("GenerateKey: %v", err)
	}
	der, err := x509.MarshalPKCS8PrivateKey(privateKey)
	if err != nil {
		t.Fatalf("MarshalPKCS8PrivateKey: %v", err)
	}
	keyPath := t.TempDir() + "/manager-key.pem"
	if err := os.WriteFile(keyPath, pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: der}), 0o600); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}
	return keyPath
}

func writeTestTLSFiles(t *testing.T, dir string) (string, string, string) {
	t.Helper()
	privateKey, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatalf("GenerateKey: %v", err)
	}
	tmpl := &x509.Certificate{
		SerialNumber: big.NewInt(1),
		Subject:      pkix.Name{CommonName: "manager"},
		NotBefore:    time.Now().Add(-time.Minute),
		NotAfter:     time.Now().Add(time.Hour),
		KeyUsage:     x509.KeyUsageDigitalSignature | x509.KeyUsageKeyEncipherment | x509.KeyUsageCertSign,
		ExtKeyUsage:  []x509.ExtKeyUsage{x509.ExtKeyUsageClientAuth, x509.ExtKeyUsageServerAuth},
		IsCA:         true,
	}
	der, err := x509.CreateCertificate(rand.Reader, tmpl, tmpl, &privateKey.PublicKey, privateKey)
	if err != nil {
		t.Fatalf("CreateCertificate: %v", err)
	}
	certPEM := pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der})
	keyPEM := pem.EncodeToMemory(&pem.Block{Type: "RSA PRIVATE KEY", Bytes: x509.MarshalPKCS1PrivateKey(privateKey)})
	certFile := dir + "/client.crt"
	keyFile := dir + "/client.key"
	caFile := dir + "/ca.crt"
	if err := os.WriteFile(certFile, certPEM, 0o600); err != nil {
		t.Fatalf("write cert: %v", err)
	}
	if err := os.WriteFile(keyFile, keyPEM, 0o600); err != nil {
		t.Fatalf("write key: %v", err)
	}
	if err := os.WriteFile(caFile, certPEM, 0o600); err != nil {
		t.Fatalf("write ca: %v", err)
	}
	return certFile, keyFile, caFile
}

func newFakeRepo() *fakeRepo {
	return &fakeRepo{
		nextID: 1,
		byID:   map[uint64]*model.Capture{},
		byKey:  map[string]*model.Capture{},
	}
}

func (r *fakeRepo) Create(_ context.Context, capture *model.Capture) error {
	capture.ID = r.nextID
	r.nextID++
	clone := *capture
	r.byID[capture.ID] = &clone
	if capture.RequestIdempotencyKey != "" {
		r.byKey[capture.RequestIdempotencyKey] = &clone
	}
	return nil
}

func (r *fakeRepo) Get(_ context.Context, id uint64) (*model.Capture, error) {
	capture, ok := r.byID[id]
	if !ok {
		return nil, errs.ErrNotFound
	}
	clone := *capture
	return &clone, nil
}

func (r *fakeRepo) GetByIdempotencyKey(_ context.Context, key string) (*model.Capture, error) {
	capture, ok := r.byKey[key]
	if !ok {
		return nil, errs.ErrNotFound
	}
	clone := *capture
	return &clone, nil
}

func (r *fakeRepo) List(_ context.Context, filter ListFilter) ([]*model.Capture, int64, error) {
	var out []*model.Capture
	for _, capture := range r.byID {
		if filter.DeviceID != 0 && capture.DeviceID != filter.DeviceID {
			continue
		}
		if filter.EdgeID != 0 && capture.EdgeID != filter.EdgeID {
			continue
		}
		if filter.State != "" && capture.State != filter.State {
			continue
		}
		clone := *capture
		out = append(out, &clone)
	}
	return out, int64(len(out)), nil
}

func (r *fakeRepo) Delete(_ context.Context, id uint64) error {
	capture, ok := r.byID[id]
	if !ok {
		return errs.ErrNotFound
	}
	delete(r.byID, id)
	if capture.RequestIdempotencyKey != "" {
		delete(r.byKey, capture.RequestIdempotencyKey)
	}
	return nil
}

func (r *fakeRepo) Transition(_ context.Context, id uint64, from []string, to string, fields map[string]any) error {
	capture, ok := r.byID[id]
	if !ok {
		return errs.ErrNotFound
	}
	allowed := false
	for _, state := range from {
		if capture.State == state {
			allowed = true
			break
		}
	}
	if !allowed {
		return errs.ErrConflict
	}
	capture.State = to
	if v, ok := fields["error_code"].(string); ok {
		capture.ErrorCode = v
	}
	if v, ok := fields["error_detail"].(string); ok {
		capture.ErrorDetail = v
	}
	if v, ok := fields["resolved_target_json"].(string); ok {
		capture.ResolvedTargetJSON = v
	}
	if v, ok := fields["captured_bytes"].(uint64); ok {
		capture.CapturedBytes = v
	}
	if v, ok := fields["captured_packets"].(uint64); ok {
		capture.CapturedPackets = v
	}
	if v, ok := fields["live_preview_json"].(string); ok {
		capture.LivePreviewJSON = v
	}
	if v, ok := fields["started_at"].(time.Time); ok {
		capture.StartedAt = &v
	}
	if v, ok := fields["finished_at"].(time.Time); ok {
		capture.FinishedAt = &v
	}
	if v, ok := fields["artifact_id"].(string); ok {
		capture.ArtifactID = v
	}
	return nil
}

func (r *fakeRepo) SetRawObject(_ context.Context, id uint64, objectKey, sha256Hex string, sizeBytes uint64) error {
	capture, ok := r.byID[id]
	if !ok {
		return errs.ErrNotFound
	}
	capture.RawObjectKey = objectKey
	capture.RawSHA256 = sha256Hex
	capture.CapturedBytes = sizeBytes
	return nil
}

func (r *fakeRepo) SetParsedArtifact(_ context.Context, id uint64, artifactID string, parsedJSON string) error {
	capture, ok := r.byID[id]
	if !ok {
		return errs.ErrNotFound
	}
	capture.ArtifactID = artifactID
	capture.ParsedJSON = parsedJSON
	capture.ErrorCode = ""
	capture.ErrorDetail = ""
	return nil
}

type fakeParser struct {
	called bool
	err    error
}

type blockingParser struct {
	started chan struct{}
	release chan struct{}
	calls   atomic.Int32
}

func (p *blockingParser) Parse(_ context.Context, capture *model.Capture, raw RawObject) (ParsedArtifact, error) {
	if p.calls.Add(1) == 1 {
		close(p.started)
	}
	<-p.release
	return ParsedArtifact{
		Summary: map[string]any{"packets_seen": 1, "bytes_seen": raw.SizeBytes},
		Packets: []map[string]any{{"number": 1, "protocol": "TCP", "info": capture.InterfaceName}},
	}, nil
}

func (p *fakeParser) Parse(_ context.Context, capture *model.Capture, raw RawObject) (ParsedArtifact, error) {
	p.called = true
	if p.err != nil {
		return ParsedArtifact{}, p.err
	}
	return ParsedArtifact{
		ArtifactID: "pcap-custom",
		Summary: map[string]any{
			"packets_seen": 1,
			"bytes_seen":   raw.SizeBytes,
		},
		Packets: []map[string]any{{
			"number":      1,
			"protocol":    "TCP",
			"source":      "10.0.0.1",
			"destination": "10.0.0.2",
			"info":        capture.InterfaceName,
		}},
	}, nil
}

type fakeResolver struct {
	edgeID uint64
	err    error
}

func (r fakeResolver) ResolveEdgeID(_ context.Context, _ uint64) (uint64, error) {
	return r.edgeID, r.err
}

type fakeCaller struct {
	method           string
	methods          []string
	edgeID           uint64
	err              error
	state            string
	livePayloadBytes int64
	livePreview      []string
}

type lockedCaller struct {
	mu     sync.Mutex
	caller *fakeCaller
}

func (c *lockedCaller) Call(ctx context.Context, edgeID uint64, method string, body []byte) ([]byte, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.caller.Call(ctx, edgeID, method, body)
}

func (c *fakeCaller) Call(_ context.Context, edgeID uint64, method string, body []byte) ([]byte, error) {
	c.edgeID = edgeID
	c.method = method
	c.methods = append(c.methods, method)
	if c.err != nil {
		return nil, c.err
	}
	now := time.Now().UTC()
	if method == tunnel.MethodCancelPacketCapture {
		var req tunnel.PacketCaptureCancelRequest
		if err := json.Unmarshal(body, &req); err != nil {
			return nil, err
		}
		return json.Marshal(tunnel.PacketCaptureTask{
			ID:         req.CaptureID,
			State:      "cancelled",
			FinishedAt: &now,
		})
	}
	if method == tunnel.MethodStopPacketCapture {
		var req tunnel.PacketCaptureStopRequest
		if err := json.Unmarshal(body, &req); err != nil {
			return nil, err
		}
		return json.Marshal(tunnel.PacketCaptureTask{
			ID:        req.CaptureID,
			State:     "running",
			CreatedAt: now,
			StartedAt: &now,
		})
	}
	if method == tunnel.MethodGetPacketCapture {
		var req tunnel.PacketCaptureGetRequest
		if err := json.Unmarshal(body, &req); err != nil {
			return nil, err
		}
		state := c.state
		if state == "" {
			state = "running"
		}
		result := tunnel.PacketCaptureResult{Packets: 12, FileBytes: 2048, StopReason: "duration", LivePreview: c.livePreview}
		if c.livePayloadBytes > 0 {
			result.FileBytes = 0
			result.PayloadBytes = c.livePayloadBytes
		}
		return json.Marshal(tunnel.PacketCaptureTask{
			ID:         req.CaptureID,
			State:      state,
			Result:     result,
			CreatedAt:  now,
			StartedAt:  &now,
			FinishedAt: &now,
		})
	}
	if method == tunnel.MethodReadPacketCapture {
		var req tunnel.PacketCaptureReadRequest
		if err := json.Unmarshal(body, &req); err != nil {
			return nil, err
		}
		data := []byte("pcap-data")
		sum := sha256.Sum256(data)
		return json.Marshal(tunnel.PacketCaptureReadResponse{
			CaptureID:  req.CaptureID,
			SizeBytes:  9,
			SHA256Hex:  hex.EncodeToString(sum[:]),
			DataBase64: base64.StdEncoding.EncodeToString(data),
		})
	}
	var req tunnel.PacketCaptureStartRequest
	if err := json.Unmarshal(body, &req); err != nil {
		return nil, err
	}
	state := c.state
	if state == "" {
		state = "running"
	}
	return json.Marshal(tunnel.PacketCaptureTask{
		ID: req.CaptureID,
		Request: tunnel.PacketCaptureWireIn{
			CaptureID:        req.CaptureID,
			Interface:        req.Interface,
			NetworkNamespace: req.NetworkNamespace,
			Filter:           req.Filter,
			DurationSeconds:  req.DurationSeconds,
			MaxBytes:         req.MaxBytes,
			MaxPackets:       req.MaxPackets,
			Snaplen:          req.Snaplen,
			Promiscuous:      req.Promiscuous,
		},
		State:     state,
		Result:    tunnel.PacketCaptureResult{Packets: 12, FileBytes: 2048, StopReason: "duration"},
		CreatedAt: now,
		StartedAt: &now,
	})
}

func TestUsecaseStopKeepsCaptureActiveUntilEdgeCompletes(t *testing.T) {
	repo := newFakeRepo()
	caller := &fakeCaller{}
	uc := New(repo, caller, fakeResolver{edgeID: 7}, nil)
	capture, err := uc.Create(context.Background(), CreateInput{DeviceID: 11, Interface: "eth0", Source: SourceAPI})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	stopped, err := uc.Stop(context.Background(), capture.Capture.ID)
	if err != nil {
		t.Fatalf("Stop: %v", err)
	}
	if caller.method != tunnel.MethodStopPacketCapture {
		t.Fatalf("method=%q want %q", caller.method, tunnel.MethodStopPacketCapture)
	}
	if stopped.State != model.StateCapturing {
		t.Fatalf("state=%q want %q", stopped.State, model.StateCapturing)
	}
}

func TestUsecaseCreateDispatchesEdgeAndPersistsState(t *testing.T) {
	repo := newFakeRepo()
	caller := &fakeCaller{}
	uc := New(repo, caller, fakeResolver{edgeID: 9}, nil)

	out, err := uc.Create(context.Background(), CreateInput{
		DeviceID:         3,
		Interface:        "eth0",
		NetworkNamespace: "ongrid-netdev-a",
		Filter:           "tcp and port 443",
		DurationSeconds:  10,
		Source:           SourceChat,
		CreatedBy:        7,
	})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	if out.Capture.ID == 0 || out.Capture.State != model.StateCapturing {
		t.Fatalf("capture state = id:%d state:%s", out.Capture.ID, out.Capture.State)
	}
	if !strings.HasPrefix(out.Capture.ArtifactID, "pcap-") {
		t.Fatalf("artifact id = %q, want pcap UUID", out.Capture.ArtifactID)
	}
	if _, parseErr := uuid.Parse(strings.TrimPrefix(out.Capture.ArtifactID, "pcap-")); parseErr != nil {
		t.Fatalf("artifact id = %q, want UUID: %v", out.Capture.ArtifactID, parseErr)
	}
	if out.Edge.Request.CaptureID != out.Capture.ArtifactID {
		t.Fatalf("edge capture id = %q, want artifact id %q", out.Edge.Request.CaptureID, out.Capture.ArtifactID)
	}
	if caller.edgeID != 9 || caller.method != tunnel.MethodStartPacketCapture {
		t.Fatalf("call = edge:%d method:%s", caller.edgeID, caller.method)
	}
	if out.Edge.Request.DurationSeconds != 10 {
		t.Fatalf("edge duration = %d", out.Edge.Request.DurationSeconds)
	}
	if out.Capture.NetworkNamespace != "ongrid-netdev-a" || out.Edge.Request.NetworkNamespace != "ongrid-netdev-a" {
		t.Fatalf("network namespace capture=%q edge=%q", out.Capture.NetworkNamespace, out.Edge.Request.NetworkNamespace)
	}
}

func TestUsecaseCreateReturnsExistingForIdempotencyKey(t *testing.T) {
	repo := newFakeRepo()
	existing := &model.Capture{State: model.StateCapturing, RequestIdempotencyKey: "same"}
	if err := repo.Create(context.Background(), existing); err != nil {
		t.Fatalf("seed: %v", err)
	}
	caller := &fakeCaller{}
	uc := New(repo, caller, fakeResolver{edgeID: 9}, nil)

	out, err := uc.Create(context.Background(), CreateInput{
		DeviceID:              3,
		Interface:             "eth0",
		RequestIdempotencyKey: "same",
	})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	if out.Capture.ID != existing.ID {
		t.Fatalf("id = %d, want %d", out.Capture.ID, existing.ID)
	}
	if caller.method != "" {
		t.Fatalf("idempotent call should not dispatch, got %s", caller.method)
	}
}

func TestUsecaseCreateRejectsDeviceWithoutHostLink(t *testing.T) {
	uc := New(newFakeRepo(), &fakeCaller{}, fakeResolver{}, nil)
	_, err := uc.Create(context.Background(), CreateInput{DeviceID: 3, Interface: "eth0"})
	if !errors.Is(err, errs.ErrInvalid) {
		t.Fatalf("err = %v, want invalid", err)
	}
}

func TestUsecaseCreateMarksDispatchFailed(t *testing.T) {
	repo := newFakeRepo()
	uc := New(repo, &fakeCaller{err: errors.New("edge offline")}, fakeResolver{edgeID: 9}, nil)

	_, err := uc.Create(context.Background(), CreateInput{DeviceID: 3, Interface: "eth0"})
	if err == nil {
		t.Fatalf("Create expected error")
	}
	if _, getErr := repo.Get(context.Background(), 1); !errors.Is(getErr, errs.ErrNotFound) {
		t.Fatalf("Get after dispatch failure = %v, want not found", getErr)
	}
}

func TestUsecaseRefreshUpdatesCaptureState(t *testing.T) {
	repo := newFakeRepo()
	caller := &fakeCaller{state: "succeeded"}
	uc := New(repo, caller, fakeResolver{edgeID: 9}, nil)

	created, err := uc.Create(context.Background(), CreateInput{DeviceID: 3, Interface: "eth0"})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	refreshed, err := uc.Refresh(context.Background(), created.Capture.ID)
	if err != nil {
		t.Fatalf("Refresh: %v", err)
	}
	if caller.method != tunnel.MethodGetPacketCapture {
		t.Fatalf("refresh method=%s", caller.method)
	}
	if refreshed.State != model.StateReady || refreshed.CapturedPackets != 12 || refreshed.CapturedBytes != 2048 {
		t.Fatalf("refreshed=%+v", refreshed)
	}
}

func TestUsecaseRefreshUsesLivePayloadBytes(t *testing.T) {
	repo := newFakeRepo()
	caller := &fakeCaller{livePayloadBytes: 768}
	uc := New(repo, caller, fakeResolver{edgeID: 9}, nil)

	created, err := uc.Create(context.Background(), CreateInput{DeviceID: 3, Interface: "eth0"})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	refreshed, err := uc.Refresh(context.Background(), created.Capture.ID)
	if err != nil {
		t.Fatalf("Refresh: %v", err)
	}
	if refreshed.State != model.StateCapturing || refreshed.CapturedPackets != 12 || refreshed.CapturedBytes != 768 {
		t.Fatalf("refreshed=%+v", refreshed)
	}
}

func TestUsecaseRefreshPersistsLivePreview(t *testing.T) {
	repo := newFakeRepo()
	caller := &fakeCaller{livePayloadBytes: 768, livePreview: []string{"IP 10.0.0.1.51515 > 10.0.0.2.443: tcp 0"}}
	uc := New(repo, caller, fakeResolver{edgeID: 9}, nil)

	created, err := uc.Create(context.Background(), CreateInput{DeviceID: 3, Interface: "eth0"})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	refreshed, err := uc.Refresh(context.Background(), created.Capture.ID)
	if err != nil {
		t.Fatalf("Refresh: %v", err)
	}
	if refreshed.LivePreviewJSON != `["IP 10.0.0.1.51515 \u003e 10.0.0.2.443: tcp 0"]` {
		t.Fatalf("live preview = %q", refreshed.LivePreviewJSON)
	}
}

func TestUsecaseRefreshIngestsRawObject(t *testing.T) {
	repo := newFakeRepo()
	caller := &fakeCaller{state: "succeeded"}
	store, err := NewLocalRawStore(t.TempDir())
	if err != nil {
		t.Fatalf("NewLocalRawStore: %v", err)
	}
	uc := New(repo, caller, fakeResolver{edgeID: 9}, nil)
	uc.SetRawStore(store)
	uc.SetParser(&fakeParser{})

	created, err := uc.Create(context.Background(), CreateInput{DeviceID: 3, Interface: "eth0"})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	refreshed, err := uc.Refresh(context.Background(), created.Capture.ID)
	if err != nil {
		t.Fatalf("Refresh: %v", err)
	}
	if caller.method != tunnel.MethodReadPacketCapture {
		t.Fatalf("last method=%s, want %s", caller.method, tunnel.MethodReadPacketCapture)
	}
	if refreshed.RawObjectKey == "" || refreshed.RawSHA256 == "" || refreshed.CapturedBytes != 9 {
		t.Fatalf("raw metadata not saved: %+v", refreshed)
	}

	_, raw, err := uc.RawObject(context.Background(), refreshed.ID)
	if err != nil {
		t.Fatalf("RawObject: %v", err)
	}
	if string(raw.Data) != "pcap-data" || raw.SizeBytes != 9 {
		t.Fatalf("raw=%+v data=%q", raw, string(raw.Data))
	}
}

func TestUsecaseRefreshStoresParsedArtifact(t *testing.T) {
	repo := newFakeRepo()
	caller := &fakeCaller{state: "succeeded"}
	store, err := NewLocalRawStore(t.TempDir())
	if err != nil {
		t.Fatalf("NewLocalRawStore: %v", err)
	}
	parser := &fakeParser{}
	uc := New(repo, caller, fakeResolver{edgeID: 9}, nil)
	uc.SetRawStore(store)
	uc.SetParser(parser)

	created, err := uc.Create(context.Background(), CreateInput{DeviceID: 3, Interface: "eth0"})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	refreshed, err := uc.Refresh(context.Background(), created.Capture.ID)
	if err != nil {
		t.Fatalf("Refresh: %v", err)
	}
	if !parser.called {
		t.Fatal("parser was not called")
	}
	if !strings.HasPrefix(refreshed.ArtifactID, "pcap-") {
		t.Fatalf("ArtifactID=%q, want generated artifact id", refreshed.ArtifactID)
	}
	if refreshed.ParsedJSON == "" || !strings.Contains(refreshed.ParsedJSON, `"packets"`) {
		t.Fatalf("ParsedJSON not saved: %q", refreshed.ParsedJSON)
	}
}

func TestUsecaseRefreshWhenParserFailsPreservesFailedCapture(t *testing.T) {
	repo := newFakeRepo()
	caller := &fakeCaller{state: "succeeded"}
	store, err := NewLocalRawStore(t.TempDir())
	if err != nil {
		t.Fatalf("NewLocalRawStore: %v", err)
	}
	uc := New(repo, caller, fakeResolver{edgeID: 9}, nil)
	uc.SetRawStore(store)
	uc.SetParser(&fakeParser{err: errors.New("parser unavailable")})

	created, err := uc.Create(context.Background(), CreateInput{DeviceID: 3, Interface: "eth0"})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	_, err = uc.Refresh(context.Background(), created.Capture.ID)
	if err == nil {
		t.Fatal("Refresh expected parser error")
	}
	failed, getErr := repo.Get(context.Background(), created.Capture.ID)
	if getErr != nil {
		t.Fatalf("Get after parser failure: %v", getErr)
	}
	if failed.State != model.StateFailed || failed.RawObjectKey == "" || failed.ErrorCode != "artifact_publish_failed" {
		t.Fatalf("failed capture not preserved: %+v", failed)
	}
}

func TestUsecaseRefreshParserFailureRetainsRawObject(t *testing.T) {
	repo := newFakeRepo()
	caller := &fakeCaller{state: "succeeded"}
	store, err := NewLocalRawStore(t.TempDir())
	if err != nil {
		t.Fatalf("NewLocalRawStore: %v", err)
	}
	uc := New(repo, caller, fakeResolver{edgeID: 9}, nil)
	uc.SetRawStore(store)
	uc.SetParser(&fakeParser{err: errors.New("parser unavailable")})

	created, err := uc.Create(context.Background(), CreateInput{DeviceID: 3, Interface: "eth0"})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	_, err = uc.Refresh(context.Background(), created.Capture.ID)
	if err == nil {
		t.Fatal("Refresh expected parser error")
	}
	entries, err := os.ReadDir(store.dir)
	if err != nil {
		t.Fatalf("ReadDir: %v", err)
	}
	if len(entries) != 1 {
		t.Fatalf("raw artifact count after parser failure = %d, want 1", len(entries))
	}
}

func TestUsecaseConcurrentRefreshPublishesArtifactOnce(t *testing.T) {
	repo := newLockedRepo()
	caller := &lockedCaller{caller: &fakeCaller{state: "succeeded"}}
	store, err := NewLocalRawStore(t.TempDir())
	if err != nil {
		t.Fatalf("NewLocalRawStore: %v", err)
	}
	parser := &blockingParser{started: make(chan struct{}), release: make(chan struct{})}
	uc := New(repo, caller, fakeResolver{edgeID: 9}, nil)
	uc.SetRawStore(store)
	uc.SetParser(parser)

	created, err := uc.Create(context.Background(), CreateInput{DeviceID: 3, Interface: "eth0"})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	errsCh := make(chan error, 2)
	go func() {
		_, refreshErr := uc.Refresh(context.Background(), created.Capture.ID)
		errsCh <- refreshErr
	}()
	select {
	case <-parser.started:
	case <-time.After(2 * time.Second):
		t.Fatal("first refresh did not enter parser")
	}
	go func() {
		_, refreshErr := uc.Refresh(context.Background(), created.Capture.ID)
		errsCh <- refreshErr
	}()

	select {
	case refreshErr := <-errsCh:
		if refreshErr != nil {
			t.Fatalf("overlapping Refresh: %v", refreshErr)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("overlapping refresh waited on the active parser")
	}
	close(parser.release)
	select {
	case refreshErr := <-errsCh:
		if refreshErr != nil {
			t.Fatalf("publishing Refresh: %v", refreshErr)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("publishing refresh did not finish")
	}

	if got := parser.calls.Load(); got != 1 {
		t.Fatalf("parser calls = %d, want 1", got)
	}
	if got := repo.deleted(); got != 0 {
		t.Fatalf("delete calls = %d, want 0", got)
	}
	final, err := repo.Get(context.Background(), created.Capture.ID)
	if err != nil {
		t.Fatalf("Get final capture: %v", err)
	}
	if final.State != model.StateReady || final.ParsedJSON == "" || final.RawObjectKey == "" {
		t.Fatalf("final capture not published: %+v", final)
	}
}

func TestLocalRawStoreReadRejectsUnsafeKey(t *testing.T) {
	store, err := NewLocalRawStore(t.TempDir())
	if err != nil {
		t.Fatalf("NewLocalRawStore: %v", err)
	}
	_, err = store.Read(context.Background(), "../capture.pcap")
	if !errors.Is(err, errs.ErrInvalid) {
		t.Fatalf("err=%v, want invalid", err)
	}
}

func TestUsecaseRawObjectValidatesChecksum(t *testing.T) {
	repo := newFakeRepo()
	store, err := NewLocalRawStore(t.TempDir())
	if err != nil {
		t.Fatalf("NewLocalRawStore: %v", err)
	}
	uc := New(repo, &fakeCaller{}, fakeResolver{edgeID: 9}, nil)
	uc.SetRawStore(store)

	key, _, size, err := store.Save(context.Background(), 1, []byte("pcap-data"))
	if err != nil {
		t.Fatalf("Save: %v", err)
	}
	capture := &model.Capture{
		State:         model.StateReady,
		RawObjectKey:  key,
		RawSHA256:     strings.Repeat("0", 64),
		CapturedBytes: size,
	}
	if err := repo.Create(context.Background(), capture); err != nil {
		t.Fatalf("Create: %v", err)
	}
	_, _, err = uc.RawObject(context.Background(), capture.ID)
	if !errors.Is(err, errs.ErrInvalid) {
		t.Fatalf("err=%v, want invalid", err)
	}
}
