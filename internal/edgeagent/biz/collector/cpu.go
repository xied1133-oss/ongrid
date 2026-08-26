package collector

import (
	"context"

	"github.com/ongridio/ongrid/internal/edgeagent/model"
)

// CollectCPU samples CPU and load averages from /proc/loadavg + /proc/stat.
// Phase 1 returns a zero value.
func CollectCPU(ctx context.Context) (model.HostMetric, error) {
	_ = ctx
	return model.HostMetric{}, nil
}
