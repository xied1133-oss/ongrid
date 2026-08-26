package store

import (
	"gorm.io/gorm"

	model "github.com/ongridio/ongrid/internal/manager/model/mcp"
)

// Migrate AutoMigrates the mcp_servers table. Registered in
// cmd/ongrid/main.go alongside the other data-package migrations.
func Migrate(db *gorm.DB) error {
	return db.AutoMigrate(&model.Server{})
}
