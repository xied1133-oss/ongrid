package mcpclient

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// mockMCP answers JSON-RPC over HTTP: initialize + tools/list (JSON body) and
// tools/call (SSE body) — exercising both response framings.
func mockMCP(t *testing.T) *httptest.Server {
	t.Helper()
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		var req struct {
			ID     int    `json:"id"`
			Method string `json:"method"`
			Params struct {
				Name      string         `json:"name"`
				Arguments map[string]any `json:"arguments"`
			} `json:"params"`
		}
		_ = json.Unmarshal(body, &req)
		w.Header().Set("Mcp-Session-Id", "sess-1")
		switch req.Method {
		case "notifications/initialized":
			w.WriteHeader(http.StatusAccepted)
		case "initialize":
			writeJSONRPC(w, req.ID, map[string]any{"protocolVersion": ProtocolVersion})
		case "tools/list":
			writeJSONRPC(w, req.ID, map[string]any{
				"tools": []map[string]any{{
					"name":        "echo",
					"description": "echo back",
					"inputSchema": map[string]any{"type": "object", "properties": map[string]any{"msg": map[string]any{"type": "string"}}},
				}},
			})
		case "tools/call":
			// stream the response as SSE
			w.Header().Set("Content-Type", "text/event-stream")
			w.WriteHeader(http.StatusOK)
			resp := map[string]any{
				"jsonrpc": "2.0", "id": req.ID,
				"result": map[string]any{"content": []map[string]any{{"type": "text", "text": "echoed:" + req.Params.Arguments["msg"].(string)}}},
			}
			b, _ := json.Marshal(resp)
			_, _ = w.Write([]byte("event: message\ndata: " + string(b) + "\n\n"))
		default:
			http.Error(w, "unknown method", http.StatusBadRequest)
		}
	}))
}

func writeJSONRPC(w http.ResponseWriter, id int, result any) {
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]any{"jsonrpc": "2.0", "id": id, "result": result})
}

func TestClient_InitializeListCall(t *testing.T) {
	srv := mockMCP(t)
	defer srv.Close()
	c := NewHTTP(srv.URL, map[string]string{"Authorization": "Bearer x"}, 0)
	ctx := context.Background()

	if err := c.Initialize(ctx); err != nil {
		t.Fatalf("initialize: %v", err)
	}
	if c.sessionID != "sess-1" {
		t.Fatalf("session id not captured: %q", c.sessionID)
	}

	tools, err := c.ListTools(ctx)
	if err != nil || len(tools) != 1 || tools[0].Name != "echo" {
		t.Fatalf("tools/list = %+v, %v", tools, err)
	}
	if !strings.Contains(string(tools[0].InputSchema), "msg") {
		t.Fatalf("input schema lost: %s", tools[0].InputSchema)
	}

	res, err := c.CallTool(ctx, "echo", map[string]any{"msg": "hi"})
	if err != nil {
		t.Fatalf("tools/call: %v", err)
	}
	if got := res.TextContent(); got != "echoed:hi" {
		t.Fatalf("call result = %q, want echoed:hi", got)
	}
}

func TestClient_RPCError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"jsonrpc":"2.0","id":1,"error":{"code":-32601,"message":"method not found"}}`))
	}))
	defer srv.Close()
	c := NewHTTP(srv.URL, nil, 0)
	if _, err := c.ListTools(context.Background()); err == nil || !strings.Contains(err.Error(), "method not found") {
		t.Fatalf("want rpc error surfaced, got %v", err)
	}
}
