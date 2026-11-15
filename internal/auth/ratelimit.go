package auth

import (
	"net"
	"net/http"
	"strings"
	"sync"
	"time"
)

// ADMIN-API §1.7: auth/* begin and finish, and auth/token.
const (
	authPerAddress = 20 // per minute per client address
	authTotal      = 60 // per minute in total
	maxBuckets     = 4096
)

// limiter is a token bucket per client address plus one for everybody. A bucket holds a minute's worth of tokens
// and refills continuously.
type limiter struct {
	perAddr, total float64

	mu      sync.Mutex
	all     bucket
	buckets map[string]*bucket
}

type bucket struct {
	tokens float64
	last   time.Time
}

func newLimiter(perAddr, total int) *limiter {
	return &limiter{perAddr: float64(perAddr), total: float64(total), buckets: map[string]*bucket{}}
}

// take refills the bucket at rate tokens per minute and takes one. It returns the wait for the next token when empty.
func (b *bucket) take(rate float64, now time.Time) (bool, time.Duration) {
	if b.last.IsZero() {
		b.tokens = rate
	} else if d := now.Sub(b.last); d > 0 {
		b.tokens = min(rate, b.tokens+d.Minutes()*rate)
	}
	b.last = now
	if b.tokens >= 1 {
		b.tokens--
		return true, 0
	}
	return false, time.Duration((1 - b.tokens) / rate * float64(time.Minute))
}

// allow takes a token for addr ("" when unknown: then only the shared bucket counts) and from the shared bucket.
// When it refuses, it returns how long to wait.
func (l *limiter) allow(addr string, now time.Time) (bool, time.Duration) {
	l.mu.Lock()
	defer l.mu.Unlock()
	if addr != "" {
		b, ok := l.buckets[addr]
		if !ok {
			if len(l.buckets) >= maxBuckets {
				l.prune(now)
			}
			b = &bucket{}
			l.buckets[addr] = b
		}
		if ok, wait := b.take(l.perAddr, now); !ok {
			return false, wait
		}
	}
	return l.all.take(l.total, now)
}

// prune drops the buckets that refilled completely; if that is not enough (an address flood), it drops half of the
// rest. The shared bucket still bounds the total.
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

// ClientAddr returns the address a request counts against for rate limits, or "" when it cannot be known.
//
// homed listens on loopback and the relay delivers every public request from 127.0.0.1. On the way, Caddy appends
// the client's address to X-Forwarded-For (dropping the client's own values unless the client is a trusted proxy)
// and frps appends the address it saw, Caddy's. So the client is the second-to-last hop; the values further left
// may come from the client and are never used. A request from a non-loopback peer counts against that peer. IPv6
// clients count per /64, since one host can use a whole /64.
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
