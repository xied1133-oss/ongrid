package packetcapture

import (
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/google/uuid"

	model "github.com/ongridio/ongrid/internal/manager/model/packetcapture"
	"github.com/ongridio/ongrid/internal/pkg/errs"
	"github.com/ongridio/ongrid/internal/pkg/tunnel"
)

const (
	SourceChat     = "chat"
	SourceWorkflow = "workflow"
	SourceAPI      = "api"

	defaultDurationSeconds = 30
	defaultMaxBytes        = 64 << 20
	defaultMaxPackets      = 100_000
	defaultSnaplen         = 1514
	maxDurationSeconds     = 300
	maxCaptureBytes        = 256 << 20
	maxCapturePackets      = 500_000
	maxCaptureSnaplen      = 65_535
)

type Repo interface {
	Create(ctx context.Context, capture *model.Capture) error
	Get(ctx context.Context, id uint64) (*model.Capture, error)
	GetByArtifactID(ctx context.Context, artifactID string) (*model.Capture, error)
	GetByIdempotencyKey(ctx context.Context, key string) (*model.Capture, error)
	List(ctx context.Context, filter ListFilter) ([]*model.Capture, int64, error)
	Delete(ctx context.Context, id uint64) error
	Transition(ctx context.Context, id uint64, from []string, to string, fields map[string]any) error
	SetRawObject(ctx context.Context, id uint64, objectKey, sha256Hex string, sizeBytes uint64) error
	SetParsedArtifact(ctx context.Context, id uint64, artifactID string, parsedJSON string) error
}

// SessionRepo is deliberately separate from Repo so existing capture-only
// adapters remain valid. The production store implements both interfaces.
type SessionRepo interface {
	CreateSession(ctx context.Context, session *model.Session) error
	GetSession(ctx context.Context, publicID string) (*model.Session, error)
	ListSessions(ctx context.Context, limit, offset int) ([]*model.Session, int64, error)
	ListBySessionID(ctx context.Context, sessionID uint64) ([]*model.Capture, error)
	SetSessionAnalysis(ctx context.Context, id uint64, state, analysisJSON string) error
	ListReconcilableSessions(ctx context.Context, limit int) ([]*model.Session, error)
	MarkSessionCompletionNotified(ctx context.Context, id uint64, at time.Time) (bool, error)
}

type SessionCaptureCountRepo interface {
	CountCapturesBySessionIDs(ctx context.Context, sessionIDs []uint64) (map[uint64]int, error)
}

type OperationSessionRepo interface {
	SetSessionOperation(ctx context.Context, id uint64, operationID string) error
}

// BindSessionOperation records the generic task after the caller has created
// it. Keeping the link on the domain session lets restart-safe reconciliation
// update the operation without parsing a chat tool result.
func (u *Usecase) BindSessionOperation(ctx context.Context, publicID, operationID string) error {
	sessions, ok := u.repo.(SessionRepo)
	if !ok {
		return errs.ErrNotWiredYet
	}
	operations, ok := u.repo.(OperationSessionRepo)
	if !ok {
		return errs.ErrNotWiredYet
	}
	session, err := sessions.GetSession(ctx, publicID)
	if err != nil {
		return err
	}
	return operations.SetSessionOperation(ctx, session.ID, operationID)
}

type EdgeCaller interface {
	Call(ctx context.Context, edgeID uint64, method string, body []byte) ([]byte, error)
}

type DeviceResolver interface {
	ResolveEdgeID(ctx context.Context, deviceID uint64) (uint64, error)
}

type Usecase struct {
	repo     Repo
	caller   EdgeCaller
	resolver DeviceResolver
	rawStore RawStore
	parser   Parser
	log      *slog.Logger
	now      func() time.Time
}

type RawStore interface {
	Save(ctx context.Context, captureID uint64, data []byte) (key string, sha256Hex string, sizeBytes uint64, err error)
	Read(ctx context.Context, key string) ([]byte, error)
	Delete(ctx context.Context, key string) error
}

type Parser interface {
	Parse(ctx context.Context, capture *model.Capture, raw RawObject) (ParsedArtifact, error)
}

type RawDownloadAuthorizer interface {
	VerifyDownloadToken(token string, captureID uint64, sha256Hex string, now time.Time) error
}

type ParsedArtifact struct {
	ArtifactID string           `json:"artifact_id"`
	Summary    map[string]any   `json:"summary,omitempty"`
	Packets    []map[string]any `json:"packets,omitempty"`
	Meta       map[string]any   `json:"meta,omitempty"`
}

type RawObject struct {
	Key       string
	SHA256Hex string
	SizeBytes uint64
	Data      []byte
}

type LocalRawStore struct {
	dir string
}

