package k8s

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/ongridio/ongrid/internal/pkg/tunnel"
)

type workloadResourceSchemaCase struct {
	name              string
	group             string
	resource          string
	kind              string
	object            string
	wantDesired       int
	wantReady         int
	wantActive        int
	wantFailed        int
	wantOwnerKind     string
	wantOwnerName     string
	wantOwnerUID      string
	wantRevision      int64
	wantCreatedAt     string
	wantConditionType string
}

func TestAPIClientListWorkloadsDecodesResourceSchemas(t *testing.T) {
	for _, tt := range workloadResourceSchemaCases() {
		t.Run(tt.name, func(t *testing.T) {
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				wantPath := "/apis/" + tt.group + "/v1/" + tt.resource
				if r.URL.Path != wantPath {
					t.Fatalf("request path = %q, want %q", r.URL.Path, wantPath)
				}
				writeTestResponse(t, w, `{"metadata":{"resourceVersion":"42"},"items":[`+tt.object+`]}`)
			}))
			t.Cleanup(srv.Close)

			api := &apiClient{baseURL: srv.URL, token: "token", http: srv.Client()}
			items, rv, err := api.listWorkloads(context.Background(), tt.group, tt.resource, tt.kind, "")
			if err != nil {
				t.Fatalf("listWorkloads() error = %v", err)
			}
			if rv != "42" {
				t.Fatalf("resourceVersion = %q, want 42", rv)
			}
			if len(items) != 1 {
				t.Fatalf("workloads len = %d, want 1", len(items))
			}
			assertWorkloadResourceSnapshot(t, items[0], tt)
		})
	}
}

func TestInventoryCacheApplyWatchUpsertDecodesWorkloadResourceSchemas(t *testing.T) {
	for _, tt := range workloadResourceSchemaCases() {
		t.Run(tt.name, func(t *testing.T) {
			cache := newInventoryCache(nil)
			trigger, err := cache.applyWatchEvent(inventoryWatchSpec{
				name:         tt.resource,
				resource:     watchResourceWorkloads,
				workloadKind: tt.kind,
			}, k8sWatchEvent{
				Type:   "ADDED",
				Object: []byte(tt.object),
			}, time.Unix(100, 0))
			if err != nil {
				t.Fatalf("applyWatchEvent() error = %v", err)
			}
			if len(trigger.workloads) != 1 {
				t.Fatalf("trigger workloads len = %d, want 1", len(trigger.workloads))
			}
			assertWorkloadResourceSnapshot(t, trigger.workloads[0], tt)
		})
	}
}

