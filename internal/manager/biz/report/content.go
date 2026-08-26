package report

import (
	"encoding/json"
	"fmt"
	"strings"
)

// ContentJSON is the structured report body the reporter agent
// produces and the SPA renders (HLD-014 §ContentJSON). It is the source
// of truth for the rich in-app view; ContentMD is rendered from it as
// the export / IM / search fallback.
//
// Anti-hallucination contract: the numeric fields (Hero values/deltas/
// sparklines, the per-tool action counts) are computed in pure SQL by
// the FactsCollector and handed to the LLM. The generator (PR-2b)
// OVERWRITES Content.Hero and Content.Actions with the collected facts
// after the LLM returns, so a model that fiddles a number can't leak it
// into the report. The LLM owns only Narrative / KeyIncidents ordering
// commentary / Advice.
type Content struct {
	Version   string    `json:"version"`
	Hero      []HeroStat `json:"hero"`
	Narrative Narrative  `json:"narrative"`
	// Resource / Fleet / Changes are facts-injected (the generator
	// overwrites them from ReportFacts post-LLM); the agent never
	// produces them. Reuse the facts types — same JSON shape.
	Resource     ResourceFacts  `json:"resource"`
	Fleet        FleetFacts     `json:"fleet"`
	KeyIncidents []KeyIncident  `json:"key_incidents"`
	Actions      ActionsSummary `json:"actions_summary"`
	Changes      []ChangeFact   `json:"changes,omitempty"`
	Assets       AssetFacts     `json:"assets"`
	Usage        UsageFacts     `json:"usage"`
	Advice       []Advice       `json:"advice"`
	Metadata     ContentMeta    `json:"metadata"`
}

// HeroStat is one big-number card. Value/DeltaPct/Sparkline are SQL-
// computed (never LLM). Unit is optional ("min", "%"); empty = bare
// count. DeltaPct is the period-over-period change; nil = no prior
// period to compare (rendered as "new" rather than an arrow).
type HeroStat struct {
	Key       string   `json:"key"`
	Label     string   `json:"label"`
	Value     float64  `json:"value"`
	Unit      string   `json:"unit,omitempty"`
	DeltaPct  *float64 `json:"delta_pct,omitempty"`
	Sparkline []int    `json:"sparkline,omitempty"`
}

// Narrative is the LLM's prose. Paragraph.Text may embed entity tokens
// `{{entity:kind:id|name}}` that the SPA renders as clickable chips;
// Entities lists them for the renderer's convenience (markdown export
// strips the token syntax to the display name).
type Narrative struct {
	Headline   string      `json:"headline"`
	Paragraphs []Paragraph `json:"paragraphs"`
}

type Paragraph struct {
	Text     string         `json:"text"`
	Entities []EntityRef     `json:"entities,omitempty"`
}

type EntityRef struct {
	Key  string `json:"key"`  // "edge:7" | "incident:1234"
	Name string `json:"name"` // display name
}

// KeyIncident is a compact incident reference for the report's incident
// list. Sourced from facts (ids, durations, status are SQL-true); the
// LLM may set RootCauseSnippet from the RCA report when one exists.
type KeyIncident struct {
	ID               uint64 `json:"id"`
	Title            string `json:"title"`
	Severity         string `json:"severity"`
	DurationMin      int    `json:"duration_min"`
	Status           string `json:"status"`
	RootCauseSnippet string `json:"root_cause_snippet,omitempty"`
}

// ActionsSummary is the agent-transparency panel. All counts SQL-true.
type ActionsSummary struct {
	MutatingTotal    int          `json:"mutating_total"`
	MutatingApproved int          `json:"mutating_approved"`
	SafeTotal        int          `json:"safe_total"`
	ByTool           []ToolCount  `json:"by_tool,omitempty"`
}

type ToolCount struct {
	Tool  string `json:"tool"`
	Count int    `json:"count"`
}

// Advice is one forward-looking recommendation. LLM-authored. Text may
// embed entity tokens like Narrative.
type Advice struct {
	Text string `json:"text"`
}

type ContentMeta struct {
	PeriodStart string   `json:"period_start"`
	PeriodEnd   string   `json:"period_end"`
	DataSources []string `json:"data_sources,omitempty"`
}

// ContentVersion is the schema version stamped into freshly generated
// reports; lets the SPA branch on shape if the schema evolves.
const ContentVersion = "1"

// ParseContent unmarshals a ContentJSON blob and validates it. Used by
// the generator (PR-2b) to check the LLM output before persisting.
func ParseContent(raw string) (*Content, error) {
	var c Content
	if err := json.Unmarshal([]byte(raw), &c); err != nil {
		return nil, fmt.Errorf("report: content unmarshal: %w", err)
	}
	if err := c.Validate(); err != nil {
		return nil, err
	}
	return &c, nil
}

// Validate enforces the minimal shape the SPA depends on. Lenient on
// optional sections (a calm report may have empty KeyIncidents/Advice)
// but strict on the spine: a headline must exist and hero cards must
// each carry a key + label so the grid renders.
func (c *Content) Validate() error {
	if strings.TrimSpace(c.Narrative.Headline) == "" {
		return fmt.Errorf("report: content missing narrative.headline")
	}
	for i, h := range c.Hero {
		if h.Key == "" || h.Label == "" {
			return fmt.Errorf("report: hero[%d] missing key/label", i)
		}
	}
	return nil
}

// MustJSON serialises content; panics only on a programming error
// (unmarshalable types can't occur with these concrete structs).
func (c *Content) MustJSON() string {
	b, err := json.Marshal(c)
	if err != nil {
		panic(fmt.Sprintf("report: content marshal: %v", err))
	}
	return string(b)
}

