package aiops

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	model "github.com/ongridio/ongrid/internal/manager/model/aiops"
	"github.com/ongridio/ongrid/internal/pkg/errs"
)

type OperationRepo interface {
	CreateOperation(ctx context.Context, operation *model.Operation) error
	GetOperation(ctx context.Context, id string) (*model.Operation, error)
	UpdateOperation(ctx context.Context, id string, states []string, updates map[string]any) error
	AppendOperationEvent(ctx context.Context, event *model.OperationEvent) (bool, error)
	CreateOperationArtifact(ctx context.Context, artifact *model.OperationArtifact) error
	ListOperationArtifacts(ctx context.Context, operationID string) ([]*model.OperationArtifact, error)
}

type OperationAction struct {
	Kind    string `json:"kind"`
	Label   string `json:"label"`
	Enabled bool   `json:"enabled"`
}

type OperationArtifactInput struct {
	Kind, Title, URL string
	Metadata         any
}

func (u *OperationUsecase) AddArtifact(ctx context.Context, operationID string, in OperationArtifactInput) (*model.OperationArtifact, error) {
	if u == nil || u.repo == nil {
		return nil, errs.ErrNotWiredYet
	}
	if strings.TrimSpace(operationID) == "" || strings.TrimSpace(in.Kind) == "" || strings.TrimSpace(in.Title) == "" || strings.TrimSpace(in.URL) == "" {
		return nil, errs.ErrInvalid
	}
	metadata, err := json.Marshal(in.Metadata)
	if err != nil {
		return nil, fmt.Errorf("operation: encode artifact metadata: %w", err)
	}
	artifact := &model.OperationArtifact{OperationID: operationID, Kind: in.Kind, Title: in.Title, URL: in.URL, MetadataJSON: string(metadata)}
	if err := u.repo.CreateOperationArtifact(ctx, artifact); err != nil {
		return nil, fmt.Errorf("operation: create artifact: %w", err)
	}
	return artifact, nil
}

func (u *OperationUsecase) ListArtifacts(ctx context.Context, operationID string) ([]*model.OperationArtifact, error) {
	if u == nil || u.repo == nil {
		return nil, errs.ErrNotWiredYet
	}
	if strings.TrimSpace(operationID) == "" {
		return nil, errs.ErrInvalid
	}
	return u.repo.ListOperationArtifacts(ctx, operationID)
}

type CreateOperationInput struct {
	ChatSessionID string
	CreatedBy     uint64
	Kind          string
	Title         string
	Summary       string
	Input         any
	Actions       []OperationAction
	DetailURL     string
}

type OperationUsecase struct {
	repo OperationRepo
	now  func() time.Time
}

// GetOwned is the ownership boundary used by operation cards and actions.
// Operations are chat-scoped, but CreatedBy is duplicated on the durable row
// so this check does not need to join historical chat session data.
func (u *OperationUsecase) GetOwned(ctx context.Context, id string, userID uint64, admin bool) (*model.Operation, error) {
	if u == nil || u.repo == nil {
		return nil, errs.ErrNotWiredYet
	}
	op, err := u.repo.GetOperation(ctx, strings.TrimSpace(id))
	if err != nil {
		return nil, err
	}
	if !admin && op.CreatedBy != userID {
		return nil, errs.ErrNotFound
	}
	return op, nil
}

func NewOperationUsecase(repo OperationRepo) *OperationUsecase {
	return &OperationUsecase{repo: repo, now: time.Now}
}

func (u *OperationUsecase) Create(ctx context.Context, in CreateOperationInput) (*model.Operation, error) {
	if u == nil || u.repo == nil {
		return nil, errs.ErrNotWiredYet
	}
	if strings.TrimSpace(in.ChatSessionID) == "" || in.CreatedBy == 0 || strings.TrimSpace(in.Kind) == "" || strings.TrimSpace(in.Title) == "" {
		return nil, fmt.Errorf("%w: operation chat session, owner, kind and title are required", errs.ErrInvalid)
	}
	input, err := json.Marshal(in.Input)
	if err != nil {
		return nil, fmt.Errorf("operation: encode input: %w", err)
	}
	actions, err := json.Marshal(in.Actions)
	if err != nil {
		return nil, fmt.Errorf("operation: encode actions: %w", err)
	}
	op := &model.Operation{ChatSessionID: in.ChatSessionID, CreatedBy: in.CreatedBy, Kind: in.Kind, State: model.OperationStateCreated, Title: in.Title, Summary: in.Summary, InputJSON: string(input), ActionsJSON: string(actions), DetailURL: in.DetailURL}
	if err := u.repo.CreateOperation(ctx, op); err != nil {
		return nil, fmt.Errorf("operation: create: %w", err)
	}
	return op, nil
}

func (u *OperationUsecase) Transition(ctx context.Context, id string, from []string, state, summary string, actions []OperationAction, eventType string, payload any) error {
	if u == nil || u.repo == nil {
		return errs.ErrNotWiredYet
	}
	if strings.TrimSpace(id) == "" || strings.TrimSpace(state) == "" {
		return fmt.Errorf("%w: operation id and state are required", errs.ErrInvalid)
	}
	if !validOperationTransition(from, state) {
		return fmt.Errorf("%w: invalid operation transition %v -> %s", errs.ErrInvalid, from, state)
	}
	encodedActions, err := json.Marshal(actions)
	if err != nil {
		return fmt.Errorf("operation: encode actions: %w", err)
	}
	updates := map[string]any{"state": state, "summary": summary, "actions_json": string(encodedActions)}
	if isTerminalOperationState(state) {
		now := u.now().UTC()
		updates["terminal_at"] = now
	}
	if err := u.repo.UpdateOperation(ctx, id, from, updates); err != nil {
		return err
	}
	encodedPayload, err := json.Marshal(payload)
	if err != nil {
		return fmt.Errorf("operation: encode event payload: %w", err)
	}
	_, err = u.repo.AppendOperationEvent(ctx, &model.OperationEvent{OperationID: id, DedupeKey: state + ":" + strings.TrimSpace(eventType), Type: eventType, PayloadJSON: string(encodedPayload)})
	return err
}

func validOperationTransition(from []string, to string) bool {
	if len(from) == 0 {
		return false
	}
	for _, state := range from {
		switch state {
		case model.OperationStateCreated:
			if to == model.OperationStateQueued || to == model.OperationStateRunning || to == model.OperationStateCanceling || to == model.OperationStateFailed {
				return true
			}
		case model.OperationStateQueued:
			if to == model.OperationStateRunning || to == model.OperationStateCanceling || to == model.OperationStateFailed || to == model.OperationStateCancelled {
				return true
			}
		case model.OperationStateRunning:
			if to == model.OperationStateCanceling || to == model.OperationStateSucceeded || to == model.OperationStateFailed || to == model.OperationStateCancelled {
				return true
			}
		case model.OperationStateCanceling:
			if to == model.OperationStateRunning || to == model.OperationStateSucceeded || to == model.OperationStateFailed || to == model.OperationStateCancelled {
				return true
			}
		}
	}
	return false
}

func isTerminalOperationState(state string) bool {
	return state == model.OperationStateSucceeded || state == model.OperationStateFailed || state == model.OperationStateCancelled
}
