// Package relay runs the home side of the frp relay inside homed.
//
// The frp client (github.com/fatedier/frp/client) is embedded in-process. It dials the relay domain on 443 over wss,
// verifies the server certificate (system roots, or only the roots in Config.CAFile), logs in with the token and
// registers one HTTP proxy, "homed", that forwards every request for the domain to the local hub. It retries forever.
// See docs/research/frp-embedding.md for the library details this package relies on.
//
// frp keeps a process-wide logger, so run at most one Client per process at a time.
package relay

import (
	"context"
	"crypto/x509"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"os"
	"strconv"
	"sync"
	"sync/atomic"
	"time"

	"github.com/fatedier/frp/client"
	"github.com/fatedier/frp/client/proxy"
	"github.com/fatedier/frp/pkg/config/source"
	v1 "github.com/fatedier/frp/pkg/config/v1"
	"github.com/fatedier/frp/pkg/config/v1/validation"
	"github.com/fatedier/frp/pkg/policy/security"
	"github.com/fatedier/frp/pkg/transport"
)

// ProxyName is the name of the single frp proxy homed registers.
const ProxyName = "homed"

// statusPoll is how often the client's proxy state is sampled; frp has no state-change callback.
var statusPoll = 500 * time.Millisecond

// Config says which relay to use and where the local hub listens.
type Config struct {
	Domain    string // relay domain; TLS name, Host header and the proxy's custom domain
	Token     string // frps token
	LocalPort int    // the hub's port on 127.0.0.1
	// CAFile, when set, is a PEM file whose roots are the ONLY ones trusted for the relay (test harnesses).
	// When empty, the system roots are used. Verification is never off.
	CAFile string
	// ServerAddr, when set, is the "host:port" dialled instead of Domain:443 (test harnesses). TLS still verifies
	// Domain, and Domain is still sent as the Host.
	ServerAddr string
}

// Status is the relay connection state.
type Status struct {
	Connected bool      // logged in to frps and the "homed" proxy is running
	Since     time.Time // when Connected last changed; zero before the first change
	LastError string    // the latest client-side failure, kept after a later success; never contains the token
}

func (c Config) validate() error {
	if err := validateDomain(c.Domain); err != nil {
		return err
	}
	if c.Token == "" {
		return errors.New("relay token is empty")
	}
	if c.LocalPort <= 0 || c.LocalPort > 65535 {
		return fmt.Errorf("invalid local port %d", c.LocalPort)
	}
	if c.ServerAddr != "" {
		if _, _, err := splitHostPort(c.ServerAddr); err != nil {
			return fmt.Errorf("invalid relay server address %q: %w", c.ServerAddr, err)
		}
	}
	if c.CAFile != "" {
		pem, err := os.ReadFile(c.CAFile)
		if err != nil {
			return fmt.Errorf("read relay CA file: %w", err)
		}
		if !x509.NewCertPool().AppendCertsFromPEM(pem) {
			return fmt.Errorf("relay CA file %s holds no PEM certificates", c.CAFile)
		}
	}
	return nil
}

func splitHostPort(addr string) (string, int, error) {
	host, p, err := net.SplitHostPort(addr)
	if err != nil {
		return "", 0, err
	}
	port, err := strconv.Atoi(p)
	if err != nil || port <= 0 || port > 65535 {
		return "", 0, fmt.Errorf("bad port %q", p)
	}
	return host, port, nil
}

// frpConfig builds the frp client settings of PLAN §5.5.
func (c Config) frpConfig() (*v1.ClientCommonConfig, *v1.HTTPProxyConfig, error) {
	host, port := c.Domain, 443
	if c.ServerAddr != "" {
		var err error
		if host, port, err = splitHostPort(c.ServerAddr); err != nil {
			return nil, nil, err
		}
	}
	loginFailExit := false
	common := &v1.ClientCommonConfig{}
	common.ServerAddr = host
	common.ServerPort = port
	common.LoginFailExit = &loginFailExit
	common.Auth.Method = v1.AuthMethodToken
	common.Auth.Token = c.Token
	common.Transport.Protocol = "wss"
	common.Transport.TLS.ServerName = c.Domain
	if c.CAFile != "" {
		common.Transport.TLS.TrustedCaFile = c.CAFile
	} else {
		common.Transport.TLS.UseSystemCa = true
	}
	if err := common.Complete(); err != nil {
		return nil, nil, err
	}

	p := &v1.HTTPProxyConfig{}
	p.Name = ProxyName
	p.Type = string(v1.ProxyTypeHTTP)
	p.LocalIP = "127.0.0.1"
	p.LocalPort = c.LocalPort
	p.CustomDomains = []string{c.Domain}
	p.Complete()

	if _, err := validation.ValidateAllClientConfig(common, []v1.ProxyConfigurer{p}, nil, security.NewUnsafeFeatures(nil)); err != nil {
		return nil, nil, fmt.Errorf("frp config: %w", err)
	}
	if err := checkVerifies(common); err != nil {
		return nil, nil, err
	}
	return common, p, nil
}

// checkVerifies builds the outer TLS config the way frp's connector does (client/connector.go, realConnect) and
// refuses any setup that would skip server certificate verification: frp's default is not to verify.
func checkVerifies(common *v1.ClientCommonConfig) error {
	tc := common.Transport.TLS
	tlsCfg, err := transport.NewClientTLSConfigWithSystemCA(tc.CertFile, tc.KeyFile, tc.TrustedCaFile, tc.ServerName, tc.UseSystemCa)
	if err != nil {
		return fmt.Errorf("relay TLS config: %w", err)
	}
	if tlsCfg.InsecureSkipVerify {
		return errors.New("relay TLS config would skip certificate verification")
	}
	if tlsCfg.ServerName == "" {
		return errors.New("relay TLS config has no server name")
	}
	return nil
}

