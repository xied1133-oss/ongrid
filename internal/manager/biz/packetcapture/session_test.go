package packetcapture

import (
	"context"
	"encoding/json"
	"fmt"
	"testing"
	"time"

	model "github.com/ongridio/ongrid/internal/manager/model/packetcapture"
	"github.com/ongridio/ongrid/internal/pkg/tunnel"
)

type sessionTestRepo struct {
	*fakeRepo
	sessions map[string]*model.Session
}

func (r *sessionTestRepo) CreateSession(_ context.Context, s *model.Session) error {
	s.ID = uint64(len(r.sessions) + 1)
	r.sessions[s.PublicID] = s
	return nil
}
func (r *sessionTestRepo) GetSession(_ context.Context, id string) (*model.Session, error) {
	s, ok := r.sessions[id]
	if !ok {
		return nil, fmt.Errorf("not found")
	}
	return s, nil
}
func (r *sessionTestRepo) ListSessions(context.Context, int, int) ([]*model.Session, int64, error) {
	out := make([]*model.Session, 0, len(r.sessions))
	for _, s := range r.sessions {
		out = append(out, s)
	}
	return out, int64(len(out)), nil
}
func (r *sessionTestRepo) ListBySessionID(_ context.Context, id uint64) ([]*model.Capture, error) {
	out := []*model.Capture{}
	for _, c := range r.byID {
		if c.SessionID == id {
			clone := *c
			out = append(out, &clone)
		}
	}
	return out, nil
}
func (r *sessionTestRepo) SetSessionAnalysis(_ context.Context, id uint64, state, raw string) error {
	for _, s := range r.sessions {
		if s.ID == id {
			s.State = state
			s.AnalysisJSON = raw
			return nil
		}
	}
	return fmt.Errorf("not found")
}

func (r *sessionTestRepo) ListReconcilableSessions(_ context.Context, _ int) ([]*model.Session, error) {
	out := make([]*model.Session, 0, len(r.sessions))
	for _, session := range r.sessions {
		if session.State == model.SessionStateCollecting ||
			(session.ChatSessionID != "" && session.CompletionNotifiedAt == nil && (session.State == model.SessionStateReady || session.State == model.SessionStatePartial || session.State == model.SessionStateCancelled || session.State == model.SessionStateFailed)) {
			out = append(out, session)
		}
	}
	return out, nil
}

func (r *sessionTestRepo) MarkSessionCompletionNotified(_ context.Context, id uint64, at time.Time) (bool, error) {
	for _, session := range r.sessions {
		if session.ID != id {
			continue
		}
		if session.CompletionNotifiedAt != nil {
			return false, nil
		}
		session.CompletionNotifiedAt = &at
		return true, nil
	}
	return false, fmt.Errorf("not found")
}

type sessionResolver map[uint64]uint64

func (r sessionResolver) ResolveEdgeID(_ context.Context, deviceID uint64) (uint64, error) {
	return r[deviceID], nil
}

func TestCreateSessionSchedulesMembersAtCommonTime(t *testing.T) {
	repo := &sessionTestRepo{fakeRepo: newFakeRepo(), sessions: map[string]*model.Session{}}
	caller := &fakeCaller{}
	uc := New(repo, caller, sessionResolver{101: 11, 102: 12}, nil)
	now := time.Date(2026, 8, 14, 10, 0, 0, 0, time.UTC)
	uc.now = func() time.Time { return now }
	out, err := uc.CreateSession(context.Background(), CreateSessionInput{Targets: []SessionTarget{{DeviceID: 101, Interface: "eth0", NetworkNamespace: "ongrid-netdev-a"}, {DeviceID: 102, Interface: "eth1", NetworkNamespace: "ongrid-netdev-a"}}, Filter: "tcp and port 443", DurationSeconds: 10, Source: SourceChat, ChatSessionID: "chat-session-1"})
	if err != nil {
		t.Fatalf("CreateSession: %v", err)
	}
	if len(out.Captures) != 2 || out.Session.PlannedStartAt != now.Add(sessionStartLeadTime) {
		t.Fatalf("output=%+v", out)
	}
	if out.Session.ChatSessionID != "chat-session-1" {
		t.Fatalf("chat session = %q", out.Session.ChatSessionID)
	}
	for _, capture := range out.Captures {
		if capture.SessionID != out.Session.ID {
			t.Fatalf("capture %d session=%d want %d", capture.ID, capture.SessionID, out.Session.ID)
		}
	}
	var wire tunnel.PacketCaptureStartRequest
	if err := json.Unmarshal([]byte(repo.byID[2].ResolvedTargetJSON), &wire); err != nil {
		t.Fatalf("decode wire: %v", err)
	}
	if wire.StartAt == nil || !wire.StartAt.Equal(out.Session.PlannedStartAt) {
		t.Fatalf("start_at=%v want %s", wire.StartAt, out.Session.PlannedStartAt)
	}
	if wire.NetworkNamespace != "ongrid-netdev-a" {
		t.Fatalf("network namespace = %q", wire.NetworkNamespace)
	}
}

