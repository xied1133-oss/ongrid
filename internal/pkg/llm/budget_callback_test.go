package llm

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/cloudwego/eino/callbacks"
	"github.com/cloudwego/eino/components"
	einomodel "github.com/cloudwego/eino/components/model"
	"github.com/cloudwego/eino/schema"
)

// recordingChecker is a BudgetChecker stub for the callback tests.
type recordingChecker struct {
	limit       int        // 0 = unlimited
	used        int        // accumulated by Record
	checks      int        // # of Check calls
	records     int        // # of Record calls
	lastUsage   Usage      // last Usage seen by Record
	lastEst     int        // last estPromptTokens passed to Check
	checkErr    error      // override Check return
	recordError error      // override Record return
}

func (r *recordingChecker) Check(_ context.Context, _ uint64, est int) error {
	r.checks++
	r.lastEst = est
	if r.checkErr != nil {
		return r.checkErr
	}
	if r.limit > 0 && r.used+est > r.limit {
		return ErrBudgetExceeded
	}
	return nil
}

func (r *recordingChecker) Record(_ context.Context, _ uint64, u Usage) error {
	r.records++
	r.lastUsage = u
	r.used += u.TotalTokens
	return r.recordError
}

func chatModelRunInfo() *callbacks.RunInfo {
	return &callbacks.RunInfo{
		Name:      "test-chat",
		Type:      "Test",
		Component: components.ComponentOfChatModel,
	}
}

func TestBudgetCallbackHandler_NeededRespectsConfig(t *testing.T) {
	t.Parallel()

	// nil checker -> not needed.
	h := NewBudgetCallbackHandler(nil, 0)
	if h.Needed(context.Background(), chatModelRunInfo(), callbacks.TimingOnStart) {
		t.Fatalf("Needed should be false when checker is nil")
	}

	checker := &recordingChecker{}
	h = NewBudgetCallbackHandler(checker, 0)
	if !h.Needed(context.Background(), chatModelRunInfo(), callbacks.TimingOnStart) {
		t.Fatalf("Needed should be true for OnStart with checker wired")
	}
	if !h.Needed(context.Background(), chatModelRunInfo(), callbacks.TimingOnEnd) {
		t.Fatalf("Needed should be true for OnEnd with checker wired")
	}
	if h.Needed(context.Background(), chatModelRunInfo(), callbacks.TimingOnError) {
		t.Fatalf("Needed should be false for OnError in PR-1")
	}

	// Non-ChatModel component should be skipped.
	other := &callbacks.RunInfo{Component: components.Component("Tool")}
	if h.Needed(context.Background(), other, callbacks.TimingOnStart) {
		t.Fatalf("Needed should be false for non-ChatModel components")
	}
}

func TestBudgetCallbackHandler_OnStartCheckSucceeds(t *testing.T) {
	t.Parallel()
	checker := &recordingChecker{limit: 0}
	h := NewBudgetCallbackHandler(checker, 99)

	in := &einomodel.CallbackInput{
		Messages: []*schema.Message{
			{Role: schema.User, Content: strings.Repeat("a", 40)},
		},
	}
	ctx := h.OnStart(context.Background(), chatModelRunInfo(), in)

	if checker.checks != 1 {
		t.Fatalf("Check calls = %d, want 1", checker.checks)
	}
	if checker.lastEst <= 0 {
		t.Fatalf("estimated tokens should be > 0, got %d", checker.lastEst)
	}
	if BudgetRejectionFromContext(ctx) != nil {
		t.Fatalf("expected no rejection on ctx, got %v", BudgetRejectionFromContext(ctx))
	}
	if got := h.Stats(); got.Checks != 1 || got.Rejects != 0 {
		t.Fatalf("Stats() = %+v", got)
	}
}

func TestBudgetCallbackHandler_OnStartRejection(t *testing.T) {
	t.Parallel()
	checker := &recordingChecker{checkErr: ErrBudgetExceeded}
	h := NewBudgetCallbackHandler(checker, 0)

	in := &einomodel.CallbackInput{
		Messages: []*schema.Message{{Role: schema.User, Content: "tiny"}},
	}
	ctx := h.OnStart(context.Background(), chatModelRunInfo(), in)
	got := BudgetRejectionFromContext(ctx)
	if got == nil {
		t.Fatalf("expected rejection on ctx, got nil")
	}
	if !errors.Is(got, ErrBudgetExceeded) {
		t.Fatalf("ctx rejection = %v, want ErrBudgetExceeded", got)
	}
	if h.Stats().Rejects != 1 {
		t.Fatalf("Rejects = %d, want 1", h.Stats().Rejects)
	}
}

func TestBudgetCallbackHandler_OnEndRecordsUsage(t *testing.T) {
	t.Parallel()
	checker := &recordingChecker{}
	h := NewBudgetCallbackHandler(checker, 0)

	out := &einomodel.CallbackOutput{
		Message: &schema.Message{Role: schema.Assistant, Content: "ok"},
		TokenUsage: &einomodel.TokenUsage{
			PromptTokens:     12,
			CompletionTokens: 5,
			TotalTokens:      17,
		},
	}
	h.OnEnd(context.Background(), chatModelRunInfo(), out)

	if checker.records != 1 {
		t.Fatalf("Record calls = %d, want 1", checker.records)
	}
	if checker.lastUsage.TotalTokens != 17 {
		t.Fatalf("recorded total tokens = %d, want 17", checker.lastUsage.TotalTokens)
	}
	if h.Stats().Records != 1 || h.Stats().TokensIn != 17 {
		t.Fatalf("Stats() = %+v", h.Stats())
	}
}

