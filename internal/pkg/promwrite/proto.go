package promwrite

import (
	"encoding/binary"
	"math"
)

// This file is the hand-rolled proto3 encoder for the Prometheus
// remote_write WriteRequest. The schema is fixed at four trivial messages
// (Label, Sample, TimeSeries, WriteRequest); rolling it by hand avoids
// dragging in github.com/prometheus/prometheus/prompb (which transitively
// pulls in most of the Prometheus server). Decode is intentionally absent —
// the manager only writes.
//
// proto3 wire-format primer (only what we need):
//   - tag = (field_number << 3) | wire_type
//   - wire_type 0 = varint
//   - wire_type 1 = 64-bit fixed (used for double, sfixed64, fixed64)
//   - wire_type 2 = length-delimited (string, bytes, embedded message)
//
// All field numbers below match the official Prometheus proto.

const (
	wireVarint = 0
	wireI64    = 1
	wireBytes  = 2
)

// appendVarint appends a base-128 varint to b.
func appendVarint(b []byte, v uint64) []byte {
	for v >= 0x80 {
		b = append(b, byte(v)|0x80)
		v >>= 7
	}
	return append(b, byte(v))
}

// appendTag appends a (field_number, wire_type) tag.
func appendTag(b []byte, fieldNum, wireType int) []byte {
	return appendVarint(b, uint64(fieldNum)<<3|uint64(wireType))
}

// appendString appends a length-delimited string field.
func appendString(b []byte, fieldNum int, s string) []byte {
	if s == "" {
		// proto3: skip default values.
		return b
	}
	b = appendTag(b, fieldNum, wireBytes)
	b = appendVarint(b, uint64(len(s)))
	return append(b, s...)
}

// appendBytesField appends a length-delimited bytes field (or sub-message).
func appendBytesField(b []byte, fieldNum int, payload []byte) []byte {
	b = appendTag(b, fieldNum, wireBytes)
	b = appendVarint(b, uint64(len(payload)))
	return append(b, payload...)
}

// appendDouble appends a fixed64 (IEEE-754) double field.
func appendDouble(b []byte, fieldNum int, v float64) []byte {
	if v == 0 {
		// proto3: skip default values. Sample.value can legitimately be 0
		// but Prom accepts that as default; the timestamp still pins the
		// sample. We emit zero anyway to be safe — Prom handles either.
	}
	b = appendTag(b, fieldNum, wireI64)
	var buf [8]byte
	binary.LittleEndian.PutUint64(buf[:], math.Float64bits(v))
	return append(b, buf[:]...)
}

// appendInt64 appends a varint int64 field.
func appendInt64(b []byte, fieldNum int, v int64) []byte {
	if v == 0 {
		return b
	}
	b = appendTag(b, fieldNum, wireVarint)
	return appendVarint(b, uint64(v))
}

// encodeLabel returns a serialised Label message.
func encodeLabel(name, value string) []byte {
	var b []byte
	b = appendString(b, 1, name)
	b = appendString(b, 2, value)
	return b
}

// encodeSample returns a serialised Sample message.
// Prom proto: Sample { double value = 1; int64 timestamp = 2; }
// Note: even when value == 0 we emit the field so a zero-valued sample
// round-trips faithfully (the appendDouble helper always writes).
func encodeSample(value float64, tsMs int64) []byte {
	var b []byte
	b = appendDouble(b, 1, value)
	b = appendInt64(b, 2, tsMs)
	return b
}

// encodeTimeSeries returns a serialised TimeSeries message:
// labels (field 1), samples (field 2), repeated.
func encodeTimeSeries(labels []Label, samples []sampleEntry) []byte {
	var b []byte
	for _, l := range labels {
		b = appendBytesField(b, 1, encodeLabel(l.Name, l.Value))
	}
	for _, s := range samples {
		b = appendBytesField(b, 2, encodeSample(s.value, s.tsMs))
	}
	return b
}

// sampleEntry is the per-series sample shape used by encodeTimeSeries.
type sampleEntry struct {
	value float64
	tsMs  int64
}

// encodeWriteRequest returns a serialised WriteRequest message:
// timeseries (field 1), repeated.
func encodeWriteRequest(seriesPayloads [][]byte) []byte {
	var b []byte
	for _, p := range seriesPayloads {
		b = appendBytesField(b, 1, p)
	}
	return b
}
