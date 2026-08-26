package packetcapture

import (
	"context"
	"encoding/json"
	"fmt"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/google/uuid"

	model "github.com/ongridio/ongrid/internal/manager/model/packetcapture"
	"github.com/ongridio/ongrid/internal/pkg/errs"
)

const sessionStartLeadTime = 5 * time.Second

type SessionTarget struct {
	DeviceID          uint64 `json:"device_id"`
	Interface         string `json:"interface"`
	NetworkNamespace  string `json:"network_namespace,omitempty"`
	StartAfterSeconds int    `json:"start_after_seconds,omitempty"`
}

type CreateSessionInput struct {
	Targets         []SessionTarget `json:"targets"`
	Filter          string          `json:"filter"`
	DurationSeconds int             `json:"duration_seconds"`
	MaxBytes        int64           `json:"max_bytes"`
	MaxPackets      int             `json:"max_packets"`
	Snaplen         int             `json:"snaplen"`
	Promiscuous     bool            `json:"promiscuous"`
	Title           string          `json:"title"`
	Description     string          `json:"description"`
	Source          string          `json:"source"`
	CreatedBy       uint64          `json:"created_by"`
	ChatSessionID   string          `json:"-"`
}

type SessionOutput struct {
	Session      *model.Session   `json:"session"`
	Captures     []*model.Capture `json:"captures"`
	MemberErrors []string         `json:"member_errors,omitempty"`
}

type SessionDetail struct {
	Session  *model.Session   `json:"session"`
	Captures []*model.Capture `json:"captures"`
	Analysis SessionAnalysis  `json:"analysis"`
}

// CreateSession creates a durable coordination record before any edge RPC.
// Calls happen outside a transaction; an unavailable member leaves a partial,
// inspectable session instead of rolling back already-started remote captures.
func (u *Usecase) CreateSession(ctx context.Context, in CreateSessionInput) (*SessionOutput, error) {
	if u == nil || u.repo == nil || u.resolver == nil {
		return nil, errs.ErrNotWiredYet
	}
	sessions, ok := u.repo.(SessionRepo)
	if !ok {
		return nil, errs.ErrNotWiredYet
	}
	if len(in.Targets) == 0 {
		return nil, fmt.Errorf("%w: at least one capture target is required", errs.ErrInvalid)
	}
	for _, target := range in.Targets {
		if target.DeviceID == 0 || strings.TrimSpace(target.Interface) == "" {
			return nil, fmt.Errorf("%w: each session target needs device_id and interface", errs.ErrInvalid)
		}
		edgeID, err := u.resolver.ResolveEdgeID(ctx, target.DeviceID)
		if err != nil || edgeID == 0 {
			return nil, fmt.Errorf("%w: resolve target device %d", errs.ErrInvalid, target.DeviceID)
		}
	}
	probe, err := normalizeCreateInput(CreateInput{DeviceID: in.Targets[0].DeviceID, Interface: in.Targets[0].Interface, NetworkNamespace: in.Targets[0].NetworkNamespace, Filter: in.Filter, DurationSeconds: in.DurationSeconds, MaxBytes: in.MaxBytes, MaxPackets: in.MaxPackets, Snaplen: in.Snaplen, Promiscuous: in.Promiscuous, Title: in.Title, Description: in.Description, Source: in.Source, CreatedBy: in.CreatedBy})
	if err != nil {
		return nil, err
	}
	plannedStart := u.now().UTC().Add(sessionStartLeadTime)
	session := &model.Session{PublicID: "pcap-session-" + uuid.NewString(), CreatedBy: probe.CreatedBy, Source: probe.Source, State: model.SessionStateCollecting, ChatSessionID: strings.TrimSpace(in.ChatSessionID), Title: probe.Title, Description: probe.Description, CanonicalFilter: probe.Filter, DurationSecs: uint32(probe.DurationSeconds), PlannedStartAt: plannedStart, ClockQuality: "uncalibrated", AnalysisJSON: "{}"}
	if err := sessions.CreateSession(ctx, session); err != nil {
		return nil, err
	}
	out := &SessionOutput{Session: session, Captures: make([]*model.Capture, 0, len(in.Targets))}
	for _, target := range in.Targets {
		memberStart := plannedStart.Add(time.Duration(target.StartAfterSeconds) * time.Second)
		created, createErr := u.Create(ctx, CreateInput{DeviceID: target.DeviceID, Interface: target.Interface, NetworkNamespace: target.NetworkNamespace, Filter: probe.Filter, DurationSeconds: probe.DurationSeconds, MaxBytes: probe.MaxBytes, MaxPackets: probe.MaxPackets, Snaplen: probe.Snaplen, Promiscuous: probe.Promiscuous, Title: probe.Title, Description: probe.Description, Source: probe.Source, CreatedBy: probe.CreatedBy, SessionID: session.ID, PlannedStartAt: &memberStart})
		if createErr != nil {
			out.MemberErrors = append(out.MemberErrors, fmt.Sprintf("device %d: %v", target.DeviceID, createErr))
			continue
		}
		if created != nil && created.Capture != nil {
			out.Captures = append(out.Captures, created.Capture)
		}
	}
	if len(out.Captures) == 0 {
		session.State = model.SessionStateFailed
	} else if len(out.MemberErrors) != 0 {
		session.State = model.SessionStatePartial
	}
	if session.State != model.SessionStateCollecting {
		if err := sessions.SetSessionAnalysis(ctx, session.ID, session.State, "{}"); err != nil {
			return nil, err
		}
	}
	return out, nil
}

