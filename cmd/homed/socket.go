package main

// The local admin socket, <data_dir>/homed.sock (0600): a tiny JSON API over HTTP for the homed CLI. Whoever can
// open the socket is the hub's owner, so it serves the admin audience: every tool, admin ones included.
//
//	GET  /v1/tools          every tool                     → [apiTool]
//	POST /v1/tools/{name}   call a tool, body = arguments  → apiResult; errors as {"error": …}
//	GET  /v1/plugins        plugin status                  → [apiPlugin]
//	GET  /v1/relay          relay status and self-check    → apiRelay
//	POST /v1/reload         re-read people and keys        → apiReload

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"net"
	"net/http"
	"path/filepath"
	"syscall"
	"time"

	"github.com/devXpro/gpt-home/internal/audit"
	"github.com/devXpro/gpt-home/internal/config"
	"github.com/devXpro/gpt-home/internal/gateway"
	"github.com/devXpro/gpt-home/internal/hub"
)

// cliCaller is who the socket's tool calls run as, in the audit log and in the plugin's request _meta.
var cliCaller = hub.Caller{Person: "admin", Via: "cli"}

type apiTool struct {
	Name        string          `json:"name"`
	Plugin      string          `json:"plugin"`
	Title       string          `json:"title,omitempty"`
	Description string          `json:"description,omitempty"`
	Audience    hub.Audience    `json:"audience"`
	Inventory   bool            `json:"inventory,omitempty"`
	TimeoutSec  int             `json:"timeout_sec"`
	InputSchema json.RawMessage `json:"input_schema,omitempty"`
}

type apiResult struct {
	Text       string          `json:"text"`
	Structured json.RawMessage `json:"structured,omitempty"`
	IsError    bool            `json:"is_error"`
}

type apiPlugin struct {
	ID        string          `json:"id"`
	Title     string          `json:"title"`
	Version   string          `json:"version,omitempty"`
	State     hub.PluginState `json:"state"`
	LastError string          `json:"last_error,omitempty"`
	Since     time.Time       `json:"since"`
}

type apiRelay struct {
	Domain         string     `json:"domain,omitempty"` // empty: no relay configured
	Connected      bool       `json:"connected"`
	Since          *time.Time `json:"since,omitempty"`
	LastError      string     `json:"last_error,omitempty"`
	SelfCheckAt    *time.Time `json:"self_check_at,omitempty"`
	SelfCheckError string     `json:"self_check_error,omitempty"`
}

type apiReload struct {
	People        int      `json:"people"`
	Keys          int      `json:"keys"`
	RestartNeeded []string `json:"restart_needed,omitempty"`
}

type apiError struct {
	Error string `json:"error"`
}

func (d *daemon) socketHandler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /v1/tools", func(w http.ResponseWriter, _ *http.Request) {
		out := []apiTool{}
		for _, t := range d.host.Tools(hub.AudienceAdmin) {
			out = append(out, apiTool{
				Name: t.Name, Plugin: t.PluginID, Title: t.Title, Description: t.Description, Audience: t.Audience,
				Inventory: t.Inventory, TimeoutSec: int(t.Timeout / time.Second), InputSchema: t.InputSchema,
			})
		}
		writeAPI(w, http.StatusOK, out)
	})
	mux.HandleFunc("POST /v1/tools/{name}", d.socketCall)
	mux.HandleFunc("GET /v1/plugins", func(w http.ResponseWriter, _ *http.Request) {
		out := []apiPlugin{}
		for _, s := range d.host.Status() {
			out = append(out, apiPlugin{
				ID: s.ID, Title: s.Title, Version: s.Version, State: s.State, LastError: s.LastError, Since: s.Since,
			})
		}
		writeAPI(w, http.StatusOK, out)
	})
	mux.HandleFunc("GET /v1/relay", func(w http.ResponseWriter, _ *http.Request) {
		writeAPI(w, http.StatusOK, d.relayStatus())
	})
	mux.HandleFunc("POST /v1/reload", func(w http.ResponseWriter, _ *http.Request) {
		restart, err := d.reload()
		if err != nil {
			writeAPI(w, http.StatusBadRequest, apiError{err.Error()})
			return
		}
		d.mu.Lock()
		cfg := d.cfg
		d.mu.Unlock()
		writeAPI(w, http.StatusOK, apiReload{People: len(cfg.People), Keys: len(cfg.KeyEntries()), RestartNeeded: restart})
	})
	return mux
}