func TestAPIClientListCoreResourcesDecodesResourceSchemas(t *testing.T) {
	t.Run("node", func(t *testing.T) {
		srv := newInventoryResourceServer(t, `{
			"metadata":{"resourceVersion":"51"},
			"items":[{
				"metadata":{"name":"node-a","uid":"node-uid","labels":{"zone":"east"}},
				"spec":{"providerID":"provider://node-a","taints":[{"key":"dedicated","value":"edge","effect":"NoSchedule","timeAdded":"2026-07-29T00:00:00Z"}]},
				"status":{
					"capacity":{"cpu":"4","memory":"8Gi"},
					"allocatable":{"cpu":"3800m","memory":"7Gi"},
					"conditions":[{"type":"Ready","status":"True","reason":"KubeletReady","message":"ready"}],
					"nodeInfo":{"kubeletVersion":"v1.34.9"}
				}
			}]
		}`)

		api := &apiClient{baseURL: srv.URL, token: "token", http: srv.Client()}
		items, rv, err := api.listNodes(context.Background())
		if err != nil {
			t.Fatalf("listNodes() error = %v", err)
		}
		if rv != "51" || len(items) != 1 {
			t.Fatalf("resourceVersion = %q nodes = %d, want 51 and 1", rv, len(items))
		}
		got := items[0]
		if got.Name != "node-a" || got.ProviderID != "provider://node-a" || got.KubeletVersion != "v1.34.9" {
			t.Fatalf("node snapshot = %+v", got)
		}
		if got.Capacity["memory"] != "8Gi" || got.Allocatable["cpu"] != "3800m" || len(got.Taints) != 1 {
			t.Fatalf("node resources = capacity:%v allocatable:%v taints:%v", got.Capacity, got.Allocatable, got.Taints)
		}
		if len(got.Conditions) != 1 || got.Conditions[0]["type"] != "Ready" {
			t.Fatalf("node conditions = %v", got.Conditions)
		}
	})

	t.Run("pod", func(t *testing.T) {
		srv := newInventoryResourceServer(t, `{
			"metadata":{"resourceVersion":"52"},
			"items":[{
				"metadata":{
					"namespace":"apps","name":"api-0","uid":"pod-uid",
					"ownerReferences":[{"kind":"ReplicaSet","name":"api-rs","uid":"rs-uid","controller":true}]
				},
				"spec":{
					"nodeName":"node-a",
					"containers":[{"name":"api","ports":[{"name":"http","containerPort":8080,"protocol":"TCP"}]}],
					"volumes":[{"name":"cache","emptyDir":{"medium":"Memory","sizeLimit":"64Mi"}}]
				},
				"status":{
					"phase":"Pending",
					"containerStatuses":[
						{"name":"api","restartCount":2,"state":{"waiting":{"reason":"CrashLoopBackOff"}}},
						{"name":"sidecar","restartCount":1,"state":{"running":{"startedAt":"2026-07-29T00:00:00Z"}}}
					]
				}
			}]
		}`)

		api := &apiClient{baseURL: srv.URL, token: "token", http: srv.Client()}
		items, rv, err := api.listPods(context.Background(), "apps")
		if err != nil {
			t.Fatalf("listPods() error = %v", err)
		}
		if rv != "52" || len(items) != 1 {
			t.Fatalf("resourceVersion = %q pods = %d, want 52 and 1", rv, len(items))
		}
		got := items[0]
		if got.Namespace != "apps" || got.Name != "api-0" || got.NodeName != "node-a" || got.Phase != "Pending" {
			t.Fatalf("pod snapshot = %+v", got)
		}
		if got.OwnerKind != "ReplicaSet" || got.OwnerName != "api-rs" || got.RestartCount != 3 || got.Reason != "CrashLoopBackOff" {
			t.Fatalf("pod ownership/status = %+v", got)
		}
	})

	t.Run("event", func(t *testing.T) {
		srv := newInventoryResourceServer(t, `{
			"metadata":{"resourceVersion":"53"},
			"items":[{
				"metadata":{"namespace":"apps","name":"api.123","uid":"event-uid"},
				"involvedObject":{"kind":"Pod","namespace":"apps","name":"api-0","uid":"pod-uid"},
				"type":"Warning","reason":"Failed","message":"container failed",
				"source":{"component":"kubelet","host":"node-a"},
				"reportingComponent":"kubelet","reportingInstance":"node-a","action":"BackOff","count":3,
				"firstTimestamp":"2026-07-29T00:00:00Z","lastTimestamp":"2026-07-29T00:01:00Z","eventTime":"2026-07-29T00:00:00.123456Z",
				"series":{"count":7,"lastObservedTime":"2026-07-29T00:03:00.123456Z"}
			}]
		}`)

		api := &apiClient{baseURL: srv.URL, token: "token", http: srv.Client()}
		items, rv, err := api.listEvents(context.Background(), "apps")
		if err != nil {
			t.Fatalf("listEvents() error = %v", err)
		}
		if rv != "53" || len(items) != 1 {
			t.Fatalf("resourceVersion = %q events = %d, want 53 and 1", rv, len(items))
		}
		got := items[0]
		if got.Name != "api.123" || got.InvolvedKind != "Pod" || got.InvolvedName != "api-0" || got.Count != 7 {
			t.Fatalf("event snapshot = %+v", got)
		}
		if got.SourceComponent != "kubelet" || got.ReportingController != "kubelet" || got.EventTime != "2026-07-29T00:00:00.123456Z" || got.LastTimestamp != "2026-07-29T00:03:00.123456Z" {
			t.Fatalf("event source/time = %+v", got)
		}
	})
}