func NewLocalRawStore(dir string) (*LocalRawStore, error) {
	dir = strings.TrimSpace(dir)
	if dir == "" {
		dir = "/var/lib/ongrid/packet-captures/raw"
	}
	clean := filepath.Clean(dir)
	if clean == "." || clean == string(filepath.Separator) {
		return nil, fmt.Errorf("%w: unsafe packet capture raw directory", errs.ErrInvalid)
	}
	if err := os.MkdirAll(clean, 0o700); err != nil {
		return nil, fmt.Errorf("packet capture: create raw directory: %w", err)
	}
	return &LocalRawStore{dir: clean}, nil
}

func (s *LocalRawStore) Save(_ context.Context, captureID uint64, data []byte) (string, string, uint64, error) {
	if s == nil || strings.TrimSpace(s.dir) == "" {
		return "", "", 0, errs.ErrNotWiredYet
	}
	if captureID == 0 || len(data) == 0 {
		return "", "", 0, fmt.Errorf("%w: packet capture raw object required", errs.ErrInvalid)
	}
	sum := sha256.Sum256(data)
	sha256Hex := hex.EncodeToString(sum[:])
	key := fmt.Sprintf("capture-%d-%s.pcap", captureID, sha256Hex[:16])
	path := filepath.Join(s.dir, key)
	if err := os.WriteFile(path, data, 0o600); err != nil {
		return "", "", 0, fmt.Errorf("packet capture: write raw object: %w", err)
	}
	return key, sha256Hex, uint64(len(data)), nil
}

func (s *LocalRawStore) Read(_ context.Context, key string) ([]byte, error) {
	if s == nil || strings.TrimSpace(s.dir) == "" {
		return nil, errs.ErrNotWiredYet
	}
	key = strings.TrimSpace(key)
	if key == "" || filepath.Base(key) != key || strings.Contains(key, "..") {
		return nil, fmt.Errorf("%w: unsafe packet capture raw object key", errs.ErrInvalid)
	}
	data, err := os.ReadFile(filepath.Join(s.dir, key))
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return nil, errs.ErrNotFound
		}
		return nil, fmt.Errorf("packet capture: read raw object: %w", err)
	}
	if len(data) == 0 {
		return nil, fmt.Errorf("%w: packet capture raw object is empty", errs.ErrInvalid)
	}
	return data, nil
}

func (s *LocalRawStore) Delete(_ context.Context, key string) error {
	if s == nil || strings.TrimSpace(s.dir) == "" {
		return errs.ErrNotWiredYet
	}
	key = strings.TrimSpace(key)
	if key == "" || filepath.Base(key) != key || strings.Contains(key, "..") {
		return fmt.Errorf("%w: unsafe packet capture raw object key", errs.ErrInvalid)
	}
	if err := os.Remove(filepath.Join(s.dir, key)); err != nil && !errors.Is(err, fs.ErrNotExist) {
		return fmt.Errorf("packet capture: delete raw object: %w", err)
	}
	return nil
}

type CreateInput struct {
	DeviceID              uint64
	Interface             string
	NetworkNamespace      string
	Filter                string
	DurationSeconds       int
	MaxBytes              int64
	MaxPackets            int
	Snaplen               int
	Promiscuous           bool
	Title                 string
	Description           string
	Source                string
	CreatedBy             uint64
	RequestIdempotencyKey string
	SessionID             uint64
	PlannedStartAt        *time.Time
}

type CreateOutput struct {
	Capture *model.Capture           `json:"capture"`
	Edge    tunnel.PacketCaptureTask `json:"edge"`
}

type ListFilter struct {
	DeviceID uint64
	EdgeID   uint64
	State    string
	Limit    int
	Offset   int
}

func New(repo Repo, caller EdgeCaller, resolver DeviceResolver, log *slog.Logger) *Usecase {
	if log == nil {
		log = slog.Default()
	}
	return &Usecase{
		repo:     repo,
		caller:   caller,
		resolver: resolver,
		log:      log,
		now:      time.Now,
	}
}

func (u *Usecase) SetRawStore(store RawStore) { u.rawStore = store }

func (u *Usecase) SetParser(parser Parser) { u.parser = parser }

func (u *Usecase) RawDownloadAuthorizer() RawDownloadAuthorizer {
	authz, _ := u.parser.(RawDownloadAuthorizer)
	return authz
}

