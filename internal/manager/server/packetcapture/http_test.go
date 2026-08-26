package packetcapture

import (
	"encoding/json"
	"strings"
	"testing"
	"time"

	bizpacketcapture "github.com/ongridio/ongrid/internal/manager/biz/packetcapture"
	model "github.com/ongridio/ongrid/internal/manager/model/packetcapture"
)

func TestToSessionListDTOKeepsSummaryOnly(t *testing.T) {
	session := &model.Session{
		PublicID:        "pcap-session-test",
		Source:          "chat",
		State:           model.SessionStateReady,
		Title:           "HTTPS capture",
		CanonicalFilter: "tcp port 443",
		PlannedStartAt:  time.Date(2026, 8, 15, 1, 2, 3, 0, time.UTC),
		ClockQuality:    "uncalibrated",
		CreatedAt:       time.Date(2026, 8, 15, 1, 0, 0, 0, time.UTC),
		UpdatedAt:       time.Date(2026, 8, 15, 1, 3, 0, 0, time.UTC),
		AnalysisJSON: `{
			"summary":{"capture_count":2,"ready_count":1,"flow_count":99,"event_count":5000,"clock_quality":"uncalibrated","warning":"compare ordering only"},
			"flows":[{"id":"tcp|10.0.0.1:443|10.0.0.2:51515","packets":123}],
			"timeline":[{"source":"10.0.0.1","destination":"10.0.0.2","info":"payload detail"}]
		}`,
	}
	dto := toSessionListDTO(session)
	raw, err := json.Marshal(dto)
	if err != nil {
		t.Fatalf("Marshal dto: %v", err)
	}
	body := string(raw)
	for _, forbidden := range []string{"flows", "timeline", "payload detail", "10.0.0.1:443"} {
		if strings.Contains(body, forbidden) {
			t.Fatalf("list dto leaked %q in %s", forbidden, body)
		}
	}
	if !strings.Contains(body, `"ready_count":1`) || !strings.Contains(body, `"flow_count":99`) {
		t.Fatalf("list dto lost summary: %s", body)
	}
}

func TestToSessionAnalysisDTOOmitsTimeline(t *testing.T) {
	dto := toSessionAnalysisDTO(bizpacketcapture.SessionAnalysis{
		Summary: bizpacketcapture.SessionSummary{CaptureCount: 2, ReadyCount: 2, FlowCount: 1, EventCount: 3},
		Flows: []bizpacketcapture.SessionFlow{{
			ID:        "tcp|10.0.0.1:443|10.0.0.2:51515",
			Protocol:  "TLSv1.2",
			Endpoints: []string{"10.0.0.1:443", "10.0.0.2:51515"},
			Packets:   3,
		}},
		Timeline: []bizpacketcapture.SessionEvent{{
			Source:      "10.0.0.1",
			Destination: "10.0.0.2",
			Info:        "packet detail",
		}},
	})

	raw, err := json.Marshal(dto)
	if err != nil {
		t.Fatalf("Marshal dto: %v", err)
	}
	body := string(raw)
	for _, forbidden := range []string{"timeline", "packet detail"} {
		if strings.Contains(body, forbidden) {
			t.Fatalf("session detail dto leaked %q in %s", forbidden, body)
		}
	}
	if !strings.Contains(body, `"protocol":"TLSv1.2"`) || !strings.Contains(body, `"event_count":3`) {
		t.Fatalf("session detail dto lost flow summary: %s", body)
	}
}
