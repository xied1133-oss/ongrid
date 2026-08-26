package operatorrun

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"io"
	"strings"
	"testing"
	"time"

	"github.com/ongridio/ongrid/internal/pkg/errs"
	"github.com/ongridio/ongrid/internal/pkg/tunnel"
)

type fakeEdgeCaller struct {
	method string
	body   []byte
	resp   tunnel.OperatorExecResponse

	streamMeta []byte
	streamBody string
	streamErr  error

	startAccepted bool
	started       chan string
}

func (f *fakeEdgeCaller) Call(_ context.Context, _ uint64, method string, body []byte) ([]byte, error) {
	f.method = method
	f.body = append([]byte(nil), body...)
	if method == tunnel.MethodOperatorExecStart {
		if !f.startAccepted {
			return nil, errors.New("no such rpc")
		}
		var in tunnel.OperatorExecStartRequest
		if err := json.Unmarshal(body, &in); err == nil && f.started != nil {
			f.started <- in.RunID
		}
		return json.Marshal(tunnel.OperatorExecStartResponse{Accepted: true})
	}
	return json.Marshal(f.resp)
}

func (f *fakeEdgeCaller) OpenStreamWithMeta(_ context.Context, _ uint64, meta []byte) (io.ReadWriteCloser, error) {
	f.streamMeta = append([]byte(nil), meta...)
	if f.streamErr != nil {
		return nil, f.streamErr
	}
	return nopReadWriteCloser{Reader: strings.NewReader(f.streamBody)}, nil
}

func TestCreateBuildsControlledPingCommand(t *testing.T) {
	caller := &fakeEdgeCaller{resp: tunnel.OperatorExecResponse{Allowed: true, Stdout: "ok\n", ExitCode: 0, DurationMs: 12}}
	svc := New(caller, nil)
	run, err := svc.Create(context.Background(), Caller{UserID: 1, Role: "admin"}, CreateInput{
		EdgeIDs:   []uint64{1},
		Command:   "ping",
		Args:      json.RawMessage(`{"host":"127.0.0.1","count":2}`),
		TimeoutMs: 3000,
	})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	waitRunDone(t, svc, run.ID)
	if caller.method != tunnel.MethodOperatorExec {
		t.Fatalf("method = %q", caller.method)
	}
	var sent tunnel.OperatorExecRequest
	if err := json.Unmarshal(caller.body, &sent); err != nil {
		t.Fatalf("decode req: %v", err)
	}
	if sent.Command != "ping" || sent.TimeoutMs != 3000 || sent.Args["host"] != "127.0.0.1" || sent.Args["count"].(float64) != 2 {
		t.Fatalf("req = %+v", sent)
	}
	got, err := svc.Get(context.Background(), Caller{UserID: 1, Role: "admin"}, run.ID)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if got.Status != StatusSuccess || len(got.Results) != 1 || got.Results[0].Stdout != "ok\n" {
		t.Fatalf("run = %+v", got)
	}
}

func TestBuildRequestTCPAcceptsLegacyTarget(t *testing.T) {
	req, display, title, _, err := buildRequest(CreateInput{
		Command:   "tcp",
		Args:      json.RawMessage(`{"target":"101.34.63.91:443"}`),
		TimeoutMs: 3000,
	})
	if err != nil {
		t.Fatalf("buildRequest: %v", err)
	}
	if req.Command != "tcp" || req.Args["host"] != "101.34.63.91" || req.Args["port"].(int) != 443 {
		t.Fatalf("req = %+v", req)
	}
	if display != "nc -vz -w 3 101.34.63.91 443" || title != "TCP 101.34.63.91:443" {
		t.Fatalf("display=%q title=%q", display, title)
	}
}

func TestBuildRequestHTTPAdvancedOptions(t *testing.T) {
	req, display, _, _, err := buildRequest(CreateInput{
		Command:   "http",
		Args:      json.RawMessage(`{"url":"https://101.34.63.91/healthz","method":"GET","skip_tls":true,"namespace":"blue"}`),
		TimeoutMs: 5000,
	})
	if err != nil {
		t.Fatalf("buildRequest: %v", err)
	}
	if req.Args["namespace"] != "blue" || req.Args["skip_tls"] != true {
		t.Fatalf("args = %+v", req.Args)
	}
	want := "ip netns exec blue curl -I -X GET --max-time 5 -k https://101.34.63.91/healthz"
	if display != want {
		t.Fatalf("display=%q want %q", display, want)
	}
}