// Client is the embedded frp client. The zero value is ready; Run it once.
type Client struct {
	mu      sync.Mutex
	status  Status
	running bool
	// Connector generations: frp opens one connector per login session and closes it when the session ends, so
	// "the newest opened connector is not closed yet" means the session is alive. Polling the proxy state alone
	// misses fast reconnects.
	openedGen, closedGen uint64
}

// watchedConnector wraps frp's default connector to report session start and end.
type watchedConnector struct {
	client.Connector
	c   *Client
	log *slog.Logger
	gen atomic.Uint64
}

func (w *watchedConnector) Open() error {
	if err := w.Connector.Open(); err != nil {
		return err
	}
	w.c.mu.Lock()
	w.c.openedGen++
	w.gen.Store(w.c.openedGen)
	w.c.mu.Unlock()
	return nil
}

func (w *watchedConnector) Close() error {
	err := w.Connector.Close()
	if gen := w.gen.Load(); gen != 0 {
		w.c.mu.Lock()
		w.c.closedGen = max(w.c.closedGen, gen)
		live := w.c.closedGen < w.c.openedGen
		w.c.mu.Unlock()
		if !live {
			w.c.setConnected(false, w.log)
		}
	}
	return err
}

// Status returns the current connection state. It is safe to call at any time, from any goroutine.
func (c *Client) Status() Status {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.status
}

func (c *Client) setConnected(connected bool, log *slog.Logger) {
	c.mu.Lock()
	// Checked under the same lock as watchedConnector.Close, so a stale sample cannot undo a pushed disconnect.
	connected = connected && c.closedGen < c.openedGen
	changed := c.status.Connected != connected
	if changed {
		c.status.Connected = connected
		c.status.Since = time.Now()
	}
	c.mu.Unlock()
	if !changed {
		return
	}
	if connected {
		log.Info("relay connected")
	} else {
		log.Warn("relay disconnected")
	}
}

func (c *Client) setError(msg string) {
	c.mu.Lock()
	c.status.LastError = msg
	c.mu.Unlock()
}

// Run connects to the relay and keeps the connection up, reconnecting with backoff, until ctx is done. It returns nil
// after ctx is done and an error only when the configuration is invalid or frp cannot start.
func (c *Client) Run(ctx context.Context, cfg Config, log *slog.Logger) error {
	if log == nil {
		log = slog.New(slog.DiscardHandler)
	}
	log = log.With("relay", cfg.Domain)
	if err := cfg.validate(); err != nil {
		return err
	}
	common, pxy, err := cfg.frpConfig()
	if err != nil {
		return err
	}

	c.mu.Lock()
	if c.running {
		c.mu.Unlock()
		return errors.New("relay client is already running")
	}
	c.running = true
	c.mu.Unlock()
	defer func() {
		c.mu.Lock()
		c.running = false
		c.mu.Unlock()
		c.setConnected(false, log)
	}()

	src := source.NewConfigSource()
	if err := src.ReplaceAll([]v1.ProxyConfigurer{pxy}, nil); err != nil {
		return fmt.Errorf("frp config source: %w", err)
	}
	svc, err := client.NewService(client.ServiceOptions{
		Common:                 common,
		ConfigSourceAggregator: source.NewAggregator(src),
		UnsafeFeatures:         security.NewUnsafeFeatures(nil),
		ConnectorCreator: func(ctx context.Context, cc *v1.ClientCommonConfig) client.Connector {
			return &watchedConnector{Connector: client.NewConnector(ctx, cc), c: c, log: log}
		},
	})
	if err != nil {
		return fmt.Errorf("start frp client: %w", err)
	}

	sink := &logSink{log: log, onClientError: c.setError}
	activeSink.Store(sink)
	defer activeSink.CompareAndSwap(sink, nil)

	runCtx, cancel := context.WithCancel(ctx)
	defer cancel()
	var wg sync.WaitGroup
	wg.Go(func() { c.watch(runCtx, svc.StatusExporter(), log) })
	defer wg.Wait()

	log.Info("relay client starting", "server", net.JoinHostPort(common.ServerAddr, strconv.Itoa(common.ServerPort)))
	runErr := svc.Run(runCtx)
	if ctx.Err() != nil {
		return nil // frp reports "login failed" when cancelled before the first login; that is a normal stop
	}
	if runErr != nil {
		return fmt.Errorf("frp client stopped: %w", runErr)
	}
	return errors.New("frp client stopped unexpectedly")
}

// watch samples the proxy state, since frp exposes no "connected" event. The proxy is "running" only after a
// successful login and NewProxy exchange; frp drops all proxies when the control connection closes. Disconnects are
// also pushed at once by watchedConnector.Close.
func (c *Client) watch(ctx context.Context, exp client.StatusExporter, log *slog.Logger) {
	t := time.NewTicker(statusPoll)
	defer t.Stop()
	lastProxyErr := ""
	for {
		st, ok := exp.GetProxyStatus(ProxyName)
		connected := ok && st.Phase == proxy.ProxyPhaseRunning
		c.setConnected(connected, log)
		if ok && st.Err != "" && st.Err != lastProxyErr {
			c.setError("proxy: " + st.Err)
		}
		if ok {
			lastProxyErr = st.Err
		}
		select {
		case <-ctx.Done():
			return
		case <-t.C:
		}
	}
}
