// Package sqlite is the MVP writer/reader for manager/metric (+
//Writer uses GORM CreateInBatches(500) for raw / 5m / 1h and
// a dedicated path for dead-letter rows. Reader picks between
// host_metrics_raw / _5m / _1h by time window (driven by biz/metric
// QueryUsecase — the reader itself is a plain DAO).
package store
