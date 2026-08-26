package store

import (
	"context"
	"fmt"
	"time"

	model "github.com/ongridio/ongrid/internal/manager/model/alert"
)

type IncidentFilter struct {
	Status   string
	DeviceID   *uint64
	RuleID   *uint64
	Severity string
	Limit    int
	Offset   int
}

type SilenceFilter struct {
	Status   string
	ActiveAt *time.Time
	Limit    int
	Offset   int
}

type RuleFilter struct {
	Enabled    *bool
	ScopeType  string
	SourceType string
	Limit      int
	Offset     int
}

type ChannelFilter struct {
	Enabled     *bool
	ChannelType string
	Limit       int
	Offset      int
}

type DeliveryFilter struct {
	IncidentID *uint64
	ChannelID  *uint64
	Status     string
	Limit      int
	Offset     int
}

// Store exposes the extra row-level CRUD surface beyond biz.Repo.
type Store interface {
	CreateAlertIncident(ctx context.Context, in *model.Incident) error
	GetIncidentByDedupeKey(ctx context.Context, dedupeKey string) (*model.Incident, error)
	ListIncidentRows(ctx context.Context, filter IncidentFilter) ([]*model.Incident, error)

	GetSilenceByID(ctx context.Context, id uint64) (*model.Silence, error)
	ListSilenceRows(ctx context.Context, filter SilenceFilter) ([]*model.Silence, error)
	UpdateSilenceStatus(ctx context.Context, id uint64, status string, cancelledBy *uint64, cancelledAt *time.Time) error

	CreateRule(ctx context.Context, in *model.Rule) error
	GetRuleByID(ctx context.Context, id uint64) (*model.Rule, error)
	ListRuleRows(ctx context.Context, filter RuleFilter) ([]*model.Rule, error)
	UpdateRuleEnabled(ctx context.Context, id uint64, enabled bool) error

	CreateChannel(ctx context.Context, in *model.Channel) error
	GetChannelByID(ctx context.Context, id uint64) (*model.Channel, error)
	ListChannelRows(ctx context.Context, filter ChannelFilter) ([]*model.Channel, error)
	UpdateChannelEnabled(ctx context.Context, id uint64, enabled bool) error

	CreateDelivery(ctx context.Context, in *model.Delivery) error
	GetDeliveryByID(ctx context.Context, id uint64) (*model.Delivery, error)
	ListDeliveryRows(ctx context.Context, filter DeliveryFilter) ([]*model.Delivery, error)
	UpdateDeliveryStatus(ctx context.Context, id uint64, status string, attemptCount uint32, providerMessageID *string, responseJSON *string, errMsg *string, sentAt *time.Time, finishedAt *time.Time) error
}

var _ Store = (*Repo)(nil)

func validateIncidentStatus(status string) error {
	switch status {
	case model.IncidentStatusOpen, model.IncidentStatusAcknowledged, model.IncidentStatusSilenced, model.IncidentStatusResolved:
		return nil
	default:
		return fmt.Errorf("unknown incident status %q", status)
	}
}

func validateSilenceStatus(status string) error {
	switch status {
	case model.SilenceStatusActive, model.SilenceStatusExpired, model.SilenceStatusCancelled:
		return nil
	default:
		return fmt.Errorf("unknown silence status %q", status)
	}
}

func validateDeliveryStatus(status string) error {
	switch status {
	case model.DeliveryStatusPending, model.DeliveryStatusSuccess, model.DeliveryStatusFailed:
		return nil
	default:
		return fmt.Errorf("unknown delivery status %q", status)
	}
}
