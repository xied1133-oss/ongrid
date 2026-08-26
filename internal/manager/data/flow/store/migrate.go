package store

import (
	"context"
	"encoding/json"
	"fmt"

	"gorm.io/gorm"

	model "github.com/ongridio/ongrid/internal/manager/model/flow"
)

// Migrate registers the flow tables with gorm AutoMigrate. AutoMigrate
// is additive-only (columns/indexes), same caveats as the sibling
// domains.
func Migrate(db *gorm.DB) error {
	return db.AutoMigrate(
		&model.Flow{},
		&model.FlowRun{},
		&model.FlowRunNode{},
	)
}

// MigrateLegacyIMNotificationTool rewrites saved workflow tool nodes created
// before send_im_message became a real IM group-send capability. Those nodes
// used the same name for notification delivery, so leaving them unchanged
// would redirect an existing workflow after upgrade.
func MigrateLegacyIMNotificationTool(ctx context.Context, db *gorm.DB) (int, error) {
	var flows []model.Flow
	if err := db.WithContext(ctx).
		Where("graph_json LIKE ?", "%send_im_message%").
		Find(&flows).Error; err != nil {
		return 0, fmt.Errorf("list legacy IM workflow nodes: %w", err)
	}
	migrated := 0
	for _, f := range flows {
		graph, changed, err := rewriteLegacyIMNotificationGraph(f.GraphJSON)
		if err != nil {
			return migrated, fmt.Errorf("rewrite flow %d: %w", f.ID, err)
		}
		if !changed {
			continue
		}
		if err := db.WithContext(ctx).Model(&model.Flow{}).Where("id = ?", f.ID).Updates(map[string]any{
			"graph_json": graph,
			"version":    gorm.Expr("version + 1"),
		}).Error; err != nil {
			return migrated, fmt.Errorf("update flow %d: %w", f.ID, err)
		}
		migrated++
	}
	return migrated, nil
}

func rewriteLegacyIMNotificationGraph(raw string) (string, bool, error) {
	var graph map[string]any
	if err := json.Unmarshal([]byte(raw), &graph); err != nil {
		return "", false, fmt.Errorf("decode graph JSON: %w", err)
	}
	nodes, ok := graph["nodes"].([]any)
	if !ok {
		return raw, false, nil
	}
	changed := false
	for _, value := range nodes {
		node, ok := value.(map[string]any)
		if !ok || node["type"] != "tool" {
			continue
		}
		cfg, ok := node["config"].(map[string]any)
		if !ok || cfg["tool"] != "send_im_message" {
			continue
		}
		cfg["tool"] = "send_notification"
		changed = true
	}
	if !changed {
		return raw, false, nil
	}
	updated, err := json.Marshal(graph)
	if err != nil {
		return "", false, fmt.Errorf("encode graph JSON: %w", err)
	}
	return string(updated), true, nil
}
