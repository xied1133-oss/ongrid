package store

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"

	"github.com/google/uuid"

	"github.com/glebarez/sqlite"
	"gorm.io/gorm"

	bizpacketcapture "github.com/ongridio/ongrid/internal/manager/biz/packetcapture"
	model "github.com/ongridio/ongrid/internal/manager/model/packetcapture"
	"github.com/ongridio/ongrid/internal/pkg/errs"
)

func TestRepo_CreateTransitionAndList(t *testing.T) {
	repo := newTestRepo(t)
	ctx := context.Background()
	capture := &model.Capture{
		RequestIdempotencyKey: "request-1", CreatedBy: 1, Source: "assistant", State: model.StateQueued,
		EdgeID: 9, TargetKind: "host_interface", RequestedTargetJSON: `{}`, ResolvedTargetJSON: `{}`,
		FilterJSON: `{}`, CanonicalFilter: "tcp", InterfaceName: "eth0", Description: "", LabelsJSON: "{}", ErrorDetail: "",
	}
	if err := repo.Create(ctx, capture); err != nil {
		t.Fatalf("Create: %v", err)
	}
	if err := repo.Transition(ctx, capture.ID, []string{model.StateQueued}, model.StateReady, map[string]any{"canonical_filter": "tcp and port 443", "parsed_json": `{}`}); err != nil {
		t.Fatalf("Transition: %v", err)
	}
	got, err := repo.Get(ctx, capture.ID)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if got.State != model.StateReady || got.CanonicalFilter != "tcp and port 443" {
		t.Fatalf("capture=%+v", got)
	}
	if err := repo.Transition(ctx, capture.ID, []string{model.StateQueued}, model.StateReady, nil); !errors.Is(err, errs.ErrConflict) {
		t.Fatalf("stale Transition error=%v, want conflict", err)
	}
	items, total, err := repo.List(ctx, bizpacketcapture.ListFilter{EdgeID: 9, State: model.StateReady})
	if err != nil || total != 1 || len(items) != 1 {
		t.Fatalf("List=%d/%d err=%v", len(items), total, err)
	}
}

func TestRepo_CreateAllowsEmptyIdempotencyKey(t *testing.T) {
	repo := newTestRepo(t)
	ctx := context.Background()
	for i := 0; i < 2; i++ {
		err := repo.Create(ctx, &model.Capture{CreatedBy: 1, Source: "ui", State: model.StateReady, EdgeID: 1, ArtifactID: fmt.Sprintf("pcap-%d", i), ParsedJSON: `{}`, RequestedTargetJSON: `{}`, ResolvedTargetJSON: `{}`, FilterJSON: `{}`, Description: "", LabelsJSON: "{}", ErrorDetail: ""})
		if err != nil {
			t.Fatalf("Create %d: %v", i, err)
		}
	}
	_, total, err := repo.List(ctx, bizpacketcapture.ListFilter{})
	if err != nil || total != 2 {
		t.Fatalf("total=%d err=%v", total, err)
	}
}

func TestRepo_SetRawObjectKeepsGeneratedPacketArtifactID(t *testing.T) {
	repo := newTestRepo(t)
	ctx := context.Background()
	capture := &model.Capture{
		CreatedBy: 1, Source: "chat", State: model.StateReady, EdgeID: 1,
		RequestedTargetJSON: `{}`, ResolvedTargetJSON: `{}`, FilterJSON: `{}`,
		Description: "", LabelsJSON: "{}", ErrorDetail: "", ArtifactID: "pcap-123e4567-e89b-12d3-a456-426614174000",
	}
	if err := repo.Create(ctx, capture); err != nil {
		t.Fatalf("Create: %v", err)
	}
	if err := repo.SetRawObject(ctx, capture.ID, "capture.pcap", "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa", 128); err != nil {
		t.Fatalf("SetRawObject: %v", err)
	}
	got, err := repo.Get(ctx, capture.ID)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if got.ArtifactID != "pcap-123e4567-e89b-12d3-a456-426614174000" {
		t.Fatalf("ArtifactID=%q, want generated UUID artifact id", got.ArtifactID)
	}
}

func TestRepo_ListReturnsPublishedArtifactsOnly(t *testing.T) {
	repo := newTestRepo(t)
	ctx := context.Background()
	for _, capture := range []*model.Capture{
		{CreatedBy: 1, Source: "api", State: model.StateReady, EdgeID: 1, ArtifactID: "pcap-ready", ParsedJSON: `{}`, RequestedTargetJSON: `{}`, ResolvedTargetJSON: `{}`, FilterJSON: `{}`, Description: "", LabelsJSON: "", ErrorDetail: ""},
		{CreatedBy: 1, Source: "api", State: model.StateReady, EdgeID: 1, ArtifactID: "pcap-unparsed", RequestedTargetJSON: `{}`, ResolvedTargetJSON: `{}`, FilterJSON: `{}`, Description: "", LabelsJSON: "", ErrorDetail: ""},
		{CreatedBy: 1, Source: "api", State: model.StateFailed, EdgeID: 1, ArtifactID: "pcap-failed", ParsedJSON: `{}`, RequestedTargetJSON: `{}`, ResolvedTargetJSON: `{}`, FilterJSON: `{}`, Description: "", LabelsJSON: "", ErrorDetail: ""},
	} {
		if err := repo.Create(ctx, capture); err != nil {
			t.Fatalf("Create: %v", err)
		}
	}
	items, total, err := repo.List(ctx, bizpacketcapture.ListFilter{})
	if err != nil || total != 1 || len(items) != 1 || items[0].ArtifactID != "pcap-ready" {
		t.Fatalf("List=%+v total=%d err=%v", items, total, err)
	}
}

