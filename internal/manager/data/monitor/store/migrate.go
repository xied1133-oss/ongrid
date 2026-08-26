// Package store is the GORM-backed persistence layer for monitor_panels.
// Mirrors the conventions of internal/manager/data/setting/store —
// dialect-agnostic AutoMigrate composed from cmd/ongrid via
// dbx.RunMigrations.
package store

import (
	"gorm.io/gorm"

	model "github.com/ongridio/ongrid/internal/manager/model/monitor"
)

// Migrate registers the monitor_panels table.
func Migrate(db *gorm.DB) error {
	return db.AutoMigrate(&model.Panel{})
}
