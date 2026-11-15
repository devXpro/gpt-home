package config

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

const hashA = "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
const hashB = "bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb"

const sample = `# my home
home_name: "Мой шалаш"
listen: 127.0.0.1:8080 # loopback only
relay:
  domain: homie.test
plugins:
  # the TV
  - id: tv
    command: ["/bin/gpt-tv", "serve"]
people:
  - id: roman
    name: Roman
    plugins: ["*"]
    keys:
      - sha256: "` + hashA + `"
        label: "ChatGPT Site"
        created: 2026-11-15T12:00:00Z
`

func writeSample(t *testing.T, content string) string {
	t.Helper()
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, FileName), []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
	return dir
}

func readFile(t *testing.T, path string) string {
	t.Helper()
	raw, err := os.ReadFile(path) //nolint:gosec // test temp dir
	if err != nil {
		t.Fatal(err)
	}
	return string(raw)
}

func TestLoad(t *testing.T) {
	c, err := Load(writeSample(t, sample))
	if err != nil {
		t.Fatal(err)
	}
	if c.HomeName != "Мой шалаш" || c.ListenAddr() != "127.0.0.1:8080" || c.Relay.Domain != "homie.test" {
		t.Fatalf("config = %+v", c)
	}
	if p := c.Plugin("tv"); p == nil || p.Command[0] != "/bin/gpt-tv" {
		t.Fatalf("plugin tv = %+v", p)
	}
	r := c.Person("roman")
	if r == nil || len(r.Keys) != 1 || r.Keys[0].Label != "ChatGPT Site" ||
		!r.Keys[0].Created.Equal(time.Date(2026, 11, 15, 12, 0, 0, 0, time.UTC)) {
		t.Fatalf("person = %+v", r)
	}
	entries := c.KeyEntries()
	if len(entries) != 1 || entries[0].SHA256 != hashA || entries[0].Person.Name != "Roman" || entries[0].Person.Plugins[0] != "*" {
		t.Fatalf("entries = %+v", entries)
	}
}

func TestLoadMissingIsEmpty(t *testing.T) {
	c, err := Load(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	if c.ListenAddr() != DefaultListen || c.Relay.Domain != "" || len(c.Plugins) != 0 {
		t.Fatalf("config = %+v", c)
	}
}

func TestValidate(t *testing.T) {
	tests := []struct {
		name, yaml, want string
	}{
		{"unknown key", "home_nam: x\n", "field home_nam not found"},
		{"public listen", "listen: 0.0.0.0:8080\n", "loopback only"},
		{"bad domain", "relay:\n  domain: https://x.test\n", "invalid relay domain"},
		{"bad plugin id", "plugins:\n  - id: TV\n    command: [x]\n", "must match"},
		{"duplicate plugin", "plugins:\n  - id: tv\n    command: [x]\n  - id: tv\n    command: [y]\n", "twice"},
		{"empty command", "plugins:\n  - id: tv\n    command: []\n", "command is empty"},
		{"bad person id", "people:\n  - id: Roman\n    name: R\n", "must match"},
		{"no name", "people:\n  - id: roman\n    name: \"\"\n", "name is empty"},
		{"bad scope", "people:\n  - id: roman\n    name: R\n    plugins: [\"TV!\"]\n", "neither"},
		{"bad hash", "people:\n  - id: roman\n    name: R\n    keys:\n      - sha256: abc\n", "64 lowercase hex"},
		{"duplicate hash", "people:\n  - id: a\n    name: A\n    keys:\n      - sha256: " + hashA +
			"\n  - id: b\n    name: B\n    keys:\n      - sha256: " + hashA + "\n", "twice"},
		{"not a mapping", "- a\n- b\n", "cannot unmarshal"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := Load(writeSample(t, tt.yaml))
			if err == nil || !strings.Contains(err.Error(), tt.want) {
				t.Fatalf("err = %v, want %q", err, tt.want)
			}
		})
	}
}