func TestBudgetCallbackHandler_OnEndUsesResponseMetaFallback(t *testing.T) {
	t.Parallel()
	checker := &recordingChecker{}
	h := NewBudgetCallbackHandler(checker, 0)

	// Simulate the "graph injected" path: only the *schema.Message is
	// available, so usage must come from ResponseMeta.
	msg := &schema.Message{
		Role:    schema.Assistant,
		Content: "ok",
		ResponseMeta: &schema.ResponseMeta{
			Usage: &schema.TokenUsage{TotalTokens: 21},
		},
	}
	h.OnEnd(context.Background(), chatModelRunInfo(), msg)

	if checker.records != 1 {
		t.Fatalf("Record calls = %d, want 1", checker.records)
	}
	if checker.lastUsage.TotalTokens != 21 {
		t.Fatalf("recorded total tokens = %d, want 21", checker.lastUsage.TotalTokens)
	}
}

func TestBudgetCallbackHandler_BlocksOverBudget_EndToEnd(t *testing.T) {
	t.Parallel()
	checker := &recordingChecker{limit: 100}
	h := NewBudgetCallbackHandler(checker, 0)

	// First call: small enough to pass.
	in := &einomodel.CallbackInput{
		Messages: []*schema.Message{{Role: schema.User, Content: strings.Repeat("a", 40)}},
	}
	ctx := h.OnStart(context.Background(), chatModelRunInfo(), in)
	if BudgetRejectionFromContext(ctx) != nil {
		t.Fatalf("first call should pass budget")
	}
	h.OnEnd(ctx, chatModelRunInfo(), &einomodel.CallbackOutput{
		TokenUsage: &einomodel.TokenUsage{TotalTokens: 95},
	})

	// Second call: estimate pushes us over the cap.
	in2 := &einomodel.CallbackInput{
		Messages: []*schema.Message{{Role: schema.User, Content: strings.Repeat("a", 80)}},
	}
	ctx2 := h.OnStart(context.Background(), chatModelRunInfo(), in2)
	if got := BudgetRejectionFromContext(ctx2); !errors.Is(got, ErrBudgetExceeded) {
		t.Fatalf("expected ErrBudgetExceeded on ctx, got %v", got)
	}
	stats := h.Stats()
	if stats.Rejects != 1 {
		t.Fatalf("Rejects = %d, want 1", stats.Rejects)
	}
	if stats.Records != 1 {
		t.Fatalf("Records = %d, want 1", stats.Records)
	}
}

func TestBudgetCallbackHandler_NoCheckerIsNoop(t *testing.T) {
	t.Parallel()
	var h *BudgetCallbackHandler
	if got := BudgetRejectionFromContext(h.OnStart(context.Background(), nil, nil)); got != nil {
		t.Fatalf("nil handler OnStart should be a noop")
	}

	h2 := NewBudgetCallbackHandler(nil, 0)
	ctx := h2.OnStart(context.Background(), chatModelRunInfo(), &einomodel.CallbackInput{
		Messages: []*schema.Message{{Role: schema.User, Content: "x"}},
	})
	if BudgetRejectionFromContext(ctx) != nil {
		t.Fatalf("handler with nil checker should never reject")
	}
	h2.OnEnd(ctx, chatModelRunInfo(), &einomodel.CallbackOutput{
		TokenUsage: &einomodel.TokenUsage{TotalTokens: 10},
	})
	if h2.Stats().Records != 0 {
		t.Fatalf("handler with nil checker should not record")
	}
}

func TestBudgetCallbackHandler_NonChatModelSkipped(t *testing.T) {
	t.Parallel()
	checker := &recordingChecker{}
	h := NewBudgetCallbackHandler(checker, 0)
	other := &callbacks.RunInfo{Component: components.Component("Tool")}
	ctx := h.OnStart(context.Background(), other, &einomodel.CallbackInput{
		Messages: []*schema.Message{{Role: schema.User, Content: "hi"}},
	})
	if BudgetRejectionFromContext(ctx) != nil {
		t.Fatalf("non-ChatModel start should not reject")
	}
	if checker.checks != 0 {
		t.Fatalf("non-ChatModel start should not call Check")
	}
}

func TestBudgetCallbackHandler_PassesGraphInjectedInput(t *testing.T) {
	t.Parallel()
	// When the graph injects callbacks (rather than the model
	// implementation itself), eino delivers the raw []*schema.Message as
	// CallbackInput. Make sure ConvCallbackInput recovers it.
	checker := &recordingChecker{}
	h := NewBudgetCallbackHandler(checker, 0)
	raw := []*schema.Message{{Role: schema.User, Content: "graph-injected"}}
	ctx := h.OnStart(context.Background(), chatModelRunInfo(), raw)
	if BudgetRejectionFromContext(ctx) != nil {
		t.Fatalf("unexpected rejection: %v", BudgetRejectionFromContext(ctx))
	}
	if checker.checks != 1 {
		t.Fatalf("Check was not called for graph-injected input")
	}
}

func TestBudgetCallbackHandler_StreamHandlersClose(t *testing.T) {
	t.Parallel()
	checker := &recordingChecker{}
	h := NewBudgetCallbackHandler(checker, 0)

	// We cannot construct a *schema.StreamReader[CallbackInput] outside
	// the package without exposed helpers, so just ensure nil is accepted.
	if got := h.OnStartWithStreamInput(context.Background(), chatModelRunInfo(), nil); got == nil {
		t.Fatalf("OnStartWithStreamInput must return a context")
	}
	if got := h.OnEndWithStreamOutput(context.Background(), chatModelRunInfo(), nil); got == nil {
		t.Fatalf("OnEndWithStreamOutput must return a context")
	}
	if got := h.OnError(context.Background(), chatModelRunInfo(), errors.New("boom")); got == nil {
		t.Fatalf("OnError must return a context")
	}
}
