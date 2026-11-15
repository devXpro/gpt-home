package main

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"

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

func TestVersion(t *testing.T) {
	var out bytes.Buffer
	if err := run([]string{"version"}, nil, &out, &out); err != nil {
		t.Fatal(err)
	}
	if out.String() != "homed dev\n" {
		t.Fatalf("version output = %q", out.String())
	}
}

func TestRelayConnect(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "data")
	existing := "# my home\nhome_name: \"Мой шалаш\"\nlisten: 127.0.0.1:8080\nplugins:\n  - id: tv\n    command: [\"/bin/gpt-tv\", \"serve\"]\n"
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, configFile), []byte(existing), 0o600); err != nil {
		t.Fatal(err)
	}

	var out, errOut bytes.Buffer
	// The code goes first and the flag after it, as people type it.
	if err := run([]string{"relay", "connect", testCode(t, "homie.test"), "--data-dir", dir}, nil, &out, &errOut); err != nil {
		t.Fatalf("%v; stderr %s", err, errOut.String())
	}
	if strings.Contains(out.String()+errOut.String(), testToken) {
		t.Fatal("relay connect printed the token")
	}

	st, err := os.Stat(filepath.Join(dir, relayTokenFile))
	if err != nil {
		t.Fatal(err)
	}
	if st.Mode().Perm() != 0o600 {
		t.Fatalf("relay-token mode = %o, want 600", st.Mode().Perm())
	}
	tok, err := readSecretFile(filepath.Join(dir, relayTokenFile))
	if err != nil || tok != testToken {
		t.Fatalf("token = %q, %v", tok, err)
	}

	cfg, err := loadConfig(dir)
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Relay.Domain != "homie.test" || cfg.HomeName != "Мой шалаш" || cfg.Listen != "127.0.0.1:8080" {
		t.Fatalf("config = %+v", cfg)
	}
	raw, err := os.ReadFile(filepath.Join(dir, configFile)) //nolint:gosec // test temp dir
	if err != nil {
		t.Fatal(err)
	}
	for _, keep := range []string{"# my home", "plugins:", "gpt-tv"} {
		if !strings.Contains(string(raw), keep) {
			t.Errorf("homed.yaml lost %q:\n%s", keep, raw)
		}
	}

	// A second connect replaces the domain in place.
	if err := run([]string{"relay", "connect", "--data-dir", dir, "-"}, strings.NewReader(testCode(t, "other.test")+"\n"), &out, &errOut); err != nil {
		t.Fatal(err)
	}
	cfg, err = loadConfig(dir)
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Relay.Domain != "other.test" {
		t.Fatalf("domain = %q after reconnect", cfg.Relay.Domain)
	}
	raw, err = os.ReadFile(filepath.Join(dir, configFile)) //nolint:gosec // test temp dir
	if err != nil {
		t.Fatal(err)
	}
	if n := strings.Count(string(raw), "relay:"); n != 1 {
		t.Fatalf("homed.yaml has %d relay keys:\n%s", n, raw)
	}
}

func TestRelayConnectNewDataDir(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "fresh")
	var out bytes.Buffer
	if err := run([]string{"relay", "connect", "--data-dir", dir, testCode(t, "homie.test")}, nil, &out, &out); err != nil {
		t.Fatal(err)
	}
	st, err := os.Stat(dir)
	if err != nil {
		t.Fatal(err)
	}
	if st.Mode().Perm() != 0o700 {
		t.Fatalf("data dir mode = %o, want 700", st.Mode().Perm())
	}
	cfg, err := loadConfig(dir)
	if err != nil || cfg.Relay.Domain != "homie.test" {
		t.Fatalf("config = %+v, %v", cfg, err)
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
		{"unknown relay command", []string{"relay", "status"}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var out bytes.Buffer
			if err := run(tt.args, strings.NewReader(""), &out, &out); err == nil {
				t.Fatal("want an error")
			}
		})
	}
	if _, err := os.Stat(filepath.Join(dir, relayTokenFile)); !os.IsNotExist(err) {
		t.Fatal("a failed connect wrote the token")
	}
}

func TestReadSecretFileRefusesLoosePerms(t *testing.T) {
	p := filepath.Join(t.TempDir(), "relay-token")
	if err := os.WriteFile(p, []byte(testToken), 0o644); err != nil { //nolint:gosec // deliberately loose for the test
		t.Fatal(err)
	}
	if _, err := readSecretFile(p); err == nil || !strings.Contains(err.Error(), "chmod 600") {
		t.Fatalf("err = %v", err)
	}
}

func TestInstanceIDIsStable(t *testing.T) {
	dir := t.TempDir()
	a, err := instanceID(dir)
	if err != nil {
		t.Fatal(err)
	}
	b, err := instanceID(dir)
	if err != nil {
		t.Fatal(err)
	}
	if a != b || len(a) != 32 {
		t.Fatalf("ids %q and %q", a, b)
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
		port, err := loopbackPort(tt.addr)
		if (err != nil) != tt.wantErr || port != tt.port {
			t.Errorf("loopbackPort(%q) = %d, %v", tt.addr, port, err)
		}
	}
}

func TestRunNeedsSpike(t *testing.T) {
	var out bytes.Buffer
	err := run([]string{"run", "--data-dir", t.TempDir()}, nil, &out, &out)
	if err == nil || !strings.Contains(err.Error(), "--spike") {
		t.Fatalf("err = %v", err)
	}
}
