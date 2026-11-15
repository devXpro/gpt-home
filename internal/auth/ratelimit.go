package auth

import (
	"net"
	"net/http"
	"strings"
	"sync"
	"time"
)

// ADMIN-API §1.7. Every class of endpoint has buckets of its own, so spending one class never locks another: an
// anonymous flood of sign-in attempts cannot stop a signed-in owner's step-up, and junk on auth/token cannot stop a
// sign-in.
const (
	tokenPerAddress = 20 // auth/token and register begin and finish: requests per minute per client address
	tokenTotal      = 60 // the same requests per minute from every client address together
	beginsPerKey    = 30 // login, step-up and passkey begins per minute per client address (login) or session
	failuresPerKey  = 10 // failed login, step-up and passkey finishes per minute per client address or session
	maxBuckets      = 4096
)

// noAddress is the bucket of requests whose client address is unknown: loopback without X-Forwarded-For (development,
// a local tool). They never share a bucket with the internet's clients, and never count against a class total.
const noAddress = "local"

// limiter is a token bucket per key plus, when total is set, one for every client address together. A bucket holds a
// minute's worth of tokens and refills continuously.
type limiter struct {
	perKey, total float64 // tokens per minute; total 0 means no shared bucket

	mu      sync.Mutex
	all     bucket
	buckets map[string]*bucket
}

type bucket struct {
	tokens float64
	last   time.Time
}

func newLimiter(perKey, total int) *limiter {
	return &limiter{perKey: float64(perKey), total: float64(total), buckets: map[string]*bucket{}}
}

// refill brings the bucket up to now at rate tokens per minute; a new bucket starts full.
func (b *bucket) refill(rate float64, now time.Time) {
	if b.last.IsZero() {
		b.tokens = rate
	} else if d := now.Sub(b.last); d > 0 {
		b.tokens = min(rate, b.tokens+d.Minutes()*rate)
	}
	b.last = now
}

// wait is how long until the bucket holds a whole token again.
func (b *bucket) wait(rate float64) time.Duration {
	return time.Duration((1 - b.tokens) / rate * float64(time.Minute))
}

// take refills the bucket and takes one token. It returns the wait for the next token when the bucket is empty.
func (b *bucket) take(rate float64, now time.Time) (bool, time.Duration) {
	b.refill(rate, now)
	if b.tokens >= 1 {
		b.tokens--
		return true, 0
	}
	return false, b.wait(rate)
}

// peek reports whether the bucket holds a token, without taking it.
func (b *bucket) peek(rate float64, now time.Time) (bool, time.Duration) {
	b.refill(rate, now)
	if b.tokens >= 1 {
		return true, 0
	}
	return false, b.wait(rate)
}

// bucketLocked returns key's bucket, making it (and pruning the map when it is full). l.mu is held.
func (l *limiter) bucketLocked(key string, now time.Time) *bucket {
	b, ok := l.buckets[key]
	if !ok {
		if len(l.buckets) >= maxBuckets {
			l.prune(now)
		}
		b = &bucket{}
		l.buckets[key] = b
	}
	return b
}

// take takes a token for key and, for a known client address, from the shared bucket. "" (no address) has its own
// bucket and never touches the shared one. When it refuses, it returns how long to wait.
func (l *limiter) take(key string, now time.Time) (bool, time.Duration) {
	l.mu.Lock()
	defer l.mu.Unlock()
	shared := l.total > 0 && key != ""
	if key == "" {
		key = noAddress
	}
	if shared {
		if ok, wait := l.all.peek(l.total, now); !ok {
			return false, wait
		}
	}
	if ok, wait := l.bucketLocked(key, now).take(l.perKey, now); !ok {
		return false, wait
	}
	if shared {
		l.all.take(l.total, now)
	}
	return true, 0
}

// peek reports whether key has a token left, without taking it.
func (l *limiter) peek(key string, now time.Time) (bool, time.Duration) {
	l.mu.Lock()
	defer l.mu.Unlock()
	if key == "" {
		key = noAddress
	}
	return l.bucketLocked(key, now).peek(l.perKey, now)
}

