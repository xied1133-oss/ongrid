// Package promwrite is a tiny client for the Prometheus remote_write
// protocol (POST /api/v1/write, snappy-compressed protobuf body).
//
// The protobuf schema is hand-rolled (no protoc / generated code) because
// pulling github.com/prometheus/prometheus just for prompb would balloon
// the module graph by ~hundreds of indirect deps. The wire format is
// stable and trivially small:
//
//	message Label      { string name = 1; string value = 2; }
//	message Sample     { double value = 1; int64 timestamp = 2; }
//	message TimeSeries { repeated Label labels = 1; repeated Sample samples = 2; }
//	message WriteRequest { repeated TimeSeries timeseries = 1; }
//
// Only the encode path is needed (manager pushes; never reads remote_write).
//
// Cross-BC: this package is dependency-free of any manager/* import; it
// lives under internal/pkg/ so both the manager-side biz wrapper and any
// future internal user (e.g. tests) can reuse it.
package promwrite
