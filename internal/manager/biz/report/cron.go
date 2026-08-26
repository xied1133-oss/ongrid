package report

import (
	"errors"
	"time"

	"github.com/robfig/cron/v3"

	model "github.com/ongridio/ongrid/internal/manager/model/report"
	"github.com/ongridio/ongrid/internal/pkg/errs"
)

// CronNext computes the next fire time strictly after `after`, in the
// given timezone, for a standard 5-field cron spec. We use robfig/cron's
// parser only — the scheduler runs its own ticker (HLD-014 §调度器), so
// none of robfig's in-process scheduling machinery is used.
//
// The returned time carries `loc` so the caller can store it as UTC and
// render local. A malformed spec is surfaced as ErrInvalid.
func CronNext(spec string, loc *time.Location, after time.Time) (time.Time, error) {
	if loc == nil {
		loc = time.UTC
	}
	sched, err := cron.ParseStandard(spec)
	if err != nil {
		return time.Time{}, errors.Join(errs.ErrInvalid, err)
	}
	return sched.Next(after.In(loc)), nil
}

// CronSpecForKind returns the default 5-field cron for a preset cadence.
// The front-end may override the time-of-day; these are the sensible
// defaults the chat-created path and tests use. Custom kind has no
// default — the caller must supply CronSpec.
//
//	daily   → 09:00 every day
//	weekly  → 09:00 every Monday
//	monthly → 09:00 on the 1st
func CronSpecForKind(kind string) (string, error) {
	switch kind {
	case model.KindDaily:
		return "0 9 * * *", nil
	case model.KindWeekly:
		return "0 9 * * 1", nil
	case model.KindMonthly:
		return "0 9 1 * *", nil
	case model.KindCustom:
		return "", errors.Join(errs.ErrInvalid, errors.New("custom kind requires an explicit cron spec"))
	default:
		return "", errors.Join(errs.ErrInvalid, errors.New("unknown report kind"))
	}
}

// ValidateCronSpec checks a spec parses, returning ErrInvalid on
// failure. Used by the API layer (PR-4) to reject a bad custom cron at
// create time rather than silently never firing.
func ValidateCronSpec(spec string) error {
	if _, err := cron.ParseStandard(spec); err != nil {
		return errors.Join(errs.ErrInvalid, err)
	}
	return nil
}
