package store

import (
	"gorm.io/gorm"

	model "github.com/ongridio/ongrid/internal/manager/model/metric"
)

// Migrate registers all metric tables with gorm's AutoMigrate: raw samples,
// 5-minute and 1-hour aggregate tiers, and the dead-letter table. Composite
// primary keys on the aggregate tables are expressed via `primaryKey;priority:N`
// tags so both MySQL and SQLite receive the right (edge_id, ts) PK ordering.
func Migrate(db *gorm.DB) error {
	return db.AutoMigrate(
		&model.HostMetric{},
		&model.HostMetric5m{},
		&model.HostMetric1h{},
		&model.DeadLetter{},
	)
}
