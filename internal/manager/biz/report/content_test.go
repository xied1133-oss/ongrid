package report

import (
	"strings"
	"testing"
)

func sampleContent() *Content {
	d := -12.0
	return &Content{
		Version: ContentVersion,
		Hero: []HeroStat{
			{Key: "incidents", Label: "Incidents", Value: 23, DeltaPct: &d, Sparkline: []int{4, 7, 3}},
			{Key: "mttr_minutes", Label: "MTTR", Value: 47, Unit: "min"},
		},
		Narrative: Narrative{
			Headline: "本周整体平稳",
			Paragraphs: []Paragraph{
				{Text: "{{entity:edge:7|db-prod-3}} 三次突破 30%，触发 {{entity:incident:1234|I-1234}}。"},
			},
		},
		KeyIncidents: []KeyIncident{
			{ID: 1234, Title: "db-prod-3 IO 饱和", Severity: "warning", DurationMin: 47, Status: "resolved"},
		},
		Actions: ActionsSummary{MutatingTotal: 11, MutatingApproved: 11, SafeTotal: 47},
		Advice:  []Advice{{Text: "把 {{entity:edge:7|db-prod-3}} backup 挪到 03:00"}},
	}
}

func TestParseContent_RoundTrip(t *testing.T) {
	raw := sampleContent().MustJSON()
	got, err := ParseContent(raw)
	if err != nil {
		t.Fatal(err)
	}
	if got.Narrative.Headline != "本周整体平稳" {
		t.Errorf("headline lost: %q", got.Narrative.Headline)
	}
	if len(got.Hero) != 2 || got.Hero[0].DeltaPct == nil || *got.Hero[0].DeltaPct != -12 {
		t.Errorf("hero delta lost: %+v", got.Hero)
	}
}

func TestValidate_RejectsMissingHeadline(t *testing.T) {
	c := sampleContent()
	c.Narrative.Headline = "  "
	if err := c.Validate(); err == nil {
		t.Error("expected error for blank headline")
	}
}

func TestValidate_RejectsHeroMissingKey(t *testing.T) {
	c := sampleContent()
	c.Hero[1].Key = ""
	if err := c.Validate(); err == nil {
		t.Error("expected error for hero without key")
	}
}

func TestValidate_AllowsCalmReport(t *testing.T) {
	// A 0-incident calm report: empty incidents/advice is fine, spine intact.
	c := &Content{
		Version:   ContentVersion,
		Hero:      []HeroStat{{Key: "incidents", Label: "Incidents", Value: 0}},
		Narrative: Narrative{Headline: "本周无异常，一切平稳"},
	}
	if err := c.Validate(); err != nil {
		t.Errorf("calm report should validate: %v", err)
	}
}

func TestRenderMarkdown_FlattensEntities(t *testing.T) {
	md := sampleContent().RenderMarkdown("周报 · 2026 W23", "zh")
	if strings.Contains(md, "{{entity:") {
		t.Errorf("entity tokens not flattened:\n%s", md)
	}
	if !strings.Contains(md, "db-prod-3") {
		t.Errorf("entity display name lost:\n%s", md)
	}
	if !strings.Contains(md, "# 周报 · 2026 W23") {
		t.Errorf("title missing:\n%s", md)
	}
	// zh locale → Chinese section titles.
	if !strings.Contains(md, "## 监控覆盖") {
		t.Errorf("zh section title missing:\n%s", md)
	}
}

// TestRenderMarkdown_Locale verifies the markdown section titles follow
// the report locale (feedback_ai_output_locale) — en yields English
// headings, never the Chinese defaults.
func TestRenderMarkdown_Locale(t *testing.T) {
	en := sampleContent().RenderMarkdown("Weekly · 2026 W23", "en")
	for _, want := range []string{"## Monitoring coverage", "## Usage", "## New assets"} {
		if !strings.Contains(en, want) {
			t.Errorf("en markdown missing %q:\n%s", want, en)
		}
	}
	for _, banned := range []string{"## 监控覆盖", "## 使用情况", "## 资源使用"} {
		if strings.Contains(en, banned) {
			t.Errorf("en markdown leaked Chinese heading %q", banned)
		}
	}
}

func TestFlattenEntities(t *testing.T) {
	cases := map[string]string{
		"plain text":                              "plain text",
		"{{entity:edge:7|db-prod-3}} down":        "db-prod-3 down",
		"a {{entity:incident:1|I-1}} b {{entity:edge:2|n2}} c": "a I-1 b n2 c",
		"{{entity:malformed":                      "{{entity:malformed", // no closer → left as-is
	}
	for in, want := range cases {
		if got := flattenEntities(in); got != want {
			t.Errorf("flatten(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestParseScope(t *testing.T) {
	s := ParseScope(`{"edge_ids":[7,9],"severity_min":"warning"}`)
	if len(s.EdgeIDs) != 2 || s.SeverityMin != "warning" {
		t.Errorf("scope parse: %+v", s)
	}
	// Empty / malformed → zero scope (full coverage), no panic.
	if got := ParseScope(""); len(got.EdgeIDs) != 0 {
		t.Errorf("empty scope should be zero: %+v", got)
	}
	if got := ParseScope("not json"); len(got.EdgeIDs) != 0 {
		t.Errorf("malformed scope should degrade to zero: %+v", got)
	}
}
