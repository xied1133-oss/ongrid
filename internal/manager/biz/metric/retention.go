package metric

import (
	"context"
	"log/slog"
	"time"
)

// Retention deletes aged-out rows from raw / 5m / 1h policy:
// raw 7d, 5m 90d, 1h 365d. Deletions are batched (limit=1000) so the
// SQLite writer lock is held for short intervals.
type Retention struct {
	writer Writer
	log    *slog.Logger

	rawTTL time.Duration
	m5TTL  time.Duration
	h1TTL  time.Duration
}

// retention defaults.
const (
	defaultRawTTL = 7 * 24 * time.Hour
	defaultM5TTL  = 90 * 24 * time.Hour
	defaultH1TTL  = 365 * 24 * time.Hour

	retentionBatchLimit = 1000
	// retentionRunAt is the hour-of-day (UTC) the Loop schedules its
	// daily pass. 03:00 UTC is the traditional "low traffic" window.
	retentionRunAtHour = 3
)

// NewRetention constructs a Retention with default TTLs.
func NewRetention(w Writer, log *slog.Logger) *Retention {
	if log == nil {
		log = slog.Default()
	}
	return &Retention{
		writer: w,
		log:    log,
		rawTTL: defaultRawTTL,
		m5TTL:  defaultM5TTL,
		h1TTL:  defaultH1TTL,
	}
}

// RunOnce executes one full retention pass over all three tiers. It loops
// per tier until Delete*Before reports 0 affected rows. Errors abort the
// current tier but not the whole pass.
func (r *Retention) RunOnce(ctx context.Context) error {
	now := time.Now().UTC()
	tiers := []struct {
		name   string
		cutoff time.Time
		del    func(ctx context.Context, cutoff time.Time, limit int) (int64, error)
	}{
		{"raw", now.Add(-r.rawTTL), r.writer.DeleteRawBefore},
		{"5m", now.Add(-r.m5TTL), r.writer.Delete5mBefore},
		{"1h", now.Add(-r.h1TTL), r.writer.Delete1hBefore},
	}
	for _, t := range tiers {
		var total int64
		for {
			if err := ctx.Err(); err != nil {
				return err
			}
			n, err := t.del(ctx, t.cutoff, retentionBatchLimit)
			if err != nil {
				r.log.Warn("retention: delete batch failed", "tier", t.name, "err", err)
				break
			}
			total += n
			if n == 0 {
				break
			}
		}
		r.log.Info("retention: tier complete", "tier", t.name, "deleted", total, "cutoff", t.cutoff)
	}
	return nil
}

// Loop runs RunOnce once a day at retentionRunAtHour UTC. Exits on ctx.
func (r *Retention) Loop(ctx context.Context) error {
	for {
		next := nextDailyRun(time.Now().UTC(), retentionRunAtHour)
		if err := sleepUntil(ctx, next); err != nil {
			return nil
		}
		if err := r.RunOnce(ctx); err != nil {
			r.log.Warn("retention: RunOnce failed", "err", err)
		}
	}
}

// nextDailyRun returns the next UTC wall-clock moment whose hour == hour.
// If now is already past today's boundary, schedules tomorrow's.
func nextDailyRun(now time.Time, hour int) time.Time {
	today := time.Date(now.Year(), now.Month(), now.Day(), hour, 0, 0, 0, time.UTC)
	if !today.After(now) {
		today = today.Add(24 * time.Hour)
	}
	return today
}