func TestUpdateKeepsUntouchedSections(t *testing.T) {
	dir := writeSample(t, sample)
	err := Update(dir, func(c *Config) error {
		c.Person("roman").Keys = append(c.Person("roman").Keys, Key{SHA256: hashB, Label: "second"})
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	out := readFile(t, filepath.Join(dir, FileName))
	for _, keep := range []string{"# my home", "# loopback only", "# the TV", `command: ["/bin/gpt-tv", "serve"]`} {
		if !strings.Contains(out, keep) {
			t.Errorf("lost %q:\n%s", keep, out)
		}
	}
	c, err := Load(dir)
	if err != nil {
		t.Fatal(err)
	}
	if n := len(c.Person("roman").Keys); n != 2 {
		t.Fatalf("keys = %d:\n%s", n, out)
	}
	st, err := os.Stat(filepath.Join(dir, FileName))
	if err != nil || st.Mode().Perm() != 0o600 {
		t.Fatalf("mode = %v, %v", st.Mode(), err)
	}
}

func TestUpdateRemovesEmptySection(t *testing.T) {
	dir := writeSample(t, sample)
	if err := Update(dir, func(c *Config) error { c.Plugins = nil; return nil }); err != nil {
		t.Fatal(err)
	}
	out := readFile(t, filepath.Join(dir, FileName))
	if strings.Contains(out, "plugins:\n  #") || strings.Contains(out, "gpt-tv") {
		t.Fatalf("plugins still there:\n%s", out)
	}
	if !strings.Contains(out, "people:") {
		t.Fatalf("people lost:\n%s", out)
	}
}

func TestUpdateNewFileAndErrors(t *testing.T) {
	dir := t.TempDir()
	if err := Update(dir, func(c *Config) error { c.Relay.Domain = "homie.test"; return nil }); err != nil {
		t.Fatal(err)
	}
	c, err := Load(dir)
	if err != nil || c.Relay.Domain != "homie.test" {
		t.Fatalf("config = %+v, %v", c, err)
	}

	before := readFile(t, filepath.Join(dir, FileName))
	boom := errors.New("boom")
	if err := Update(dir, func(*Config) error { return boom }); !errors.Is(err, boom) {
		t.Fatalf("err = %v", err)
	}
	if err := Update(dir, func(c *Config) error { c.Listen = "0.0.0.0:1"; return nil }); err == nil {
		t.Fatal("an invalid change was accepted")
	}
	if after := readFile(t, filepath.Join(dir, FileName)); after != before {
		t.Fatalf("a failed update wrote the file:\n%s", after)
	}
}

func TestRelayToken(t *testing.T) {
	dir := t.TempDir()
	if err := WriteRelayToken(dir, "tok"); err != nil {
		t.Fatal(err)
	}
	if tok, err := ReadRelayToken(dir); err != nil || tok != "tok" {
		t.Fatalf("token = %q, %v", tok, err)
	}
	if err := os.Chmod(filepath.Join(dir, RelayTokenFile), 0o644); err != nil { //nolint:gosec // deliberately loose
		t.Fatal(err)
	}
	if _, err := ReadRelayToken(dir); err == nil || !strings.Contains(err.Error(), "chmod 600") {
		t.Fatalf("err = %v", err)
	}
}

func TestInstanceIDIsStable(t *testing.T) {
	dir := t.TempDir()
	a, err := InstanceID(dir)
	if err != nil {
		t.Fatal(err)
	}
	b, err := InstanceID(dir)
	if err != nil {
		t.Fatal(err)
	}
	if a != b || len(a) != 32 {
		t.Fatalf("ids %q and %q", a, b)
	}
}

func TestResolveDataDir(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "fresh")
	got, err := ResolveDataDir(dir)
	if err != nil || got != dir {
		t.Fatalf("dir = %q, %v", got, err)
	}
	if st, err := os.Stat(dir); err != nil || st.Mode().Perm() != 0o700 {
		t.Fatalf("mode = %v, %v", st.Mode(), err)
	}
	env := filepath.Join(t.TempDir(), "env")
	t.Setenv("HOMED_DATA_DIR", env)
	if got, err := ResolveDataDir(""); err != nil || got != env {
		t.Fatalf("dir = %q, %v", got, err)
	}
}

func TestLoopbackPort(t *testing.T) {
	tests := []struct {
		addr    string
		port    int
		wantErr bool
	}{
		{"127.0.0.1:8080", 8080, false},
		{"[::1]:9000", 9000, false},
		{"localhost:8081", 8081, false},
		{"0.0.0.0:8080", 0, true},
		{"192.168.68.63:8080", 0, true},
		{"127.0.0.1", 0, true},
		{"127.0.0.1:0", 0, true},
		{"127.0.0.1:http", 0, true},
	}
	for _, tt := range tests {
		port, err := LoopbackPort(tt.addr)
		if (err != nil) != tt.wantErr || port != tt.port {
			t.Errorf("LoopbackPort(%q) = %d, %v", tt.addr, port, err)
		}
	}
}
