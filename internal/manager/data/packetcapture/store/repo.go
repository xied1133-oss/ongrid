// Package store persists packet-capture task metadata. It deliberately owns no
// object-store client: blob writes happen outside database transactions and
// are reconciled through the state transition methods below.
package store

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"
	"gorm.io/gorm"

	bizpacketcapture "github.com/ongridio/ongrid/internal/manager/biz/packetcapture"
	model "github.com/ongridio/ongrid/internal/manager/model/packetcapture"
	"github.com/ongridio/ongrid/internal/pkg/errs"
)

func Migrate(db *gorm.DB) error {
	migrator := db.Migrator()
	if migrator.HasIndex(&model.Capture{}, "idx_packet_capture_idempotency") {
		if err := migrator.DropIndex(&model.Capture{}, "idx_packet_capture_idempotency"); err != nil {
			return fmt.Errorf("packet capture: drop idempotency index: %w", err)
		}
	}
	if err := db.AutoMigrate(&model.Session{}, &model.Capture{}); err != nil {
		return err
	}
	return migrateLegacyArtifactIDs(db)
}

// migrateLegacyArtifactIDs makes the public artifact namespace opaque for
// captures created before packet artifacts used UUID identifiers. Only
// published artifacts are migrated; failed/incomplete captures are never
// exposed by the artifact API.
func migrateLegacyArtifactIDs(db *gorm.DB) error {
	var captures []model.Capture
	if err := db.Where("state = ? AND parsed_json <> ''", model.StateReady).Find(&captures).Error; err != nil {
		return fmt.Errorf("packet capture: list legacy artifacts: %w", err)
	}
	for _, capture := range captures {
		if validArtifactID(capture.ArtifactID) {
			continue
		}
		artifactID := "pcap-" + uuid.NewString()
		parsedJSON, err := rewriteArtifactID(capture.ParsedJSON, artifactID)
		if err != nil {
			return fmt.Errorf("packet capture: rewrite legacy artifact %d: %w", capture.ID, err)
		}
		if err := db.Model(&model.Capture{}).Where("id = ?", capture.ID).Updates(map[string]any{
			"artifact_id": artifactID,
			"parsed_json": parsedJSON,
		}).Error; err != nil {
			return fmt.Errorf("packet capture: migrate legacy artifact %d: %w", capture.ID, err)
		}
	}
	return nil
}

func validArtifactID(artifactID string) bool {
	artifactID = strings.TrimSpace(artifactID)
	if !strings.HasPrefix(artifactID, "pcap-") {
		return false
	}
	_, err := uuid.Parse(strings.TrimPrefix(artifactID, "pcap-"))
	return err == nil
}

func rewriteArtifactID(parsedJSON, artifactID string) (string, error) {
	var parsed map[string]any
	if err := json.Unmarshal([]byte(parsedJSON), &parsed); err != nil {
		return "", fmt.Errorf("decode parsed artifact: %w", err)
	}
	parsed["artifact_id"] = artifactID
	encoded, err := json.Marshal(parsed)
	if err != nil {
		return "", fmt.Errorf("encode parsed artifact: %w", err)
	}
	return string(encoded), nil
}

type Repo struct{ db *gorm.DB }

func New(db *gorm.DB) *Repo { return &Repo{db: db} }

func (r *Repo) Create(ctx context.Context, capture *model.Capture) error {
	if capture == nil {
		return fmt.Errorf("packet capture: create nil capture: %w", errs.ErrInvalid)
	}
	if err := r.db.WithContext(ctx).Create(capture).Error; err != nil {
		if isDuplicate(err) {
			return errs.ErrConflict
		}
		return fmt.Errorf("packet capture: create: %w", err)
	}
	return nil
}

func (r *Repo) Get(ctx context.Context, id uint64) (*model.Capture, error) {
	var capture model.Capture
	if err := r.db.WithContext(ctx).First(&capture, "id = ?", id).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, errs.ErrNotFound
		}
		return nil, fmt.Errorf("packet capture: get: %w", err)
	}
	return &capture, nil
}

func (r *Repo) GetByArtifactID(ctx context.Context, artifactID string) (*model.Capture, error) {
	artifactID = strings.TrimSpace(artifactID)
	if artifactID == "" {
		return nil, fmt.Errorf("packet capture: artifact id required: %w", errs.ErrInvalid)
	}
	var capture model.Capture
	if err := r.db.WithContext(ctx).
		Where("artifact_id = ? AND state = ? AND parsed_json <> ''", artifactID, model.StateReady).
		First(&capture).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, errs.ErrNotFound
		}
		return nil, fmt.Errorf("packet capture: get artifact: %w", err)
	}
	return &capture, nil
}

