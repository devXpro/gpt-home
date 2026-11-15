package main

import (
	"bytes"
	"errors"
	"flag"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/devXpro/gpt-home/internal/config"
	"github.com/devXpro/gpt-home/internal/keys"
	"github.com/devXpro/gpt-home/internal/relay"
)

const testToken = "0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef"

func testCode(t *testing.T, domain string) string {
	t.Helper()
	code, err := relay.FormatCode(relay.Code{Domain: domain, Token: testToken})
	if err != nil {
		t.Fatal(err)
	}
	return code
}

// cli runs homed with args and returns stdout and stderr.
func cli(t *testing.T, stdin string, args ...string) (stdout, stderr string, err error) {
	t.Helper()
	var out, errOut bytes.Buffer
	err = run(args, strings.NewReader(stdin), &out, &errOut)
	return out.String(), errOut.String(), err
}

func mustCLI(t *testing.T, args ...string) string {
	t.Helper()
	out, errOut, err := cli(t, "", args...)
	if err != nil {
		t.Fatalf("homed %s: %v\nstdout: %s\nstderr: %s", strings.Join(args, " "), err, out, errOut)
	}
	return out
}

func TestVersion(t *testing.T) {
	if out := mustCLI(t, "version"); out != "homed dev\n" {
		t.Fatalf("version output = %q", out)
	}
}

func TestUnknownCommands(t *testing.T) {
	for _, args := range [][]string{{"bogus"}, {"relay", "bogus"}, {"key"}, {"tool", "x"}} {
		if _, _, err := cli(t, "", args...); err == nil || !strings.Contains(err.Error(), "unknown command") {
			t.Errorf("homed %v: err = %v", args, err)
		}
	}
	if _, _, err := cli(t, ""); !errors.Is(err, flag.ErrHelp) {
		t.Errorf("no args: err = %v", err)
	}
}

func TestParseFlags(t *testing.T) {
	tests := []struct {
		args    []string
		dataDir string
		pos     []string
	}{
		{[]string{"a", "--data-dir", "D", "b"}, "D", []string{"a", "b"}},
		{[]string{"--data-dir", "D", "a"}, "D", []string{"a"}},
		{[]string{"-"}, "", []string{"-"}},
		{[]string{"--data-dir", "D", "--", "/bin/x", "--data-dir", "y"}, "D", []string{"/bin/x", "--data-dir", "y"}},
		{[]string{"tv_set", `{"percent":20}`}, "", []string{"tv_set", `{"percent":20}`}},
	}
	for _, tt := range tests {
		fl, dataDir := newFlags("t", &bytes.Buffer{})
		pos, err := parseFlags(fl, tt.args)
		if err != nil || *dataDir != tt.dataDir || strings.Join(pos, "|") != strings.Join(tt.pos, "|") {
			t.Errorf("parseFlags(%q) = %q, data-dir %q, %v", tt.args, pos, *dataDir, err)
		}
	}
}

