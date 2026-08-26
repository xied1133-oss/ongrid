// Package store is the data layer for the report sub-domain
// (report_schedules + reports). See HLD-014.
package store

import (
	"gorm.io/gorm"

	model "github.com/ongridio/ongrid/internal/manager/model/report"
	"github.com/ongridio/ongrid/internal/pkg/dbx"
)

// Migrate registers the report tables with gorm AutoMigrate. AutoMigrate
// adds new columns/indexes but never drops or narrows existing ones, so
// re-running on every boot is safe. Wired into the manager startup
// migration list in cmd/ongrid/main.go.
func Migrate(db *gorm.DB) error {
	if dbx.NeedsDeleteMarkerMigration(db, model.Report{}.TableName()) {
		if err := dbx.DropIndexes(
			db,
			&model.Report{},
			"uniq_report_sched_period",
			"idx_report_share",
		); err != nil {
			return err
		}
	}
	if err := db.AutoMigrate(
		&model.ReportSchedule{},
		&model.Report{},
		&model.Task{}, // HLD-022 Phase 2: unified task spine (oneoff rows)
	); err != nil {
		return err
	}
	if err := dbx.BackfillDeleteMarkerWithValue(db, model.Report{}.TableName(), "1"); err != nil {
		return err
	}
	// HLD-022 backfill: stamp the owning-task back-ref on existing scheduled
	// reports. Idempotent (only fills empty task_id), additive (a new column),
	// safe to re-run every boot.
	return db.Exec(
		"UPDATE reports SET task_id = CONCAT('report-schedule:', schedule_id) WHERE schedule_id IS NOT NULL AND (task_id IS NULL OR task_id = '')",
	).Error
}
