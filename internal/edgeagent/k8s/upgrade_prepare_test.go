package k8s

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

func TestPrepareUpgradeStopsLegacyControllerMetricsBeforeSplit(t *testing.T) {
	t.Parallel()

	var mu sync.Mutex
	patches := make([]map[string]any, 0, 1)
	patched := false
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/apis/apps/v1/namespaces/ongrid-system/deployments/ongrid-edge-controller" {
			http.NotFound(w, r)
			return
		}
		switch r.Method {
		case http.MethodGet:
			mu.Lock()
			isPatched := patched
			mu.Unlock()
			writeUpgradeDeployment(t, w, legacyUpgradeController(isPatched))
		case http.MethodPatch:
			if got := r.Header.Get("Content-Type"); got != kubernetesStrategicMergePatchContent {
				t.Errorf("patch Content-Type = %q", got)
			}
			body, err := io.ReadAll(r.Body)
			if err != nil {
				t.Errorf("read patch: %v", err)
				http.Error(w, err.Error(), http.StatusBadRequest)
				return
			}
			var patch map[string]any
			if err := json.Unmarshal(body, &patch); err != nil {
				t.Errorf("decode patch: %v", err)
				http.Error(w, err.Error(), http.StatusBadRequest)
				return
			}
			mu.Lock()
			patches = append(patches, patch)
			patched = true
			mu.Unlock()
			writeUpgradeDeployment(t, w, legacyUpgradeController(true))
		default:
			http.Error(w, "unexpected method", http.StatusMethodNotAllowed)
		}
	}))
	defer server.Close()

	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	err := prepareUpgradeWithClient(ctx, &apiClient{baseURL: server.URL, http: server.Client()}, UpgradePreparationConfig{
		Namespace:                "ongrid-system",
		ControllerDeployment:     "ongrid-edge-controller",
		MetricsScraperDeployment: "ongrid-edge-metrics-scraper",
		TargetGatewayMode:        "deployment",
		TargetMetricsMode:        "scraper",
		PollInterval:             time.Millisecond,
	})
	if err != nil {
		t.Fatalf("prepareUpgradeWithClient() error = %v", err)
	}

	mu.Lock()
	defer mu.Unlock()
	if len(patches) != 1 {
		t.Fatalf("patch count = %d, want 1", len(patches))
	}
	raw, err := json.Marshal(patches[0])
	if err != nil {
		t.Fatalf("marshal captured patch: %v", err)
	}
	for _, want := range []string{
		`"ongrid.io/telemetry-backend":"true"`,
		`"name":"ONGRID_K8S_METRICS_ENDPOINT"`,
		`"$patch":"delete"`,
		`"name":"ONGRID_K8S_APP_METRICS_DISCOVERY"`,
		`"value":"false"`,
		`"valueFrom":null`,
	} {
		if !strings.Contains(string(raw), want) {
			t.Fatalf("patch = %s, missing %s", raw, want)
		}
	}
}

func TestPrepareUpgradeAlreadySplitDoesNotPatch(t *testing.T) {
	t.Parallel()

	var patchCount atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodPatch {
			patchCount.Add(1)
			http.Error(w, "image-only upgrade must not patch", http.StatusInternalServerError)
			return
		}
		if r.URL.Path != "/apis/apps/v1/namespaces/ongrid-system/deployments/ongrid-edge-controller" {
			http.NotFound(w, r)
			return
		}
		writeUpgradeDeployment(t, w, splitUpgradeController())
	}))
	defer server.Close()

	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	err := prepareUpgradeWithClient(ctx, &apiClient{baseURL: server.URL, http: server.Client()}, UpgradePreparationConfig{
		Namespace:                "ongrid-system",
		ControllerDeployment:     "ongrid-edge-controller",
		MetricsScraperDeployment: "ongrid-edge-metrics-scraper",
		TargetGatewayMode:        "deployment",
		TargetMetricsMode:        "scraper",
		PollInterval:             time.Millisecond,
	})
	if err != nil {
		t.Fatalf("prepareUpgradeWithClient() error = %v", err)
	}
	if got := patchCount.Load(); got != 0 {
		t.Fatalf("patch count = %d, want 0", got)
	}
}

