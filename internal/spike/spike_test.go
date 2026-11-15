package spike

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

const (
	testKey = "ghk_test-key"
	testID  = "instance-123"
)

var fixedNow = time.Date(2026, 11, 15, 12, 0, 0, 0, time.UTC)

func newTestHandler(authlessMCP bool, key string) http.Handler {
	return New(Options{
		InstanceID:  testID,
		Key:         key,
		AuthlessMCP: authlessMCP,
		Version:     "test",
		Now:         func() time.Time { return fixedNow },
	})
}

func TestREST(t *testing.T) {
	tests := []struct {
		name       string
		key        string // configured key
		method     string
		path       string
		auth       string
		body       string
		wantStatus int
		wantBody   string // substring
	}{
		{"healthz", testKey, "GET", "/healthz", "", "", 200, `{"ok":true}`},
		{"healthz needs no key even when none is set", "", "GET", "/healthz", "", "", 200, `{"ok":true}`},
		{"list without key", testKey, "GET", "/api/v1/tools", "", "", 401, `{"error":"unauthorized"}`},
		{"list with wrong key", testKey, "GET", "/api/v1/tools", "Bearer nope", "", 401, `"unauthorized"`},
		{"list with basic auth", testKey, "GET", "/api/v1/tools", "Basic " + testKey, "", 401, `"unauthorized"`},
		{"list when no key is configured", "", "GET", "/api/v1/tools", "Bearer ", "", 401, `"unauthorized"`},
		{"list", testKey, "GET", "/api/v1/tools", "Bearer " + testKey, "", 200, `"name":"home_ping"`},
		{"list has instructions", testKey, "GET", "/api/v1/tools", "Bearer " + testKey, "", 200, `"instructions":"This is GPT-Home`},
		{"list marks read-only", testKey, "GET", "/api/v1/tools", "Bearer " + testKey, "", 200, `"readOnlyHint":true`},
		{"call without key", testKey, "POST", "/api/v1/tools/home_ping", "", "{}", 401, `"unauthorized"`},
		{"call", testKey, "POST", "/api/v1/tools/home_ping", "Bearer " + testKey, "{}", 200, `{"message":"pong","at":"2026-11-15T12:00:00Z"}`},
		{"call with empty body", testKey, "POST", "/api/v1/tools/home_ping", "Bearer " + testKey, "", 200, `"pong"`},
		{"call with invalid JSON", testKey, "POST", "/api/v1/tools/home_ping", "Bearer " + testKey, "{", 400, `"error"`},
		{"call with an array", testKey, "POST", "/api/v1/tools/home_ping", "Bearer " + testKey, "[]", 400, `JSON object`},
		{"call with null", testKey, "POST", "/api/v1/tools/home_ping", "Bearer " + testKey, "null", 400, `JSON object`},
		{"unknown tool", testKey, "POST", "/api/v1/tools/tv_power_on", "Bearer " + testKey, "{}", 404, `{"error":"unknown tool"}`},
		{"unknown path", testKey, "GET", "/nope", "", "", 404, `{"error":"not found"}`},
		{"mcp is off by default", testKey, "POST", "/mcp", "", "{}", 404, `"not found"`},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			h := newTestHandler(false, tt.key)
			req := httptest.NewRequest(tt.method, tt.path, strings.NewReader(tt.body))
			if tt.auth != "" {
				req.Header.Set("Authorization", tt.auth)
			}
			rec := httptest.NewRecorder()
			h.ServeHTTP(rec, req)
			if rec.Code != tt.wantStatus {
				t.Errorf("status = %d, want %d; body %s", rec.Code, tt.wantStatus, rec.Body)
			}
			if !strings.Contains(rec.Body.String(), tt.wantBody) {
				t.Errorf("body = %s, want it to contain %s", rec.Body, tt.wantBody)
			}
			if got := rec.Header().Get("X-GPT-Home-Instance"); got != testID {
				t.Errorf("X-GPT-Home-Instance = %q, want %q", got, testID)
			}
			if ct := rec.Header().Get("Content-Type"); ct != "application/json" {
				t.Errorf("Content-Type = %q", ct)
			}
		})
	}
}

func TestToolListShape(t *testing.T) {
	h := newTestHandler(false, testKey)
	req := httptest.NewRequest(http.MethodGet, "/api/v1/tools", http.NoBody)
	req.Header.Set("Authorization", "Bearer "+testKey)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	var out struct {
		Instructions string                       `json:"instructions"`
		Tools        []map[string]json.RawMessage `json:"tools"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &out); err != nil {
		t.Fatal(err)
	}
	if len(out.Tools) != 1 {
		t.Fatalf("tools = %d, want 1", len(out.Tools))
	}
	for _, k := range []string{"name", "title", "description", "inputSchema", "outputSchema", "annotations"} {
		v, ok := out.Tools[0][k]
		if !ok || string(v) == "null" {
			t.Errorf("tool lacks %q", k)
		}
	}
}

// hostRewriter makes every request look like it came through the relay: Host is the public domain while the
// connection is to loopback.
type hostRewriter struct{ host string }

func (h hostRewriter) RoundTrip(r *http.Request) (*http.Response, error) {
	r = r.Clone(r.Context())
	r.Host = h.host
	return http.DefaultTransport.RoundTrip(r)
}

func TestAuthlessMCPThroughRelayHost(t *testing.T) {
	ts := httptest.NewServer(newTestHandler(true, ""))
	defer ts.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	client := mcp.NewClient(&mcp.Implementation{Name: "test", Version: "1"}, nil)
	cs, err := client.Connect(ctx, &mcp.StreamableClientTransport{
		Endpoint:   ts.URL + "/mcp",
		HTTPClient: &http.Client{Transport: hostRewriter{host: "homie.test"}},
	}, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer cs.Close()

	if got := cs.InitializeResult().ServerInfo.Name; got != "gpt-home" {
		t.Errorf("serverInfo.name = %q", got)
	}
	tools, err := cs.ListTools(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(tools.Tools) != 1 || tools.Tools[0].Name != PingTool || !tools.Tools[0].Annotations.ReadOnlyHint {
		t.Fatalf("tools = %+v", tools.Tools)
	}
	res, err := cs.CallTool(ctx, &mcp.CallToolParams{Name: PingTool, Arguments: map[string]any{}})
	if err != nil {
		t.Fatal(err)
	}
	if res.IsError {
		t.Fatalf("tool failed: %+v", res.Content)
	}
	raw, err := json.Marshal(res.StructuredContent)
	if err != nil {
		t.Fatal(err)
	}
	var got PingResult
	if err := json.Unmarshal(raw, &got); err != nil {
		t.Fatal(err)
	}
	if want := (PingResult{Message: "pong", At: "2026-11-15T12:00:00Z"}); got != want {
		t.Fatalf("structuredContent = %s, want %+v", raw, want)
	}
}

func TestMCPResponsesCarryInstanceID(t *testing.T) {
	ts := httptest.NewServer(newTestHandler(true, ""))
	defer ts.Close()
	req, err := http.NewRequestWithContext(context.Background(), http.MethodGet, ts.URL+"/mcp", http.NoBody)
	if err != nil {
		t.Fatal(err)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	_, _ = io.Copy(io.Discard, resp.Body)
	if resp.StatusCode != http.StatusMethodNotAllowed { // stateless MCP has no GET stream
		t.Errorf("GET /mcp status = %d", resp.StatusCode)
	}
	if resp.Header.Get("X-GPT-Home-Instance") != testID {
		t.Error("GET /mcp lacks X-GPT-Home-Instance")
	}
}
