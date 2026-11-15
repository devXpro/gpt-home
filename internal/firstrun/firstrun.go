// Package firstrun serves the first-run page on the home network (docs/ADMIN-API.md §5, DESIGN.md §8.3): the owner
// enters a PIN printed in homed's terminal, pastes the server's connect code if homed has no relay yet, waits for the
// relay, and gets the one-time setup link https://<domain>/admin/setup#t=<token> to open on the phone or the Mac.
//
// The listener (setup_listen, default 0.0.0.0:8090) speaks plain HTTP on the LAN, so:
//   - the PIN gate allows 5 wrong PINs per 10 minutes; the fifth prints a new PIN, and the page stays locked until
//     the window passes, which caps guessing at 720 tries a day against a million PINs;
//   - the gh_firstrun cookie (HttpOnly, SameSite=Strict, 30 minutes) is random and kept only as a hash, in memory;
//   - only Host names a LAN can have pass (IP literals, localhost, single-label and .local-style names), which stops
//     DNS rebinding; mutating requests need X-GPT-Home: 1 and a same-origin Origin and Sec-Fetch-Site;
//   - once an owner with a passkey exists every path answers 410 setup_done.
//
// T2.6 wires it: when Needed reports true, New(Options{…}) and ListenAndServe(ctx, setupListen). Relay is homed's
// relay client (the same adapter that serves internal/admin, plus SelfCheckResult); UI is the SPA bundle that has
// the /firstrun route (internal/web).
package firstrun

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"math/big"
	"net"
	"net/http"
	"net/url"
	"os"
	"slices"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/devXpro/gpt-home/internal/auth"
	"github.com/devXpro/gpt-home/internal/auth/wire"
	"github.com/devXpro/gpt-home/internal/relay"
	"github.com/devXpro/gpt-home/internal/store"
)

// DefaultListen is setup_listen when homed.yaml sets none.
const DefaultListen = "0.0.0.0:8090"

// CookieName is the first-run cookie. It is not __Host- and not Secure: the page is plain http on the LAN.
const CookieName = "gh_firstrun"

const (
	cookieTTL    = 30 * time.Minute
	maxCookies   = 64
	maxWrongPINs = 5
	pinWindow    = 10 * time.Minute
)

// Relay is homed's relay as the first-run page needs it. The adapter T2.6 gives internal/admin (admin.Relay) has
// Domain, Status and Connect already; SelfCheckResult is the one method more.
type Relay interface {
	// Domain is the configured relay domain; empty when homed has no relay yet.
	Domain() string
	// Status reports the relay client.
	Status() relay.Status
	// SelfCheckResult returns the latest self-check: when it ran (zero: never) and why it failed ("" if it passed).
	SelfCheckResult() (checkedAt time.Time, failure string)
	// Connect stores the code the way `homed relay connect` does and (re)connects. It should run a self-check as
	// soon as the relay connects, so the page does not wait for the 30-second check loop.
	Connect(ctx context.Context, code relay.Code) error
}

// Options configures the first-run server. Store and Relay are required.
type Options struct {
	Store store.Store
	Relay Relay
	// Audit receives the setup.link and relay.change records; nil appends them to Store.Audit directly.
	Audit auth.AuditRecorder
	// UI serves every path outside /api/: the SPA bundle with its /firstrun route and assets. Nil answers 404.
	UI http.Handler
	// Terminal is where the PIN is printed; nil means os.Stderr. It must be the terminal, never a log file.
	Terminal io.Writer
	// Log receives the page's events; the PIN and tokens never reach it.
	Log *slog.Logger
	// Now is the clock; nil means time.Now.
	Now func() time.Time
}

// Server is the first-run page. Create it with New.
type Server struct {
	st    store.Store
	relay Relay
	audit auth.AuditRecorder
	ui    http.Handler
	term  io.Writer
	log   *slog.Logger
	now   func() time.Time

	pollEvery, pingEvery time.Duration // the event stream; tests shorten them

	mu      sync.Mutex
	pin     string
	wrong   []time.Time          // wrong PINs inside the window
	cookies map[string]time.Time // sha256 of the cookie → expiry
}