func (r *Repo) GetByIdempotencyKey(ctx context.Context, key string) (*model.Capture, error) {
	var capture model.Capture
	if err := r.db.WithContext(ctx).First(&capture, "request_idempotency_key = ?", key).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, errs.ErrNotFound
		}
		return nil, fmt.Errorf("packet capture: get idempotency key: %w", err)
	}
	return &capture, nil
}

func (r *Repo) List(ctx context.Context, filter bizpacketcapture.ListFilter) ([]*model.Capture, int64, error) {
	if filter.Limit <= 0 || filter.Limit > 200 {
		filter.Limit = 50
	}
	if filter.Offset < 0 {
		filter.Offset = 0
	}
	db := r.db.WithContext(ctx).Model(&model.Capture{}).Where("state = ? AND parsed_json <> ''", model.StateReady)
	if filter.EdgeID != 0 {
		db = db.Where("edge_id = ?", filter.EdgeID)
	}
	if filter.DeviceID != 0 {
		db = db.Where("device_id = ?", filter.DeviceID)
	}
	if state := strings.TrimSpace(filter.State); state != "" {
		db = db.Where("state = ?", state)
	}
	var total int64
	if err := db.Count(&total).Error; err != nil {
		return nil, 0, fmt.Errorf("packet capture: count: %w", err)
	}
	var captures []*model.Capture
	if err := db.Order("created_at DESC, id DESC").Offset(filter.Offset).Limit(filter.Limit).Find(&captures).Error; err != nil {
		return nil, 0, fmt.Errorf("packet capture: list: %w", err)
	}
	return captures, total, nil
}

func (r *Repo) CreateSession(ctx context.Context, session *model.Session) error {
	if session == nil || strings.TrimSpace(session.PublicID) == "" {
		return fmt.Errorf("packet capture: create session input: %w", errs.ErrInvalid)
	}
	if err := r.db.WithContext(ctx).Create(session).Error; err != nil {
		if isDuplicate(err) {
			return errs.ErrConflict
		}
		return fmt.Errorf("packet capture: create session: %w", err)
	}
	return nil
}

func (r *Repo) GetSession(ctx context.Context, publicID string) (*model.Session, error) {
	var session model.Session
	if err := r.db.WithContext(ctx).Where("public_id = ?", strings.TrimSpace(publicID)).First(&session).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, errs.ErrNotFound
		}
		return nil, fmt.Errorf("packet capture: get session: %w", err)
	}
	return &session, nil
}

func (r *Repo) ListSessions(ctx context.Context, limit, offset int) ([]*model.Session, int64, error) {
	if limit <= 0 || limit > 200 {
		limit = 50
	}
	if offset < 0 {
		offset = 0
	}
	db := r.db.WithContext(ctx).Model(&model.Session{})
	var total int64
	if err := db.Count(&total).Error; err != nil {
		return nil, 0, fmt.Errorf("packet capture: count sessions: %w", err)
	}
	var sessions []*model.Session
	if err := db.Order("created_at DESC, id DESC").Offset(offset).Limit(limit).Find(&sessions).Error; err != nil {
		return nil, 0, fmt.Errorf("packet capture: list sessions: %w", err)
	}
	return sessions, total, nil
}

func (r *Repo) ListBySessionID(ctx context.Context, sessionID uint64) ([]*model.Capture, error) {
	var captures []*model.Capture
	if err := r.db.WithContext(ctx).Where("session_id = ?", sessionID).Order("id ASC").Find(&captures).Error; err != nil {
		return nil, fmt.Errorf("packet capture: list session captures: %w", err)
	}
	return captures, nil
}

func (r *Repo) CountCapturesBySessionIDs(ctx context.Context, sessionIDs []uint64) (map[uint64]int, error) {
	out := make(map[uint64]int, len(sessionIDs))
	if len(sessionIDs) == 0 {
		return out, nil
	}
	type row struct {
		SessionID uint64
		Count     int64
	}
	var rows []row
	if err := r.db.WithContext(ctx).
		Model(&model.Capture{}).
		Select("session_id, COUNT(*) AS count").
		Where("session_id IN ?", sessionIDs).
		Group("session_id").
		Scan(&rows).Error; err != nil {
		return nil, fmt.Errorf("packet capture: count session captures: %w", err)
	}
	for _, row := range rows {
		out[row.SessionID] = int(row.Count)
	}
	return out, nil
}

func (r *Repo) SetSessionAnalysis(ctx context.Context, id uint64, state, analysisJSON string) error {
	res := r.db.WithContext(ctx).Model(&model.Session{}).Where("id = ?", id).Updates(map[string]any{"state": state, "analysis_json": analysisJSON})
	if res.Error != nil {
		return fmt.Errorf("packet capture: set session analysis: %w", res.Error)
	}
	if res.RowsAffected == 0 {
		return errs.ErrNotFound
	}
	return nil
}