// CompletionEvent is emitted once after a chat-bound capture session reaches
// a terminal state. It carries only metadata; raw PCAP never enters chat.
type CompletionEvent struct {
	ChatSessionID string
	Session       *model.Session
	Analysis      SessionAnalysis
}

// ReconcileActiveSessions refreshes active sessions and emits terminal events
// exactly once. The durable CAS marker makes retry and manager restart safe.
func (u *Usecase) ReconcileActiveSessions(ctx context.Context, limit int, notify func(context.Context, CompletionEvent) error) error {
	sessions, ok := u.repo.(SessionRepo)
	if !ok {
		return errs.ErrNotWiredYet
	}
	active, err := sessions.ListReconcilableSessions(ctx, limit)
	if err != nil {
		return err
	}
	for _, session := range active {
		if err := ctx.Err(); err != nil {
			return err
		}
		detail, refreshErr := u.RefreshSession(ctx, session.PublicID)
		if refreshErr != nil {
			u.log.Warn("packet capture: reconcile session", "session", session.PublicID, "err", refreshErr)
			continue
		}
		if detail.Session.State == model.SessionStateCollecting || strings.TrimSpace(detail.Session.ChatSessionID) == "" || notify == nil {
			continue
		}
		if detail.Session.CompletionNotifiedAt != nil {
			continue
		}
		if err := notify(ctx, CompletionEvent{ChatSessionID: detail.Session.ChatSessionID, Session: detail.Session, Analysis: detail.Analysis}); err != nil {
			u.log.Error("packet capture: notify chat completion failed", "session", detail.Session.PublicID, "err", err)
			continue
		}
		if _, err := sessions.MarkSessionCompletionNotified(ctx, detail.Session.ID, u.now().UTC()); err != nil {
			return fmt.Errorf("packet capture: mark completion notification: %w", err)
		}
	}
	return nil
}

func (u *Usecase) ListSessions(ctx context.Context, limit, offset int) ([]*model.Session, int64, error) {
	sessions, ok := u.repo.(SessionRepo)
	if !ok {
		return nil, 0, errs.ErrNotWiredYet
	}
	return sessions.ListSessions(ctx, limit, offset)
}

func (u *Usecase) CountSessionCaptures(ctx context.Context, sessionIDs []uint64) (map[uint64]int, error) {
	counts, ok := u.repo.(SessionCaptureCountRepo)
	if !ok {
		return nil, errs.ErrNotWiredYet
	}
	return counts.CountCapturesBySessionIDs(ctx, sessionIDs)
}

