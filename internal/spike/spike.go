// Package spike is the phase 0 stand-in for the hub's public HTTP surface (PLAN §5.4): /healthz, the REST façade
// and /mcp with a single read-only tool, home_ping. It exists only to prove the relay and the ChatGPT paths (T0.5)
// and is replaced by internal/gateway in T1.3.
package spike

import (
	"bytes"
	"context"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"strings"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/devXpro/gpt-home/internal/relay"
)

// PingTool is the only tool of the spike.
const PingTool = "home_ping"

const (
	instructions = "This is GPT-Home, a home hub (connection test build). Call home_ping only when the user asks to " +
		"ping or check the home; it answers pong with the hub's time. Confirm the result in one short sentence."
	pingDescription = "Checks that the home hub is reachable. Returns {\"message\":\"pong\",\"at\":<hub time, RFC 3339>}. " +
		"Changes nothing."
	maxBody = 64 << 10
)

// Options configures the spike handler.
type Options struct {
	InstanceID  string           // sent as X-GPT-Home-Instance on every response
	Key         string           // bearer key for the REST façade; empty means every REST call is 401
	AuthlessMCP bool             // serve /mcp without any auth (T0.5 path A test only)
	Version     string           // hub version for MCP serverInfo
	Now         func() time.Time // clock; nil means time.Now
	Logger      *slog.Logger     // nil means discard
}

// PingResult is home_ping's structured result.
type PingResult struct {
	Message string `json:"message"`
	At      string `json:"at"`
}

type server struct {
	opts    Options
	keyHash [sha256.Size]byte
}

// New returns the spike's HTTP handler.
func New(opts Options) http.Handler {
	if opts.Now == nil {
		opts.Now = time.Now
	}
	if opts.Logger == nil {
		opts.Logger = slog.New(slog.DiscardHandler)
	}
	s := &server{opts: opts, keyHash: sha256.Sum256([]byte(opts.Key))}

	mux := http.NewServeMux()
	mux.HandleFunc("GET /healthz", func(w http.ResponseWriter, _ *http.Request) {
		writeJSON(w, http.StatusOK, map[string]bool{"ok": true})
	})
	mux.HandleFunc("GET /api/v1/tools", s.auth(s.listTools))
	mux.HandleFunc("POST /api/v1/tools/{name}", s.auth(s.callTool))
	if opts.AuthlessMCP {
		mux.Handle("/mcp", s.mcpHandler())
	}
	mux.HandleFunc("/", func(w http.ResponseWriter, _ *http.Request) {
		writeJSON(w, http.StatusNotFound, errorBody("not found"))
	})

	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set(relay.InstanceHeader, opts.InstanceID)
		mux.ServeHTTP(w, r)
	})
}

func (s *server) ping() PingResult {
	return PingResult{Message: "pong", At: s.opts.Now().UTC().Format(time.RFC3339)}
}

// auth checks "Authorization: Bearer <key>" in constant time. An empty configured key rejects everything.
func (s *server) auth(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		got, ok := strings.CutPrefix(r.Header.Get("Authorization"), "Bearer ")
		gotHash := sha256.Sum256([]byte(strings.TrimSpace(got)))
		if !ok || s.opts.Key == "" || subtle.ConstantTimeCompare(gotHash[:], s.keyHash[:]) != 1 {
			writeJSON(w, http.StatusUnauthorized, errorBody("unauthorized"))
			return
		}
		next(w, r)
	}
}

type restTool struct {
	Name         string          `json:"name"`
	Title        string          `json:"title,omitempty"`
	Description  string          `json:"description"`
	InputSchema  json.RawMessage `json:"inputSchema"`
	OutputSchema json.RawMessage `json:"outputSchema,omitempty"`
	Annotations  json.RawMessage `json:"annotations,omitempty"`
}

func (s *server) listTools(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, http.StatusOK, map[string]any{
		"instructions": instructions,
		"tools": []restTool{{
			Name:         PingTool,
			Title:        "Ping",
			Description:  pingDescription,
			InputSchema:  json.RawMessage(`{"type":"object","properties":{},"additionalProperties":false}`),
			OutputSchema: json.RawMessage(`{"type":"object","properties":{"message":{"type":"string"},"at":{"type":"string"}},"required":["message","at"]}`),
			Annotations:  json.RawMessage(`{"readOnlyHint":true,"openWorldHint":false}`),
		}},
	})
}

func (s *server) callTool(w http.ResponseWriter, r *http.Request) {
	if r.PathValue("name") != PingTool {
		writeJSON(w, http.StatusNotFound, errorBody("unknown tool"))
		return
	}
	body, err := io.ReadAll(http.MaxBytesReader(w, r.Body, maxBody))
	if err != nil {
		writeJSON(w, http.StatusBadRequest, errorBody("cannot read the request body"))
		return
	}
	// An empty body counts as {}; anything else must be a JSON object.
	if len(bytes.TrimSpace(body)) > 0 {
		var args map[string]json.RawMessage
		if err := json.Unmarshal(body, &args); err != nil || args == nil {
			writeJSON(w, http.StatusBadRequest, errorBody("the body must be a JSON object"))
			return
		}
	}
	s.opts.Logger.Info("tool call", "tool", PingTool, "via", "site")
	writeJSON(w, http.StatusOK, s.ping())
}

func (s *server) mcpHandler() http.Handler {
	srv := mcp.NewServer(&mcp.Implementation{Name: "gpt-home", Title: "GPT-Home (spike)", Version: s.opts.Version},
		&mcp.ServerOptions{Instructions: instructions})
	mcp.AddTool(srv, &mcp.Tool{
		Name:        PingTool,
		Title:       "Ping",
		Description: pingDescription,
		Annotations: &mcp.ToolAnnotations{ReadOnlyHint: true, OpenWorldHint: new(false)},
	}, func(context.Context, *mcp.CallToolRequest, struct{}) (*mcp.CallToolResult, PingResult, error) {
		s.opts.Logger.Info("tool call", "tool", PingTool, "via", "mcp")
		p := s.ping()
		return &mcp.CallToolResult{Content: []mcp.Content{&mcp.TextContent{Text: "pong at " + p.At}}}, p, nil
	})
	return mcp.NewStreamableHTTPHandler(func(*http.Request) *mcp.Server { return srv }, &mcp.StreamableHTTPOptions{
		Stateless:    true,
		JSONResponse: true,
		// The hub listens on loopback, and the relay delivers requests whose Host is the public domain. go-sdk's
		// DNS-rebinding guard would answer those with 403, so it is off; the bearer/OAuth layer protects /mcp.
		DisableLocalhostProtection: true,
	})
}

func errorBody(msg string) map[string]string { return map[string]string{"error": msg} }

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v) // the client went away; nothing to do
}
