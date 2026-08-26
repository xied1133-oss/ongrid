package dbx

import (
	"errors"
	"fmt"
	"log/slog"
	"time"

	"gorm.io/gorm"
)

// Migrator is any function that registers models with gorm. Typically each
// data-layer package (e.g. internal/iam/data/user/sqlite) exposes one as
// sqlite.Migrate that calls db.AutoMigrate(&User{}, ...).
//
// The name "sqlite" in those packages is historical; the same function
// works for MySQL because AutoMigrate is dialect-agnostic.
type Migrator func(db *gorm.DB) error

// RunMigrations invokes each migrator in order. The first error aborts and
// is returned wrapped with the migrator's index (1-based) for easier
// diagnosis. A nil db or a nil migrator entry is reported as an error.
//
// Wall time is logged for each migrator so slow auto-migrations are easy
// to spot in operator logs.
func RunMigrations(db *gorm.DB, log *slog.Logger, migrators ...Migrator) error {
	if db == nil {
		return errors.New("dbx.RunMigrations: nil db")
	}
	for i, m := range migrators {
		if m == nil {
			return fmt.Errorf("dbx.RunMigrations: migrator #%d is nil", i+1)
		}
		if log != nil {
			log.Info("migration start", "index", i+1, "total", len(migrators))
		}
		start := time.Now()
		if err := m(db); err != nil {
			return fmt.Errorf("dbx.RunMigrations: migrator #%d: %w", i+1, err)
		}
		if log != nil {
			log.Info("migration done", "index", i+1, "elapsed", time.Since(start).String())
		}
	}
	return nil
}
