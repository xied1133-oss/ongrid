package tools

import (
	"encoding/json"
	"strconv"
	"testing"
	"time"

	"github.com/ongridio/ongrid/internal/pkg/logquery"
)

func TestLogEntriesFromQueryLogQLResultSupportsSelectedBackendShapes(t *testing.T) {
	ts := time.Date(2026, 8, 24, 3, 4, 5, 6, time.UTC)
	tests := []struct {
		name   string
		result logquery.QueryLogQLResult
	}{
		{
			name: "loki",
			result: &logquery.QueryRangeResult{
				ResultType: "streams",
				Result: json.RawMessage(`[{"stream":{"device_id":"42","level":"error"},"values":[["` +
					formatLokiNanoTimestamp(ts) + `","loki failure"]]}]`),
			},
		},
		{
			name: "elasticsearch",
			result: &logquery.SearchResult{Records: []logquery.Record{{
				Timestamp: ts, Message: "elasticsearch failure", SeverityText: "ERROR", Backend: "elasticsearch",
				ResourceAttributes: map[string]string{"device_id": "42"},
			}}},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			entries, err := logEntriesFromQueryLogQLResult(tt.result)
			if err != nil {
				t.Fatalf("logEntriesFromQueryLogQLResult() error = %v", err)
			}
			if len(entries) != 1 || entries[0].Timestamp != ts || entries[0].Labels["device_id"] != "42" {
				t.Fatalf("entries = %#v", entries)
			}
		})
	}
}

func formatLokiNanoTimestamp(value time.Time) string {
	return strconv.FormatInt(value.UnixNano(), 10)
}