func (u *Usecase) GetSession(ctx context.Context, publicID string) (*SessionDetail, error) {
	sessions, ok := u.repo.(SessionRepo)
	if !ok {
		return nil, errs.ErrNotWiredYet
	}
	session, err := sessions.GetSession(ctx, publicID)
	if err != nil {
		return nil, err
	}
	captures, err := sessions.ListBySessionID(ctx, session.ID)
	if err != nil {
		return nil, err
	}
	analysis := analyzeSession(captures)
	if session.AnalysisJSON != "" && session.AnalysisJSON != "{}" {
		if err := json.Unmarshal([]byte(session.AnalysisJSON), &analysis); err != nil {
			return nil, fmt.Errorf("packet capture: decode session analysis: %w", err)
		}
	}
	return &SessionDetail{Session: session, Captures: captures, Analysis: analysis}, nil
}

func (u *Usecase) RefreshSession(ctx context.Context, publicID string) (*SessionDetail, error) {
	detail, err := u.GetSession(ctx, publicID)
	if err != nil {
		return nil, err
	}
	for _, capture := range detail.Captures {
		if capture.ParsedJSON != "" {
			continue
		}
		if capture.State == model.StateFailed || capture.State == model.StateCancelled {
			continue
		}
		if _, refreshErr := u.Refresh(ctx, capture.ID); refreshErr != nil {
			u.log.Warn("packet capture: refresh session member", "session", publicID, "capture_id", capture.ID, "err", refreshErr)
		}
	}
	return u.updateSessionAnalysis(ctx, publicID)
}

// CancelSession stops every non-terminal member of a coordinated capture.
// Cancellation is best-effort across members: unavailable edges leave their
// last state intact and the returned session remains inspectable.
func (u *Usecase) CancelSession(ctx context.Context, publicID string) (*SessionDetail, error) {
	detail, err := u.GetSession(ctx, publicID)
	if err != nil {
		return nil, err
	}
	for _, capture := range detail.Captures {
		if capture.State == model.StateReady || capture.State == model.StateFailed || capture.State == model.StateCancelled || capture.State == model.StateExpired || capture.State == model.StateDeleted {
			continue
		}
		if _, cancelErr := u.Cancel(ctx, capture.ID); cancelErr != nil {
			u.log.Warn("packet capture: cancel session member", "session", publicID, "capture_id", capture.ID, "err", cancelErr)
		}
	}
	return u.updateSessionAnalysis(ctx, publicID)
}

// StopSession gracefully finishes each active member and retains completed
// partial PCAPs. Follow-up RefreshSession calls publish those PCAPs through the
// regular artifact pipeline once the edge reports success.
func (u *Usecase) StopSession(ctx context.Context, publicID string) (*SessionDetail, error) {
	detail, err := u.GetSession(ctx, publicID)
	if err != nil {
		return nil, err
	}
	for _, capture := range detail.Captures {
		if capture.State == model.StateReady || capture.State == model.StateFailed || capture.State == model.StateCancelled || capture.State == model.StateExpired || capture.State == model.StateDeleted {
			continue
		}
		if _, stopErr := u.Stop(ctx, capture.ID); stopErr != nil {
			u.log.Warn("packet capture: stop session member", "session", publicID, "capture_id", capture.ID, "err", stopErr)
		}
	}
	return u.updateSessionAnalysis(ctx, publicID)
}

func (u *Usecase) updateSessionAnalysis(ctx context.Context, publicID string) (*SessionDetail, error) {
	sessions, ok := u.repo.(SessionRepo)
	if !ok {
		return nil, errs.ErrNotWiredYet
	}
	detail, err := u.GetSession(ctx, publicID)
	if err != nil {
		return nil, err
	}
	analysis := analyzeSession(detail.Captures)
	state := model.SessionStateReady
	ready := 0
	cancelled := 0
	incomplete := false
	for _, capture := range detail.Captures {
		if capture.State == model.StateReady && capture.ParsedJSON != "" {
			ready++
			continue
		}
		if capture.State == model.StateCancelled {
			cancelled++
			continue
		}
		if capture.State != model.StateFailed && capture.State != model.StateCancelled && capture.State != model.StateExpired && capture.State != model.StateDeleted {
			incomplete = true
		}
	}
	if incomplete {
		state = model.SessionStateCollecting
	} else if ready == 0 {
		if len(detail.Captures) > 0 && cancelled == len(detail.Captures) {
			state = model.SessionStateCancelled
		} else {
			state = model.SessionStateFailed
		}
	} else if ready != len(detail.Captures) {
		state = model.SessionStatePartial
	}
	raw, err := json.Marshal(analysis)
	if err != nil {
		return nil, fmt.Errorf("packet capture: encode session analysis: %w", err)
	}
	if err := sessions.SetSessionAnalysis(ctx, detail.Session.ID, state, string(raw)); err != nil {
		return nil, err
	}
	return u.GetSession(ctx, publicID)
}

