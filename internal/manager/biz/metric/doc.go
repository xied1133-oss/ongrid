// Package metric is the manager/metric sub-domain biz layer.
//
// Responsibilities: async batched ingest of host metrics from edges, query
// with automatic table selection (raw / 5m / 1h) by time window,
// downsampling jobs (5m and 1h), retention enforcement.
package metric