func (u *Usecase) Create(ctx context.Context, in CreateInput) (*CreateOutput, error) {
	if u == nil || u.repo == nil || u.caller == nil || u.resolver == nil {
		return nil, errs.ErrNotWiredYet
	}
	normalized, err := normalizeCreateInput(in)
	if err != nil {
		return nil, err
	}
	if normalized.RequestIdempotencyKey != "" {
		existing, getErr := u.repo.GetByIdempotencyKey(ctx, normalized.RequestIdempotencyKey)
		if getErr == nil && existing != nil {
			return &CreateOutput{Capture: existing}, nil
		}
		if getErr != nil && !errors.Is(getErr, errs.ErrNotFound) {
			return nil, fmt.Errorf("packet capture: get idempotency: %w", getErr)
		}
	}
	edgeID, err := u.resolver.ResolveEdgeID(ctx, normalized.DeviceID)
	if err != nil {
		return nil, fmt.Errorf("packet capture: resolve device %d: %w", normalized.DeviceID, err)
	}
	if edgeID == 0 {
		return nil, fmt.Errorf("%w: device_id=%d has no host-edge link", errs.ErrInvalid, normalized.DeviceID)
	}
	requestJSON, err := marshalJSON(normalized)
	if err != nil {
		return nil, err
	}
	filterJSON, err := marshalJSON(map[string]string{"grammar": "pcap-simple-v1", "value": normalized.Filter})
	if err != nil {
		return nil, err
	}
	capture := &model.Capture{
		RequestIdempotencyKey: normalized.RequestIdempotencyKey,
		CreatedBy:             normalized.CreatedBy,
		Source:                normalized.Source,
		State:                 model.StateQueued,
		EdgeID:                edgeID,
		DeviceID:              normalized.DeviceID,
		TargetKind:            "host_interface",
		RequestedTargetJSON:   requestJSON,
		ResolvedTargetJSON:    requestJSON,
		FilterJSON:            filterJSON,
		CanonicalFilter:       normalized.Filter,
		InterfaceName:         normalized.Interface,
		NetworkNamespace:      normalized.NetworkNamespace,
		Direction:             "inout",
		Format:                "pcap",
		Promiscuous:           normalized.Promiscuous,
		Immediate:             true,
		DurationSecs:          uint32(normalized.DurationSeconds),
		MaxBytes:              uint64(normalized.MaxBytes),
		MaxPackets:            uint64(normalized.MaxPackets),
		Snaplen:               uint32(normalized.Snaplen),
		Title:                 normalized.Title,
		Description:           normalized.Description,
		ArtifactID:            "pcap-" + uuid.NewString(),
		SessionID:             normalized.SessionID,
		LabelsJSON:            "{}",
	}
	if err := u.repo.Create(ctx, capture); err != nil {
		return nil, err
	}
	// The edge task and published artifact share one opaque identifier. Database
	// primary keys remain internal implementation details.
	captureID := capture.ArtifactID
	req := tunnel.PacketCaptureStartRequest{
		CaptureID:        captureID,
		Interface:        normalized.Interface,
		NetworkNamespace: normalized.NetworkNamespace,
		Filter:           normalized.Filter,
		DurationSeconds:  normalized.DurationSeconds,
		MaxBytes:         normalized.MaxBytes,
		MaxPackets:       normalized.MaxPackets,
		Snaplen:          normalized.Snaplen,
		Promiscuous:      normalized.Promiscuous,
		StartAt:          normalized.PlannedStartAt,
	}
	body, err := json.Marshal(req)
	if err != nil {
		u.discardFailedCapture(ctx, capture, err)
		return nil, fmt.Errorf("packet capture: marshal edge request: %w", err)
	}
	callCtx, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()
	respBody, err := u.caller.Call(callCtx, edgeID, tunnel.MethodStartPacketCapture, body)
	if err != nil {
		u.discardFailedCapture(ctx, capture, err)
		return nil, fmt.Errorf("packet capture: dispatch edge: %w", err)
	}
	var edgeTask tunnel.PacketCaptureTask
	if err := json.Unmarshal(respBody, &edgeTask); err != nil {
		u.discardFailedCapture(ctx, capture, err)
		return nil, fmt.Errorf("packet capture: decode edge response: %w", err)
	}
	startFields := map[string]any{
		"resolved_target_json": mustJSON(req),
		"captured_bytes":       uint64(edgeTask.Result.FileBytes),
		"captured_packets":     uint64(edgeTask.Result.Packets),
		"error_detail":         edgeTask.Error,
	}
	if edgeTask.StartedAt != nil {
		startFields["started_at"] = *edgeTask.StartedAt
	}
	if edgeTask.FinishedAt != nil {
		startFields["finished_at"] = *edgeTask.FinishedAt
	}
	state := mapEdgeState(edgeTask.State)
	if state == model.StateFailed {
		u.discardFailedCapture(ctx, capture, errors.New(edgeTask.Error))
		return nil, fmt.Errorf("packet capture: edge completed without artifact: %w", errs.ErrInvalid)
	}
	if err := u.repo.Transition(ctx, capture.ID, []string{model.StateQueued}, state, startFields); err != nil {
		u.log.Warn("packet capture: persist dispatch state failed", slog.Uint64("capture_id", capture.ID), slog.Any("err", err))
	} else if refreshed, getErr := u.repo.Get(ctx, capture.ID); getErr == nil {
		capture = refreshed
	}
	return &CreateOutput{Capture: capture, Edge: edgeTask}, nil
}

