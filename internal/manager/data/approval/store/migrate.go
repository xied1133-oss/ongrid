package store

import (
	"gorm.io/gorm"

	model "github.com/ongridio/ongrid/internal/manager/model/approval"
)

// Migrate AutoMigrates the approvals table (additive — new table only).
func Migrate(db *gorm.DB) error {
	return db.AutoMigrate(&model.Approval{})
}