type SessionAnalysis struct {
	Summary  SessionSummary `json:"summary"`
	Flows    []SessionFlow  `json:"flows"`
	Timeline []SessionEvent `json:"timeline"`
}
type SessionSummary struct {
	CaptureCount int    `json:"capture_count"`
	ReadyCount   int    `json:"ready_count"`
	FlowCount    int    `json:"flow_count"`
	EventCount   int    `json:"event_count"`
	ClockQuality string `json:"clock_quality"`
	Warning      string `json:"warning"`
}
type SessionFlow struct {
	ID             string    `json:"id"`
	Protocol       string    `json:"protocol"`
	Endpoints      []string  `json:"endpoints"`
	EdgeIDs        []uint64  `json:"edge_ids"`
	MissingEdgeIDs []uint64  `json:"missing_edge_ids,omitempty"`
	Packets        int       `json:"packets"`
	FirstSeenAt    time.Time `json:"first_seen_at"`
	LastSeenAt     time.Time `json:"last_seen_at"`
}
type SessionEvent struct {
	CaptureID   uint64    `json:"capture_id"`
	ArtifactID  string    `json:"artifact_id"`
	EdgeID      uint64    `json:"edge_id"`
	DeviceID    uint64    `json:"device_id"`
	Timestamp   time.Time `json:"timestamp"`
	Source      string    `json:"source"`
	Destination string    `json:"destination"`
	Protocol    string    `json:"protocol"`
	Length      int       `json:"length"`
	Info        string    `json:"info"`
	FlowID      string    `json:"flow_id"`
}

var epochPattern = regexp.MustCompile(`Epoch Time:\s*([0-9]+(?:\.[0-9]+)?)\s*seconds`)

func analyzeSession(captures []*model.Capture) SessionAnalysis {
	analysis := SessionAnalysis{Summary: SessionSummary{CaptureCount: len(captures), ClockQuality: "uncalibrated", Warning: "Edge clocks are not calibrated; compare ordering, not absolute latency."}, Flows: []SessionFlow{}, Timeline: []SessionEvent{}}
	allEdges := map[uint64]struct{}{}
	flowEvents := map[string][]SessionEvent{}
	for _, capture := range captures {
		allEdges[capture.EdgeID] = struct{}{}
		if capture.State != model.StateReady || capture.ParsedJSON == "" {
			continue
		}
		analysis.Summary.ReadyCount++
		var parsed ParsedArtifact
		if json.Unmarshal([]byte(capture.ParsedJSON), &parsed) != nil {
			continue
		}
		for _, packet := range parsed.Packets {
			event, ok := normalizeSessionEvent(capture, packet)
			if !ok {
				continue
			}
			analysis.Timeline = append(analysis.Timeline, event)
			flowEvents[event.FlowID] = append(flowEvents[event.FlowID], event)
		}
	}
	sort.Slice(analysis.Timeline, func(i, j int) bool { return analysis.Timeline[i].Timestamp.Before(analysis.Timeline[j].Timestamp) })
	if len(analysis.Timeline) > 5000 {
		analysis.Timeline = analysis.Timeline[:5000]
	}
	analysis.Summary.EventCount = len(analysis.Timeline)
	for id, events := range flowEvents {
		flow := SessionFlow{ID: id, Protocol: events[0].Protocol, Endpoints: sortedEndpoints(events[0].Source, events[0].Destination), Packets: len(events), FirstSeenAt: events[0].Timestamp, LastSeenAt: events[0].Timestamp}
		edges := map[uint64]struct{}{}
		for _, event := range events {
			edges[event.EdgeID] = struct{}{}
			if event.Timestamp.Before(flow.FirstSeenAt) {
				flow.FirstSeenAt = event.Timestamp
			}
			if event.Timestamp.After(flow.LastSeenAt) {
				flow.LastSeenAt = event.Timestamp
			}
		}
		for edge := range edges {
			flow.EdgeIDs = append(flow.EdgeIDs, edge)
		}
		sort.Slice(flow.EdgeIDs, func(i, j int) bool { return flow.EdgeIDs[i] < flow.EdgeIDs[j] })
		for edge := range allEdges {
			if _, ok := edges[edge]; !ok {
				flow.MissingEdgeIDs = append(flow.MissingEdgeIDs, edge)
			}
		}
		sort.Slice(flow.MissingEdgeIDs, func(i, j int) bool { return flow.MissingEdgeIDs[i] < flow.MissingEdgeIDs[j] })
		analysis.Flows = append(analysis.Flows, flow)
	}
	sort.Slice(analysis.Flows, func(i, j int) bool { return analysis.Flows[i].FirstSeenAt.Before(analysis.Flows[j].FirstSeenAt) })
	analysis.Summary.FlowCount = len(analysis.Flows)
	return analysis
}

