package edge

import (
	"context"
	"strings"
	"testing"

	model "github.com/ongridio/ongrid/internal/manager/model/edge"
)

func TestSetCustomMetricsRejectsDuplicateTargetURL(t *testing.T) {
	repo := newFakePluginConfigRepo()
	uc := NewPluginConfigUC(repo, nil, fakeEndpointResolver{}, nil)

	_, err := uc.Set(context.Background(), 7, model.PluginNameCustomMetrics, SetInput{
		Enabled: true,
		Spec: map[string]interface{}{
			"targets": []interface{}{
				map[string]interface{}{
					"id":         "mysql-exporter",
					"target_url": "http://127.0.0.1:9104/metrics",
				},
				map[string]interface{}{
					"id":         "mysql-exporter-copy",
					"target_url": "http://127.0.0.1:9104/metrics",
				},
			},
		},
	})
	if err == nil {
		t.Fatal("Set() error = nil, want duplicate target_url error")
	}
	if !strings.Contains(err.Error(), "duplicate target_url") {
		t.Fatalf("Set() error = %v", err)
	}
	if repo.rows[model.PluginNameCustomMetrics] != nil {
		t.Fatal("custommetrics row was persisted after validation error")
	}
}

func TestSetCustomMetricsAcceptsDatabaseType(t *testing.T) {
	repo := newFakePluginConfigRepo()
	uc := NewPluginConfigUC(repo, nil, fakeEndpointResolver{}, nil)

	row, err := uc.Set(context.Background(), 7, model.PluginNameCustomMetrics, SetInput{
		Enabled: true,
		Spec: map[string]interface{}{
			"targets": []interface{}{
				map[string]interface{}{
					"id":         "redis-exporter",
					"target_url": "http://127.0.0.1:9121/metrics",
					"resource": map[string]interface{}{
						"category": "database",
						"type":     "redis",
					},
				},
			},
		},
	})
	if err != nil {
		t.Fatalf("Set() error = %v", err)
	}
	targets, ok := row.Spec["targets"].([]interface{})
	if !ok || len(targets) != 1 {
		t.Fatalf("targets=%#v, want one target", row.Spec["targets"])
	}
	target, ok := targets[0].(map[string]interface{})
	if !ok {
		t.Fatalf("target=%T, want map", targets[0])
	}
	resource, ok := target["resource"].(map[string]interface{})
	if !ok {
		t.Fatalf("resource=%T, want map", target["resource"])
	}
	if got := resource["type"]; got != "redis" {
		t.Fatalf("resource.type=%v, want redis", got)
	}
}

func TestSetCustomMetricsRejectsDatabaseResourceWithoutDatabaseType(t *testing.T) {
	repo := newFakePluginConfigRepo()
	uc := NewPluginConfigUC(repo, nil, fakeEndpointResolver{}, nil)

	_, err := uc.Set(context.Background(), 7, model.PluginNameCustomMetrics, SetInput{
		Enabled: true,
		Spec: map[string]interface{}{
			"targets": []interface{}{
				map[string]interface{}{
					"id":         "db-exporter",
					"target_url": "http://127.0.0.1:9100/metrics",
					"resource": map[string]interface{}{
						"category": "database",
					},
				},
			},
		},
	})
	if err == nil {
		t.Fatal("Set() error = nil, want required db_type error")
	}
	if !strings.Contains(err.Error(), "resource.type required") {
		t.Fatalf("Set() error = %v", err)
	}
	if repo.rows[model.PluginNameCustomMetrics] != nil {
		t.Fatal("custommetrics row was persisted after validation error")
	}
}

func TestSetCustomMetricsRejectsUnsupportedDatabaseType(t *testing.T) {
	repo := newFakePluginConfigRepo()
	uc := NewPluginConfigUC(repo, nil, fakeEndpointResolver{}, nil)

	_, err := uc.Set(context.Background(), 7, model.PluginNameCustomMetrics, SetInput{
		Enabled: true,
		Spec: map[string]interface{}{
			"targets": []interface{}{
				map[string]interface{}{
					"id":         "oracle-exporter",
					"target_url": "http://127.0.0.1:9161/metrics",
					"resource": map[string]interface{}{
						"category": "database",
						"type":     "oracle",
					},
				},
			},
		},
	})
	if err == nil {
		t.Fatal("Set() error = nil, want unsupported db_type error")
	}
	if !strings.Contains(err.Error(), "unsupported") {
		t.Fatalf("Set() error = %v", err)
	}
	if repo.rows[model.PluginNameCustomMetrics] != nil {
		t.Fatal("custommetrics row was persisted after validation error")
	}
}

func TestSetCustomMetricsRejectsTopLevelDatabaseType(t *testing.T) {
	repo := newFakePluginConfigRepo()
	uc := NewPluginConfigUC(repo, nil, fakeEndpointResolver{}, nil)

	_, err := uc.Set(context.Background(), 7, model.PluginNameCustomMetrics, SetInput{
		Enabled: true,
		Spec: map[string]interface{}{
			"targets": []interface{}{
				map[string]interface{}{
					"id":         "db-exporter",
					"target_url": "http://127.0.0.1:9100/metrics",
					"db_type":    "mysql",
				},
			},
		},
	})
	if err == nil {
		t.Fatal("Set() error = nil, want top-level db_type error")
	}
	if !strings.Contains(err.Error(), "resource.category/type") {
		t.Fatalf("Set() error = %v", err)
	}
	if repo.rows[model.PluginNameCustomMetrics] != nil {
		t.Fatal("custommetrics row was persisted after validation error")
	}
}

func TestSetCustomMetricsRejectsUnsupportedResourceCategory(t *testing.T) {
	repo := newFakePluginConfigRepo()
	uc := NewPluginConfigUC(repo, nil, fakeEndpointResolver{}, nil)

	_, err := uc.Set(context.Background(), 7, model.PluginNameCustomMetrics, SetInput{
		Enabled: true,
		Spec: map[string]interface{}{
			"targets": []interface{}{
				map[string]interface{}{
					"id":         "kafka-exporter",
					"target_url": "http://127.0.0.1:9308/metrics",
					"resource": map[string]interface{}{
						"category": "queue",
						"type":     "kafka",
					},
				},
			},
		},
	})
	if err == nil {
		t.Fatal("Set() error = nil, want unsupported resource category error")
	}
	if !strings.Contains(err.Error(), "resource.category unsupported") {
		t.Fatalf("Set() error = %v", err)
	}
	if repo.rows[model.PluginNameCustomMetrics] != nil {
		t.Fatal("custommetrics row was persisted after validation error")
	}
}
