package tools

import (
	"context"

	"github.com/ongridio/ongrid/internal/manager/biz/aiops/tools/basetool"
)

// QueryLogQLTool is the BaseTool form of query_logql. Mirrors the closure
// executor in query_logql.go: same args, same timeouts, same compact output.
type QueryLogQLTool struct {
	logQuery LogQuerier
}

// NewQueryLogQLTool builds the BaseTool variant.
func NewQueryLogQLTool(lq LogQuerier) *QueryLogQLTool {
	return &QueryLogQLTool{logQuery: lq}
}

// queryLogQLWhenToUse — the canonical reverse-guard for log search.
// explicit "do NOT use for ..." steers the model away from
// the metric / trace tools when the question is really about log content.
const queryLogQLWhenToUse = "When the user asks about log CONTENT — grep error / panic / fatal, see the line text " +
	"that explains why a service failed, or inspect backend-selected log streams. Use stream selectors and line filters for portable Loki/Elasticsearch queries; metric LogQL is Loki-only. " +
	"NOT for filesystem state, file names or file sizes (use a host_files skill). " +
	"NOT for metric trends like cpu/mem (use query_promql). " +
	"NOT for traces / span timelines (use query_traceql)."

// Info returns metadata. Class=read.
func (t *QueryLogQLTool) Info(_ context.Context) (*basetool.ToolInfo, error) {
	return &basetool.ToolInfo{
		Name:        ToolNameQueryLogQL,
		Description: QueryLogQLDescription,
		WhenToUse:   queryLogQLWhenToUse,
		Parameters:  QueryLogQLSchema,
		Class:       "read",
	}, nil
}

// InvokableRun runs the LogQL range query.
func (t *QueryLogQLTool) InvokableRun(ctx context.Context, argsJSON string, _ ...basetool.InvokeOption) (string, error) {
	out, err := runQueryLogQL(ctx, t.logQuery, []byte(argsJSON))
	if err != nil {
		return "", err
	}
	return string(out), nil
}