// New returns the server with a fresh PIN; Serve prints it.
func New(o Options) (*Server, error) {
	if o.Store == nil || o.Relay == nil {
		return nil, errors.New("firstrun: Store and Relay are required")
	}
	if o.Terminal == nil {
		o.Terminal = os.Stderr
	}
	if o.Log == nil {
		o.Log = slog.New(slog.DiscardHandler)
	}
	if o.Now == nil {
		o.Now = time.Now
	}
	return &Server{
		st: o.Store, relay: o.Relay, audit: o.Audit, ui: o.UI, term: o.Terminal, log: o.Log, now: o.Now,
		pollEvery: time.Second, pingEvery: 20 * time.Second,
		pin: newPIN(), cookies: map[string]time.Time{},
	}, nil
}

// Needed reports whether the first-run page is still needed: no owner has a passkey yet.
func Needed(ctx context.Context, st store.Store) (bool, error) {
	ready, err := auth.OwnerReady(ctx, st)
	return !ready, err
}

// newPIN returns six random digits.
func newPIN() string {
	n, err := rand.Int(rand.Reader, big.NewInt(1_000_000))
	if err != nil {
		panic(err) // crypto/rand does not fail since Go 1.24
	}
	return fmt.Sprintf("%06d", n.Int64())
}

// ListenAndServe listens on addr (DefaultListen when empty) and serves until ctx is done.
func (s *Server) ListenAndServe(ctx context.Context, addr string) error {
	if addr == "" {
		addr = DefaultListen
	}
	var lc net.ListenConfig
	ln, err := lc.Listen(ctx, "tcp", addr)
	if err != nil {
		return fmt.Errorf("firstrun: listen on %s: %w", addr, err)
	}
	return s.Serve(ctx, ln)
}

// Serve prints the page's address and the PIN to the terminal, then serves ln until ctx is done. It closes ln.
func (s *Server) Serve(ctx context.Context, ln net.Listener) error {
	s.mu.Lock()
	pin := s.pin
	s.mu.Unlock()
	_, _ = fmt.Fprintf(s.term, "\nhomed first run: open %s on your home network and enter the PIN %s\n\n",
		strings.Join(pageURLs(ln.Addr()), " or "), pin)
	srv := &http.Server{
		Handler:           s.Handler(),
		ReadHeaderTimeout: 10 * time.Second,
		IdleTimeout:       2 * time.Minute,
		MaxHeaderBytes:    64 << 10,
		BaseContext:       func(net.Listener) context.Context { return ctx },
	}
	errc := make(chan error, 1)
	go func() { errc <- srv.Serve(ln) }()
	select {
	case err := <-errc:
		return fmt.Errorf("firstrun: %w", err)
	case <-ctx.Done():
	}
	shutdown, cancel := context.WithTimeout(context.WithoutCancel(ctx), 5*time.Second)
	defer cancel()
	if err := srv.Shutdown(shutdown); err != nil {
		_ = srv.Close()
	}
	<-errc
	return nil
}

// pageURLs lists the page's addresses for the terminal: every LAN IPv4 address and localhost when the listener is
// on all interfaces.
func pageURLs(addr net.Addr) []string {
	tcp, ok := addr.(*net.TCPAddr)
	if !ok {
		return []string{"http://" + addr.String() + "/firstrun"}
	}
	var hosts []string
	if tcp.IP.IsUnspecified() {
		if addrs, err := net.InterfaceAddrs(); err == nil {
			for _, a := range addrs {
				if n, ok := a.(*net.IPNet); ok && n.IP.To4() != nil && !n.IP.IsLoopback() && !n.IP.IsLinkLocalUnicast() {
					hosts = append(hosts, n.IP.String())
				}
			}
		}
		hosts = append(hosts, "localhost")
	} else {
		hosts = append(hosts, tcp.IP.String())
	}
	urls := make([]string, 0, len(hosts))
	for _, h := range hosts {
		urls = append(urls, "http://"+net.JoinHostPort(h, strconv.Itoa(tcp.Port))+"/firstrun")
	}
	return urls
}

