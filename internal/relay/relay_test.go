package relay

import (
	"context"
	"net"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	v1 "github.com/fatedier/frp/pkg/config/v1"
)

func TestFrpConfig(t *testing.T) {
	pki := newTestPKI(t)
	tests := []struct {
		name         string
		cfg          Config
		wantAddr     string
		wantPort     int
		wantSystemCA bool
		wantCAFile   string
	}{
		{
			name:         "production: domain on 443, system roots",
			cfg:          Config{Domain: "homie.jobbyjobs.work", Token: testToken, LocalPort: 8080},
			wantAddr:     "homie.jobbyjobs.work",
			wantPort:     443,
			wantSystemCA: true,
		},
		{
			name:       "harness: CA file and dial override",
			cfg:        Config{Domain: "homie.test", Token: testToken, LocalPort: 18080, CAFile: pki.CAFile, ServerAddr: "127.0.0.1:18443"},
			wantAddr:   "127.0.0.1",
			wantPort:   18443,
			wantCAFile: pki.CAFile,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			common, p, err := tt.cfg.frpConfig()
			if err != nil {
				t.Fatal(err)
			}
			tr := common.Transport
			checks := []struct {
				what      string
				got, want any
			}{
				{"serverAddr", common.ServerAddr, tt.wantAddr},
				{"serverPort", common.ServerPort, tt.wantPort},
				{"protocol", tr.Protocol, "wss"},
				{"tls.serverName", tr.TLS.ServerName, tt.cfg.Domain},
				{"tls.useSystemCa", tr.TLS.UseSystemCa, tt.wantSystemCA},
				{"tls.trustedCaFile", tr.TLS.TrustedCaFile, tt.wantCAFile},
				{"loginFailExit", *common.LoginFailExit, false},
				{"auth.method", common.Auth.Method, v1.AuthMethodToken},
				{"auth.token", common.Auth.Token, testToken},
				{"admin webServer off", common.WebServer.Port, 0},
				{"proxy name", p.Name, ProxyName},
				{"proxy type", p.Type, "http"},
				{"proxy localIP", p.LocalIP, "127.0.0.1"},
				{"proxy localPort", p.LocalPort, tt.cfg.LocalPort},
				{"proxy customDomains", strings.Join(p.CustomDomains, ","), tt.cfg.Domain},
			}
			for _, c := range checks {
				if c.got != c.want {
					t.Errorf("%s = %v, want %v", c.what, c.got, c.want)
				}
			}
		})
	}
}

// TestCheckVerifiesRejectsFrpDefault pins the trap: frp skips verification when no trust source is set.
func TestCheckVerifiesRejectsFrpDefault(t *testing.T) {
	common := &v1.ClientCommonConfig{}
	common.Transport.Protocol = "wss"
	common.Transport.TLS.ServerName = "homie.test"
	if err := common.Complete(); err != nil {
		t.Fatal(err)
	}
	if err := checkVerifies(common); err == nil {
		t.Fatal("checkVerifies accepted a config without system roots or a CA file")
	}
}

func TestConfigValidate(t *testing.T) {
	pki := newTestPKI(t)
	notPEM := filepath.Join(t.TempDir(), "x.pem")
	if err := os.WriteFile(notPEM, []byte("hello"), 0o600); err != nil {
		t.Fatal(err)
	}
	ok := Config{Domain: "homie.test", Token: testToken, LocalPort: 8080}
	tests := []struct {
		name    string
		mut     func(*Config)
		wantErr string
	}{
		{"valid", func(*Config) {}, ""},
		{"valid with CA", func(c *Config) { c.CAFile = pki.CAFile }, ""},
		{"no domain", func(c *Config) { c.Domain = "" }, "invalid relay domain"},
		{"no token", func(c *Config) { c.Token = "" }, "token is empty"},
		{"port 0", func(c *Config) { c.LocalPort = 0 }, "invalid local port"},
		{"port too big", func(c *Config) { c.LocalPort = 70000 }, "invalid local port"},
		{"bad server addr", func(c *Config) { c.ServerAddr = "nohost" }, "invalid relay server address"},
		{"missing CA file", func(c *Config) { c.CAFile = "/nonexistent/ca.pem" }, "read relay CA file"},
		{"CA file not PEM", func(c *Config) { c.CAFile = notPEM }, "no PEM certificates"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			c := ok
			tt.mut(&c)
			err := c.validate()
			if tt.wantErr == "" {
				if err != nil {
					t.Fatal(err)
				}
				return
			}
			if err == nil || !strings.Contains(err.Error(), tt.wantErr) {
				t.Fatalf("err = %v, want %q", err, tt.wantErr)
			}
		})
	}
}

