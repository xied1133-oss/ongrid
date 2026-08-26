package collector

import (
	"encoding/json"
	"math"
	"testing"
	"time"

	dto "github.com/prometheus/client_model/go"

	"github.com/ongridio/ongrid/internal/pkg/tunnel"
)

func TestFlattenSamplesDropsNonFiniteValues(t *testing.T) {
	name := "demo_value"
	typ := dto.MetricType_GAUGE
	finite := 7.0
	nan := math.NaN()
	inf := math.Inf(1)

	samples := FlattenSamples(time.Unix(1, 0), "custom:demo", []*dto.MetricFamily{{
		Name: &name,
		Type: &typ,
		Metric: []*dto.Metric{
			gaugeMetric("kind", "finite", finite),
			gaugeMetric("kind", "nan", nan),
			gaugeMetric("kind", "inf", inf),
		},
	}}, nil)

	if len(samples) != 1 {
		t.Fatalf("len(samples) = %d, want 1: %#v", len(samples), samples)
	}
	if got := samples[0].Labels["kind"]; got != "finite" {
		t.Fatalf("remaining sample kind = %q, want finite", got)
	}
	if _, err := json.Marshal(tunnel.PushPromSamplesRequest{
		Source:  "custom:demo",
		Samples: samples,
	}); err != nil {
		t.Fatalf("json.Marshal() error = %v", err)
	}
}

func gaugeMetric(labelName, labelValue string, value float64) *dto.Metric {
	return &dto.Metric{
		Label: []*dto.LabelPair{{
			Name:  &labelName,
			Value: &labelValue,
		}},
		Gauge: &dto.Gauge{Value: &value},
	}
}
