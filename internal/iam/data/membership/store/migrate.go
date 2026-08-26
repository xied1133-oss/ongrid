// Package store is the GORM-backed data layer for org memberships.
package store

import (
	"gorm.io/gorm"

	"github.com/ongridio/ongrid/internal/iam/model"
)

// Migrate runs AutoMigrate for the org_memberships table.
func Migrate(db *gorm.DB) error {
	return db.AutoMigrate(&model.OrgMembership{})
}
