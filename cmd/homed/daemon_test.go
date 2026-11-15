package main

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/devXpro/gpt-home/internal/config"
	"github.com/devXpro/gpt-home/internal/hub"
)

// fakeBin is the plugin the daemon test runs (internal/plugins/testdata/fakeplugin, plugin id "fake").
var fakeBin string

func TestMain(m *testing.M) {
	os.Exit(testMain(m))
}

func testMain(m *testing.M) int {
	dir, err := os.MkdirTemp("", "homed-test-")
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 1
	}
	defer func() { _ = os.RemoveAll(dir) }()
	// A command that misses its --data-dir must never touch the real data dir.
	if err := os.Setenv("HOMED_DATA_DIR", filepath.Join(dir, "default-data")); err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 1
	}
	fakeBin = filepath.Join(dir, "fakeplugin")
	out, err := exec.Command("go", "build", "-o", fakeBin, "../../internal/plugins/testdata/fakeplugin").CombinedOutput() //nolint:gosec // fixed test package
	if err != nil {
		fmt.Fprintf(os.Stderr, "building the fake plugin: %v\n%s", err, out)
		return 1
	}
	return m.Run()
}

// shortTempDir is a data dir short enough for a unix socket path (t.TempDir on macOS is too long).
func shortTempDir(t *testing.T) string {
	t.Helper()
	dir, err := os.MkdirTemp("/tmp", "homed-")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(dir) })
	return dir
}

