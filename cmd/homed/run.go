package main

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"slices"
	"sync"
	"syscall"
	"time"

	"github.com/devXpro/gpt-home/internal/audit"
	"github.com/devXpro/gpt-home/internal/config"
	"github.com/devXpro/gpt-home/internal/gateway"
	"github.com/devXpro/gpt-home/internal/keys"
	"github.com/devXpro/gpt-home/internal/plugins"
	"github.com/devXpro/gpt-home/internal/relay"
)

const (
	selfCheckInterval = 30 * time.Second
	shutdownTimeout   = 5 * time.Second
)

// runOptions are the `homed run` settings.
type runOptions struct {
	dataDir    string
	listen     string // overrides homed.yaml
	caFile     string // test harness only
	serverAddr string // test harness only
	ready      func(*daemon)
}

// daemon is a running homed.
type daemon struct {
	dir        string
	log        *slog.Logger
	instanceID string
	host       *plugins.Host
	resolver   *keys.Resolver
	audit      *audit.Log
	relay      *relay.Client // nil when no relay is configured
	relayCfg   relay.Config
	httpAddr   net.Addr

	mu        sync.Mutex
	cfg       *config.Config // as loaded at start or at the last reload
	selfCheck selfCheckResult
}

type selfCheckResult struct {
	at  time.Time
	err string // "" when it passed
}

func cmdRun(args []string, stderr io.Writer) error {
	fl, dataDir := newFlags("run", stderr)
	listen := fl.String("listen", "", "loopback address to serve on (default: homed.yaml listen, else "+config.DefaultListen+")")
	caFile := fl.String("relay-ca-file", "", "test harness only: trust only this PEM CA for the relay")
	serverAddr := fl.String("relay-server-addr", "", "test harness only: dial this host:port instead of <domain>:443")
	pos, err := parseFlags(fl, args)
	if err != nil {
		return err
	}
	if len(pos) > 0 {
		return fmt.Errorf("unexpected arguments: %v", pos)
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	return serve(ctx, runOptions{dataDir: *dataDir, listen: *listen, caFile: *caFile, serverAddr: *serverAddr}, newLogger(stderr))
}

// serve runs homed until ctx is done: the gateway on the listen address, the plugins, the relay client with its
// self-check, and the local admin socket. SIGHUP reloads the people and keys from homed.yaml.
func serve(ctx context.Context, opts runOptions, log *slog.Logger) error {
	dir, err := config.ResolveDataDir(opts.dataDir)
	if err != nil {
		return err
	}
	// The instance lock comes first and is held until serve returns: one homed per data dir.
	unlock, err := config.LockInstance(dir)
	if err != nil {
		return err
	}
	defer unlock()
	if w := config.DataDirWarning(dir); w != "" {
		log.Warn(w)
	}
	cfg, err := config.Load(dir)
	if err != nil {
		return err
	}
	id, err := config.InstanceID(dir)
	if err != nil {
		return fmt.Errorf("instance id: %w", err)
	}
	addr := cfg.ListenAddr()
	if opts.listen != "" {
		addr = opts.listen
	}
	port, err := config.LoopbackPort(addr)
	if err != nil {
		return err
	}
	resolver, err := keys.New(cfg.KeyEntries())
	if err != nil {
		return fmt.Errorf("%s: %w", config.FileName, err)
	}
	d := &daemon{dir: dir, log: log, instanceID: id, resolver: resolver, cfg: cfg}

	var relayToken string
	if cfg.Relay.Domain != "" {
		if relayToken, err = config.ReadRelayToken(dir); err != nil {
			return fmt.Errorf("relay token: %w", err)
		}
	}

	sockLn, err := listenSocket(ctx, filepath.Join(dir, config.SocketFile))
	if err != nil {
		return err
	}
	defer sockLn.Close()
	httpLn, err := (&net.ListenConfig{}).Listen(ctx, "tcp", addr)
	if err != nil {
		return err
	}
	defer httpLn.Close()
	d.httpAddr = httpLn.Addr()

	if d.audit, err = audit.Open(filepath.Join(dir, config.AuditDir), log.With("component", "audit")); err != nil {
		return err
	}
	defer func() {
		if err := d.audit.Close(); err != nil {
			log.Error("audit log", "err", err)
		}
		if n := d.audit.Dropped(); n > 0 {
			log.Warn("audit entries dropped", "count", n)
		}
	}()

	specs := make([]plugins.Spec, 0, len(cfg.Plugins))
	for _, p := range cfg.Plugins {
		specs = append(specs, plugins.Spec{ID: p.ID, Command: p.Command})
	}
	if d.host, err = plugins.New(specs, dir, log.With("component", "plugins")); err != nil {
		return err
	}
	d.host.HubVersion = version
	d.host.Start(ctx)
	defer d.host.Close()

	gw := gateway.New(d.host, resolver, d.audit, gateway.Options{
		HomeName:     cfg.HomeName,
		Instructions: d.host.Instructions,
		InstanceID:   id,
		Version:      version,
		Log:          log.With("component", "gateway"),
	})
	httpSrv := &http.Server{Handler: hostGuard(cfg.Relay.Domain, port, gw), ReadHeaderTimeout: 10 * time.Second}
	sockSrv := &http.Server{Handler: d.socketHandler(), ReadHeaderTimeout: 10 * time.Second}

	var wg sync.WaitGroup
	defer wg.Wait()
	srvErr := make(chan error, 2)
	wg.Go(func() { srvErr <- httpSrv.Serve(httpLn) })
	wg.Go(func() { srvErr <- sockSrv.Serve(sockLn) })
	log.Info("homed listening", "addr", httpLn.Addr().String(), "data_dir", dir, "version", version,
		"plugins", len(specs), "people", len(cfg.People))

	runCtx, cancel := context.WithCancel(ctx)
	defer cancel()
	if cfg.Relay.Domain == "" {
		log.Warn("relay not configured; serving locally only (run: homed relay connect - and paste the code)")
	} else {
		d.relay = &relay.Client{}
		d.relayCfg = relay.Config{
			Domain: cfg.Relay.Domain, Token: relayToken, LocalPort: port, CAFile: opts.caFile, ServerAddr: opts.serverAddr,
		}
		wg.Go(func() {
			if err := d.relay.Run(runCtx, d.relayCfg, log); err != nil {
				log.Error("relay stopped", "err", err)
			}
		})
		wg.Go(func() { d.selfCheckLoop(runCtx) })
	}

	hup := make(chan os.Signal, 1)
	signal.Notify(hup, syscall.SIGHUP)
	defer signal.Stop(hup)
	wg.Go(func() {
		for {
			select {
			case <-runCtx.Done():
				return
			case <-hup:
				if _, err := d.reload(); err != nil {
					log.Error("reload failed", "err", err)
				}
			}
		}
	})

	if opts.ready != nil {
		opts.ready(d)
	}
	var serveErr error
	select {
	case serveErr = <-srvErr:
	case <-ctx.Done():
	}
	log.Info("shutting down")
	cancel()
	shCtx, shCancel := context.WithTimeout(context.Background(), shutdownTimeout)
	defer shCancel()
	for _, srv := range []*http.Server{httpSrv, sockSrv} {
		if err := srv.Shutdown(shCtx); err != nil {
			_ = srv.Close() // a tool call still running (a pairing may take minutes) is cut
		}
	}
	return serveErr
}

// reload re-reads homed.yaml and applies the people and their keys. Other changes need a restart; their section
// names are returned and logged.
func (d *daemon) reload() ([]string, error) {
	cfg, err := config.Load(d.dir)
	if err != nil {
		return nil, err
	}
	if err := d.resolver.Update(cfg.KeyEntries()); err != nil {
		return nil, err
	}
	d.mu.Lock()
	old := d.cfg
	d.cfg = cfg
	d.mu.Unlock()

	var restart []string
	if cfg.HomeName != old.HomeName {
		restart = append(restart, "home_name")
	}
	if cfg.ListenAddr() != old.ListenAddr() {
		restart = append(restart, "listen")
	}
	if cfg.Relay != old.Relay {
		restart = append(restart, "relay")
	}
	if !slices.EqualFunc(cfg.Plugins, old.Plugins, func(a, b config.Plugin) bool {
		return a.ID == b.ID && slices.Equal(a.Command, b.Command)
	}) {
		restart = append(restart, "plugins")
	}
	d.log.Info("reloaded people and keys", "people", len(cfg.People), "keys", len(cfg.KeyEntries()))
	if len(restart) > 0 {
		d.log.Warn("homed.yaml changes that apply only after a restart", "sections", restart)
	}
	return restart, nil
}

// selfCheckLoop runs the relay self-check every 30 s while connected and logs only changes of the outcome.
func (d *daemon) selfCheckLoop(ctx context.Context) {
	t := time.NewTicker(selfCheckInterval)
	defer t.Stop()
	last := "unknown"
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
		}
		if !d.relay.Status().Connected {
			continue
		}
		outcome := ""
		if err := d.relayCfg.SelfCheck(ctx, d.instanceID); err != nil {
			outcome = err.Error()
		}
		d.mu.Lock()
		d.selfCheck = selfCheckResult{at: time.Now(), err: outcome}
		d.mu.Unlock()
		if outcome != last {
			if outcome == "" {
				d.log.Info("relay self-check passed", "url", "https://"+d.relayCfg.Domain+"/healthz")
			} else {
				d.log.Warn("relay self-check failed", "err", outcome)
			}
			last = outcome
		}
	}
}

