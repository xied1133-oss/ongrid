package custommetrics

import (
	"strings"
	"testing"
)

func TestParseSpecCustomDatabaseType(t *testing.T) {
	targets, err := parseSpec(map[string]interface{}{
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
	})
	if err != nil {
		t.Fatalf("parseSpec() error = %v", err)
	}
	if len(targets) != 1 {
		t.Fatalf("targets=%d, want 1", len(targets))
	}
	if targets[0].Kind != "redis" {
		t.Fatalf("Kind=%q, want redis", targets[0].Kind)
	}
	if got := targets[0].ExtraLabels["db_type"]; got != "redis" {
		t.Fatalf("ExtraLabels[db_type]=%q, want redis", got)
	}
	if got := targets[0].ExtraLabels["resource_category"]; got != "database" {
		t.Fatalf("ExtraLabels[resource_category]=%q, want database", got)
	}
}

func TestParseSpecRejectsUnsupportedCustomDatabaseType(t *testing.T) {
	_, err := parseSpec(map[string]interface{}{
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
	})
	if err == nil {
		t.Fatal("parseSpec() error = nil, want unsupported db_type error")
	}
	if !strings.Contains(err.Error(), "unsupported") {
		t.Fatalf("parseSpec() error = %v", err)
	}
}

func TestParseSpecRejectsDatabaseResourceWithoutDatabaseType(t *testing.T) {
	_, err := parseSpec(map[string]interface{}{
		"targets": []interface{}{
			map[string]interface{}{
				"id":         "db-exporter",
				"target_url": "http://127.0.0.1:9100/metrics",
				"resource": map[string]interface{}{
					"category": "database",
				},
			},
		},
	})
	if err == nil {
		t.Fatal("parseSpec() error = nil, want required db_type error")
	}
	if !strings.Contains(err.Error(), "database type required") {
		t.Fatalf("parseSpec() error = %v", err)
	}
}

func TestParseSpecRejectsUnsupportedResourceCategory(t *testing.T) {
	_, err := parseSpec(map[string]interface{}{
		"targets": []interface{}{
			map[string]interface{}{
				"id":         "db-exporter",
				"target_url": "http://127.0.0.1:9100/metrics",
				"resource": map[string]interface{}{
					"category": "queue",
					"type":     "kafka",
				},
			},
		},
	})
	if err == nil {
		t.Fatal("parseSpec() error = nil, want unsupported resource category error")
	}
	if !strings.Contains(err.Error(), "unsupported category") {
		t.Fatalf("parseSpec() error = %v", err)
	}
}

func TestParseSpecRejectsDuplicateTargetURL(t *testing.T) {
	_, err := parseSpec(map[string]interface{}{
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
	})
	if err == nil {
		t.Fatal("parseSpec() error = nil, want duplicate target_url error")
	}
	if !strings.Contains(err.Error(), "duplicate target_url") {
		t.Fatalf("parseSpec() error = %v", err)
	}
}