// prune drops the buckets that refilled completely; if that is not enough (an address flood), it drops half of the
// rest.
func (l *limiter) prune(now time.Time) {
	for k, b := range l.buckets {
		if now.Sub(b.last) >= time.Minute {
			delete(l.buckets, k)
		}
	}
	for k := range l.buckets {
		if len(l.buckets) < maxBuckets/2 {
			break
		}
		delete(l.buckets, k)
	}
}

// gate limits one passkey ceremony class per key, a client address (login) or a session (step-up, another passkey):
// begins are counted, and so are failed finishes, not the successful ones. While a key's failures are spent, its
// begins and finishes are both refused.
type gate struct {
	begins, failures *limiter
}

func newGate() *gate {
	return &gate{begins: newLimiter(beginsPerKey, 0), failures: newLimiter(failuresPerKey, 0)}
}

func (g *gate) begin(key string, now time.Time) (bool, time.Duration) {
	if ok, wait := g.failures.peek(key, now); !ok {
		return false, wait
	}
	return g.begins.take(key, now)
}

func (g *gate) finish(key string, now time.Time) (bool, time.Duration) {
	return g.failures.peek(key, now)
}

func (g *gate) failed(key string, now time.Time) { g.failures.take(key, now) }

// limits are the auth endpoints' buckets (ADMIN-API §1.7).
type limits struct {
	token   *limiter // auth/token, register/begin, register/finish: every request, per address and in total
	login   *gate    // login/begin and login/finish, per client address; no shared bucket
	stepUp  *gate    // stepup/begin and stepup/finish, per session
	passkey *gate    // me/passkeys/begin and me/passkeys/finish, per session
}

func newLimits() limits {
	return limits{token: newLimiter(tokenPerAddress, tokenTotal), login: newGate(), stepUp: newGate(), passkey: newGate()}
}

// sessionKey identifies the session a request claims, for the per-session buckets, without a database lookup: the
// hash of its cookie ("" without one). A request with a made-up cookie gets a bucket of its own and is refused at
// authentication anyway.
func sessionKey(r *http.Request) string {
	c, err := r.Cookie(CookieName)
	if err != nil || !wellFormedToken(c.Value) {
		return ""
	}
	return hashToken(c.Value)
}

// ClientAddr returns the address a request counts against for rate limits, or "" when it cannot be known.
//
// The trust assumption: homed listens on loopback, and every public request arrives from 127.0.0.1 through the relay
// as Caddy → frps → frpc. Caddy replaces any X-Forwarded-For a client sends with the client's own address (it trusts no
// proxy in front of it; with trusted_proxies set, the hop it appends is still the one it saw), and frps appends the
// address it saw, Caddy's (frp v0.72.0 pkg/util/vhost/http.go, SetXForwarded). So the client is the second-to-last hop;
// values further left may come from the client and are never used. A CDN or another proxy in front of Caddy would
// make that hop the proxy's address (docs/SECURITY.md). A request from a non-loopback peer counts against that peer.
// IPv6 clients count per /64, since one host can use a whole /64. Loopback requests without the two hops (development,
// a local tool) have no address: they get a bucket of their own, never the internet's.
func ClientAddr(r *http.Request) string {
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		return ""
	}
	ip := net.ParseIP(host)
	if ip == nil {
		return ""
	}
	if !ip.IsLoopback() {
		return addrKey(ip)
	}
	var hops []string
	for _, v := range r.Header.Values("X-Forwarded-For") {
		for h := range strings.SplitSeq(v, ",") {
			hops = append(hops, strings.TrimSpace(h))
		}
	}
	if len(hops) < 2 {
		return ""
	}
	if ip = net.ParseIP(hops[len(hops)-2]); ip == nil {
		return ""
	}
	return addrKey(ip)
}

func addrKey(ip net.IP) string {
	if v4 := ip.To4(); v4 != nil {
		return v4.String()
	}
	return ip.Mask(net.CIDRMask(64, 128)).String() + "/64"
}