func (d *daemon) relayStatus() apiRelay {
	if d.relay == nil {
		return apiRelay{}
	}
	st := d.relay.Status()
	out := apiRelay{Domain: d.relayCfg.Domain, Connected: st.Connected, LastError: st.LastError}
	if !st.Since.IsZero() {
		out.Since = &st.Since
	}
	d.mu.Lock()
	sc := d.selfCheck
	d.mu.Unlock()
	if !sc.at.IsZero() {
		out.SelfCheckAt, out.SelfCheckError = &sc.at, sc.err
	}
	return out
}

func (d *daemon) socketCall(w http.ResponseWriter, r *http.Request) {
	name := r.PathValue("name")
	body, err := io.ReadAll(io.LimitReader(r.Body, 1<<20))
	if err != nil {
		writeAPI(w, http.StatusBadRequest, apiError{"read body: " + err.Error()})
		return
	}
	args := json.RawMessage(bytes.TrimSpace(body))
	if len(args) == 0 {
		args = json.RawMessage(`{}`)
	}
	var obj map[string]json.RawMessage
	if err := json.Unmarshal(args, &obj); err != nil || obj == nil {
		writeAPI(w, http.StatusBadRequest, apiError{"the arguments must be a JSON object"})
		return
	}

	start := time.Now()
	res, err := d.host.Call(r.Context(), cliCaller, name, args)
	entry := hub.AuditEntry{
		At: start, Caller: cliCaller, Tool: name, Args: audit.Redact(args),
		OK: err == nil && !res.IsError, Error: gateway.AuditError(res, err), Duration: time.Since(start),
	}
	d.audit.Record(r.Context(), entry)

	switch {
	case err == nil:
		writeAPI(w, http.StatusOK, apiResult{Text: res.Text, Structured: res.Structured, IsError: res.IsError})
	case errors.Is(err, hub.ErrUnknownTool):
		writeAPI(w, http.StatusNotFound, apiError{"unknown tool " + name})
	case errors.Is(err, hub.ErrPluginDown):
		writeAPI(w, http.StatusServiceUnavailable, apiError{"plugin is not running: " + err.Error()})
	case errors.Is(err, context.DeadlineExceeded):
		writeAPI(w, http.StatusGatewayTimeout, apiError{"the tool timed out"})
	default:
		writeAPI(w, http.StatusInternalServerError, apiError{err.Error()})
	}
}

func writeAPI(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}

// errNotRunning means nothing answers on the socket.
var errNotRunning = errors.New("homed is not running")

// apiCall is one request from the CLI to the running homed. out receives the 200 answer; any other status
// becomes an error with the server's message.
func apiCall(dir, method, path string, body []byte, out any) error {
	sock := filepath.Join(dir, config.SocketFile)
	client := &http.Client{
		Transport: &http.Transport{DialContext: func(ctx context.Context, _, _ string) (net.Conn, error) {
			var dialer net.Dialer
			return dialer.DialContext(ctx, "unix", sock)
		}},
		Timeout: 6 * time.Minute, // above the longest tool timeout (300 s)
	}
	// The host part is a placeholder: the transport always dials the socket.
	req, err := http.NewRequestWithContext(context.Background(), method, "http://homed"+path, bytes.NewReader(body)) //nolint:gosec // G704: dials only the local socket
	if err != nil {
		return err
	}
	resp, err := client.Do(req) //nolint:gosec // G704: dials only the local socket
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) || errors.Is(err, syscall.ECONNREFUSED) {
			return fmt.Errorf("%w (no answer on %s); start it with: homed run", errNotRunning, sock)
		}
		return err
	}
	defer resp.Body.Close()
	raw, err := io.ReadAll(resp.Body)
	if err != nil {
		return err
	}
	if resp.StatusCode != http.StatusOK {
		var e apiError
		if json.Unmarshal(raw, &e) == nil && e.Error != "" {
			return errors.New(e.Error)
		}
		return fmt.Errorf("homed answered %s", resp.Status)
	}
	if out == nil {
		return nil
	}
	return json.Unmarshal(raw, out)
}
