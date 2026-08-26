package report

import (
	"context"
	"encoding/json"
	"time"
)

// ReportFacts is the deterministic, pre-computed input handed to the
// reporter agent (HLD-014 §数据收集). The agent narrates these facts —
// it never computes the numbers. Redesigned (2026-06-06 review): the
// report leads with fleet resource trends + monitoring coverage rather
// than being incident-only, so a calm period still carries substance.
// Every number comes from Prometheus (period avg/peak) or a SQL query;
// the agent's only freedom is prose (narrative + advice).
type ReportFacts struct {
	Period     Period `json:"-"`
	PrevPeriod Period `json:"-"`

	// Hero is the pre-computed big-number set. Redesigned to lead with
	// non-incident signals (devices monitored, fleet CPU/mem avg, disk
	// peak) so the cards are meaningful even when nothing fired.
	Hero []HeroStat `json:"hero"`

	// Resource is the fleet-aggregate resource trend over the period
	// (avg + peak), from Prometheus. Aggregated across devices — not
	// per-device (per-device reads as noise; review decision).
	Resource ResourceFacts `json:"resource"`

	// Fleet is the monitoring-coverage snapshot: how many devices, how
	// many online, role breakdown.
	Fleet FleetFacts `json:"fleet"`

	// Incidents / Actions / AlertCounts stay, but the report de-
	// emphasizes them (a secondary section, not the lead).
	Incidents   []IncidentFact `json:"incidents"`
	Actions     ActionsSummary `json:"actions"`
	AlertCounts map[string]int `json:"alert_counts"`

	// Changes is the period's product-side change log (audit_logs):
	// rule / setting / device / channel / user edits. "Who changed what."
	Changes []ChangeFact `json:"changes"`

	// Assets is what was newly added to the platform this period —
	// custom assistants, skills, knowledge repos. The "我们建设了什么" row.
	Assets AssetFacts `json:"assets"`

	// Usage is the platform-usage signal: chat sessions + LLM token spend
	// over the period. The "用了多少" row.
	Usage UsageFacts `json:"usage"`
}

// AssetFacts counts platform assets created within the period.
type AssetFacts struct {
	NewAgents int `json:"new_agents"`
	NewSkills int `json:"new_skills"`
	NewRepos  int `json:"new_repos"`
}

// UsageFacts is the platform-usage summary over the period.
type UsageFacts struct {
	Sessions         int   `json:"sessions"`
	PromptTokens     int64 `json:"prompt_tokens"`
	CompletionTokens int64 `json:"completion_tokens"`
}

// ResourceFacts is the fleet resource trend over the period. Available
// is false when Prometheus is unreachable or has no data for the window
// (old period past retention, or no metrics yet) — the renderer then
// shows a "no data" note instead of fake zeros.
type ResourceFacts struct {
	Available bool    `json:"available"`
	CPUAvg    float64 `json:"cpu_avg"`
	CPUPeak   float64 `json:"cpu_peak"`
	MemAvg    float64 `json:"mem_avg"`
	MemPeak   float64 `json:"mem_peak"`
	DiskAvg   float64 `json:"disk_avg"`
	DiskPeak  float64 `json:"disk_peak"`
}

// FleetFacts is the monitoring-coverage snapshot at period end.
type FleetFacts struct {
	Total  int            `json:"total"`
	Online int            `json:"online"`
	Roles  map[string]int `json:"roles,omitempty"` // role name → device count
}

// IncidentFact is one incident's SQL-true facts. DurationMin is
// resolved_at - first_fired_at (or now - first_fired_at if still open).
type IncidentFact struct {
	ID          uint64 `json:"id"`
	Title       string `json:"title"`
	Severity    string `json:"severity"`
	Status      string `json:"status"`
	DeviceID    uint64 `json:"device_id,omitempty"`
	DurationMin int    `json:"duration_min"`
}

// ChangeFact is one product-side change from the audit log within the
// period — the "who changed what" signal that's useful regardless of
// incidents.
type ChangeFact struct {
	At           time.Time `json:"at"`
	Action       string    `json:"action"`        // e.g. rule_update
	ResourceType string    `json:"resource_type"` // e.g. alert_rule
	ResourceName string    `json:"resource_name,omitempty"`
	Actor        string    `json:"actor,omitempty"` // user email
}

// Scope is the parsed ReportSchedule.ScopeJSON. v1 honours EdgeIDs and
// SeverityMin; FleetTags is parsed but a no-op until G.2.6 edge tags.
type Scope struct {
	FleetTags   []string `json:"fleet_tags,omitempty"`
	EdgeIDs     []uint64 `json:"edge_ids,omitempty"`
	SeverityMin string   `json:"severity_min,omitempty"`
}

// ParseScope reads a ScopeJSON blob. Empty / "{}" / invalid → zero
// Scope (full coverage) rather than an error.
func ParseScope(raw string) Scope {
	var s Scope
	if raw == "" {
		return s
	}
	_ = json.Unmarshal([]byte(raw), &s)
	return s
}

// FactsCollector runs the pure-data collection. Implemented by
// data/report/store.FactsCollector. Period and prev are pre-computed by
// the Usecase (PeriodFor); scope filters the queries.
type FactsCollector interface {
	Collect(ctx context.Context, period, prev Period, scope Scope) (*ReportFacts, error)
}