func TestCreateSessionSupportsSingleCaptureTask(t *testing.T) {
	repo := &sessionTestRepo{fakeRepo: newFakeRepo(), sessions: map[string]*model.Session{}}
	caller := &fakeCaller{}
	uc := New(repo, caller, sessionResolver{101: 11}, nil)

	out, err := uc.CreateSession(context.Background(), CreateSessionInput{
		Targets: []SessionTarget{{DeviceID: 101, Interface: "eth0"}},
		Filter:  "tcp",
		Source:  SourceChat,
	})
	if err != nil {
		t.Fatalf("CreateSession: %v", err)
	}
	if out.Session == nil || len(out.Captures) != 1 {
		t.Fatalf("output=%+v", out)
	}
	if out.Captures[0].SessionID != out.Session.ID {
		t.Fatalf("capture session=%d want %d", out.Captures[0].SessionID, out.Session.ID)
	}
}

func TestReconcileActiveSessionsNotifiesReadySessionAfterInterruptedNotify(t *testing.T) {
	repo := &sessionTestRepo{fakeRepo: newFakeRepo(), sessions: map[string]*model.Session{}}
	uc := New(repo, &fakeCaller{}, sessionResolver{101: 11}, nil)
	session := &model.Session{
		ID:            7,
		PublicID:      "pcap-session-ready",
		State:         model.SessionStateReady,
		ChatSessionID: "chat-session-1",
		Title:         "HTTPS capture",
		AnalysisJSON:  "{}",
	}
	repo.sessions[session.PublicID] = session
	repo.byID[12] = &model.Capture{
		ID:              12,
		SessionID:       session.ID,
		State:           model.StateReady,
		ArtifactID:      "pcap-artifact-12",
		CapturedPackets: 3,
		ParsedJSON:      `{"packets":[{"source":"10.0.0.1","destination":"10.0.0.2","protocol":"TCP","index":{"srcport":"51234","dstport":"443"}}]}`,
	}

	var got CompletionEvent
	if err := uc.ReconcileActiveSessions(context.Background(), 50, func(_ context.Context, event CompletionEvent) error {
		got = event
		return nil
	}); err != nil {
		t.Fatalf("ReconcileActiveSessions: %v", err)
	}
	if got.Session == nil || got.Session.PublicID != session.PublicID {
		t.Fatalf("completion event = %+v", got)
	}
	if session.CompletionNotifiedAt == nil {
		t.Fatal("ready session was not marked notified")
	}
	if got.Analysis.Summary.ReadyCount != 1 || got.Analysis.Summary.EventCount != 1 {
		t.Fatalf("analysis summary = %+v", got.Analysis.Summary)
	}
}

func TestCancelSessionStopsAllActiveMembersOnce(t *testing.T) {
	repo := &sessionTestRepo{fakeRepo: newFakeRepo(), sessions: map[string]*model.Session{}}
	caller := &fakeCaller{}
	uc := New(repo, caller, sessionResolver{101: 11, 102: 12}, nil)

	created, err := uc.CreateSession(context.Background(), CreateSessionInput{
		Targets: []SessionTarget{{DeviceID: 101, Interface: "eth0"}, {DeviceID: 102, Interface: "eth1"}},
		Filter:  "tcp and port 443",
		Source:  SourceChat,
	})
	if err != nil {
		t.Fatalf("CreateSession: %v", err)
	}
	if len(created.Captures) != 2 {
		t.Fatalf("members=%d want 2", len(created.Captures))
	}

	detail, err := uc.CancelSession(context.Background(), created.Session.PublicID)
	if err != nil {
		t.Fatalf("CancelSession: %v", err)
	}
	if detail.Session.State != model.SessionStateCancelled {
		t.Fatalf("session state=%q want %q when all members are cancelled without evidence", detail.Session.State, model.SessionStateCancelled)
	}
	for _, capture := range detail.Captures {
		if capture.State != model.StateCancelled {
			t.Fatalf("capture %d state=%q want cancelled", capture.ID, capture.State)
		}
	}
	if got := countPacketCaptureMethod(caller.methods, tunnel.MethodCancelPacketCapture); got != 2 {
		t.Fatalf("cancel calls=%d want 2", got)
	}

	if _, err := uc.CancelSession(context.Background(), created.Session.PublicID); err != nil {
		t.Fatalf("idempotent CancelSession: %v", err)
	}
	if got := countPacketCaptureMethod(caller.methods, tunnel.MethodCancelPacketCapture); got != 2 {
		t.Fatalf("second stop issued extra edge calls: %d", got)
	}
}

