package aiops

import (
	"context"
	"testing"

	model "github.com/ongridio/ongrid/internal/manager/model/aiops"
	"github.com/ongridio/ongrid/internal/pkg/errs"
)

type memoryOperationRepo struct {
	op     *model.Operation
	events []*model.OperationEvent
}

func (r *memoryOperationRepo) CreateOperation(_ context.Context, op *model.Operation) error {
	if op.ID == "" {
		op.ID = "operation-test"
	}
	r.op = op
	return nil
}

func (r *memoryOperationRepo) GetOperation(_ context.Context, _ string) (*model.Operation, error) {
	if r.op == nil {
		return nil, errs.ErrNotFound
	}
	return r.op, nil
}

func (r *memoryOperationRepo) UpdateOperation(_ context.Context, _ string, states []string, updates map[string]any) error {
	allowed := len(states) == 0
	for _, state := range states {
		allowed = allowed || state == r.op.State
	}
	if !allowed {
		return errs.ErrConflict
	}
	r.op.State = updates["state"].(string)
	r.op.Summary = updates["summary"].(string)
	r.op.ActionsJSON = updates["actions_json"].(string)
	return nil
}

func (r *memoryOperationRepo) AppendOperationEvent(_ context.Context, event *model.OperationEvent) (bool, error) {
	r.events = append(r.events, event)
	return true, nil
}

func (*memoryOperationRepo) CreateOperationArtifact(context.Context, *model.OperationArtifact) error {
	return nil
}
func (*memoryOperationRepo) ListOperationArtifacts(context.Context, string) ([]*model.OperationArtifact, error) {
	return nil, nil
}

func TestOperationUsecaseLifecycleAndOwnership(t *testing.T) {
	repo := &memoryOperationRepo{}
	uc := NewOperationUsecase(repo)
	op, err := uc.Create(context.Background(), CreateOperationInput{
		ChatSessionID: "session-1", CreatedBy: 7, Kind: "diagnostic", Title: "Collect evidence",
		Input: map[string]string{"target": "edge-001"}, Actions: []OperationAction{{Kind: "cancel", Label: "Stop", Enabled: true}},
	})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	if err := uc.Transition(context.Background(), op.ID, []string{model.OperationStateCreated}, model.OperationStateRunning, "Collecting", []OperationAction{{Kind: "cancel", Label: "Stop", Enabled: true}}, "started", nil); err != nil {
		t.Fatalf("Transition: %v", err)
	}
	if repo.op.State != model.OperationStateRunning || len(repo.events) != 1 {
		t.Fatalf("operation=%+v events=%d", repo.op, len(repo.events))
	}
	if _, err := uc.GetOwned(context.Background(), op.ID, 8, false); err == nil || !isErr(err, errs.ErrNotFound) {
		t.Fatalf("foreign owner error = %v", err)
	}
	if _, err := uc.GetOwned(context.Background(), op.ID, 8, true); err != nil {
		t.Fatalf("admin GetOwned: %v", err)
	}
}

func TestOperationTransitionsRejectTerminalAndBackwardStates(t *testing.T) {
	if validOperationTransition([]string{model.OperationStateSucceeded}, model.OperationStateRunning) {
		t.Fatal("terminal operation accepted a restart")
	}
	if validOperationTransition([]string{model.OperationStateRunning}, model.OperationStateQueued) {
		t.Fatal("running operation accepted backward transition")
	}
	if !validOperationTransition([]string{model.OperationStateRunning}, model.OperationStateCanceling) {
		t.Fatal("running operation cannot enter canceling")
	}
	if !validOperationTransition([]string{model.OperationStateCreated, model.OperationStateQueued, model.OperationStateRunning, model.OperationStateCanceling}, model.OperationStateSucceeded) {
		t.Fatal("allowed current-state set rejected running/canceling completion")
	}
	if !validOperationTransition([]string{model.OperationStateCanceling}, model.OperationStateRunning) {
		t.Fatal("canceling operation cannot return to running when cancellation is not confirmed")
	}
}

func isErr(err, target error) bool { return err == target }