// Handler serves the page: /api/firstrun/* and, for every other path, the UI.
func (s *Server) Handler() http.Handler {
	mux := http.NewServeMux()
	mux.Handle("/api/firstrun/state", s.endpoint(http.MethodGet, false, s.state))
	mux.Handle("/api/firstrun/pin", s.endpoint(http.MethodPost, false, s.enterPIN))
	mux.Handle("/api/firstrun/relay", s.endpoint(http.MethodPost, true, s.connect))
	mux.Handle("/api/firstrun/events", s.endpoint(http.MethodGet, true, s.events))
	mux.Handle("/api/firstrun/link", s.endpoint(http.MethodPost, true, s.link))
	mux.HandleFunc("/api/", func(w http.ResponseWriter, _ *http.Request) {
		wire.Headers(w)
		wire.WriteError(w, wire.Errorf(http.StatusNotFound, "not_found", "endpoint not found"))
	})
	mux.HandleFunc("GET /{$}", func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, "/firstrun", http.StatusFound)
	})
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		if s.ui == nil {
			http.NotFound(w, r)
			return
		}
		s.ui.ServeHTTP(w, r)
	})
	return s.guard(mux)
}

// guard refuses foreign Host names and closes everything once the home has an owner with a passkey.
func (s *Server) guard(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !lanHost(r.Host) {
			wire.Headers(w)
			wire.WriteError(w, wire.Errorf(http.StatusMisdirectedRequest, "forbidden",
				"open the setup page by the address homed printed"))
			return
		}
		ready, err := auth.OwnerReady(r.Context(), s.st)
		if err != nil {
			s.log.Error("firstrun: cannot read the owner", "err", err)
			wire.Headers(w)
			wire.WriteError(w, err)
			return
		}
		if ready {
			done(w, r)
			return
		}
		next.ServeHTTP(w, r)
	})
}

// done answers 410 setup_done: JSON for the API, a short bilingual page for a browser.
func done(w http.ResponseWriter, r *http.Request) {
	wire.Headers(w)
	if strings.HasPrefix(r.URL.Path, "/api/") {
		wire.WriteError(w, wire.Errorf(http.StatusGone, "setup_done", "the home is set up; this page is closed"))
		return
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Header().Set("Content-Security-Policy", "default-src 'none'")
	w.WriteHeader(http.StatusGone)
	_, _ = io.WriteString(w, `<!doctype html><html lang="ru"><meta charset="utf-8"><title>GPT-Home</title>`+
		`<p>Дом уже настроен. Эта страница больше не нужна.</p>`+
		`<p lang="en">Your home is set up. You don't need this page anymore.</p></html>`)
}

// lanHost accepts the Host names a browser on the home network uses: IP literals, localhost, single-label names
// and the usual local suffixes. A public name means DNS rebinding, so it is refused.
func lanHost(hostport string) bool {
	host := hostport
	if h, _, err := net.SplitHostPort(hostport); err == nil {
		host = h
	}
	host = strings.TrimSuffix(strings.ToLower(strings.Trim(host, "[]")), ".")
	switch {
	case host == "":
		return false
	case net.ParseIP(host) != nil, host == "localhost", !strings.Contains(host, "."):
		return true
	}
	for _, suffix := range []string{".local", ".lan", ".home", ".home.arpa", ".internal"} {
		if strings.HasSuffix(host, suffix) {
			return true
		}
	}
	return false
}

type handlerFunc func(w http.ResponseWriter, r *http.Request) error

// endpoint wraps an API endpoint: the common headers, the method, the CSRF rule for POST and, when signedIn is
// set, the first-run cookie.
func (s *Server) endpoint(method string, cookie bool, h handlerFunc) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		wire.Headers(w)
		var err error
		switch {
		case r.Method != method:
			w.Header().Set("Allow", method)
			err = wire.Errorf(http.StatusMethodNotAllowed, "method_not_allowed", r.Method+" is not allowed here")
		case method == http.MethodPost && !sameOrigin(r):
			err = wire.Errorf(http.StatusForbidden, "csrf_failed", "cross-site request refused")
		case cookie && !s.signedIn(r):
			err = wire.Errorf(http.StatusUnauthorized, "unauthenticated", "enter the PIN first")
		default:
			err = h(w, r)
		}
		if err != nil {
			if _, ok := errors.AsType[*wire.Error](err); !ok {
				s.log.Error("firstrun: request failed", "path", r.URL.Path, "err", err)
			}
			wire.WriteError(w, err)
		}
	})
}