func TestInventoryCacheApplyWatchUpsertDecodesCoreResourceSchemas(t *testing.T) {
	t.Run("node", func(t *testing.T) {
		cache := newInventoryCache(nil)
		trigger, err := cache.applyWatchEvent(inventoryWatchSpec{
			name:     "nodes",
			resource: watchResourceNodes,
		}, k8sWatchEvent{
			Type: "ADDED",
			Object: []byte(`{
				"metadata":{"name":"node-a","uid":"node-uid","resourceVersion":"61"},
				"spec":{"providerID":"provider://node-a","taints":[{"key":"dedicated","value":"edge","effect":"NoSchedule"}]},
				"status":{
					"capacity":{"cpu":"4"},"allocatable":{"cpu":"3800m"},
					"conditions":[{"type":"Ready","status":"True","reason":"KubeletReady","message":"ready"}],
					"nodeInfo":{"kubeletVersion":"v1.34.9"}
				}
			}`),
		}, time.Unix(100, 0))
		if err != nil {
			t.Fatalf("applyWatchEvent() error = %v", err)
		}
		if len(trigger.nodes) != 1 {
			t.Fatalf("trigger nodes len = %d, want 1", len(trigger.nodes))
		}
		got := trigger.nodes[0]
		if got.Name != "node-a" || got.ProviderID != "provider://node-a" || got.KubeletVersion != "v1.34.9" || got.Capacity["cpu"] != "4" {
			t.Fatalf("node snapshot = %+v", got)
		}
	})

	t.Run("pod", func(t *testing.T) {
		cache := newInventoryCache(nil)
		trigger, err := cache.applyWatchEvent(inventoryWatchSpec{
			name:     "pods",
			resource: watchResourcePods,
		}, k8sWatchEvent{
			Type: "ADDED",
			Object: []byte(`{
				"metadata":{"namespace":"apps","name":"api-0","uid":"pod-uid","resourceVersion":"62","ownerReferences":[{"kind":"ReplicaSet","name":"api-rs","controller":true}]},
				"spec":{"nodeName":"node-a","containers":[{"name":"api","ports":[{"containerPort":8080,"protocol":"TCP"}]}],"volumes":[{"emptyDir":{"medium":"Memory"}}]},
				"status":{"phase":"Pending","containerStatuses":[{"name":"api","restartCount":2,"state":{"waiting":{"reason":"CrashLoopBackOff"}}}]}
			}`),
		}, time.Unix(100, 0))
		if err != nil {
			t.Fatalf("applyWatchEvent() error = %v", err)
		}
		if len(trigger.pods) != 1 {
			t.Fatalf("trigger pods len = %d, want 1", len(trigger.pods))
		}
		got := trigger.pods[0]
		if got.Name != "api-0" || got.OwnerName != "api-rs" || got.RestartCount != 2 || got.Reason != "CrashLoopBackOff" {
			t.Fatalf("pod snapshot = %+v", got)
		}
	})

	t.Run("event", func(t *testing.T) {
		cache := newInventoryCache(nil)
		trigger, err := cache.applyWatchEvent(inventoryWatchSpec{
			name:     "events",
			resource: watchResourceEvents,
		}, k8sWatchEvent{
			Type: "ADDED",
			Object: []byte(`{
				"metadata":{"namespace":"apps","name":"api.123","uid":"event-uid","resourceVersion":"63"},
				"involvedObject":{"kind":"Pod","namespace":"apps","name":"api-0","uid":"pod-uid"},
				"type":"Warning","reason":"Failed","message":"container failed","source":{"component":"kubelet","host":"node-a"},
				"reportingComponent":"kubelet","reportingInstance":"node-a","action":"BackOff","count":3,
				"firstTimestamp":"2026-07-29T00:00:00Z","lastTimestamp":"2026-07-29T00:01:00Z","eventTime":"2026-07-29T00:00:00.123456Z",
				"series":{"count":7,"lastObservedTime":"2026-07-29T00:03:00.123456Z"}
			}`),
		}, time.Unix(100, 0))
		if err != nil {
			t.Fatalf("applyWatchEvent() error = %v", err)
		}
		if len(trigger.events) != 1 {
			t.Fatalf("trigger events len = %d, want 1", len(trigger.events))
		}
		got := trigger.events[0]
		if got.Name != "api.123" || got.InvolvedName != "api-0" || got.Count != 7 || got.EventTime != "2026-07-29T00:00:00.123456Z" || got.LastTimestamp != "2026-07-29T00:03:00.123456Z" {
			t.Fatalf("event snapshot = %+v", got)
		}
	})
}

