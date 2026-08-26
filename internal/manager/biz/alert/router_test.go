package alert

import (
	"context"
	"testing"

	model "github.com/ongridio/ongrid/internal/manager/model/alert"
)

type fakeChannelLister struct {
	rows []*model.Channel
	err  error
}

func (f *fakeChannelLister) ListEnabledChannels(_ context.Context) ([]*model.Channel, error) {
	return f.rows, f.err
}

func TestDBChannelResolverFiltersBySeverity(t *testing.T) {
	src := &fakeChannelLister{rows: []*model.Channel{
		{ID: 1, Name: "log", Enabled: true},
		{ID: 2, Name: "feishu-critical", Enabled: true, MatchSeverityMin: "critical"},
		{ID: 3, Name: "slack-warning", Enabled: true, MatchSeverityMin: "warning"},
	}}
	r := NewDBChannelResolver(src, []string{"log"})

	got := r.ChannelsFor(context.Background(), &model.Incident{Severity: "warning"})
	names := channelNames(got)
	if !contains(names, "log") || !contains(names, "slack-warning") {
		t.Errorf("warning incident channels = %v, want log + slack-warning", names)
	}
	if contains(names, "feishu-critical") {
		t.Errorf("warning incident should not match critical-only channel")
	}

	gotCrit := r.ChannelsFor(context.Background(), &model.Incident{Severity: "critical"})
	critNames := channelNames(gotCrit)
	if !contains(critNames, "feishu-critical") {
		t.Errorf("critical incident missing feishu-critical, got %v", critNames)
	}
}

func TestDBChannelResolverFiltersByScope(t *testing.T) {
	src := &fakeChannelLister{rows: []*model.Channel{
		{ID: 1, Name: "log", Enabled: true},
		{ID: 2, Name: "pipeline-only", Enabled: true, MatchScopeTypes: "monitoring_pipeline"},
	}}
	r := NewDBChannelResolver(src, []string{"log"})

	got := channelNames(r.ChannelsFor(context.Background(), &model.Incident{ScopeType: "host", Severity: "warning"}))
	if contains(got, "pipeline-only") {
		t.Errorf("host incident should not match pipeline-only channel: %v", got)
	}
	got = channelNames(r.ChannelsFor(context.Background(), &model.Incident{ScopeType: "monitoring_pipeline", Severity: "warning"}))
	if !contains(got, "pipeline-only") {
		t.Errorf("pipeline incident should match pipeline-only: %v", got)
	}
}

func TestDBChannelResolverFallsBackOnEmpty(t *testing.T) {
	src := &fakeChannelLister{rows: []*model.Channel{
		{ID: 1, Name: "log", Enabled: true, MatchScopeTypes: "monitoring_pipeline"},
	}}
	r := NewDBChannelResolver(src, []string{"fallback-log"})

	got := channelNames(r.ChannelsFor(context.Background(), &model.Incident{ScopeType: "host"}))
	if !contains(got, "fallback-log") {
		t.Errorf("expected fallback channel to be used, got %v", got)
	}
}

func channelNames(chs []*model.Channel) []string {
	out := make([]string, 0, len(chs))
	for _, c := range chs {
		out = append(out, c.Name)
	}
	return out
}