func (r *Repo) ListReconcilableSessions(ctx context.Context, limit int) ([]*model.Session, error) {
	if limit <= 0 || limit > 200 {
		limit = 50
	}
	var sessions []*model.Session
	terminalStates := []string{model.SessionStateReady, model.SessionStatePartial, model.SessionStateCancelled, model.SessionStateFailed}
	if err := r.db.WithContext(ctx).
		Where("state = ? OR (state IN ? AND chat_session_id <> '' AND completion_notified_at IS NULL)", model.SessionStateCollecting, terminalStates).
		Order("created_at ASC, id ASC").
		Limit(limit).
		Find(&sessions).Error; err != nil {
		return nil, fmt.Errorf("packet capture: list reconcilable sessions: %w", err)
	}
	return sessions, nil
}

func (r *Repo) MarkSessionCompletionNotified(ctx context.Context, id uint64, at time.Time) (bool, error) {
	res := r.db.WithContext(ctx).Model(&model.Session{}).Where("id = ? AND completion_notified_at IS NULL", id).Update("completion_notified_at", at)
	if res.Error != nil {
		return false, fmt.Errorf("packet capture: mark completion notified: %w", res.Error)
	}
	return res.RowsAffected == 1, nil
}

func (r *Repo) SetSessionOperation(ctx context.Context, id uint64, operationID string) error {
	res := r.db.WithContext(ctx).Model(&model.Session{}).Where("id = ?", id).Update("operation_id", strings.TrimSpace(operationID))
	if res.Error != nil {
		return fmt.Errorf("packet capture: set session operation: %w", res.Error)
	}
	if res.RowsAffected == 0 {
		return errs.ErrNotFound
	}
	return nil
}

func (r *Repo) Delete(ctx context.Context, id uint64) error {
	if id == 0 {
		return fmt.Errorf("packet capture: delete input: %w", errs.ErrInvalid)
	}
	res := r.db.WithContext(ctx).Delete(&model.Capture{}, id)
	if res.Error != nil {
		return fmt.Errorf("packet capture: delete: %w", res.Error)
	}
	if res.RowsAffected == 0 {
		return errs.ErrNotFound
	}
	return nil
}

// Transition performs compare-and-set status movement. An edge retry cannot
// revive a cancelled/expired task because the expected current state is part
// of the update predicate.
func (r *Repo) Transition(ctx context.Context, id uint64, from []string, to string, fields map[string]any) error {
	if id == 0 || len(from) == 0 || strings.TrimSpace(to) == "" {
		return fmt.Errorf("packet capture: transition input: %w", errs.ErrInvalid)
	}
	updates := make(map[string]any, len(fields)+1)
	for key, value := range fields {
		updates[key] = value
	}
	updates["state"] = to
	res := r.db.WithContext(ctx).Model(&model.Capture{}).Where("id = ? AND state IN ?", id, from).Updates(updates)
	if res.Error != nil {
		return fmt.Errorf("packet capture: transition: %w", res.Error)
	}
	if res.RowsAffected == 0 {
		return errs.ErrConflict
	}
	return nil
}

func (r *Repo) SetRawObject(ctx context.Context, id uint64, objectKey, sha256Hex string, sizeBytes uint64) error {
	if id == 0 || strings.TrimSpace(objectKey) == "" || strings.TrimSpace(sha256Hex) == "" || sizeBytes == 0 {
		return fmt.Errorf("packet capture: set raw object input: %w", errs.ErrInvalid)
	}
	res := r.db.WithContext(ctx).Model(&model.Capture{}).Where("id = ?", id).Updates(map[string]any{
		"raw_object_key": objectKey,
		"raw_sha256":     strings.ToLower(sha256Hex),
		"captured_bytes": sizeBytes,
	})
	if res.Error != nil {
		return fmt.Errorf("packet capture: set raw object: %w", res.Error)
	}
	if res.RowsAffected == 0 {
		return errs.ErrNotFound
	}
	return nil
}

func (r *Repo) SetParsedArtifact(ctx context.Context, id uint64, artifactID string, parsedJSON string) error {
	if id == 0 || strings.TrimSpace(artifactID) == "" || strings.TrimSpace(parsedJSON) == "" {
		return fmt.Errorf("packet capture: set parsed artifact input: %w", errs.ErrInvalid)
	}
	res := r.db.WithContext(ctx).Model(&model.Capture{}).Where("id = ?", id).Updates(map[string]any{
		"artifact_id":  artifactID,
		"parsed_json":  parsedJSON,
		"error_code":   "",
		"error_detail": "",
	})
	if res.Error != nil {
		return fmt.Errorf("packet capture: set parsed artifact: %w", res.Error)
	}
	if res.RowsAffected == 0 {
		return errs.ErrNotFound
	}
	return nil
}

func isDuplicate(err error) bool {
	message := err.Error()
	return strings.Contains(message, "Error 1062") || strings.Contains(message, "UNIQUE constraint failed") || strings.Contains(message, "duplicate key")
}