func TestListNetNSParsesEdgeResponse(t *testing.T) {
	caller := &fakeEdgeCaller{resp: tunnel.OperatorExecResponse{Allowed: true, Stdout: "blue (id: 1)\nred\nbad$name\nblue (id: 1)\n", ExitCode: 0}}
	svc := New(caller, nil)
	got, err := svc.ListNetNS(context.Background(), Caller{UserID: 1, Role: "admin"}, 7)
	if err != nil {
		t.Fatalf("ListNetNS: %v", err)
	}
	if caller.method != tunnel.MethodOperatorExec {
		t.Fatalf("method = %q", caller.method)
	}
	var sent tunnel.OperatorExecRequest
	if err := json.Unmarshal(caller.body, &sent); err != nil {
		t.Fatalf("decode req: %v", err)
	}
	if sent.Command != "list_netns" || sent.TimeoutMs != 3000 {
		t.Fatalf("req = %+v", sent)
	}
	if got.EdgeID != 7 || len(got.Namespaces) != 2 || got.Namespaces[0] != "blue" || got.Namespaces[1] != "red" {
		t.Fatalf("got = %+v", got)
	}
}

func TestCreateStreamsOperatorOutputBeforeDone(t *testing.T) {
	caller := &fakeEdgeCaller{streamBody: streamEvent(t, tunnel.OperatorStreamEvent{Type: EventStdout, Stream: "stdout", Message: "line 1\n"}) +
		streamEvent(t, tunnel.OperatorStreamEvent{Type: EventStdout, Stream: "stdout", Message: "line 2\n"}) +
		streamEvent(t, tunnel.OperatorStreamEvent{Type: EventDone, Status: StatusSuccess, Allowed: true, ExitCode: 0, DurationMs: 15})}
	svc := New(caller, nil)
	run, err := svc.Create(context.Background(), Caller{UserID: 1, Role: "admin"}, CreateInput{
		EdgeIDs: []uint64{1},
		Command: "ping",
		Args:    json.RawMessage(`{"host":"127.0.0.1","count":2}`),
	})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	waitRunDone(t, svc, run.ID)
	if caller.method != tunnel.MethodOperatorExecStart {
		t.Fatalf("start probe method = %q", caller.method)
	}
	var meta tunnel.OperatorStreamMeta
	if err := json.Unmarshal(caller.streamMeta, &meta); err != nil {
		t.Fatalf("decode meta: %v", err)
	}
	if meta.Kind != tunnel.StreamKindOperatorExec || meta.Req.Command != "ping" {
		t.Fatalf("meta = %+v", meta)
	}
	got, err := svc.Get(context.Background(), Caller{UserID: 1, Role: "admin"}, run.ID)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if got.Status != StatusSuccess || got.Results[0].Stdout != "line 1\nline 2\n" {
		t.Fatalf("run = %+v", got)
	}
	stdoutEvents := 0
	for _, ev := range got.Events {
		if ev.Type == EventStdout {
			stdoutEvents++
		}
	}
	if stdoutEvents != 2 {
		t.Fatalf("stdout events = %d, want 2", stdoutEvents)
	}
}

func TestCreateAcceptsPushedOperatorEvents(t *testing.T) {
	started := make(chan string, 1)
	caller := &fakeEdgeCaller{startAccepted: true, started: started}
	svc := New(caller, nil)
	run, err := svc.Create(context.Background(), Caller{UserID: 1, Role: "admin"}, CreateInput{
		EdgeIDs: []uint64{1},
		Command: "ping",
		Args:    json.RawMessage(`{"host":"127.0.0.1","count":2}`),
	})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	var runID string
	select {
	case runID = <-started:
	case <-time.After(time.Second):
		t.Fatal("start request not observed")
	}
	if runID != run.ID {
		t.Fatalf("runID = %q want %q", runID, run.ID)
	}
	if _, err := svc.HandlePushEvent(context.Background(), 1, mustJSON(t, tunnel.OperatorPushEventRequest{RunID: run.ID, Event: tunnel.OperatorStreamEvent{Type: EventStdout, Stream: "stdout", Message: "line 1\n"}})); err != nil {
		t.Fatalf("push stdout: %v", err)
	}
	if _, err := svc.HandlePushEvent(context.Background(), 1, mustJSON(t, tunnel.OperatorPushEventRequest{RunID: run.ID, Event: tunnel.OperatorStreamEvent{Type: EventDone, Status: StatusSuccess, Allowed: true, ExitCode: 0}})); err != nil {
		t.Fatalf("push done: %v", err)
	}
	waitRunDone(t, svc, run.ID)
	got, err := svc.Get(context.Background(), Caller{UserID: 1, Role: "admin"}, run.ID)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if got.Status != StatusSuccess || got.Results[0].Stdout != "line 1\n" {
		t.Fatalf("run = %+v", got)
	}
}

