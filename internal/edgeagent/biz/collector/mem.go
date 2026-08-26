package collector

import (
	"context"

	"github.com/ongridio/ongrid/internal/edgeagent/model"
)

// CollectMem samples memory usage from /proc/meminfo.
// Phase 1 returns a zero value.
func CollectMem(ctx context.Context) (model.HostMetric, error) {
	_ = ctx
	return model.HostMetric{}, nil
}
