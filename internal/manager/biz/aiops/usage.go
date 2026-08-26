// Package aiops — usage aggregation.
//
// UsageUsecase exposes "how many tokens did we burn today" — a small
// read-only aggregation over chat_messages. The cluster-global total is
// what the dashboard pill consumes for MVP; per-user / per-org budgeting
// will reuse the same SessionRepo.SumTokensSince hook with a richer
// filter when those features land.
package aiops

import (
	"context"
	"fmt"
	"log/slog"
	"time"
)

// UsageUsecase serves token-usage rollups.
type UsageUsecase struct {
	repo SessionRepo
	log  *slog.Logger
}

// DailyUsage is a fully-summed token report for a 24h window starting at
// Date (UTC midnight). TotalTokens = PromptTokens + CompletionTokens.
type DailyUsage struct {
	Date             time.Time // UTC start-of-day
	PromptTokens     int64
	CompletionTokens int64
	TotalTokens      int64
	Requests         int64
}

// NewUsageUsecase builds the usecase.
func NewUsageUsecase(repo SessionRepo, log *slog.Logger) *UsageUsecase {
	return &UsageUsecase{repo: repo, log: log}
}

// Today returns aggregated token usage since UTC midnight today.
func (u *UsageUsecase) Today(ctx context.Context) (*DailyUsage, error) {
	since := time.Now().UTC().Truncate(24 * time.Hour)
	sums, err := u.repo.SumTokensSince(ctx, since)
	if err != nil {
		return nil, fmt.Errorf("aiops usage: sum tokens since %s: %w", since.Format(time.RFC3339), err)
	}
	return &DailyUsage{
		Date:             since,
		PromptTokens:     sums.PromptTokens,
		CompletionTokens: sums.CompletionTokens,
		TotalTokens:      sums.PromptTokens + sums.CompletionTokens,
		Requests:         sums.Requests,
	}, nil
}