func freePort(t *testing.T) int {
	t.Helper()
	ln, err := (&net.ListenConfig{}).Listen(context.Background(), "tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	port := ln.Addr().(*net.TCPAddr).Port
	_ = ln.Close()
	return port
}

// syncBuffer collects the daemon's log; it is printed when the test fails.
type syncBuffer struct {
	mu  sync.Mutex
	buf strings.Builder
}

func (b *syncBuffer) Write(p []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.Write(p)
}

func (b *syncBuffer) String() string {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.String()
}

// startDaemon runs serve in the background and returns once the fake plugin's tools are listed.
func startDaemon(t *testing.T, opts runOptions) (*daemon, func() error) {
	t.Helper()
	logs := &syncBuffer{}
	t.Cleanup(func() {
		if t.Failed() {
			t.Logf("homed log:\n%s", logs.String())
		}
	})
	ctx, cancel := context.WithCancel(context.Background())
	ready := make(chan *daemon, 1)
	opts.ready = func(d *daemon) { ready <- d }
	done := make(chan error, 1)
	go func() {
		done <- serve(ctx, opts, slog.New(slog.NewTextHandler(logs, &slog.HandlerOptions{Level: slog.LevelDebug})))
	}()
	stop := sync.OnceValue(func() error {
		cancel()
		return <-done
	})
	var d *daemon
	select {
	case d = <-ready:
	case err := <-done:
		cancel()
		t.Fatalf("serve: %v", err)
	case <-time.After(30 * time.Second):
		cancel()
		t.Fatal("homed did not start")
	}
	t.Cleanup(func() { _ = stop() })

	deadline := time.After(30 * time.Second)
	for {
		changed := d.host.Changed()
		if len(d.host.Tools(hub.AudienceAdmin)) > 0 {
			return d, stop
		}
		select {
		case <-changed:
		case <-deadline:
			t.Fatalf("the fake plugin never listed its tools: %+v", d.host.Status())
		}
	}
}

func get(t *testing.T, url, host, key string) (int, string) {
	t.Helper()
	req, err := http.NewRequestWithContext(context.Background(), http.MethodGet, url, http.NoBody)
	if err != nil {
		t.Fatal(err)
	}
	if host != "" {
		req.Host = host
	}
	if key != "" {
		req.Header.Set("Authorization", "Bearer "+key)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)
	if resp.Header.Get("X-GPT-Home-Instance") == "" && resp.StatusCode != http.StatusMisdirectedRequest {
		t.Errorf("GET %s: no instance header", url)
	}
	return resp.StatusCode, string(body)
}

// TestDaemon is the T1.3 acceptance: homed run with the fake plugin; key new on the running homed;
// GET /api/v1/tools with that key; tool call through the socket; and the rest of the CLI against it.
func TestDaemon(t *testing.T) {
	dir := shortTempDir(t)
	port := freePort(t)
	base := "http://127.0.0.1:" + strconv.Itoa(port)
	mustCLI(t, "plugin", "add", "--id", "fake", "--data-dir", dir, "--", fakeBin, "serve")
	// A relay on a closed local port: the client keeps retrying, which is all this test needs from it.
	mustCLI(t, "relay", "connect", testCode(t, "homie.test"), "--data-dir", dir)
	relayPort := freePort(t)

	_, stop := startDaemon(t, runOptions{
		dataDir: dir, listen: "127.0.0.1:" + strconv.Itoa(port), serverAddr: "127.0.0.1:" + strconv.Itoa(relayPort),
	})

	if st, err := os.Stat(filepath.Join(dir, config.SocketFile)); err != nil || st.Mode().Perm() != 0o600 {
		t.Fatalf("socket: %v, %v", st, err)
	}
	if code, body := get(t, base+"/healthz", "", ""); code != http.StatusOK || !strings.Contains(body, `"ok":true`) {
		t.Fatalf("healthz: %d %s", code, body)
	}
	if code, _ := get(t, base+"/api/v1/tools", "", ""); code != http.StatusUnauthorized {
		t.Fatalf("tools without a key: %d", code)
	}

	// key new on the running homed: the daemon picks it up at once.
	out, errOut, err := cli(t, "", "key", "new", "--person", "roman", "--name", "Roman", "--data-dir", dir)
	if err != nil || !strings.Contains(errOut, "picked up") {
		t.Fatalf("key new: %v\n%s", err, errOut)
	}
	key := strings.TrimSpace(out)

	code, body := get(t, base+"/api/v1/tools", "", key)
	if code != http.StatusOK {
		t.Fatalf("tools: %d %s", code, body)
	}
	var manifest struct {
		Instructions string `json:"instructions"`
		Tools        []struct {
			Name string `json:"name"`
		} `json:"tools"`
	}
	if err := json.Unmarshal([]byte(body), &manifest); err != nil {
		t.Fatal(err)
	}
	var names []string
	for _, tl := range manifest.Tools {
		names = append(names, tl.Name)
	}
	if !strings.Contains(strings.Join(names, ","), "fake_echo") || strings.Contains(strings.Join(names, ","), "fake_add") {
		t.Fatalf("assistant tools = %v", names)
	}
	if !strings.Contains(manifest.Instructions, "Fake instructions.") {
		t.Fatalf("instructions = %q", manifest.Instructions)
	}

	// Through the relay the Host is the domain; anything else is refused.
	if code, _ := get(t, base+"/api/v1/tools", "homie.test", key); code != http.StatusOK {
		t.Fatalf("relay Host: %d", code)
	}
	if code, _ := get(t, base+"/healthz", "evil.test", ""); code != http.StatusMisdirectedRequest {
		t.Fatalf("foreign Host: %d", code)
	}

	// The socket serves every tool, admin ones included, and calls them as the CLI.
	list := mustCLI(t, "tool", "list", "--data-dir", dir)
	if !strings.Contains(list, "fake_echo") || !strings.Contains(list, "fake_add") || !strings.Contains(list, "admin") {
		t.Fatalf("tool list:\n%s", list)
	}
	out = mustCLI(t, "tool", "call", "fake_echo", `{"text":"hello"}`, "--data-dir", dir)
	if !strings.Contains(out, "hello") || !strings.Contains(out, `"via": "cli"`) || !strings.Contains(out, `"caller": "admin"`) {
		t.Fatalf("tool call output:\n%s", out)
	}
	if out := mustCLI(t, "tool", "call", "fake_add", `{"text":"extra"}`, "--data-dir", dir); !strings.Contains(out, "added") {
		t.Fatalf("admin tool call: %s", out)
	}
	if _, errOut, err := cli(t, "", "tool", "call", "fake_fail", "--data-dir", dir); !errors.Is(err, errToolFailed) ||
		!strings.Contains(errOut, "unreachable") {
		t.Fatalf("failing tool: %v %q", err, errOut)
	}
	if _, _, err := cli(t, "", "tool", "call", "fake_nope", `{"pin":"1234"}`, "--data-dir", dir); err == nil ||
		!strings.Contains(err.Error(), "unknown tool") {
		t.Fatalf("unknown tool: %v", err)
	}
	if _, _, err := cli(t, "", "tool", "call", "fake_echo", `[1]`, "--data-dir", dir); err == nil ||
		!strings.Contains(err.Error(), "JSON object") {
		t.Fatalf("array arguments: %v", err)
	}

	if out := mustCLI(t, "plugin", "list", "--data-dir", dir); !strings.Contains(out, "up") || !strings.Contains(out, "1.0.0") {
		t.Fatalf("plugin list:\n%s", out)
	}
	if out := mustCLI(t, "relay", "status", "--data-dir", dir); !strings.Contains(out, "homie.test") ||
		!strings.Contains(out, "disconnected") || !strings.Contains(out, "not run yet") {
		t.Fatalf("relay status:\n%s", out)
	}

	// A second homed on the same data dir is refused.
	err = serve(context.Background(), runOptions{dataDir: dir, listen: "127.0.0.1:" + strconv.Itoa(freePort(t))},
		slog.New(slog.DiscardHandler))
	if err == nil || !strings.Contains(err.Error(), "already running") {
		t.Fatalf("second homed: %v", err)
	}

	// Revoking on the running homed takes effect at once.
	cfg, err := config.Load(dir)
	if err != nil {
		t.Fatal(err)
	}
	mustCLI(t, "key", "revoke", cfg.Person("roman").Keys[0].SHA256[:10], "--data-dir", dir)
	if code, _ := get(t, base+"/api/v1/tools", "", key); code != http.StatusUnauthorized {
		t.Fatalf("revoked key: %d", code)
	}

	if err := stop(); err != nil {
		t.Fatalf("shutdown: %v", err)
	}
	if _, err := os.Stat(filepath.Join(dir, config.SocketFile)); !os.IsNotExist(err) {
		t.Fatalf("socket left behind: %v", err)
	}
	checkAudit(t, dir)
}

// checkAudit expects the REST and CLI calls in the audit file, with secrets redacted.
func checkAudit(t *testing.T, dir string) {
	t.Helper()
	files, err := filepath.Glob(filepath.Join(dir, config.AuditDir, "audit-*.jsonl"))
	if err != nil || len(files) != 1 {
		t.Fatalf("audit files = %v, %v", files, err)
	}
	f, err := os.Open(files[0])
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	seen := map[string]bool{}
	sc := bufio.NewScanner(f)
	for sc.Scan() {
		var e struct {
			Person, Via, Tool string
			Args              json.RawMessage
			OK                bool
		}
		if err := json.Unmarshal(sc.Bytes(), &e); err != nil {
			t.Fatal(err)
		}
		seen[e.Via+" "+e.Tool] = true
		if e.Tool == "fake_nope" && (!strings.Contains(string(e.Args), "[redacted]") || strings.Contains(string(e.Args), "1234")) {
			t.Errorf("args not redacted: %s", e.Args)
		}
		if e.Via == "cli" && e.Person != "admin" {
			t.Errorf("cli entry person = %q", e.Person)
		}
	}
	for _, want := range []string{"cli fake_echo", "cli fake_add", "cli fake_fail", "cli fake_nope"} {
		if !seen[want] {
			t.Errorf("audit lacks %q; has %v", want, seen)
		}
	}
}