func TestRelayConnect(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "data")
	existing := "# my home\nhome_name: \"Мой шалаш\"\nlisten: 127.0.0.1:8080\nplugins:\n  - id: tv\n    command: [\"/bin/gpt-tv\", \"serve\"]\n"
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, config.FileName), []byte(existing), 0o600); err != nil {
		t.Fatal(err)
	}

	// The code goes first and the flag after it, as people type it.
	out, errOut, err := cli(t, "", "relay", "connect", testCode(t, "homie.test"), "--data-dir", dir)
	if err != nil {
		t.Fatalf("%v; stderr %s", err, errOut)
	}
	if strings.Contains(out+errOut, testToken) {
		t.Fatal("relay connect printed the token")
	}
	st, err := os.Stat(filepath.Join(dir, config.RelayTokenFile))
	if err != nil || st.Mode().Perm() != 0o600 {
		t.Fatalf("relay-token: %v, %v", st, err)
	}
	if tok, err := config.ReadRelayToken(dir); err != nil || tok != testToken {
		t.Fatalf("token = %q, %v", tok, err)
	}
	cfg, err := config.Load(dir)
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Relay.Domain != "homie.test" || cfg.HomeName != "Мой шалаш" || cfg.Listen != "127.0.0.1:8080" {
		t.Fatalf("config = %+v", cfg)
	}
	raw, err := os.ReadFile(filepath.Join(dir, config.FileName)) //nolint:gosec // test temp dir
	if err != nil {
		t.Fatal(err)
	}
	for _, keep := range []string{"# my home", "plugins:", "gpt-tv"} {
		if !strings.Contains(string(raw), keep) {
			t.Errorf("homed.yaml lost %q:\n%s", keep, raw)
		}
	}

	// A second connect, from stdin, replaces the domain in place.
	if _, _, err := cli(t, testCode(t, "other.test")+"\n", "relay", "connect", "--data-dir", dir, "-"); err != nil {
		t.Fatal(err)
	}
	cfg, err = config.Load(dir)
	if err != nil || cfg.Relay.Domain != "other.test" {
		t.Fatalf("config = %+v, %v", cfg, err)
	}
	raw, err = os.ReadFile(filepath.Join(dir, config.FileName)) //nolint:gosec // test temp dir
	if err != nil {
		t.Fatal(err)
	}
	if n := strings.Count(string(raw), "relay:"); n != 1 {
		t.Fatalf("homed.yaml has %d relay keys:\n%s", n, raw)
	}
}

func TestRelayConnectErrors(t *testing.T) {
	dir := t.TempDir()
	tests := []struct {
		name string
		args []string
	}{
		{"no code", []string{"relay", "connect", "--data-dir", dir}},
		{"bad code", []string{"relay", "connect", "--data-dir", dir, "ghr1.zzz"}},
		{"extra args", []string{"relay", "connect", "--data-dir", dir, testCode(t, "a.test"), "extra"}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if _, _, err := cli(t, "", tt.args...); err == nil {
				t.Fatal("want an error")
			}
		})
	}
	if _, err := os.Stat(filepath.Join(dir, config.RelayTokenFile)); !os.IsNotExist(err) {
		t.Fatal("a failed connect wrote the token")
	}
}

func TestCommandsNeedRunningHomed(t *testing.T) {
	dir := t.TempDir()
	for _, args := range [][]string{{"relay", "status"}, {"tool", "list"}, {"tool", "call", "x_y"}} {
		_, _, err := cli(t, "", append(args, "--data-dir", dir)...)
		if !errors.Is(err, errNotRunning) {
			t.Errorf("homed %v: err = %v", args, err)
		}
	}
}

func TestHostGuard(t *testing.T) {
	h := hostGuard("homie.test", 8080, http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusTeapot)
	}))
	tests := []struct {
		host string
		want int
	}{
		{"homie.test", http.StatusTeapot},
		{"HOMIE.test", http.StatusTeapot},
		{"homie.test:18443", http.StatusTeapot},
		{"homie.test.", http.StatusTeapot},
		{"127.0.0.1:8080", http.StatusTeapot},
		{"localhost:8080", http.StatusTeapot},
		{"[::1]:8080", http.StatusTeapot},
		{"127.0.0.1:9090", http.StatusMisdirectedRequest},
		{"127.0.0.1", http.StatusMisdirectedRequest},
		{"evil.test", http.StatusMisdirectedRequest},
		{"homie.test.evil.test", http.StatusMisdirectedRequest},
		{"", http.StatusMisdirectedRequest},
	}
	for _, tt := range tests {
		r := httptest.NewRequest(http.MethodGet, "/healthz", http.NoBody)
		r.Host = tt.host
		w := httptest.NewRecorder()
		h.ServeHTTP(w, r)
		if w.Code != tt.want {
			t.Errorf("Host %q: status %d, want %d", tt.host, w.Code, tt.want)
		}
	}
	// Without a relay only the loopback names pass.
	h = hostGuard("", 8080, http.NotFoundHandler())
	r := httptest.NewRequest(http.MethodGet, "/", http.NoBody)
	r.Host = "homie.test"
	w := httptest.NewRecorder()
	h.ServeHTTP(w, r)
	if w.Code != http.StatusMisdirectedRequest {
		t.Errorf("no relay: status %d", w.Code)
	}
}

