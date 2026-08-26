package tools

import (
	"encoding/json"
	"fmt"
	"time"

	"github.com/ongridio/ongrid/internal/pkg/logquery"
)

// queryLogQLToolSearchResult keeps only the fields the model needs to locate
// and explain a log line. The Logs UI continues to use logquery.SearchResult.
type queryLogQLToolSearchResult struct {
	Records  []queryLogQLToolRecord `json:"records"`
	HasMore  bool                   `json:"has_more"`
	TookMS   int64                  `json:"took_ms"`
	Backends []string               `json:"backends"`
}

type queryLogQLToolRecord struct {
	Timestamp    time.Time `json:"timestamp"`
	Message      string    `json:"message"`
	SeverityText string    `json:"severity_text,omitempty"`
	DeviceID     string    `json:"device_id,omitempty"`
	ClusterID    string    `json:"cluster_id,omitempty"`
	Namespace    string    `json:"namespace,omitempty"`
	Workload     string    `json:"workload,omitempty"`
	Pod          string    `json:"pod,omitempty"`
	Container    string    `json:"container,omitempty"`
	Node         string    `json:"node,omitempty"`
	SourceID     string    `json:"source_id,omitempty"`
	File         string    `json:"file,omitempty"`
	Unit         string    `json:"unit,omitempty"`
	TraceID      string    `json:"trace_id,omitempty"`
}

type queryLogQLToolLokiStream struct {
	Stream map[string]string   `json:"stream"`
	Values [][]json.RawMessage `json:"values"`
}

// marshalQueryLogQLToolResult preserves the selected backend's established
// outer shape while removing storage and model-context noise from log rows.
func marshalQueryLogQLToolResult(result logquery.QueryLogQLResult) ([]byte, error) {
	switch typed := result.(type) {
	case *logquery.SearchResult:
		if typed == nil {
			return nil, fmt.Errorf("query_logql: Elasticsearch query result is nil")
		}
		compact := queryLogQLToolSearchResult{
			Records:  make([]queryLogQLToolRecord, 0, len(typed.Records)),
			HasMore:  typed.HasMore,
			TookMS:   typed.TookMS,
			Backends: typed.Backends,
		}
		for _, record := range typed.Records {
			compact.Records = append(compact.Records, compactQueryLogQLToolRecord(record))
		}
		return json.Marshal(compact)
	case *logquery.QueryRangeResult:
		if typed == nil {
			return nil, fmt.Errorf("query_logql: Loki query result is nil")
		}
		if typed.ResultType != "streams" {
			return json.Marshal(typed)
		}
		compactResult, err := compactQueryLogQLLokiStreams(typed.Result)
		if err != nil {
			return nil, err
		}
		return json.Marshal(&logquery.QueryRangeResult{
			ResultType: typed.ResultType,
			Result:     compactResult,
		})
	default:
		return nil, fmt.Errorf("query_logql: unsupported result type %T", result)
	}
}

func compactQueryLogQLToolRecord(record logquery.Record) queryLogQLToolRecord {
	return queryLogQLToolRecord{
		Timestamp:    record.Timestamp,
		Message:      record.Message,
		SeverityText: record.SeverityText,
		DeviceID:     queryLogQLRecordField(record, "device_id"),
		ClusterID:    queryLogQLRecordField(record, "cluster_id"),
		Namespace:    queryLogQLRecordField(record, "namespace"),
		Workload:     queryLogQLRecordField(record, "workload"),
		Pod:          queryLogQLRecordField(record, "pod"),
		Container:    queryLogQLRecordField(record, "container"),
		Node:         queryLogQLRecordField(record, "node"),
		SourceID:     queryLogQLRecordField(record, "source_id"),
		File:         queryLogQLRecordField(record, "file"),
		Unit:         queryLogQLRecordField(record, "unit"),
		TraceID:      record.TraceID,
	}
}

func queryLogQLRecordField(record logquery.Record, names ...string) string {
	for _, name := range names {
		if value := record.ResourceAttributes[name]; value != "" {
			return value
		}
	}
	for _, name := range names {
		if value := record.Attributes[name]; value != "" {
			return value
		}
	}
	return ""
}

func compactQueryLogQLLokiStreams(raw json.RawMessage) (json.RawMessage, error) {
	var streams []queryLogQLToolLokiStream
	if err := json.Unmarshal(raw, &streams); err != nil {
		return nil, fmt.Errorf("query_logql: decode Loki streams for compact response: %w", err)
	}
	for streamIndex := range streams {
		streams[streamIndex].Stream = compactQueryLogQLLokiDimensions(streams[streamIndex].Stream)
		for valueIndex, value := range streams[streamIndex].Values {
			if len(value) < 2 {
				return nil, fmt.Errorf("query_logql: compact Loki stream %d value %d: expected timestamp and message", streamIndex, valueIndex)
			}
			compactValue := append([]json.RawMessage(nil), value[:2]...)
			if len(value) >= 3 && string(value[2]) != "null" {
				var metadata map[string]string
				if err := json.Unmarshal(value[2], &metadata); err != nil {
					return nil, fmt.Errorf("query_logql: decode Loki stream %d value %d metadata: %w", streamIndex, valueIndex, err)
				}
				metadata = compactQueryLogQLLokiDimensions(metadata)
				if len(metadata) > 0 {
					encoded, err := json.Marshal(metadata)
					if err != nil {
						return nil, fmt.Errorf("query_logql: encode Loki stream %d value %d metadata: %w", streamIndex, valueIndex, err)
					}
					compactValue = append(compactValue, encoded)
				}
			}
			streams[streamIndex].Values[valueIndex] = compactValue
		}
	}
	encoded, err := json.Marshal(streams)
	if err != nil {
		return nil, fmt.Errorf("query_logql: encode compact Loki streams: %w", err)
	}
	return encoded, nil
}

func compactQueryLogQLLokiDimensions(input map[string]string) map[string]string {
	out := make(map[string]string)
	for _, name := range []string{
		"device_id", "cluster_id", "namespace", "workload", "pod", "container",
		"node", "ongrid_source", "level", "filename", "unit", "trace_id",
	} {
		if value := input[name]; value != "" {
			out[name] = value
		}
	}
	return out
}
