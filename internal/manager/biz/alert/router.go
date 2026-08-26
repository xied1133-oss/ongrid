package alert

import (
	"context"
	"encoding/json"
	"strings"

	model "github.com/ongridio/ongrid/internal/manager/model/alert"
)

// ChannelResolver picks the notification channels for an incident. The
// default DBChannelResolver reads notification_channels rows and applies
// the row-level severity / scope filters; tests stub it directly.
type ChannelResolver interface {
	ChannelsFor(ctx context.Context, incident *model.Incident) []*model.Channel
}

// channelLister is the narrow Repo subset DBChannelResolver depends on.
// Re-declaring it locally keeps the resolver test-fakeable without forcing
// every test to satisfy the full biz.Repo surface.
type channelLister interface {
	ListEnabledChannels(ctx context.Context) ([]*model.Channel, error)
}

// RuleLookup loads the rule that produced an incident. The resolver
// uses it to honour rule-level notify_channel_ids overrides — see
// model.Rule.NotifyChannelIDsJSON. nil-safe (resolver falls back to
// global filter behaviour when the lookup is unwired).
type RuleLookup func(ctx context.Context, key string) (*model.Rule, error)

// DBChannelResolver enumerates enabled channel rows on each call and
// filters them against the incident's severity + scope_type.
type DBChannelResolver struct {
	src      channelLister
	fallback []string
	rules    RuleLookup
}

// NewDBChannelResolver wires a DB-backed resolver. fallback is the list of
// channel names used when no DB channel matches — typically the env-seeded
// default set, kept for the migration window and as an operator escape hatch.
func NewDBChannelResolver(src channelLister, fallback []string) *DBChannelResolver {
	return &DBChannelResolver{
		src:      src,
		fallback: append([]string(nil), fallback...),
	}
}

// SetRuleLookup wires the rule lookup used to honour per-rule channel
// pinning. Optional — when nil the resolver only applies the global
// severity/scope filters.
func (r *DBChannelResolver) SetRuleLookup(lookup RuleLookup) {
	r.rules = lookup
}

// ChannelsFor returns the matching channel rows. When no row matches, the
// resolver returns synthetic *model.Channel entries (Name only) for every
// fallback name so MaybeNotify still has something to send to. Synthetic
// rows have ID=0, which MaybeNotify already handles (no delivery row).
//
// Per-rule pinning takes precedence: if the originating rule has a
// non-empty NotifyChannelIDsJSON, only those channel ids match (still
// gated by ch.Enabled). The global severity/scope filters are skipped
// in that branch — operator-pinned channels are explicit intent.
func (r *DBChannelResolver) ChannelsFor(ctx context.Context, incident *model.Incident) []*model.Channel {
	if r.src == nil || incident == nil {
		return synthetic(r.fallback)
	}
	rows, err := r.src.ListEnabledChannels(ctx)
	if err != nil {
		return synthetic(r.fallback)
	}

	// Per-rule pinning. Look up the rule, parse the json blob; on any
	// error fall through to the global filter behaviour.
	if pinned := r.pinnedIDs(ctx, incident); len(pinned) > 0 {
		want := make(map[uint64]struct{}, len(pinned))
		for _, id := range pinned {
			want[id] = struct{}{}
		}
		var matched []*model.Channel
		for _, ch := range rows {
			if !ch.Enabled {
				continue
			}
			if _, ok := want[ch.ID]; ok {
				matched = append(matched, ch)
			}
		}
		if len(matched) > 0 {
			return matched
		}
		// Pinned IDs all disabled / deleted — fall through to global
		// filter so we don't drop the notification entirely.
	}

	var matched []*model.Channel
	for _, ch := range rows {
		if !ch.Enabled {
			continue
		}
		if !channelMatches(ch, incident) {
			continue
		}
		matched = append(matched, ch)
	}
	if len(matched) == 0 {
		return synthetic(r.fallback)
	}
	return matched
}

// pinnedIDs returns the rule-level channel-pinning override for the
// incident, or nil when none is set / lookup is unwired.
func (r *DBChannelResolver) pinnedIDs(ctx context.Context, incident *model.Incident) []uint64 {
	if r.rules == nil || incident == nil || incident.Rule == "" {
		return nil
	}
	rule, err := r.rules(ctx, incident.Rule)
	if err != nil || rule == nil || rule.NotifyChannelIDsJSON == nil || *rule.NotifyChannelIDsJSON == "" {
		return nil
	}
	var ids []uint64
	if err := json.Unmarshal([]byte(*rule.NotifyChannelIDsJSON), &ids); err != nil {
		return nil
	}
	return ids
}

func channelMatches(ch *model.Channel, inc *model.Incident) bool {
	if ch.MatchSeverityMin != "" && !severityAtLeast(inc.Severity, ch.MatchSeverityMin) {
		return false
	}
	if ch.MatchScopeTypes != "" {
		want := splitCSV(ch.MatchScopeTypes)
		if !contains(want, inc.ScopeType) {
			return false
		}
	}
	return true
}

// severityAtLeast returns true when actual >= floor in the strictly-ordered
// severity ladder info < warning < critical. Unknown severities count as
// "info" (lowest).
func severityAtLeast(actual, floor string) bool {
	return severityRank(actual) >= severityRank(floor)
}

func severityRank(s string) int {
	switch strings.ToLower(s) {
	case "critical":
		return 3
	case "warning":
		return 2
	case "info":
		return 1
	default:
		return 0
	}
}

func splitCSV(s string) []string {
	parts := strings.Split(s, ",")
	out := make([]string, 0, len(parts))
	for _, p := range parts {
		p = strings.TrimSpace(p)
		if p != "" {
			out = append(out, p)
		}
	}
	return out
}

func contains(haystack []string, needle string) bool {
	for _, h := range haystack {
		if h == needle {
			return true
		}
	}
	return false
}

func synthetic(names []string) []*model.Channel {
	out := make([]*model.Channel, 0, len(names))
	for _, n := range names {
		if n == "" {
			continue
		}
		out = append(out, &model.Channel{Name: n, Enabled: true})
	}
	return out
}