// sameOrigin is ADMIN-API §1.4 for a page whose origin is whatever LAN address the browser used.
func sameOrigin(r *http.Request) bool {
	if r.Header.Get("X-GPT-Home") != "1" {
		return false
	}
	if o := r.Header.Get("Origin"); o != "" {
		u, err := url.Parse(o)
		if err != nil || (u.Scheme != "http" && u.Scheme != "https") || !strings.EqualFold(u.Host, r.Host) {
			return false
		}
	}
	if sf := r.Header.Get("Sec-Fetch-Site"); sf != "" && sf != "same-origin" {
		return false
	}
	return true
}

func hashCookie(v string) string {
	sum := sha256.Sum256([]byte(v))
	return hex.EncodeToString(sum[:])
}

// signedIn reports whether the request carries a live first-run cookie.
func (s *Server) signedIn(r *http.Request) bool {
	c, err := r.Cookie(CookieName)
	if err != nil {
		return false
	}
	key := hashCookie(c.Value)
	s.mu.Lock()
	defer s.mu.Unlock()
	exp, ok := s.cookies[key]
	if ok && !s.now().Before(exp) {
		delete(s.cookies, key)
		return false
	}
	return ok
}

func (s *Server) enterPIN(w http.ResponseWriter, r *http.Request) error {
	var body struct {
		PIN string `json:"pin"`
	}
	if err := wire.Decode(w, r, &body); err != nil {
		return err
	}
	now := s.now()
	s.mu.Lock()
	defer s.mu.Unlock()
	s.wrong = slices.DeleteFunc(s.wrong, func(t time.Time) bool { return !now.Before(t.Add(pinWindow)) })
	if len(s.wrong) >= maxWrongPINs {
		return s.locked(now)
	}
	if subtle.ConstantTimeCompare([]byte(strings.TrimSpace(body.PIN)), []byte(s.pin)) != 1 {
		s.wrong = append(s.wrong, now)
		s.log.Warn("firstrun: wrong PIN", "client", r.RemoteAddr, "tries", len(s.wrong))
		if len(s.wrong) < maxWrongPINs {
			return wire.Errorf(http.StatusUnauthorized, "wrong_pin", "wrong PIN")
		}
		s.pin = newPIN()
		s.log.Warn("firstrun: too many wrong PINs; a new PIN was printed")
		_, _ = fmt.Fprintf(s.term, "\nhomed first run: too many wrong PINs. The new PIN is %s (it works in %s).\n\n",
			s.pin, s.wrong[0].Add(pinWindow).Sub(now).Round(time.Second))
		return s.locked(now)
	}
	for k, exp := range s.cookies {
		if !now.Before(exp) {
			delete(s.cookies, k)
		}
	}
	if len(s.cookies) >= maxCookies {
		return wire.Errorf(http.StatusTooManyRequests, "rate_limited", "too many open setup pages")
	}
	token := wire.Random(32)
	s.cookies[hashCookie(token)] = now.Add(cookieTTL)
	//nolint:gosec // G124: plain http on the LAN by design (ADMIN-API §5); a Secure cookie would never come back.
	http.SetCookie(w, &http.Cookie{
		Name: CookieName, Value: token, Path: "/", MaxAge: int(cookieTTL / time.Second),
		HttpOnly: true, SameSite: http.SameSiteStrictMode,
	})
	w.WriteHeader(http.StatusNoContent)
	return nil
}

// locked answers while the wrong PINs fill the window; it ends when the oldest one leaves it.
func (s *Server) locked(now time.Time) error {
	return &wire.Error{Status: http.StatusTooManyRequests, Code: "rate_limited",
		Message: "too many wrong PINs; homed printed a new one", RetryAfter: s.wrong[0].Add(pinWindow).Sub(now)}
}
