package tools

import (
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/ongridio/ongrid/internal/pkg/logquery"
)

func TestMarshalQueryLogQLToolResult_ElasticsearchKeepsOnlyKeyFields(t *testing.T) {
	timestamp := time.Date(2026, 8, 24, 4, 5, 6, 7, time.UTC)
	result := &logquery.SearchResult{
		Records: []logquery.Record{{
			ID:                "document-id",
			Timestamp:         timestamp,
			ObservedTimestamp: timestamp.Add(time.Second),
			Message:           "request timed out",
			SeverityText:      "error",
			SeverityNumber:    17,
			Backend:           "elasticsearch",
			Attributes: map[string]string{
				"custom": "drop-me",
				"unit":   "nginx.service",
			},
			ResourceAttributes: map[string]string{
				"device_id":    "123",
				"cluster_id":   "48",
				"namespace":    "ongrid-system",
				"workload":     "ongrid-edge-node",
				"pod":          "ongrid-edge-node-hgzlq",
				"container":    "edge-node",
				"node":         "worker1",
				"source_id":    "kubernetes:pod",
				"file":         "/var/log/pods/edge.log",
				"service_name": "unknown_service",
				"custom":       "drop-me-too",
			},
			TraceID: "trace-1",
			SpanID:  "span-1",
		}},
		NextCursor: "unused-cursor",
		HasMore:    true,
		TookMS:     237,
		Backends:   []string{"elasticsearch"},
	}

	body, err := marshalQueryLogQLToolResult(result)
	if err != nil {
		t.Fatalf("marshalQueryLogQLToolResult: %v", err)
	}

	var compact queryLogQLToolSearchResult
	if err := json.Unmarshal(body, &compact); err != nil {
		t.Fatalf("decode compact result: %v", err)
	}
	if len(compact.Records) != 1 {
		t.Fatalf("records = %d, want 1", len(compact.Records))
	}
	record := compact.Records[0]
	if record.Timestamp != timestamp || record.Message != "request timed out" || record.SeverityText != "error" {
		t.Fatalf("record core fields = %#v", record)
	}
	if record.DeviceID != "123" || record.ClusterID != "48" || record.Namespace != "ongrid-system" ||
		record.Workload != "ongrid-edge-node" || record.Pod != "ongrid-edge-node-hgzlq" ||
		record.Container != "edge-node" || record.Node != "worker1" {
		t.Fatalf("record location fields = %#v", record)
	}
	if record.SourceID != "kubernetes:pod" || record.File != "/var/log/pods/edge.log" ||
		record.Unit != "nginx.service" || record.TraceID != "trace-1" {
		t.Fatalf("record optional key fields = %#v", record)
	}
	if !compact.HasMore || compact.TookMS != 237 || len(compact.Backends) != 1 || compact.Backends[0] != "elasticsearch" {
		t.Fatalf("query metadata = %#v", compact)
	}

	var envelope map[string]json.RawMessage
	if err := json.Unmarshal(body, &envelope); err != nil {
		t.Fatalf("decode result envelope: %v", err)
	}
	if _, ok := envelope["next_cursor"]; ok {
		t.Fatalf("next_cursor leaked into compact result: %s", body)
	}
	var records []map[string]json.RawMessage
	if err := json.Unmarshal(envelope["records"], &records); err != nil {
		t.Fatalf("decode raw records: %v", err)
	}
	for _, field := range []string{
		"id", "observed_timestamp", "severity_number", "backend", "attributes",
		"resource_attributes", "span_id", "service_name",
	} {
		if _, ok := records[0][field]; ok {
			t.Errorf("field %q leaked into compact record: %s", field, body)
		}
	}
}

func TestMarshalQueryLogQLToolResult_LokiStreamsKeepOnlyKeyDimensions(t *testing.T) {
	result := &logquery.QueryRangeResult{
		ResultType: "streams",
		Result: json.RawMessage(`[{
			"stream":{
				"device_id":"123",
				"cluster_id":"48",
				"namespace":"ongrid-system",
				"pod":"edge-node-1",
				"service_name":"unknown_service",
				"span_id":"span-1",
				"job":"drop-me"
			},
			"values":[[
				"1700000000000000000",
				"request timed out",
				{
					"ongrid_source":"kubernetes:pod",
					"level":"error",
					"trace_id":"trace-1",
					"custom":"drop-me"
				}
			]]
		}]`),
	}

	body, err := marshalQueryLogQLToolResult(result)
	if err != nil {
		t.Fatalf("marshalQueryLogQLToolResult: %v", err)
	}
	var compact logquery.QueryRangeResult
	if err := json.Unmarshal(body, &compact); err != nil {
		t.Fatalf("decode compact result: %v", err)
	}
	if compact.ResultType != "streams" {
		t.Fatalf("resultType = %q, want streams", compact.ResultType)
	}
	var streams []queryLogQLToolLokiStream
	if err := json.Unmarshal(compact.Result, &streams); err != nil {
		t.Fatalf("decode compact streams: %v", err)
	}
	if len(streams) != 1 || len(streams[0].Values) != 1 {
		t.Fatalf("streams = %#v", streams)
	}
	if streams[0].Stream["device_id"] != "123" || streams[0].Stream["cluster_id"] != "48" ||
		streams[0].Stream["namespace"] != "ongrid-system" || streams[0].Stream["pod"] != "edge-node-1" {
		t.Fatalf("stream dimensions = %#v", streams[0].Stream)
	}
	for _, field := range []string{"service_name", "span_id", "job"} {
		if _, ok := streams[0].Stream[field]; ok {
			t.Errorf("stream field %q was not removed: %#v", field, streams[0].Stream)
		}
	}
	value := streams[0].Values[0]
	if len(value) != 3 {
		t.Fatalf("value parts = %d, want timestamp, message, compact metadata", len(value))
	}
	var metadata map[string]string
	if err := json.Unmarshal(value[2], &metadata); err != nil {
		t.Fatalf("decode compact metadata: %v", err)
	}
	if metadata["ongrid_source"] != "kubernetes:pod" || metadata["level"] != "error" || metadata["trace_id"] != "trace-1" {
		t.Fatalf("metadata = %#v", metadata)
	}
	if _, ok := metadata["custom"]; ok {
		t.Errorf("custom metadata was not removed: %#v", metadata)
	}
}

func TestMarshalQueryLogQLToolResult_LokiMetricResultStaysNative(t *testing.T) {
	raw := json.RawMessage(`[{"metric":{"pod":"edge-node-1","job":"loki-native"},"values":[[1,"2"]]}]`)
	body, err := marshalQueryLogQLToolResult(&logquery.QueryRangeResult{ResultType: "matrix", Result: raw})
	if err != nil {
		t.Fatalf("marshalQueryLogQLToolResult: %v", err)
	}
	if !strings.Contains(string(body), `"job":"loki-native"`) {
		t.Fatalf("metric result was unexpectedly compacted: %s", body)
	}
}

func TestMarshalQueryLogQLToolResult_LokiRejectsMalformedStreamValue(t *testing.T) {
	_, err := marshalQueryLogQLToolResult(&logquery.QueryRangeResult{
		ResultType: "streams",
		Result:     json.RawMessage(`[{"stream":{},"values":[["1700000000000000000"]]}]`),
	})
	if err == nil || !strings.Contains(err.Error(), "expected timestamp and message") {
		t.Fatalf("error = %v, want malformed value error", err)
	}
}