func (u *Usecase) List(ctx context.Context, filter ListFilter) ([]*model.Capture, int64, error) {
	if u == nil || u.repo == nil {
		return nil, 0, errs.ErrNotWiredYet
	}
	return u.repo.List(ctx, filter)
}

func (u *Usecase) Get(ctx context.Context, id uint64) (*model.Capture, error) {
	if u == nil || u.repo == nil {
		return nil, errs.ErrNotWiredYet
	}
	if id == 0 {
		return nil, fmt.Errorf("%w: capture id required", errs.ErrInvalid)
	}
	return u.repo.Get(ctx, id)
}

// GetArtifact returns a completed packet artifact by its public opaque ID.
func (u *Usecase) GetArtifact(ctx context.Context, artifactID string) (*model.Capture, error) {
	if u == nil || u.repo == nil {
		return nil, errs.ErrNotWiredYet
	}
	artifactID = strings.TrimSpace(artifactID)
	if !strings.HasPrefix(artifactID, "pcap-") {
		return nil, fmt.Errorf("%w: packet artifact id required", errs.ErrInvalid)
	}
	if _, err := uuid.Parse(strings.TrimPrefix(artifactID, "pcap-")); err != nil {
		return nil, fmt.Errorf("%w: invalid packet artifact id", errs.ErrInvalid)
	}
	return u.repo.GetByArtifactID(ctx, artifactID)
}

func (u *Usecase) RawObject(ctx context.Context, id uint64) (*model.Capture, RawObject, error) {
	if u == nil || u.repo == nil || u.rawStore == nil {
		return nil, RawObject{}, errs.ErrNotWiredYet
	}
	capture, err := u.Get(ctx, id)
	if err != nil {
		return nil, RawObject{}, err
	}
	if capture.RawObjectKey == "" {
		return nil, RawObject{}, errs.ErrNotFound
	}
	data, err := u.rawStore.Read(ctx, capture.RawObjectKey)
	if err != nil {
		return nil, RawObject{}, err
	}
	if capture.CapturedBytes != 0 && uint64(len(data)) != capture.CapturedBytes {
		return nil, RawObject{}, fmt.Errorf("%w: packet capture raw size mismatch", errs.ErrInvalid)
	}
	sum := sha256.Sum256(data)
	sha256Hex := hex.EncodeToString(sum[:])
	if capture.RawSHA256 != "" && !strings.EqualFold(capture.RawSHA256, sha256Hex) {
		return nil, RawObject{}, fmt.Errorf("%w: packet capture raw checksum mismatch", errs.ErrInvalid)
	}
	return capture, RawObject{
		Key:       capture.RawObjectKey,
		SHA256Hex: sha256Hex,
		SizeBytes: uint64(len(data)),
		Data:      data,
	}, nil
}

