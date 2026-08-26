// Package sqlite is the GORM-backed persistence layer for the
// system_settings table. The package is named "sqlite" by convention
// (see iam/data/user/sqlite, manager/data/alert/store); the AutoMigrate
// call is dialect-agnostic and works on MySQL just as well.
package store

import (
	"gorm.io/gorm"

	model "github.com/ongridio/ongrid/internal/manager/model/setting"
)

// Migrate registers the system_settings table with GORM's AutoMigrate.
// Composed from cmd/ongrid via dbx.RunMigrations like the other BC
// migrations.
func Migrate(db *gorm.DB) error {
	return db.AutoMigrate(&model.Setting{})
}