func newInventoryResourceServer(t *testing.T, response string) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		writeTestResponse(t, w, response)
	}))
	t.Cleanup(srv.Close)
	return srv
}

func assertWorkloadResourceSnapshot(t *testing.T, got tunnel.KubernetesWorkloadSnapshot, want workloadResourceSchemaCase) {
	t.Helper()
	if got.Kind != want.kind || got.Namespace != "apps" || got.Name != want.name || got.UID != want.name+"-uid" {
		t.Fatalf("workload identity = %+v", got)
	}
	if got.DesiredReplicas != want.wantDesired || got.ReadyReplicas != want.wantReady {
		t.Fatalf("workload replicas = desired:%d ready:%d, want desired:%d ready:%d", got.DesiredReplicas, got.ReadyReplicas, want.wantDesired, want.wantReady)
	}
	if got.ActiveReplicas != want.wantActive || got.FailedReplicas != want.wantFailed {
		t.Fatalf("workload execution = active:%d failed:%d, want active:%d failed:%d", got.ActiveReplicas, got.FailedReplicas, want.wantActive, want.wantFailed)
	}
	if got.OwnerKind != want.wantOwnerKind || got.OwnerName != want.wantOwnerName || got.OwnerUID != want.wantOwnerUID || got.Revision != want.wantRevision {
		t.Fatalf("workload rollout metadata = owner:%s/%s/%s revision:%d, want owner:%s/%s/%s revision:%d", got.OwnerKind, got.OwnerName, got.OwnerUID, got.Revision, want.wantOwnerKind, want.wantOwnerName, want.wantOwnerUID, want.wantRevision)
	}
	if want.wantCreatedAt == "" {
		if got.CreationTimestamp != nil {
			t.Fatalf("workload creation timestamp = %v, want nil", got.CreationTimestamp)
		}
	} else if got.CreationTimestamp == nil || got.CreationTimestamp.UTC().Format(time.RFC3339) != want.wantCreatedAt {
		t.Fatalf("workload creation timestamp = %v, want %s", got.CreationTimestamp, want.wantCreatedAt)
	}
	if got.Labels["app"] != want.name || got.Annotations["example.com/note"] != "safe" {
		t.Fatalf("workload metadata = labels:%v annotations:%v", got.Labels, got.Annotations)
	}
	if want.wantConditionType == "" {
		if len(got.Conditions) != 0 {
			t.Fatalf("workload conditions = %v, want none", got.Conditions)
		}
		return
	}
	if len(got.Conditions) != 1 || got.Conditions[0]["type"] != want.wantConditionType {
		t.Fatalf("workload conditions = %v, want type %q", got.Conditions, want.wantConditionType)
	}
}