func (u *Usecase) Refresh(ctx context.Context, id uint64) (*model.Capture, error) {
	if u == nil || u.repo == nil || u.caller == nil {
		return nil, errs.ErrNotWiredYet
	}
	capture, err := u.Get(ctx, id)
	if err != nil {
		return nil, err
	}
	edgeCaptureID := edgeCaptureIDFromResolved(capture.ResolvedTargetJSON)
	if edgeCaptureID == "" {
		return capture, nil
	}
	body, err := json.Marshal(tunnel.PacketCaptureGetRequest{CaptureID: edgeCaptureID})
	if err != nil {
		return nil, fmt.Errorf("packet capture: marshal refresh request: %w", err)
	}
	callCtx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	respBody, err := u.caller.Call(callCtx, capture.EdgeID, tunnel.MethodGetPacketCapture, body)
	if err != nil {
		return nil, fmt.Errorf("packet capture: refresh edge status: %w", err)
	}
	var edgeTask tunnel.PacketCaptureTask
	if err := json.Unmarshal(respBody, &edgeTask); err != nil {
		return nil, fmt.Errorf("packet capture: decode refresh response: %w", err)
	}
	capturedBytes := edgeTask.Result.FileBytes
	if edgeTask.State == "running" || capturedBytes == 0 {
		// The edge only knows the final pcap file length once it closes the
		// writer. During capture, payload bytes are the accurate live counter.
		capturedBytes = edgeTask.Result.PayloadBytes
	}
	fields := map[string]any{
		"captured_bytes":   uint64(capturedBytes),
		"captured_packets": uint64(edgeTask.Result.Packets),
		"error_detail":     edgeTask.Error,
	}
	if preview, marshalErr := json.Marshal(edgeTask.Result.LivePreview); marshalErr == nil {
		fields["live_preview_json"] = string(preview)
	} else {
		u.log.Warn("packet capture: marshal live preview", slog.Any("err", marshalErr))
	}
	if edgeTask.StartedAt != nil {
		fields["started_at"] = *edgeTask.StartedAt
	}
	if edgeTask.FinishedAt != nil {
		fields["finished_at"] = *edgeTask.FinishedAt
	}
	nextState := mapEdgeState(edgeTask.State)
	if nextState == model.StateFailed {
		u.discardFailedCapture(ctx, capture, errors.New(edgeTask.Error))
		return nil, fmt.Errorf("packet capture: edge completed without artifact: %w", errs.ErrInvalid)
	}
	if err := u.repo.Transition(ctx, capture.ID, refreshableStates(), nextState, fields); err != nil && !errors.Is(err, errs.ErrConflict) {
		return nil, err
	}
	refreshed, err := u.repo.Get(ctx, capture.ID)
	if err != nil {
		return nil, err
	}
	if nextState == model.StateReady {
		if processed, processErr := u.ingestCompletedCapture(ctx, refreshed); processErr == nil && processed != nil {
			return processed, nil
		} else if processErr != nil {
			latest, preserveErr := u.preserveFailedIngestion(ctx, refreshed.ID, processErr)
			if preserveErr != nil {
				return nil, fmt.Errorf("packet capture: preserve failed artifact: %w", preserveErr)
			}
			if latest != nil && latest.State == model.StateReady && latest.ParsedJSON != "" {
				return latest, nil
			}
			return nil, fmt.Errorf("packet capture: publish artifact: %w", processErr)
		}
	}
	return refreshed, nil
}

// Cancel asks the owning edge to abandon a queued or active capture. Cancelled
// captures are deliberately not uploaded or published as artifacts.
func (u *Usecase) Cancel(ctx context.Context, id uint64) (*model.Capture, error) {
	if u == nil || u.repo == nil || u.caller == nil {
		return nil, errs.ErrNotWiredYet
	}
	capture, err := u.Get(ctx, id)
	if err != nil {
		return nil, err
	}
	edgeCaptureID := edgeCaptureIDFromResolved(capture.ResolvedTargetJSON)
	if edgeCaptureID == "" {
		return nil, fmt.Errorf("%w: edge capture id missing", errs.ErrInvalid)
	}
	body, err := json.Marshal(tunnel.PacketCaptureCancelRequest{CaptureID: edgeCaptureID})
	if err != nil {
		return nil, fmt.Errorf("packet capture: marshal cancel request: %w", err)
	}
	callCtx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	respBody, err := u.caller.Call(callCtx, capture.EdgeID, tunnel.MethodCancelPacketCapture, body)
	if err != nil {
		return nil, fmt.Errorf("packet capture: cancel edge capture: %w", err)
	}
	var edgeTask tunnel.PacketCaptureTask
	if err := json.Unmarshal(respBody, &edgeTask); err != nil {
		return nil, fmt.Errorf("packet capture: decode cancel response: %w", err)
	}
	fields := map[string]any{"error_detail": edgeTask.Error}
	if edgeTask.FinishedAt != nil {
		fields["finished_at"] = *edgeTask.FinishedAt
	}
	if err := u.repo.Transition(ctx, capture.ID, refreshableStates(), mapEdgeState(edgeTask.State), fields); err != nil && !errors.Is(err, errs.ErrConflict) {
		return nil, err
	}
	return u.repo.Get(ctx, capture.ID)
}