func TestKeyCommands(t *testing.T) {
	dir := t.TempDir()
	out, errOut, err := cli(t, "", "key", "new", "--person", "roman", "--label", "ChatGPT Site", "--data-dir", dir)
	if err != nil {
		t.Fatal(err)
	}
	key := strings.TrimSpace(out)
	if !strings.HasPrefix(key, keys.Prefix) || strings.Count(out, "\n") != 1 {
		t.Fatalf("stdout must be the key alone, got %q", out)
	}
	if strings.Contains(errOut, key) || !strings.Contains(errOut, "not running") {
		t.Fatalf("stderr = %q", errOut)
	}
	cfg, err := config.Load(dir)
	if err != nil {
		t.Fatal(err)
	}
	p := cfg.Person("roman")
	if p == nil || p.Name != "Roman" || strings.Join(p.Plugins, ",") != "*" || len(p.Keys) != 1 ||
		p.Keys[0].SHA256 != keys.Hash(key) || p.Keys[0].Label != "ChatGPT Site" || p.Keys[0].Created.IsZero() {
		t.Fatalf("person = %+v", p)
	}
	raw, err := os.ReadFile(filepath.Join(dir, config.FileName)) //nolint:gosec // test temp dir
	if err != nil || strings.Contains(string(raw), key) {
		t.Fatalf("homed.yaml holds the key itself (%v)", err)
	}

	// A second key for the same person changes the name and scopes; another person gets one too.
	mustCLI(t, "key", "new", "--person", "roman", "--name", "Роман", "--plugins", "tv, kettle", "--data-dir", dir)
	mustCLI(t, "key", "new", "--person", "tanya", "--data-dir", dir)
	cfg, err = config.Load(dir)
	if err != nil {
		t.Fatal(err)
	}
	if p := cfg.Person("roman"); p.Name != "Роман" || strings.Join(p.Plugins, ",") != "tv,kettle" || len(p.Keys) != 2 {
		t.Fatalf("roman = %+v", p)
	}
	list := mustCLI(t, "key", "list", "--data-dir", dir)
	id := keys.Hash(key)[:keyIDLen]
	if !strings.Contains(list, id) || !strings.Contains(list, "tanya") || strings.Contains(list, key) {
		t.Fatalf("key list:\n%s", list)
	}

	for _, bad := range [][]string{
		{"key", "new"},                                       // no person
		{"key", "new", "--person", "Bad Id"},                 // invalid id
		{"key", "new", "--person", "x", "--plugins", "TV!"},  // invalid scope
		{"key", "revoke", "abc"},                             // prefix too short
		{"key", "revoke", "zzzzzzzz"},                        // no match
		{"key", "revoke", strings.Repeat("0", 64) + "extra"}, // no match
	} {
		if _, _, err := cli(t, "", append(bad, "--data-dir", dir)...); err == nil {
			t.Errorf("homed %v: want an error", bad)
		}
	}
	mustCLI(t, "key", "revoke", id[:8], "--data-dir", dir)
	cfg, err = config.Load(dir)
	if err != nil {
		t.Fatal(err)
	}
	if n := len(cfg.Person("roman").Keys); n != 1 {
		t.Fatalf("roman has %d keys after revoke", n)
	}
}