func TestPrepareUpgradeStopsScraperBeforeControllerRollback(t *testing.T) {
	t.Parallel()

	var mu sync.Mutex
	scraperStopped := false
	patchCount := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/apis/apps/v1/namespaces/ongrid-system/deployments/ongrid-edge-metrics-scraper":
			mu.Lock()
			stopped := scraperStopped
			mu.Unlock()
			if r.Method == http.MethodPatch {
				body, err := io.ReadAll(r.Body)
				if err != nil {
					t.Errorf("read scraper patch: %v", err)
				}
				if !strings.Contains(string(body), `"replicas":0`) {
					t.Errorf("scraper patch = %s, want replicas 0", body)
				}
				mu.Lock()
				scraperStopped = true
				patchCount++
				stopped = true
				mu.Unlock()
			}
			replicas := 1
			generation := int64(1)
			if stopped {
				replicas = 0
				generation = 2
			}
			writeUpgradeDeployment(t, w, upgradeDeploymentObject(generation, nil, []map[string]any{{"name": "metrics-scraper"}}, replicas))
		case "/apis/apps/v1/namespaces/ongrid-system/deployments/ongrid-edge-controller":
			if r.Method != http.MethodGet {
				t.Errorf("controller method = %s, want GET", r.Method)
			}
			writeUpgradeDeployment(t, w, splitUpgradeController())
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()

	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	err := prepareUpgradeWithClient(ctx, &apiClient{baseURL: server.URL, http: server.Client()}, UpgradePreparationConfig{
		Namespace:                "ongrid-system",
		ControllerDeployment:     "ongrid-edge-controller",
		MetricsScraperDeployment: "ongrid-edge-metrics-scraper",
		TargetGatewayMode:        "embedded",
		TargetMetricsMode:        "controller",
		PollInterval:             time.Millisecond,
	})
	if err != nil {
		t.Fatalf("prepareUpgradeWithClient() error = %v", err)
	}
	mu.Lock()
	defer mu.Unlock()
	if !scraperStopped || patchCount != 1 {
		t.Fatalf("scraper stopped = %v, patch count = %d; want true, 1", scraperStopped, patchCount)
	}
}

func legacyUpgradeController(patched bool) map[string]any {
	generation := int64(1)
	labels := map[string]string{"app.kubernetes.io/component": "controller"}
	env := []map[string]any{
		{
			"name": "ONGRID_K8S_METRICS_ENDPOINT",
			"valueFrom": map[string]any{
				"configMapKeyRef": map[string]string{"name": "ongrid-edge-config", "key": "k8s-metrics-endpoint"},
			},
		},
		{
			"name": "ONGRID_K8S_APP_METRICS_DISCOVERY",
			"valueFrom": map[string]any{
				"configMapKeyRef": map[string]string{"name": "ongrid-edge-config", "key": "k8s-app-metrics-discovery"},
			},
		},
	}
	if patched {
		generation = 2
		labels[telemetryBackendLabel] = "true"
		env = []map[string]any{{"name": "ONGRID_K8S_APP_METRICS_DISCOVERY", "value": "false"}}
	}
	return upgradeDeploymentObject(generation, labels, []map[string]any{{
		"name":  "edge-controller",
		"ports": []map[string]any{{"name": "otlp-grpc"}, {"name": "otlp-http"}},
		"env":   env,
	}}, 1)
}

func splitUpgradeController() map[string]any {
	return upgradeDeploymentObject(3, map[string]string{
		"app.kubernetes.io/component": "controller",
		telemetryBackendLabel:         "false",
	}, []map[string]any{{
		"name": "edge-controller",
		"env":  []map[string]any{{"name": "ONGRID_K8S_APP_METRICS_DISCOVERY", "value": "false"}},
	}}, 1)
}

func upgradeDeploymentObject(generation int64, labels map[string]string, containers []map[string]any, replicas int) map[string]any {
	return map[string]any{
		"metadata": map[string]any{"generation": generation},
		"spec": map[string]any{
			"replicas": replicas,
			"template": map[string]any{
				"metadata": map[string]any{"labels": labels},
				"spec":     map[string]any{"containers": containers},
			},
		},
		"status": map[string]any{
			"observedGeneration":  generation,
			"updatedReplicas":     replicas,
			"readyReplicas":       replicas,
			"availableReplicas":   replicas,
			"unavailableReplicas": 0,
		},
	}
}

func writeUpgradeDeployment(t *testing.T, w http.ResponseWriter, deployment map[string]any) {
	t.Helper()
	w.Header().Set("Content-Type", "application/json")
	if err := json.NewEncoder(w).Encode(deployment); err != nil {
		t.Errorf("encode deployment: %v", err)
	}
}