func TestRepo_GetByArtifactIDReturnsPublishedArtifactOnly(t *testing.T) {
	repo := newTestRepo(t)
	ctx := context.Background()
	for _, capture := range []*model.Capture{
		{CreatedBy: 1, Source: "api", State: model.StateReady, EdgeID: 1, ArtifactID: "pcap-123e4567-e89b-12d3-a456-426614174000", ParsedJSON: `{}`, RequestedTargetJSON: `{}`, ResolvedTargetJSON: `{}`, FilterJSON: `{}`, Description: "", LabelsJSON: "", ErrorDetail: ""},
		{CreatedBy: 1, Source: "api", State: model.StateReady, EdgeID: 1, ArtifactID: "pcap-unpublished", RequestedTargetJSON: `{}`, ResolvedTargetJSON: `{}`, FilterJSON: `{}`, Description: "", LabelsJSON: "", ErrorDetail: ""},
	} {
		if err := repo.Create(ctx, capture); err != nil {
			t.Fatalf("Create: %v", err)
		}
	}
	got, err := repo.GetByArtifactID(ctx, "pcap-123e4567-e89b-12d3-a456-426614174000")
	if err != nil || got.ArtifactID != "pcap-123e4567-e89b-12d3-a456-426614174000" {
		t.Fatalf("GetByArtifactID got=%+v err=%v", got, err)
	}
	if _, err := repo.GetByArtifactID(ctx, "pcap-unpublished"); !errors.Is(err, errs.ErrNotFound) {
		t.Fatalf("GetByArtifactID unpublished err=%v, want not found", err)
	}
}

func TestMigrateConvertsLegacyPublishedArtifactIDToUUID(t *testing.T) {
	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{})
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	if err := db.AutoMigrate(&model.Capture{}); err != nil {
		t.Fatalf("initial migrate: %v", err)
	}
	legacy := &model.Capture{
		CreatedBy: 1, Source: "api", State: model.StateReady, EdgeID: 1, ArtifactID: "pcap-17", ParsedJSON: `{"artifact_id":"pcap-17","summary":{}}`,
		RequestedTargetJSON: `{}`, ResolvedTargetJSON: `{}`, FilterJSON: `{}`, Description: "", LabelsJSON: "", ErrorDetail: "",
	}
	if err := db.Create(legacy).Error; err != nil {
		t.Fatalf("seed legacy artifact: %v", err)
	}
	if err := Migrate(db); err != nil {
		t.Fatalf("Migrate: %v", err)
	}
	var got model.Capture
	if err := db.First(&got, legacy.ID).Error; err != nil {
		t.Fatalf("load migrated artifact: %v", err)
	}
	if !strings.HasPrefix(got.ArtifactID, "pcap-") {
		t.Fatalf("artifact id=%q", got.ArtifactID)
	}
	if _, err := uuid.Parse(strings.TrimPrefix(got.ArtifactID, "pcap-")); err != nil {
		t.Fatalf("artifact id=%q is not UUID: %v", got.ArtifactID, err)
	}
	if !strings.Contains(got.ParsedJSON, got.ArtifactID) {
		t.Fatalf("parsed JSON was not migrated: %s", got.ParsedJSON)
	}
}

func TestRepo_SetParsedArtifact(t *testing.T) {
	repo := newTestRepo(t)
	ctx := context.Background()
	capture := &model.Capture{
		CreatedBy: 1, Source: "chat", State: model.StateParsing, EdgeID: 1,
		RequestedTargetJSON: `{}`, ResolvedTargetJSON: `{}`, FilterJSON: `{}`,
		Description: "", LabelsJSON: "{}", ErrorDetail: "old error",
	}
	if err := repo.Create(ctx, capture); err != nil {
		t.Fatalf("Create: %v", err)
	}
	if err := repo.SetParsedArtifact(ctx, capture.ID, "pcap-custom", `{"summary":{"packets_seen":1}}`); err != nil {
		t.Fatalf("SetParsedArtifact: %v", err)
	}
	got, err := repo.Get(ctx, capture.ID)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if got.ArtifactID != "pcap-custom" || got.ParsedJSON == "" || got.ErrorDetail != "" {
		t.Fatalf("capture=%+v", got)
	}
}

func TestRepo_CountCapturesBySessionIDs(t *testing.T) {
	repo := newTestRepo(t)
	ctx := context.Background()
	for _, sessionID := range []uint64{7, 7, 9} {
		capture := &model.Capture{
			CreatedBy: 1, Source: "chat", State: model.StateReady, EdgeID: 1, SessionID: sessionID,
			RequestedTargetJSON: `{}`, ResolvedTargetJSON: `{}`, FilterJSON: `{}`,
			Description: "", LabelsJSON: "{}", ErrorDetail: "",
		}
		if err := repo.Create(ctx, capture); err != nil {
			t.Fatalf("Create session %d: %v", sessionID, err)
		}
	}
	counts, err := repo.CountCapturesBySessionIDs(ctx, []uint64{7, 8, 9})
	if err != nil {
		t.Fatalf("CountCapturesBySessionIDs: %v", err)
	}
	if counts[7] != 2 || counts[8] != 0 || counts[9] != 1 {
		t.Fatalf("counts=%+v", counts)
	}
}

func newTestRepo(t *testing.T) *Repo {
	t.Helper()
	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{})
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	if err := Migrate(db); err != nil {
		t.Fatalf("Migrate: %v", err)
	}
	return New(db)
}