// Stop asks the owning edge to gracefully finish a running capture. The edge
// keeps its flushed PCAP prefix and subsequently reports succeeded, allowing
// Refresh to use the normal upload and parser pipeline.
func (u *Usecase) Stop(ctx context.Context, id uint64) (*model.Capture, error) {
	if u == nil || u.repo == nil || u.caller == nil {
		return nil, errs.ErrNotWiredYet
	}
	capture, err := u.Get(ctx, id)
	if err != nil {
		return nil, err
	}
	edgeCaptureID := edgeCaptureIDFromResolved(capture.ResolvedTargetJSON)
	if edgeCaptureID == "" {
		return nil, fmt.Errorf("%w: edge capture id missing", errs.ErrInvalid)
	}
	body, err := json.Marshal(tunnel.PacketCaptureStopRequest{CaptureID: edgeCaptureID})
	if err != nil {
		return nil, fmt.Errorf("packet capture: marshal stop request: %w", err)
	}
	callCtx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	respBody, err := u.caller.Call(callCtx, capture.EdgeID, tunnel.MethodStopPacketCapture, body)
	if err != nil {
		return nil, fmt.Errorf("packet capture: stop edge capture: %w", err)
	}
	var edgeTask tunnel.PacketCaptureTask
	if err := json.Unmarshal(respBody, &edgeTask); err != nil {
		return nil, fmt.Errorf("packet capture: decode stop response: %w", err)
	}
	fields := map[string]any{"error_detail": edgeTask.Error}
	if edgeTask.StartedAt != nil {
		fields["started_at"] = *edgeTask.StartedAt
	}
	if edgeTask.FinishedAt != nil {
		fields["finished_at"] = *edgeTask.FinishedAt
	}
	if err := u.repo.Transition(ctx, capture.ID, refreshableStates(), mapEdgeState(edgeTask.State), fields); err != nil && !errors.Is(err, errs.ErrConflict) {
		return nil, err
	}
	return u.repo.Get(ctx, capture.ID)
}

func (u *Usecase) ingestCompletedCapture(ctx context.Context, capture *model.Capture) (*model.Capture, error) {
	if capture == nil || capture.ID == 0 || capture.ParsedJSON != "" || capture.State == model.StateParsing {
		return capture, nil
	}
	if u.rawStore == nil {
		return capture, nil
	}
	if capture.RawObjectKey == "" && edgeCaptureIDFromResolved(capture.ResolvedTargetJSON) == "" {
		return capture, nil
	}
	// ready -> parsing 是解析所有权的唯一入口。轮询、停止请求和后台协调器
	// 即使同时看到 edge succeeded，也只能有一个调用方读取并解析 PCAP。
	if err := u.repo.Transition(ctx, capture.ID, []string{model.StateReady}, model.StateParsing, nil); err != nil {
		if errors.Is(err, errs.ErrConflict) {
			return u.repo.Get(ctx, capture.ID)
		}
		return nil, err
	}
	refreshed, err := u.repo.Get(ctx, capture.ID)
	if err != nil {
		return nil, err
	}
	if refreshed.ParsedJSON != "" {
		return refreshed, nil
	}

	var raw RawObject
	if refreshed.RawObjectKey != "" {
		data, readErr := u.rawStore.Read(ctx, refreshed.RawObjectKey)
		if readErr != nil {
			return nil, fmt.Errorf("packet capture: read retained raw object: %w", readErr)
		}
		sum := sha256.Sum256(data)
		sha256Hex := hex.EncodeToString(sum[:])
		if refreshed.RawSHA256 != "" && !strings.EqualFold(refreshed.RawSHA256, sha256Hex) {
			return nil, fmt.Errorf("%w: packet capture raw checksum mismatch", errs.ErrInvalid)
		}
		raw = RawObject{Key: refreshed.RawObjectKey, SHA256Hex: sha256Hex, SizeBytes: uint64(len(data)), Data: data}
	} else {
		edgeCaptureID := edgeCaptureIDFromResolved(refreshed.ResolvedTargetJSON)
		if edgeCaptureID == "" {
			return refreshed, nil
		}
		body, marshalErr := json.Marshal(tunnel.PacketCaptureReadRequest{
			CaptureID: edgeCaptureID,
			MaxBytes:  refreshed.MaxBytes,
		})
		if marshalErr != nil {
			return nil, fmt.Errorf("packet capture: marshal read request: %w", marshalErr)
		}
		callCtx, cancel := context.WithTimeout(ctx, 30*time.Second)
		defer cancel()
		respBody, callErr := u.caller.Call(callCtx, refreshed.EdgeID, tunnel.MethodReadPacketCapture, body)
		if callErr != nil {
			return nil, fmt.Errorf("packet capture: read raw object from edge: %w", callErr)
		}
		var edgeRaw tunnel.PacketCaptureReadResponse
		if unmarshalErr := json.Unmarshal(respBody, &edgeRaw); unmarshalErr != nil {
			return nil, fmt.Errorf("packet capture: decode raw object response: %w", unmarshalErr)
		}
		data, decodeErr := base64.StdEncoding.DecodeString(edgeRaw.DataBase64)
		if decodeErr != nil {
			return nil, fmt.Errorf("packet capture: decode raw object: %w", decodeErr)
		}
		if uint64(len(data)) != edgeRaw.SizeBytes {
			return nil, fmt.Errorf("%w: packet capture raw size mismatch", errs.ErrInvalid)
		}
		sum := sha256.Sum256(data)
		sha256Hex := hex.EncodeToString(sum[:])
		if edgeRaw.SHA256Hex != "" && !strings.EqualFold(edgeRaw.SHA256Hex, sha256Hex) {
			return nil, fmt.Errorf("%w: packet capture raw checksum mismatch", errs.ErrInvalid)
		}
		key, savedSHA, size, saveErr := u.rawStore.Save(ctx, capture.ID, data)
		if saveErr != nil {
			return nil, saveErr
		}
		raw = RawObject{Key: key, SHA256Hex: savedSHA, SizeBytes: size, Data: data}
		if setErr := u.repo.SetRawObject(ctx, capture.ID, raw.Key, raw.SHA256Hex, raw.SizeBytes); setErr != nil {
			return nil, setErr
		}
		refreshed, err = u.repo.Get(ctx, capture.ID)
		if err != nil {
			return nil, err
		}
	}
	if u.parser == nil {
		return nil, fmt.Errorf("%w: packet parser is required to publish an artifact", errs.ErrNotWiredYet)
	}
	parsed, err := u.parser.Parse(ctx, refreshed, raw)
	if err != nil {
		return nil, fmt.Errorf("packet capture: parse raw object: %w", err)
	}
	artifactID := strings.TrimSpace(refreshed.ArtifactID)
	if artifactID == "" {
		return nil, fmt.Errorf("%w: packet artifact id required", errs.ErrInvalid)
	}
	parsed.ArtifactID = artifactID
	parsedJSON, err := marshalJSON(parsed)
	if err != nil {
		return nil, err
	}
	if err := u.repo.SetParsedArtifact(ctx, capture.ID, artifactID, parsedJSON); err != nil {
		return nil, err
	}
	if err := u.repo.Transition(ctx, capture.ID, []string{model.StateParsing}, model.StateReady, nil); err != nil {
		if errors.Is(err, errs.ErrConflict) {
			latest, getErr := u.repo.Get(ctx, capture.ID)
			if getErr == nil && latest.State == model.StateReady && latest.ParsedJSON != "" {
				return latest, nil
			}
		}
		return nil, err
	}
	return u.repo.Get(ctx, capture.ID)
}