func TestParseFrpLine(t *testing.T) {
	tests := []struct {
		line, caller, msg string
	}{
		{"2026-11-15 13:52:00.123 [W] [client/service.go:358] connect to server error: EOF\n", "client/service.go:358", "connect to server error: EOF"},
		{"2026-11-15 13:52:00.123 [I] [client/service.go:367] [abc123] login to server success\n", "client/service.go:367", "[abc123] login to server success"},
		{"2026-11-15 13:52:00.123 [I] plain\n", "", "plain"},
	}
	for _, tt := range tests {
		caller, msg := parseFrpLine(tt.line)
		if caller != tt.caller || msg != tt.msg {
			t.Errorf("parseFrpLine(%q) = (%q, %q), want (%q, %q)", tt.line, caller, msg, tt.caller, tt.msg)
		}
	}
}

func TestRunRejectsBadConfig(t *testing.T) {
	var c Client
	err := c.Run(context.Background(), Config{Domain: "homie.test"}, nil)
	if err == nil {
		t.Fatal("Run accepted a config without token and port")
	}
	// `homed relay status` reads the status, so the reason the relay never connected must be there.
	if st := c.Status(); st.LastError != err.Error() || st.Connected {
		t.Fatalf("status after a config error = %+v, want LastError %q", st, err)
	}
	missingCA := Config{Domain: "homie.test", Token: testToken, LocalPort: 8080, CAFile: filepath.Join(t.TempDir(), "nope.pem")}
	if err := c.Run(context.Background(), missingCA, nil); err == nil || c.Status().LastError != err.Error() ||
		strings.Contains(c.Status().LastError, testToken) {
		t.Fatalf("missing CA file: err %v, status %+v", err, c.Status())
	}
}

// TestRunRetriesAndStops runs the real frp client against a closed port: it must keep retrying, report the error,
// stay disconnected and return nil once the context is cancelled.
func TestRunRetriesAndStops(t *testing.T) {
	pki := newTestPKI(t)
	ln, err := (&net.ListenConfig{}).Listen(context.Background(), "tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	addr := ln.Addr().String()
	_ = ln.Close() // now nothing listens there

	ctx, cancel := context.WithCancel(context.Background())
	var c Client
	done := make(chan error, 1)
	go func() {
		done <- c.Run(ctx, Config{Domain: "homie.test", Token: testToken, LocalPort: 8080, CAFile: pki.CAFile, ServerAddr: addr}, nil)
	}()
	waitFor(t, 10*time.Second, "a connect error", func() bool { return c.Status().LastError != "" })
	st := c.Status()
	t.Logf("LastError: %s", st.LastError)
	if st.Connected {
		t.Fatal("connected to a closed port")
	}
	if !strings.Contains(st.LastError, "connect") || strings.Contains(st.LastError, testToken) {
		t.Fatalf("LastError = %q", st.LastError)
	}
	cancel()
	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("Run after cancel = %v, want nil", err)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("Run did not return after cancel")
	}
	if err := c.Run(context.Background(), Config{}, nil); err == nil {
		t.Fatal("second Run with a bad config succeeded")
	}
}
