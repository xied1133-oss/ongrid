package report

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	model "github.com/ongridio/ongrid/internal/manager/model/report"
)

// Deliverer fans a finished report out to notification channels. It's a
// seam (implemented in main.go over the notify router + channel store)
// so biz/report stays free of the notify / alert imports. A nil
// Deliverer means in-app only — the report is still viewable, just not
// pushed. The locked decision: only ready reports are delivered; a
// failed report is never pushed (no half-baked sends).
type Deliverer interface {
	// Deliver sends summary to each channel id and returns one record
	// per channel. Must not block indefinitely — the caller runs it
	// inline after MarkReady, so the impl bounds each send with a
	// timeout. Errors are captured in the records, not returned.
	Deliver(ctx context.Context, summary DeliverySummary, channelIDs []uint64) []DeliveryRecord
}

// DeliverySummary is the channel-agnostic payload. The concrete
// Deliverer renders it into each channel's native format (markdown
// text for v1; a Feishu interactive card is a future enhancement). The
// DeepLink is the in-app report URL the "view full report" affordance
// points at.
type DeliverySummary struct {
	Title    string
	Headline string
	Hero     []HeroStat
	DeepLink string // absolute or path; resolved by the impl from PublicURL
	ReportID string
}

// DeliveryRecord is one channel's delivery outcome, persisted into
// reports.delivery_json and surfaced on the detail page.
type DeliveryRecord struct {
	ChannelID    uint64    `json:"channel_id"`
	ChannelType  string    `json:"channel_type,omitempty"`
	Status       string    `json:"status"` // "sent" | "failed"
	SentAt       time.Time `json:"sent_at"`
	Error        string    `json:"error,omitempty"`
	FallbackUsed bool      `json:"fallback_used,omitempty"`
}

// MarkdownSummary renders the channel-agnostic markdown body senders
// use. Hero numbers as a compact line, headline, then the deep link.
// Channels that don't render markdown still show readable plain text.
func (s DeliverySummary) MarkdownSummary() string {
	var b strings.Builder
	b.WriteString("**" + s.Title + "**\n")
	if len(s.Hero) > 0 {
		parts := make([]string, 0, len(s.Hero))
		for _, h := range s.Hero {
			v := fmt.Sprintf("%s %s%s", h.Label, formatNum(h.Value), h.Unit)
			if h.DeltaPct != nil {
				arrow := "→"
				if *h.DeltaPct < 0 {
					arrow = "↓"
				} else if *h.DeltaPct > 0 {
					arrow = "↑"
				}
				v += fmt.Sprintf(" %s%.0f%%", arrow, abs(*h.DeltaPct))
			}
			parts = append(parts, v)
		}
		b.WriteString(strings.Join(parts, " · ") + "\n")
	}
	if s.Headline != "" {
		b.WriteString("\n" + flattenEntities(s.Headline) + "\n")
	}
	if s.DeepLink != "" {
		b.WriteString("\n[查看完整报告 →](" + s.DeepLink + ")")
	}
	return b.String()
}

// deliveryFor builds the summary for a ready report by parsing its
// content. Falls back to the stored SummaryText when content can't be
// parsed (never blocks delivery on a content quirk).
func deliveryFor(rpt *model.Report, deepLink string) DeliverySummary {
	s := DeliverySummary{
		Title:    rpt.Title,
		Headline: rpt.SummaryText,
		DeepLink: deepLink,
		ReportID: rpt.ID,
	}
	if rpt.ContentJSON != "" {
		if c, err := ParseContent(rpt.ContentJSON); err == nil {
			s.Hero = c.Hero
			if c.Narrative.Headline != "" {
				s.Headline = c.Narrative.Headline
			}
		}
	}
	return s
}

// recordDelivery serialises the per-channel records into the report row.
func recordDelivery(rpt *model.Report, records []DeliveryRecord) {
	if len(records) == 0 {
		return
	}
	if b, err := json.Marshal(records); err == nil {
		rpt.DeliveryJSON = string(b)
	}
}

func abs(f float64) float64 {
	if f < 0 {
		return -f
	}
	return f
}