func (u *Usecase) preserveFailedIngestion(ctx context.Context, captureID uint64, cause error) (*model.Capture, error) {
	fields := map[string]any{
		"error_code":   "artifact_publish_failed",
		"error_detail": cause.Error(),
	}
	if err := u.repo.Transition(ctx, captureID, []string{model.StateParsing}, model.StateFailed, fields); err != nil && !errors.Is(err, errs.ErrConflict) {
		return nil, err
	}
	return u.repo.Get(ctx, captureID)
}

func (u *Usecase) discardFailedCapture(ctx context.Context, capture *model.Capture, cause error) {
	if u == nil || u.repo == nil || capture == nil || capture.ID == 0 {
		return
	}
	// Ingestion writes the raw object before it updates the parsed artifact.
	// Reload so cleanup also sees a raw key written by an earlier failed step.
	if capture.RawObjectKey == "" {
		if latest, err := u.repo.Get(ctx, capture.ID); err == nil && latest != nil {
			capture = latest
		}
	}
	if capture.RawObjectKey != "" && u.rawStore != nil {
		if err := u.rawStore.Delete(ctx, capture.RawObjectKey); err != nil {
			u.log.Warn("packet capture: discard raw object", slog.Uint64("capture_id", capture.ID), slog.Any("err", err))
		}
	}
	if err := u.repo.Delete(ctx, capture.ID); err != nil && !errors.Is(err, errs.ErrNotFound) {
		u.log.Warn("packet capture: discard failed capture", slog.Uint64("capture_id", capture.ID), slog.Any("cause", cause), slog.Any("err", err))
	}
}

type normalizedCreateInput struct {
	DeviceID              uint64     `json:"device_id"`
	Interface             string     `json:"interface"`
	NetworkNamespace      string     `json:"network_namespace,omitempty"`
	Filter                string     `json:"filter,omitempty"`
	DurationSeconds       int        `json:"duration_seconds"`
	MaxBytes              int64      `json:"max_bytes"`
	MaxPackets            int        `json:"max_packets"`
	Snaplen               int        `json:"snaplen"`
	Promiscuous           bool       `json:"promiscuous"`
	Title                 string     `json:"title,omitempty"`
	Description           string     `json:"description,omitempty"`
	Source                string     `json:"source"`
	CreatedBy             uint64     `json:"created_by"`
	RequestIdempotencyKey string     `json:"request_idempotency_key,omitempty"`
	SessionID             uint64     `json:"session_id,omitempty"`
	PlannedStartAt        *time.Time `json:"planned_start_at,omitempty"`
}

