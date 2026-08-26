// Package store is the GORM-backed data layer for orgs.
package store

import (
	"gorm.io/gorm"

	"github.com/ongridio/ongrid/internal/iam/model"
)

// Migrate runs AutoMigrate for the orgs table.
func Migrate(db *gorm.DB) error {
	return db.AutoMigrate(&model.Org{})
}