func TestStopSessionKeepsMembersCollectingUntilUpload(t *testing.T) {
	repo := &sessionTestRepo{fakeRepo: newFakeRepo(), sessions: map[string]*model.Session{}}
	caller := &fakeCaller{}
	uc := New(repo, caller, sessionResolver{101: 11, 102: 12}, nil)

	created, err := uc.CreateSession(context.Background(), CreateSessionInput{
		Targets: []SessionTarget{{DeviceID: 101, Interface: "eth0"}, {DeviceID: 102, Interface: "eth1"}},
		Filter:  "tcp and port 443",
		Source:  SourceChat,
	})
	if err != nil {
		t.Fatalf("CreateSession: %v", err)
	}
	detail, err := uc.StopSession(context.Background(), created.Session.PublicID)
	if err != nil {
		t.Fatalf("StopSession: %v", err)
	}
	if detail.Session.State != model.SessionStateCollecting {
		t.Fatalf("session state=%q want %q", detail.Session.State, model.SessionStateCollecting)
	}
	for _, capture := range detail.Captures {
		if capture.State != model.StateCapturing {
			t.Fatalf("capture %d state=%q want capturing", capture.ID, capture.State)
		}
	}
	if got := countPacketCaptureMethod(caller.methods, tunnel.MethodStopPacketCapture); got != 2 {
		t.Fatalf("stop calls=%d want 2", got)
	}
}

func countPacketCaptureMethod(methods []string, want string) int {
	count := 0
	for _, method := range methods {
		if method == want {
			count++
		}
	}
	return count
}

func TestAnalyzeSessionCorrelatesBidirectionalFlowAcrossEdges(t *testing.T) {
	started := time.Date(2026, 8, 14, 10, 0, 0, 0, time.UTC)
	captures := []*model.Capture{
		{ID: 1, EdgeID: 11, DeviceID: 101, State: model.StateReady, StartedAt: &started, ParsedJSON: `{"packets":[{"observed_at":"0.25","source":"10.0.0.1","destination":"10.0.0.2","protocol":"TCP","length":"60","index":{"srcport":"51000","dstport":"443"}}]}`},
		{ID: 2, EdgeID: 12, DeviceID: 102, State: model.StateReady, StartedAt: &started, ParsedJSON: `{"packets":[{"observed_at":"0.75","source":"10.0.0.2","destination":"10.0.0.1","protocol":"TCP","length":70,"index":{"srcport":"443","dstport":"51000"}}]}`},
	}
	analysis := analyzeSession(captures)
	if analysis.Summary.ReadyCount != 2 || analysis.Summary.FlowCount != 1 || analysis.Summary.EventCount != 2 {
		t.Fatalf("summary=%+v", analysis.Summary)
	}
	flow := analysis.Flows[0]
	if flow.Packets != 2 || len(flow.EdgeIDs) != 2 || len(flow.MissingEdgeIDs) != 0 {
		t.Fatalf("flow=%+v", flow)
	}
	if got := analysis.Timeline[0].Timestamp; !got.Equal(started.Add(250 * time.Millisecond)) {
		t.Fatalf("timestamp=%s", got)
	}
}

func TestAnalyzeSessionMarksMissingObservationWithoutClaimingLoss(t *testing.T) {
	captures := []*model.Capture{
		{ID: 1, EdgeID: 11, State: model.StateReady, ParsedJSON: `{"packets":[{"source":"10.0.0.1","destination":"10.0.0.2","protocol":"UDP","index":{"srcport":"53","dstport":"50000"},"protocol_tree":[{"name":"Epoch Time: 1786701600.125 seconds"}]}]}`},
		{ID: 2, EdgeID: 12, State: model.StateReady, ParsedJSON: `{"packets":[]}`},
	}
	analysis := analyzeSession(captures)
	if len(analysis.Flows) != 1 || len(analysis.Flows[0].MissingEdgeIDs) != 1 || analysis.Flows[0].MissingEdgeIDs[0] != 12 {
		t.Fatalf("flows=%+v", analysis.Flows)
	}
	if analysis.Summary.Warning == "" {
		t.Fatal("clock/observation warning missing")
	}
}

func TestUpdateSessionAnalysisKeepsActiveCaptureCollecting(t *testing.T) {
	repo := &sessionTestRepo{fakeRepo: newFakeRepo(), sessions: map[string]*model.Session{}}
	session := &model.Session{ID: 1, PublicID: "pcap-session-active", State: model.SessionStateCollecting}
	repo.sessions[session.PublicID] = session
	repo.byID[1] = &model.Capture{ID: 1, SessionID: session.ID, State: model.StateCapturing}
	uc := New(repo, &fakeCaller{}, sessionResolver{}, nil)

	detail, err := uc.updateSessionAnalysis(context.Background(), session.PublicID)
	if err != nil {
		t.Fatalf("update session analysis: %v", err)
	}
	if detail.Session.State != model.SessionStateCollecting {
		t.Fatalf("state = %q, want collecting", detail.Session.State)
	}
}

func TestUpdateSessionAnalysisMarksEmptySessionFailed(t *testing.T) {
	repo := &sessionTestRepo{fakeRepo: newFakeRepo(), sessions: map[string]*model.Session{}}
	session := &model.Session{ID: 1, PublicID: "pcap-session-empty", State: model.SessionStateCollecting}
	repo.sessions[session.PublicID] = session
	uc := New(repo, &fakeCaller{}, sessionResolver{}, nil)

	detail, err := uc.updateSessionAnalysis(context.Background(), session.PublicID)
	if err != nil {
		t.Fatalf("update session analysis: %v", err)
	}
	if detail.Session.State != model.SessionStateFailed {
		t.Fatalf("state = %q, want failed", detail.Session.State)
	}
}