func normalizeCreateInput(in CreateInput) (normalizedCreateInput, error) {
	out := normalizedCreateInput{
		DeviceID:              in.DeviceID,
		Interface:             strings.TrimSpace(in.Interface),
		NetworkNamespace:      strings.TrimSpace(in.NetworkNamespace),
		Filter:                strings.TrimSpace(in.Filter),
		DurationSeconds:       in.DurationSeconds,
		MaxBytes:              in.MaxBytes,
		MaxPackets:            in.MaxPackets,
		Snaplen:               in.Snaplen,
		Promiscuous:           in.Promiscuous,
		Title:                 strings.TrimSpace(in.Title),
		Description:           strings.TrimSpace(in.Description),
		Source:                strings.TrimSpace(in.Source),
		CreatedBy:             in.CreatedBy,
		RequestIdempotencyKey: strings.TrimSpace(in.RequestIdempotencyKey),
		SessionID:             in.SessionID,
		PlannedStartAt:        in.PlannedStartAt,
	}
	if out.DeviceID == 0 {
		return normalizedCreateInput{}, fmt.Errorf("%w: device_id required", errs.ErrInvalid)
	}
	if out.Interface == "" || strings.ContainsAny(out.Interface, "/\\\x00") || len(out.Interface) > 15 {
		return normalizedCreateInput{}, fmt.Errorf("%w: valid interface required", errs.ErrInvalid)
	}
	if !validNetworkNamespace(out.NetworkNamespace) {
		return normalizedCreateInput{}, fmt.Errorf("%w: invalid network_namespace", errs.ErrInvalid)
	}
	if out.DurationSeconds <= 0 {
		out.DurationSeconds = defaultDurationSeconds
	}
	if out.DurationSeconds > maxDurationSeconds {
		return normalizedCreateInput{}, fmt.Errorf("%w: duration_seconds exceeds %d", errs.ErrInvalid, maxDurationSeconds)
	}
	if out.MaxBytes <= 0 {
		out.MaxBytes = defaultMaxBytes
	}
	if out.MaxBytes > maxCaptureBytes {
		return normalizedCreateInput{}, fmt.Errorf("%w: max_bytes exceeds %d", errs.ErrInvalid, maxCaptureBytes)
	}
	if out.MaxPackets <= 0 {
		out.MaxPackets = defaultMaxPackets
	}
	if out.MaxPackets > maxCapturePackets {
		return normalizedCreateInput{}, fmt.Errorf("%w: max_packets exceeds %d", errs.ErrInvalid, maxCapturePackets)
	}
	if out.Snaplen <= 0 {
		out.Snaplen = defaultSnaplen
	}
	if out.Snaplen > maxCaptureSnaplen {
		return normalizedCreateInput{}, fmt.Errorf("%w: snaplen exceeds %d", errs.ErrInvalid, maxCaptureSnaplen)
	}
	if out.Source == "" {
		out.Source = SourceAPI
	}
	if out.Title == "" {
		out.Title = fmt.Sprintf("Packet capture on device %d %s", out.DeviceID, out.Interface)
	}
	return out, nil
}

func validNetworkNamespace(namespace string) bool {
	if namespace == "" {
		return true
	}
	if len(namespace) > 128 {
		return false
	}
	for _, r := range namespace {
		if (r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') || (r >= '0' && r <= '9') || r == '_' || r == '-' || r == '.' || r == ':' {
			continue
		}
		return false
	}
	return true
}

func mapEdgeState(state string) string {
	switch strings.TrimSpace(state) {
	case "queued":
		return model.StateQueued
	case "running":
		return model.StateCapturing
	case "succeeded":
		return model.StateReady
	case "cancelled":
		return model.StateCancelled
	case "failed":
		return model.StateFailed
	default:
		return model.StateQueued
	}
}

func marshalJSON(v any) (string, error) {
	b, err := json.Marshal(v)
	if err != nil {
		return "", fmt.Errorf("packet capture: marshal json: %w", err)
	}
	return string(b), nil
}

func mustJSON(v any) string {
	b, err := json.Marshal(v)
	if err != nil {
		return "{}"
	}
	return string(b)
}

func edgeCaptureIDFromResolved(raw string) string {
	if strings.TrimSpace(raw) == "" {
		return ""
	}
	var target struct {
		CaptureID string `json:"capture_id"`
	}
	if err := json.Unmarshal([]byte(raw), &target); err != nil {
		return ""
	}
	return strings.TrimSpace(target.CaptureID)
}

func refreshableStates() []string {
	return []string{
		model.StateQueued,
		model.StateDispatching,
		model.StateCapturing,
		model.StateUploading,
	}
}