// listenSocket listens on the unix socket path (0600). The caller holds the instance lock, so a socket file left
// there belongs to a homed that is gone and is replaced; one that still answers is refused, never removed.
func listenSocket(ctx context.Context, path string) (net.Listener, error) {
	// sun_path holds 104 bytes on macOS (108 on Linux), the terminating NUL included.
	if len(path) > 103 {
		return nil, fmt.Errorf("socket path %s is too long for a unix socket; use a shorter --data-dir", path)
	}
	var dialer net.Dialer
	if c, err := dialer.DialContext(ctx, "unix", path); err == nil {
		_ = c.Close()
		return nil, fmt.Errorf("something already answers on the socket %s", path)
	}
	if err := os.Remove(path); err != nil && !errors.Is(err, os.ErrNotExist) {
		return nil, err
	}
	ln, err := (&net.ListenConfig{}).Listen(ctx, "unix", path)
	if err != nil {
		return nil, err
	}
	if err := os.Chmod(path, 0o600); err != nil {
		_ = ln.Close()
		return nil, err
	}
	return ln, nil
}

func newLogger(w io.Writer) *slog.Logger {
	level := slog.LevelInfo
	if os.Getenv("HOMED_LOG_LEVEL") == "debug" {
		level = slog.LevelDebug
	}
	return slog.New(slog.NewTextHandler(w, &slog.HandlerOptions{Level: level}))
}
