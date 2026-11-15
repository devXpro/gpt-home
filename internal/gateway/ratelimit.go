package gateway

import (
	"sync"
	"time"
)

// limiter is a token bucket per key: perMinute tokens of burst, refilled at perMinute per minute.
// Buckets are only made for keys that passed authentication, so the map stays as small as the key list.
type limiter struct {
	perMinute float64
	now       func() time.Time

	mu      sync.Mutex
	buckets map[string]*bucket
}

type bucket struct {
	tokens float64
	last   time.Time
}

func newLimiter(perMinute int, now func() time.Time) *limiter {
	return &limiter{perMinute: float64(perMinute), now: now, buckets: map[string]*bucket{}}
}

// allow takes one token from key's bucket and reports whether there was one.
func (l *limiter) allow(key string) bool {
	l.mu.Lock()
	defer l.mu.Unlock()
	now := l.now()
	b, ok := l.buckets[key]
	if !ok {
		b = &bucket{tokens: l.perMinute, last: now}
		l.buckets[key] = b
	}
	b.tokens = min(l.perMinute, b.tokens+now.Sub(b.last).Minutes()*l.perMinute)
	b.last = now
	if b.tokens < 1 {
		return false
	}
	b.tokens--
	return true
}
