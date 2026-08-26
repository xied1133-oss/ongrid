package report

import "time"

// task.go — the unified "任务" spine (HLD-022 Phase 2). A Task is the entity a
// user manages on the 任务 page. The physical table stores only NON-recurring
// tasks (today: kind="oneoff" — a one-shot generation triggered immediately
// from the task side). Recurring tasks remain report_schedules and are unioned
// in as tasks at the API layer (id="report-schedule:<id>"), so the 任务 list is
// a single unified surface without a disruptive migration of the schedule path.
//
// Artifacts (reports) back-reference their owning task via report.task_id:
//   - recurring → "report-schedule:<schedule_id>"
//   - oneoff    → "oneoff:<task_id>"
// so a task's detail lists every artifact it produced, regardless of kind.

const (
	TaskKindOneoff    = "oneoff"           // one-shot, run immediately from the task side
	TaskKindRecurring = "recurring_report" // view over report_schedules (not stored here)
)

// Task is a stored non-recurring task. Recurring tasks are NOT rows here.
type Task struct {
	ID string `gorm:"primaryKey;type:char(36);column:id"`

	// Kind is the task type. Stored rows are "oneoff" today; the constant set
	// leaves room for chat_todo etc. without a schema change.
	Kind string `gorm:"column:kind;size:32;not null;index"`

	Title string `gorm:"column:title;size:255;not null"`

	// ReportKind / ScopeJSON snapshot the report config a oneoff task generates
	// with (daily/weekly/monthly + data scope), so the task is reproducible.
	ReportKind string `gorm:"column:report_kind;size:16;not null;default:''"`
	ScopeJSON  string `gorm:"column:scope_json;type:text;not null"`

	// Status: active | done | failed — mirrors the latest run's outcome for the
	// task list badge.
	Status    string `gorm:"column:status;size:16;not null;default:'active'"`
	CreatedBy uint64 `gorm:"column:created_by;not null"`

	CreatedAt time.Time  `gorm:"column:created_at;autoCreateTime"`
	UpdatedAt time.Time  `gorm:"column:updated_at;autoUpdateTime"`
	DeletedAt *time.Time `gorm:"column:deleted_at;index"`
}

// TableName pins the schema name.
func (Task) TableName() string { return "tasks" }

// TaskRef builds the artifact back-ref string for a oneoff task id.
func OneoffTaskRef(taskID string) string { return "oneoff:" + taskID }
