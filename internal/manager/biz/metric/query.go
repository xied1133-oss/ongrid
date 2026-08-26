package metric

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"time"

	model "github.com/ongridio/ongrid/internal/manager/model/metric"
	"github.com/ongridio/ongrid/internal/pkg/errs"
)

// Resolution selects which physical table feeds a range query.
//
// ResAuto lets the biz layer pick based on window size:
//
//	window <= 6h → raw
//	window <= 7d → 5m
//	otherwise → 1h
type Resolution string

// Resolution values.
const (
	ResAuto Resolution = "auto"
	ResRaw  Resolution = "raw"
	Res5m   Resolution = "5m"
	Res1h   Resolution = "1h"
)

// Bounds used by the auto-resolution selector and the window validator.
const (
	autoRawUpper = 6 * time.Hour
	auto5mUpper  = 7 * 24 * time.Hour
	maxWindow    = 365 * 24 * time.Hour
)

// RangeQuery is the single input to Query. EdgeID is required; Resolution
// defaults to ResAuto if zero.
type RangeQuery struct {
	EdgeID     uint64
	From       time.Time
	To         time.Time
	Resolution Resolution
}

// Series is the query response. Exactly one of RawPoints / Buckets5m /
// Buckets1h is non-nil; Resolution records which one.
type Series struct {
	Resolution Resolution
	RawPoints  []model.Point
	Buckets5m  []model.Bucket5m
	Buckets1h  []model.Bucket1h
}

// QueryUsecase is the biz-layer facade over Reader.
type QueryUsecase struct {
	reader Reader
	log    *slog.Logger
}

// NewQueryUsecase builds the usecase. log may be nil.
func NewQueryUsecase(r Reader, log *slog.Logger) *QueryUsecase {
	if log == nil {
		log = slog.Default()
	}
	return &QueryUsecase{reader: r, log: log}
}

// Query validates the range, resolves the target resolution, and delegates
// to the appropriate Reader method.
//
// Validation (returns errs.ErrInvalid-wrapped errors):
//   - EdgeID must be non-zero.
//   - From must be strictly before To.
//   - Window may not exceed 365 days.
//   - Resolution must be one of the declared values (empty → ResAuto).
func (u *QueryUsecase) Query(ctx context.Context, q RangeQuery) (*Series, error) {
	if q.EdgeID == 0 {
		return nil, fmt.Errorf("%w: edge_id required", errs.ErrInvalid)
	}
	if !q.From.Before(q.To) {
		return nil, fmt.Errorf("%w: from must be before to", errs.ErrInvalid)
	}
	window := q.To.Sub(q.From)
	if window > maxWindow {
		return nil, fmt.Errorf("%w: window %s exceeds 365d", errs.ErrInvalid, window)
	}

	res := q.Resolution
	if res == "" {
		res = ResAuto
	}
	if res == ResAuto {
		res = autoResolution(window)
	}

	switch res {
	case ResRaw:
		pts, err := u.reader.QueryRaw(ctx, q.EdgeID, q.From, q.To)
		if err != nil {
			return nil, err
		}
		return &Series{Resolution: ResRaw, RawPoints: pts}, nil
	case Res5m:
		bs, err := u.reader.Query5m(ctx, q.EdgeID, q.From, q.To)
		if err != nil {
			return nil, err
		}
		return &Series{Resolution: Res5m, Buckets5m: bs}, nil
	case Res1h:
		bs, err := u.reader.Query1h(ctx, q.EdgeID, q.From, q.To)
		if err != nil {
			return nil, err
		}
		return &Series{Resolution: Res1h, Buckets1h: bs}, nil
	default:
		return nil, errors.Join(errs.ErrInvalid, fmt.Errorf("unknown resolution %q", res))
	}
}

// autoResolution picks raw / 5m / 1h based on window size
func autoResolution(window time.Duration) Resolution {
	switch {
	case window <= autoRawUpper:
		return ResRaw
	case window <= auto5mUpper:
		return Res5m
	default:
		return Res1h
	}
}
