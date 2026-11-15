package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"log/slog"
	"net"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"strconv"
	"sync"
	"syscall"
	"time"

	"github.com/devXpro/gpt-home/internal/relay"
	"github.com/devXpro/gpt-home/internal/spike"
)

const (
	defaultListen     = "127.0.0.1:8080"
	selfCheckInterval = 30 * time.Second
)

func cmdRun(args []string, stderr io.Writer) error {
	fl := flag.NewFlagSet("run", flag.ContinueOnError)
	fl.SetOutput(stderr)
	dataDir := fl.String("data-dir", "", "data directory (default: platform data dir, or HOMED_DATA_DIR)")
	listen := fl.String("listen", "", "loopback address to serve on (default: homed.yaml listen, else "+defaultListen+")")
	spikeMode := fl.Bool("spike", false, "serve the phase 0 spike endpoints (the only mode so far)")
	authlessMCP := fl.Bool("spike-authless-mcp", false, "serve /mcp WITHOUT auth (connection test of path A only)")
	caFile := fl.String("relay-ca-file", "", "test harness only: trust only this PEM CA for the relay")
	serverAddr := fl.String("relay-server-addr", "", "test harness only: dial this host:port instead of <domain>:443")
	if err := fl.Parse(args); err != nil {
		return err
	}
	if fl.NArg() > 0 {
		return fmt.Errorf("unexpected arguments: %v", fl.Args())
	}
	if !*spikeMode {
		return errors.New("only the spike exists in this build: run with --spike")
	}

	log := newLogger(stderr)
	dir, err := resolveDataDir(*dataDir)
	if err != nil {
		return err
	}
	cfg, err := loadConfig(dir)
	if err != nil {
		return err
	}
	id, err := instanceID(dir)
	if err != nil {
		return fmt.Errorf("instance id: %w", err)
	}

	addr := *listen
	if addr == "" {
		addr = cfg.Listen
	}
	if addr == "" {
		addr = defaultListen
	}
	port, err := loopbackPort(addr)
	if err != nil {
		return err
	}

	key := os.Getenv("HOMED_SPIKE_KEY")
	if key == "" {
		log.Warn("HOMED_SPIKE_KEY is empty: every /api/v1/tools call will answer 401")
	}
	if *authlessMCP {
		log.Warn("/mcp is served WITHOUT auth (--spike-authless-mcp); use only for the path A connection test")
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	ln, err := (&net.ListenConfig{}).Listen(ctx, "tcp", addr)
	if err != nil {
		return err
	}
	srv := &http.Server{
		Handler: spike.New(spike.Options{
			InstanceID: id, Key: key, AuthlessMCP: *authlessMCP, Version: version, Logger: log,
		}),
		ReadHeaderTimeout: 10 * time.Second,
	}
	var wg sync.WaitGroup
	defer wg.Wait()
	srvErr := make(chan error, 1)
	wg.Go(func() { srvErr <- srv.Serve(ln) })
	log.Info("homed listening", "addr", ln.Addr().String(), "data_dir", dir, "version", version)

	if cfg.Relay.Domain == "" {
		log.Warn("relay not configured; serving locally only (run: homed relay connect <code>)")
	} else {
		token, err := readSecretFile(filepath.Join(dir, relayTokenFile))
		if err != nil {
			_ = srv.Close()
			return fmt.Errorf("relay token: %w", err)
		}
		rc := relay.Config{
			Domain: cfg.Relay.Domain, Token: token, LocalPort: port, CAFile: *caFile, ServerAddr: *serverAddr,
		}
		var client relay.Client
		wg.Go(func() {
			if err := client.Run(ctx, rc, log); err != nil {
				log.Error("relay stopped", "err", err)
			}
		})
		wg.Go(func() { selfCheckLoop(ctx, &client, rc, id, log) })
	}

	select {
	case err := <-srvErr:
		stop()
		return err
	case <-ctx.Done():
	}
	log.Info("shutting down")
	shCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	return srv.Shutdown(shCtx)
}

// selfCheckLoop runs the relay self-check every 30 s while connected and logs only changes of the outcome.
func selfCheckLoop(ctx context.Context, c *relay.Client, rc relay.Config, id string, log *slog.Logger) {
	t := time.NewTicker(selfCheckInterval)
	defer t.Stop()
	last := "unknown"
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
		}
		if !c.Status().Connected {
			continue
		}
		outcome := "ok"
		if err := rc.SelfCheck(ctx, id); err != nil {
			outcome = err.Error()
		}
		if outcome != last {
			if outcome == "ok" {
				log.Info("relay self-check passed", "url", "https://"+rc.Domain+"/healthz")
			} else {
				log.Warn("relay self-check failed", "err", outcome)
			}
			last = outcome
		}
	}
}

// loopbackPort insists on a loopback listen address: the relay forwards to 127.0.0.1:<port>, and nothing else
// should reach the hub directly.
func loopbackPort(addr string) (int, error) {
	host, p, err := net.SplitHostPort(addr)
	if err != nil {
		return 0, fmt.Errorf("listen %q: %w", addr, err)
	}
	if ip := net.ParseIP(host); host != "localhost" && (ip == nil || !ip.IsLoopback()) {
		return 0, fmt.Errorf("listen %q: the hub serves on loopback only (e.g. %s)", addr, defaultListen)
	}
	port, err := strconv.Atoi(p)
	if err != nil || port <= 0 || port > 65535 {
		return 0, fmt.Errorf("listen %q: bad port", addr)
	}
	return port, nil
}

func newLogger(w io.Writer) *slog.Logger {
	level := slog.LevelInfo
	if os.Getenv("HOMED_LOG_LEVEL") == "debug" {
		level = slog.LevelDebug
	}
	return slog.New(slog.NewTextHandler(w, &slog.HandlerOptions{Level: level}))
}