func normalizeSessionEvent(c *model.Capture, packet map[string]any) (SessionEvent, bool) {
	source, destination := stringValue(packet["source"]), stringValue(packet["destination"])
	index, _ := packet["index"].(map[string]any)
	if source == "" {
		source = stringValue(index["ipsrc"])
	}
	if destination == "" {
		destination = stringValue(index["ipdst"])
	}
	if source == "" || destination == "" {
		return SessionEvent{}, false
	}
	protocol := strings.ToLower(stringValue(packet["protocol"]))
	if protocol == "" {
		protocol = strings.ToLower(stringValue(index["protocol"]))
	}
	if protocol == "" {
		protocol = "unknown"
	}
	source = appendPort(source, stringValue(index["srcport"]))
	destination = appendPort(destination, stringValue(index["dstport"]))
	timestamp := packetTimestamp(packet, c.StartedAt)
	if timestamp.IsZero() {
		timestamp = c.CreatedAt
	}
	ends := sortedEndpoints(source, destination)
	flowID := protocol + "|" + strings.Join(ends, "|")
	return SessionEvent{CaptureID: c.ID, ArtifactID: c.ArtifactID, EdgeID: c.EdgeID, DeviceID: c.DeviceID, Timestamp: timestamp, Source: source, Destination: destination, Protocol: protocol, Length: intValue(packet["length"]), Info: stringValue(packet["info"]), FlowID: flowID}, true
}

func packetTimestamp(packet map[string]any, fallback *time.Time) time.Time {
	if raw := stringValue(packet["timestamp"]); raw != "" {
		if t, err := time.Parse(time.RFC3339Nano, raw); err == nil {
			return t
		}
	}
	if raw := stringValue(packet["observed_at"]); raw != "" {
		if t, err := time.Parse(time.RFC3339Nano, raw); err == nil {
			return t
		}
		if seconds, err := strconv.ParseFloat(raw, 64); err == nil && fallback != nil {
			return fallback.Add(time.Duration(seconds * float64(time.Second)))
		}
	}
	var walk func(any) time.Time
	walk = func(v any) time.Time {
		switch node := v.(type) {
		case map[string]any:
			if name := stringValue(node["name"]); name != "" {
				if m := epochPattern.FindStringSubmatch(name); len(m) == 2 {
					if s, e := strconv.ParseFloat(m[1], 64); e == nil {
						return time.Unix(0, int64(s*float64(time.Second))).UTC()
					}
				}
			}
			for _, child := range []any{node["children"], node["protocol_tree"]} {
				if t := walk(child); !t.IsZero() {
					return t
				}
			}
		case []any:
			for _, child := range node {
				if t := walk(child); !t.IsZero() {
					return t
				}
			}
		}
		return time.Time{}
	}
	return walk(packet["protocol_tree"])
}
func stringValue(v any) string {
	switch n := v.(type) {
	case string:
		return strings.TrimSpace(n)
	case json.Number:
		return n.String()
	default:
		return ""
	}
}
func intValue(v any) int {
	switch n := v.(type) {
	case float64:
		return int(n)
	case int:
		return n
	case string:
		out, _ := strconv.Atoi(n)
		return out
	default:
		return 0
	}
}
func appendPort(host, port string) string {
	if host == "" || port == "" {
		return host
	}
	return host + ":" + port
}
func sortedEndpoints(a, b string) []string { out := []string{a, b}; sort.Strings(out); return out }