func TestKeyNewToKeychain(t *testing.T) {
	dir := t.TempDir()
	// A fake `security` that records its arguments and its stdin.
	fake := filepath.Join(dir, "security")
	script := "#!/bin/sh\nprintf '%s\\n' \"$*\" > \"$0.args\"\ncat > \"$0.stdin\"\n"
	if err := os.WriteFile(fake, []byte(script), 0o700); err != nil { //nolint:gosec // an executable test script
		t.Fatal(err)
	}
	old := securityCmd
	securityCmd = fake
	t.Cleanup(func() { securityCmd = old })

	out, errOut, err := cli(t, "", "key", "new", "--person", "roman", "--keychain", "gpt-home-key", "--data-dir", dir)
	if err != nil {
		t.Fatalf("%v: %s", err, errOut)
	}
	cfg, err := config.Load(dir)
	if err != nil {
		t.Fatal(err)
	}
	hash := cfg.Person("roman").Keys[0].SHA256
	args, _ := os.ReadFile(fake + ".args")   //nolint:gosec // test temp dir
	stdin, _ := os.ReadFile(fake + ".stdin") //nolint:gosec // test temp dir
	if strings.TrimSpace(string(args)) != "-i" {
		t.Fatalf("security args = %q", args)
	}
	line := strings.TrimSpace(string(stdin))
	const prefix = `add-generic-password -U -s "gpt-home-key" -a "roman" -w "`
	if !strings.HasPrefix(line, prefix) || !strings.HasSuffix(line, `"`) {
		t.Fatalf("security stdin = %q", line)
	}
	key := strings.TrimSuffix(strings.TrimPrefix(line, prefix), `"`)
	if keys.Hash(key) != hash {
		t.Fatal("the Keychain got another key than the one stored")
	}
	if strings.Contains(out+errOut, key) {
		t.Fatal("the key was printed")
	}

	// A Keychain failure leaves homed.yaml alone.
	if err := os.WriteFile(fake, []byte("#!/bin/sh\necho boom\nexit 45\n"), 0o700); err != nil { //nolint:gosec // test script
		t.Fatal(err)
	}
	if _, _, err := cli(t, "", "key", "new", "--person", "roman", "--keychain", "gpt-home-key", "--data-dir", dir); err == nil ||
		!strings.Contains(err.Error(), "boom") {
		t.Fatalf("err = %v", err)
	}
	if cfg, err := config.Load(dir); err != nil || len(cfg.Person("roman").Keys) != 1 {
		t.Fatalf("keys after a failed Keychain write: %+v, %v", cfg, err)
	}
}

func TestPluginCommands(t *testing.T) {
	dir := t.TempDir()
	out := mustCLI(t, "plugin", "add", "--id", "tv", "--data-dir", dir, "--", "sh", "serve", "--flag")
	if !strings.Contains(out, "Added plugin tv") {
		t.Fatalf("stdout = %q", out)
	}
	cfg, err := config.Load(dir)
	if err != nil {
		t.Fatal(err)
	}
	cmd := cfg.Plugin("tv").Command
	if len(cmd) != 3 || !filepath.IsAbs(cmd[0]) || cmd[1] != "serve" || cmd[2] != "--flag" {
		t.Fatalf("command = %q", cmd)
	}
	// --data-dir goes before "--": everything after it is the plugin's command.
	for _, bad := range [][]string{
		{"plugin", "add", "--data-dir", dir, "--id", "tv", "--", "sh"},                    // exists
		{"plugin", "add", "--data-dir", dir, "--id", "TV", "--", "sh"},                    // invalid id
		{"plugin", "add", "--data-dir", dir, "--id", "kettle"},                            // no command
		{"plugin", "add", "--data-dir", dir, "--id", "kettle", "--", "/no/such/gpt-tool"}, // missing binary
		{"plugin", "remove", "--data-dir", dir, "kettle"},                                 // unknown
	} {
		if _, _, err := cli(t, "", bad...); err == nil {
			t.Errorf("homed %v: want an error", bad)
		}
	}
	list, errOut, err := cli(t, "", "plugin", "list", "--data-dir", dir)
	if err != nil || !strings.Contains(list, "tv") || !strings.Contains(errOut, "not running") {
		t.Fatalf("plugin list: %v\n%s\n%s", err, list, errOut)
	}
	mustCLI(t, "plugin", "remove", "tv", "--data-dir", dir)
	if cfg, err := config.Load(dir); err != nil || len(cfg.Plugins) != 0 {
		t.Fatalf("plugins after remove: %+v, %v", cfg, err)
	}
}