func TestCreateRejectsUnsafeHost(t *testing.T) {
	svc := New(&fakeEdgeCaller{}, nil)
	if _, err := svc.Create(context.Background(), Caller{UserID: 1, Role: "admin"}, CreateInput{
		EdgeIDs: []uint64{1},
		Command: "ping",
		Args:    json.RawMessage(`{"host":"127.0.0.1;rm -rf /"}`),
	}); err == nil {
		t.Fatal("Create error = nil")
	}
}

func TestOperatorRunRejectsViewerAndOtherUsers(t *testing.T) {
	svc := New(&fakeEdgeCaller{resp: tunnel.OperatorExecResponse{Allowed: true, ExitCode: 0}}, nil)
	input := CreateInput{EdgeIDs: []uint64{1}, Command: "ping", Args: json.RawMessage(`{"host":"127.0.0.1"}`)}
	if _, err := svc.Create(context.Background(), Caller{UserID: 2, Role: "viewer"}, input); !errors.Is(err, errs.ErrForbidden) {
		t.Fatalf("viewer Create error=%v want forbidden", err)
	}
	run, err := svc.Create(context.Background(), Caller{UserID: 1, Role: "user"}, input)
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	if _, err := svc.Get(context.Background(), Caller{UserID: 2, Role: "user"}, run.ID); !errors.Is(err, errs.ErrForbidden) {
		t.Fatalf("other user Get error=%v want forbidden", err)
	}
	if _, err := svc.Cancel(context.Background(), Caller{UserID: 2, Role: "user"}, run.ID); !errors.Is(err, errs.ErrForbidden) {
		t.Fatalf("other user Cancel error=%v want forbidden", err)
	}
}

func TestHandlePushEventRejectsUnrelatedEdge(t *testing.T) {
	svc := New(&fakeEdgeCaller{resp: tunnel.OperatorExecResponse{Allowed: true, ExitCode: 0}}, nil)
	run, err := svc.Create(context.Background(), Caller{UserID: 1, Role: "admin"}, CreateInput{
		EdgeIDs: []uint64{1}, Command: "ping", Args: json.RawMessage(`{"host":"127.0.0.1"}`),
	})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	body := mustJSON(t, tunnel.OperatorPushEventRequest{RunID: run.ID, Event: tunnel.OperatorStreamEvent{Type: EventDone, Status: StatusSuccess, Allowed: true}})
	respBody, err := svc.HandlePushEvent(context.Background(), 2, body)
	if err != nil {
		t.Fatalf("HandlePushEvent: %v", err)
	}
	var resp tunnel.OperatorPushEventResponse
	if err := json.Unmarshal(respBody, &resp); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if resp.OK {
		t.Fatal("unrelated edge push was accepted")
	}
}

func waitRunDone(t *testing.T, svc *Service, id string) {
	t.Helper()
	for i := 0; i < 100; i++ {
		run, err := svc.Get(context.Background(), Caller{UserID: 1, Role: "admin"}, id)
		if err != nil {
			t.Fatalf("Get: %v", err)
		}
		if run.Status != StatusRunning {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatal("run did not finish")
}

func streamEvent(t *testing.T, ev tunnel.OperatorStreamEvent) string {
	t.Helper()
	payload, err := json.Marshal(ev)
	if err != nil {
		t.Fatalf("marshal event: %v", err)
	}
	return base64.StdEncoding.EncodeToString(payload) + "\n"
}

func mustJSON(t *testing.T, v any) []byte {
	t.Helper()
	b, err := json.Marshal(v)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	return b
}

type nopReadWriteCloser struct {
	*strings.Reader
}

func (n nopReadWriteCloser) Write(p []byte) (int, error) { return len(p), nil }
func (n nopReadWriteCloser) Close() error                { return nil }
