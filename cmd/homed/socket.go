package main

// The local admin socket, <data_dir>/homed.sock (ADMIN-API §6): the admin API over HTTP/1.1 on a unix socket, mode
// 0600, for the homed CLI. The principal is the owner, Via "cli"; no cookie, no CSRF header, no step-up, because
// whoever can open the socket already owns the data dir. The listener also checks that the peer runs as homed's own
// user (getpeereid / SO_PEERCRED), so a mode slip alone does not open it to others.

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"os"
)

// listenSocket listens on the unix socket path (0600). The caller holds the instance lock, so a socket file left
// there belongs to a homed that is gone and is replaced; one that still answers is refused, never removed.
func listenSocket(ctx context.Context, path string, log *slog.Logger) (net.Listener, error) {
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
	return &ownerListener{Listener: ln, uid: os.Getuid(), log: log}, nil
}

// ownerListener accepts only connections from processes of the given user.
type ownerListener struct {
	net.Listener
	uid int
	log *slog.Logger
}

func (l *ownerListener) Accept() (net.Conn, error) {
	for {
		c, err := l.Listener.Accept()
		if err != nil {
			return nil, err
		}
		uid, err := peerUID(c)
		if err == nil && uid == l.uid {
			return c, nil
		}
		l.log.Warn("refused a connection on the admin socket from another user", "uid", uid, "err", err)
		_ = c.Close()
	}
}

// socketHandler serves the admin API's socket routes (/api/admin/*, the /local/* ones included) and a JSON 404 for
// anything else.
func socketHandler(api http.Handler) http.Handler {
	mux := http.NewServeMux()
	mux.Handle("/api/admin/", api)
	mux.HandleFunc("/", func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json; charset=utf-8")
		w.WriteHeader(http.StatusNotFound)
		_, _ = w.Write([]byte(`{"error":{"code":"not_found","message":"the socket serves /api/admin/ only"}}` + "\n"))
	})
	return mux
}