// RenderMarkdown produces the ContentMD fallback from structured
// content. Used for export, IM plain-text fallback, and full-text
// search. Entity tokens are flattened to their display name.
func (c *Content) RenderMarkdown(title, locale string) string {
	en := locale == "en"
	mtr := func(zh, eng string) string {
		if en {
			return eng
		}
		return zh
	}
	var b strings.Builder
	b.WriteString("# " + title + "\n\n")

	if c.Narrative.Headline != "" {
		b.WriteString("## " + c.Narrative.Headline + "\n\n")
	}
	for _, p := range c.Narrative.Paragraphs {
		b.WriteString(flattenEntities(p.Text) + "\n\n")
	}

	if c.Resource.Available {
		b.WriteString("## " + mtr("资源使用（周期 均值 / 峰值）", "Resource usage (period avg / peak)") + "\n\n")
		avg, peak := mtr("均", "avg"), mtr("峰", "peak")
		b.WriteString(fmt.Sprintf("- CPU: %s %.1f%% · %s %.1f%%\n", avg, c.Resource.CPUAvg, peak, c.Resource.CPUPeak))
		b.WriteString(fmt.Sprintf("- %s: %s %.1f%% · %s %.1f%%\n", mtr("内存", "Memory"), avg, c.Resource.MemAvg, peak, c.Resource.MemPeak))
		b.WriteString(fmt.Sprintf("- %s: %s %.1f%% · %s %.1f%%\n\n", mtr("磁盘", "Disk"), avg, c.Resource.DiskAvg, peak, c.Resource.DiskPeak))
	}

	b.WriteString("## " + mtr("监控覆盖", "Monitoring coverage") + "\n\n")
	b.WriteString(fmt.Sprintf("- %s\n", mtr(
		fmt.Sprintf("监控设备 %d 台 · 在线 %d 台", c.Fleet.Total, c.Fleet.Online),
		fmt.Sprintf("%d devices · %d online", c.Fleet.Total, c.Fleet.Online))))
	if len(c.Fleet.Roles) > 0 {
		var roles []string
		for r, n := range c.Fleet.Roles {
			roles = append(roles, fmt.Sprintf("%s ×%d", r, n))
		}
		b.WriteString("- " + mtr("角色", "Roles") + ": " + strings.Join(roles, " · ") + "\n")
	}
	b.WriteString("\n")

	b.WriteString("## " + mtr("知识资产新增", "New assets") + "\n\n")
	b.WriteString(fmt.Sprintf("- %s\n\n", mtr(
		fmt.Sprintf("新增助理 %d · 新增技能 %d · 新增仓库 %d", c.Assets.NewAgents, c.Assets.NewSkills, c.Assets.NewRepos),
		fmt.Sprintf("%d assistants · %d skills · %d repos", c.Assets.NewAgents, c.Assets.NewSkills, c.Assets.NewRepos))))

	b.WriteString("## " + mtr("使用情况", "Usage") + "\n\n")
	b.WriteString(fmt.Sprintf("- %s\n\n", mtr(
		fmt.Sprintf("会话 %d · 输入 token %d · 输出 token %d", c.Usage.Sessions, c.Usage.PromptTokens, c.Usage.CompletionTokens),
		fmt.Sprintf("%d sessions · %d prompt tokens · %d completion tokens", c.Usage.Sessions, c.Usage.PromptTokens, c.Usage.CompletionTokens))))

	if len(c.KeyIncidents) > 0 {
		b.WriteString("## " + mtr("告警与处置", "Alerts & response") + "\n\n")
		for _, ki := range c.KeyIncidents {
			b.WriteString(fmt.Sprintf("- I-%d %s (%s, %dm, %s)\n",
				ki.ID, ki.Title, ki.Severity, ki.DurationMin, ki.Status))
		}
		b.WriteString(fmt.Sprintf("- %s\n\n", mtr(
			fmt.Sprintf("Agent 动作: mutating %d（批准 %d）· 只读 %d", c.Actions.MutatingTotal, c.Actions.MutatingApproved, c.Actions.SafeTotal),
			fmt.Sprintf("Agent actions: mutating %d (approved %d) · read-only %d", c.Actions.MutatingTotal, c.Actions.MutatingApproved, c.Actions.SafeTotal))))
	}

	if len(c.Changes) > 0 {
		b.WriteString("## " + mtr("变更记录", "Changes") + "\n\n")
		for _, ch := range c.Changes {
			b.WriteString(fmt.Sprintf("- %s %s %s\n", ch.At.Format("01-02 15:04"), ch.Action, ch.ResourceName))
		}
		b.WriteString("\n")
	}

	if len(c.Advice) > 0 {
		b.WriteString("## " + mtr("建议", "Recommendations") + "\n\n")
		for _, a := range c.Advice {
			b.WriteString("- " + flattenEntities(a.Text) + "\n")
		}
		b.WriteString("\n")
	}

	return b.String()
}

// formatNum prints an integer without trailing .0, else a 1-decimal.
func formatNum(v float64) string {
	if v == float64(int64(v)) {
		return fmt.Sprintf("%d", int64(v))
	}
	return fmt.Sprintf("%.1f", v)
}

// flattenEntities rewrites `{{entity:kind:id|name}}` → `name` for the
// markdown fallback (chips are an SPA-only affordance).
func flattenEntities(s string) string {
	for {
		start := strings.Index(s, "{{entity:")
		if start < 0 {
			return s
		}
		end := strings.Index(s[start:], "}}")
		if end < 0 {
			return s // malformed; leave as-is
		}
		end += start
		token := s[start+2 : end] // entity:kind:id|name
		name := token
		if bar := strings.LastIndex(token, "|"); bar >= 0 {
			name = token[bar+1:]
		}
		s = s[:start] + name + s[end+2:]
	}
}
