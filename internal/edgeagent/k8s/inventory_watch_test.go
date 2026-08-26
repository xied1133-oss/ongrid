package k8s

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/ongridio/ongrid/internal/pkg/tunnel"
)

func TestAPIClientWatchDecodesEventsAndBookmarks(t *testing.T) {
	var gotPath, gotWatch, gotRV, gotBookmarks, gotTimeout string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Path
		gotWatch = r.URL.Query().Get("watch")
		gotRV = r.URL.Query().Get("resourceVersion")
		gotBookmarks = r.URL.Query().Get("allowWatchBookmarks")
		gotTimeout = r.URL.Query().Get("timeoutSeconds")
		if r.Header.Get("Authorization") != "Bearer test-token" {
			t.Fatalf("Authorization header = %q", r.Header.Get("Authorization"))
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"type":"ADDED","object":{"metadata":{"resourceVersion":"101","name":"api-1"}}}` + "\n"))
		_, _ = w.Write([]byte(`{"type":"BOOKMARK","object":{"metadata":{"resourceVersion":"102"}}}` + "\n"))
	}))
	defer srv.Close()

	c := &apiClient{
		baseURL: srv.URL,
		token:   "test-token",
		http:    srv.Client(),
	}
	var gotTypes []string
	latest, err := c.watch(context.Background(), "/api/v1/pods", "100", func(event k8sWatchEvent) error {
		gotTypes = append(gotTypes, event.Type)
		return nil
	})
	if err != nil {
		t.Fatalf("watch() error = %v", err)
	}
	if gotPath != "/api/v1/pods" || gotWatch != "1" || gotRV != "100" || gotBookmarks != "true" || gotTimeout == "" {
		t.Fatalf("unexpected watch request path=%q watch=%q rv=%q bookmarks=%q timeout=%q", gotPath, gotWatch, gotRV, gotBookmarks, gotTimeout)
	}
	if latest != "102" {
		t.Fatalf("latest resourceVersion = %q, want 102", latest)
	}
	if len(gotTypes) != 2 || gotTypes[0] != "ADDED" || gotTypes[1] != "BOOKMARK" {
		t.Fatalf("event types = %v", gotTypes)
	}
}

func TestAPIClientWatchHandlesErrorEvents(t *testing.T) {
	tests := []struct {
		name      string
		status    string
		wantError error
		wantText  string
	}{
		{
			name:      "expired code",
			status:    `{"code":410,"reason":"Expired","message":"too old resource version"}`,
			wantError: errResourceExpired,
		},
		{
			name:      "expired reason",
			status:    `{"reason":"Expired","message":"resource version compacted"}`,
			wantError: errResourceExpired,
		},
		{
			name:      "forbidden",
			status:    `{"code":403,"reason":"Forbidden"}`,
			wantError: errForbidden,
		},
		{
			name:      "not found",
			status:    `{"code":404,"reason":"NotFound"}`,
			wantError: errNotFound,
		},
		{
			name:     "server failure",
			status:   `{"code":500,"reason":"InternalError","message":"temporary failure"}`,
			wantText: `code=500`,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				w.Header().Set("Content-Type", "application/json")
				if _, err := fmt.Fprintf(w, `{"type":"ERROR","object":%s}`+"\n", tt.status); err != nil {
					t.Errorf("write watch response: %v", err)
				}
			}))
			defer srv.Close()

			client := &apiClient{baseURL: srv.URL, token: "test-token", http: srv.Client()}
			callbackCalled := false
			latest, err := client.watch(context.Background(), "/api/v1/events", "100", func(k8sWatchEvent) error {
				callbackCalled = true
				return nil
			})
			if tt.wantError != nil && !errors.Is(err, tt.wantError) {
				t.Fatalf("watch() error = %v, want %v", err, tt.wantError)
			}
			if tt.wantText != "" && (err == nil || !strings.Contains(err.Error(), tt.wantText)) {
				t.Fatalf("watch() error = %v, want text %q", err, tt.wantText)
			}
			if latest != "100" {
				t.Fatalf("latest resourceVersion = %q, want 100", latest)
			}
			if callbackCalled {
				t.Fatal("watch callback called for ERROR event")
			}
		})
	}
}

func TestInventoryPusherWatchSpecsNamespaceScopeExcludeNodes(t *testing.T) {
	p := &InventoryPusher{
		info: tunnel.KubernetesInfo{ClusterID: 7, Role: "controller", Namespace: "apps"},
		api:  &apiClient{namespace: "apps"},
	}
	specs := p.watchSpecs(&inventorySnapshot{
		scope:     inventoryScopeNamespace,
		namespace: "apps",
		resourceVersions: map[string]string{
			"pods:apps":             "10",
			"events:apps":           "11",
			"apps/deployments:apps": "12",
		},
	})
	if len(specs) == 0 {
		t.Fatalf("expected namespace watch specs")
	}
	for _, spec := range specs {
		if spec.name == "nodes" {
			t.Fatalf("namespace watch specs must not include nodes: %+v", specs)
		}
		if spec.apiPath == "/api/v1/pods" || spec.apiPath == "/api/v1/events" {
			t.Fatalf("namespace watch spec must use namespaced API path: %+v", spec)
		}
	}
}

func TestInventoryCacheApplyWatchDeleteBuildsDelta(t *testing.T) {
	cache := newInventoryCache(&inventorySnapshot{
		scope: inventoryScopeCluster,
		pods: []tunnel.KubernetesPodSnapshot{{
			Namespace: "default",
			Name:      "api-1",
			UID:       "pod-uid-1",
			Phase:     "Running",
		}},
	})
	spec := inventoryWatchSpec{
		name:     "pods",
		resource: watchResourcePods,
	}
	trigger, err := cache.applyWatchEvent(spec, k8sWatchEvent{
		Type:   "DELETED",
		Object: []byte(`{"metadata":{"namespace":"default","name":"api-1","uid":"pod-uid-1","resourceVersion":"123"}}`),
	}, time.Unix(100, 0))
	if err != nil {
		t.Fatalf("applyWatchEvent() error = %v", err)
	}
	if trigger.syncType != inventorySyncDelta || trigger.resourceVersion != "123" {
		t.Fatalf("trigger metadata = sync:%q rv:%q", trigger.syncType, trigger.resourceVersion)
	}
	if len(trigger.deletedPods) != 1 || trigger.deletedPods[0].UID != "pod-uid-1" {
		t.Fatalf("deleted pods = %+v, want pod-uid-1", trigger.deletedPods)
	}
	if len(cache.pods) != 0 {
		t.Fatalf("cache pods len = %d, want 0", len(cache.pods))
	}
}

func TestInventoryPusherPushDeltaUsesDeltaPayload(t *testing.T) {
	fc := &fakeTunnelClient{handlers: map[string]tunnel.Handler{}}
	p := &InventoryPusher{
		client: fc,
		info:   tunnel.KubernetesInfo{ClusterID: 7, Role: "controller"},
		log:    slog.Default(),
	}
	trigger := newInventoryWatchTrigger("pods:ADDED", time.Unix(100, 0))
	trigger.resourceVersion = "124"
	trigger.resourceVersions = map[string]string{"pods": "124"}
	trigger.pods = []tunnel.KubernetesPodSnapshot{{
		Namespace: "default",
		Name:      "api-2",
		UID:       "pod-uid-2",
		Phase:     "Pending",
	}}
	if err := p.pushDelta(context.Background(), 55, &inventorySnapshot{scope: inventoryScopeCluster}, trigger); err != nil {
		t.Fatalf("pushDelta() error = %v", err)
	}
	req, ok := fc.lastRequest.(tunnel.KubernetesInventoryRequest)
	if !ok {
		t.Fatalf("request type = %T, want KubernetesInventoryRequest", fc.lastRequest)
	}
	if req.SyncType != inventorySyncDelta {
		t.Fatalf("SyncType = %q, want delta", req.SyncType)
	}
	if req.ResourceVersion != "124" || req.ResourceVersions["pods"] != "124" {
		t.Fatalf("resource versions = rv:%q all:%v", req.ResourceVersion, req.ResourceVersions)
	}
	if len(req.Pods) != 1 || req.Pods[0].UID != "pod-uid-2" {
		t.Fatalf("pods = %+v, want pod-uid-2", req.Pods)
	}
	if req.WatchEventObservedAt != 100 || req.WatchTriggerReason != "pods:ADDED" {
		t.Fatalf("watch metadata = observed:%d reason:%q", req.WatchEventObservedAt, req.WatchTriggerReason)
	}
}

func TestInventoryWatchAccumulatorKeepsFinalDelete(t *testing.T) {
	accumulator := newInventoryWatchAccumulator()
	upsert := newInventoryWatchTrigger("pods:MODIFIED", time.Unix(100, 0))
	upsert.pods = []tunnel.KubernetesPodSnapshot{{Namespace: "default", Name: "api", UID: "pod-uid", Phase: "Running"}}
	deleted := newInventoryWatchTrigger("pods:DELETED", time.Unix(101, 0))
	deleted.deletedPods = []tunnel.KubernetesPodRef{{Namespace: "default", Name: "api", UID: "pod-uid"}}
	accumulator.add(upsert)
	accumulator.add(deleted)

	select {
	case <-accumulator.notifications():
	case <-time.After(time.Second):
		t.Fatal("watch accumulator did not signal pending changes")
	}
	got, ok := waitForWatchDebounce(context.Background(), accumulator, 0)
	if !ok {
		t.Fatal("waitForWatchDebounce() canceled")
	}
	if len(got.pods) != 0 || len(got.deletedPods) != 1 || got.deletedPods[0].UID != "pod-uid" {
		t.Fatalf("final pod operations = upserts:%+v deletes:%+v, want one delete", got.pods, got.deletedPods)
	}
}

func TestInventoryWatchAccumulatorDoesNotLoseBurst(t *testing.T) {
	accumulator := newInventoryWatchAccumulator()
	const total = 1000
	for i := 0; i < total; i++ {
		trigger := newInventoryWatchTrigger("pods:ADDED", time.Unix(int64(i+1), 0))
		trigger.pods = []tunnel.KubernetesPodSnapshot{{
			Namespace: "default",
			Name:      fmt.Sprintf("pod-%d", i),
			UID:       fmt.Sprintf("uid-%d", i),
		}}
		accumulator.add(trigger)
	}
	select {
	case <-accumulator.notifications():
	case <-time.After(time.Second):
		t.Fatal("watch accumulator did not signal burst")
	}
	got, ok := waitForWatchDebounce(context.Background(), accumulator, 0)
	if !ok {
		t.Fatal("waitForWatchDebounce() canceled")
	}
	if len(got.pods) != total || got.count != total {
		t.Fatalf("burst = pods:%d count:%d, want %d", len(got.pods), got.count, total)
	}
}

func TestInventoryPusherStartsWatchAfterLaterFullSyncSucceeds(t *testing.T) {
	watchStarted := make(chan struct{})
	var watchOnce sync.Once
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Query().Get("watch") == "1" {
			watchOnce.Do(func() { close(watchStarted) })
			return
		}
		writeTestResponse(t, w, `{"metadata":{"resourceVersion":"100"},"items":[]}`)
	}))
	defer srv.Close()

	client := &flakyInventoryTunnelClient{failures: 1}
	p := &InventoryPusher{
		client:   client,
		info:     tunnel.KubernetesInfo{ClusterID: 7, Role: "controller"},
		edgeID:   func() uint64 { return 55 },
		interval: 10 * time.Millisecond,
		log:      slog.Default(),
		api:      &apiClient{baseURL: srv.URL, token: "token", http: srv.Client()},
		watch:    true,
	}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- p.Run(ctx) }()
	select {
	case <-watchStarted:
	case <-time.After(2 * time.Second):
		cancel()
		t.Fatal("watch did not start after the later full sync succeeded")
	}
	cancel()
	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("Run() error = %v", err)
		}
	case <-time.After(time.Second):
		t.Fatal("Run() did not stop after cancellation")
	}
	if client.callCount() < 2 {
		t.Fatalf("inventory calls = %d, want initial failure and later success", client.callCount())
	}
}

type flakyInventoryTunnelClient struct {
	fakeTunnelClient
	mu       sync.Mutex
	failures int
	calls    int
}

func (f *flakyInventoryTunnelClient) Call(ctx context.Context, method string, req any, resp any) error {
	f.mu.Lock()
	f.calls++
	if f.failures > 0 {
		f.failures--
		f.mu.Unlock()
		return fmt.Errorf("temporary inventory push failure")
	}
	f.mu.Unlock()
	return f.fakeTunnelClient.Call(ctx, method, req, resp)
}

func (f *flakyInventoryTunnelClient) callCount() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.calls
}