func workloadResourceSchemaCases() []workloadResourceSchemaCase {
	return []workloadResourceSchemaCase{
		{
			name:              "deployment",
			group:             "apps",
			resource:          "deployments",
			kind:              "Deployment",
			wantDesired:       3,
			wantReady:         2,
			wantConditionType: "Available",
			object: `{
				"metadata":{"namespace":"apps","name":"deployment","uid":"deployment-uid","labels":{"app":"deployment"},"annotations":{"example.com/note":"safe"}},
				"spec":{"replicas":3},
				"status":{"replicas":3,"readyReplicas":2,"availableReplicas":2,"conditions":[{"type":"Available","status":"True","reason":"MinimumReplicasAvailable","message":"ready"}]}
			}`,
		},
		{
			name:              "statefulset",
			group:             "apps",
			resource:          "statefulsets",
			kind:              "StatefulSet",
			wantDesired:       2,
			wantReady:         1,
			wantConditionType: "Available",
			object: `{
				"metadata":{"namespace":"apps","name":"statefulset","uid":"statefulset-uid","labels":{"app":"statefulset"},"annotations":{"example.com/note":"safe"}},
				"spec":{"replicas":2},
				"status":{"replicas":2,"readyReplicas":1,"currentReplicas":1,"updatedReplicas":1,"conditions":[{"type":"Available","status":"False","reason":"ReplicasNotReady","message":"waiting"}]}
			}`,
		},
		{
			name:              "daemonset",
			group:             "apps",
			resource:          "daemonsets",
			kind:              "DaemonSet",
			wantDesired:       4,
			wantReady:         3,
			wantConditionType: "Progressing",
			object: `{
				"metadata":{"namespace":"apps","name":"daemonset","uid":"daemonset-uid","labels":{"app":"daemonset"},"annotations":{"example.com/note":"safe"}},
				"spec":{"selector":{"matchLabels":{"app":"daemonset"}}},
				"status":{"desiredNumberScheduled":4,"numberReady":3,"numberAvailable":3,"conditions":[{"type":"Progressing","status":"True","reason":"RollingUpdate","message":"updating"}]}
			}`,
		},
		{
			name:              "replicaset",
			group:             "apps",
			resource:          "replicasets",
			kind:              "ReplicaSet",
			wantDesired:       5,
			wantReady:         4,
			wantOwnerKind:     "Deployment",
			wantOwnerName:     "api",
			wantOwnerUID:      "deployment-uid",
			wantRevision:      12,
			wantCreatedAt:     "2026-07-28T08:09:10Z",
			wantConditionType: "ReplicaFailure",
			object: `{
				"metadata":{"namespace":"apps","name":"replicaset","uid":"replicaset-uid","creationTimestamp":"2026-07-28T08:09:10Z","labels":{"app":"replicaset"},"annotations":{"example.com/note":"safe","deployment.kubernetes.io/revision":"12"},"ownerReferences":[{"kind":"Deployment","name":"api","uid":"deployment-uid","controller":true}]},
				"spec":{"replicas":5},
				"status":{"replicas":5,"readyReplicas":4,"availableReplicas":4,"conditions":[{"type":"ReplicaFailure","status":"True","reason":"FailedCreate","message":"waiting"}]}
			}`,
		},
		{
			name:              "job",
			group:             "batch",
			resource:          "jobs",
			kind:              "Job",
			wantDesired:       2,
			wantReady:         1,
			wantActive:        1,
			wantFailed:        1,
			wantConditionType: "Complete",
			object: `{
				"metadata":{"namespace":"apps","name":"job","uid":"job-uid","labels":{"app":"job"},"annotations":{"example.com/note":"safe"}},
				"spec":{"completions":2,"parallelism":1},
				"status":{"active":1,"succeeded":1,"failed":1,"conditions":[{"type":"Complete","status":"False","reason":"Running","message":"one completion remains"}]}
			}`,
		},
		{
			name:        "cronjob",
			group:       "batch",
			resource:    "cronjobs",
			kind:        "CronJob",
			wantDesired: 0,
			wantReady:   0,
			wantActive:  1,
			object: `{
				"metadata":{"namespace":"apps","name":"cronjob","uid":"cronjob-uid","labels":{"app":"cronjob"},"annotations":{"example.com/note":"safe"}},
				"spec":{"schedule":"*/5 * * * *","jobTemplate":{"spec":{"template":{"spec":{"restartPolicy":"Never","containers":[{"name":"task","image":"busybox"}]}}}}},
				"status":{"active":[{"apiVersion":"batch/v1","kind":"Job","namespace":"apps","name":"cronjob-123","uid":"job-uid","resourceVersion":"99"}],"lastScheduleTime":"2026-07-29T00:00:00Z"}
			}`,
		},
	}
}
