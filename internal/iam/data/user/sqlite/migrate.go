package sqlite

import (
	"gorm.io/gorm"

	"github.com/ongridio/ongrid/internal/iam/model"
)

// Migrate registers the iam user model with gorm's AutoMigrate. It is
// dialect-agnostic — gorm picks BIGINT UNSIGNED AUTO_INCREMENT on MySQL and
// INTEGER AUTOINCREMENT on SQLite, VARCHAR(N) vs TEXT for sized strings, and
// respects the declarative `check:` constraints on both backends.
//
// cmd/ongrid composes this with the other data packages' Migrate functions
// via dbx.RunMigrations at startup.
func Migrate(db *gorm.DB) error {
	if err := db.AutoMigrate(&model.User{}); err != nil {
		return err
	}
	// existing deploys had `chk_users_role` baked in with the
	// old 2-value set (admin / user). AutoMigrate doesn't ALTER existing
	// CHECK constraints, so drop + recreate idempotently. Error is
	// silently ignored — fresh deploys never have the stale constraint
	// and the new one is already in place via the struct tag. On MySQL
	// the DROP succeeds; on SQLite checks live inline with the column
	// type and there's nothing to drop.
	_ = db.Exec("ALTER TABLE users DROP CONSTRAINT chk_users_role").Error
	_ = db.Exec("ALTER TABLE users ADD CONSTRAINT chk_users_role CHECK (role IN ('admin','user','viewer'))").Error
	return nil
}
