// Package monitor holds the persistence entity for user-managed Monitor
// page panels. Operators create / edit / delete panels through the SPA;
// the rows are the source of truth and ongrid asynchronously mirrors
// them into a single Grafana dashboard so deep-links / "在 Grafana 中
//打开" keep working.
//
// One-way sync: ongrid is the source of truth. Edits made in Grafana to
// the mirrored dashboard are NOT pulled back — operators wanting to keep
// changes must round-trip through the ongrid UI.
package monitor

import "time"

// PanelType enumerates the renderable panel shapes. The SPA's
// PromQLPanel uses the same identifiers; values map 1:1 onto Grafana
// panel types so the mirror dashboard renders identically.
const (
	PanelTypeTimeseries = "timeseries"
	PanelTypeStat       = "stat"
	PanelTypeGauge      = "gauge"
)

// Panel is one user-defined Monitor panel.
//
// Ordinal controls render order on the page. Newly created panels get
// max(ordinal)+1 so they land at the bottom; reordering is done via
// PATCH ordinal. LastSyncError records the most recent Grafana mirror
// failure (if any); empty string means the last sync succeeded or has
// not been attempted yet.
type Panel struct {
	ID        uint64    `gorm:"primaryKey;autoIncrement"                                json:"id"`
	Title     string    `gorm:"size:128;not null"                                       json:"title"`
	Type      string    `gorm:"size:32;not null;default:timeseries"                     json:"type"`
	PromQL    string    `gorm:"type:text;not null;column:promql"                        json:"promql"`
	Legend    string    `gorm:"size:255;not null;default:''"                            json:"legend"`
	Unit      string    `gorm:"size:32;not null;default:''"                             json:"unit"`
	Ordinal   int       `gorm:"not null;default:0;index"                                json:"ordinal"`
	LastSyncError string `gorm:"size:512;not null;default:'';column:last_sync_error"     json:"last_sync_error,omitempty"`
	LastSyncAt    *time.Time `gorm:"column:last_sync_at"                                 json:"last_sync_at,omitempty"`
	UpdatedAt time.Time `gorm:"autoUpdateTime"                                          json:"updated_at"`
	CreatedAt time.Time `gorm:"autoCreateTime"                                          json:"created_at"`
}

// TableName pins the SQL table so package renames don't accidentally
// branch the schema.
func (Panel) TableName() string { return "monitor_panels" }

// ValidPanelType returns true if t is one of the supported panel types.
// Used by the biz validator before persisting.
func ValidPanelType(t string) bool {
	switch t {
	case PanelTypeTimeseries, PanelTypeStat, PanelTypeGauge:
		return true
	}
	return false
}
