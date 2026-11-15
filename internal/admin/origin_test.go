package admin

import (
	"sync"
	"testing"
)

// OriginFunc is read on every request: a relay connected while homed runs moves the allowed origin at once, and with
// no relay a request that sends an Origin is refused.
func TestOriginFunc(t *testing.T) {
	var mu sync.Mutex
	current := ""
	setOrigin := func(o string) {
		mu.Lock()
		defer mu.Unlock()
		current = o
	}
	e := newEnv(t, func(o *Options) {
		o.Origin = "https://static.example" // OriginFunc wins
		o.OriginFunc = func() string {
			mu.Lock()
			defer mu.Unlock()
			return current
		}
	})
	patch := func(from string) reply {
		return e.do(call{
			method: "PATCH", path: "/settings", tok: "tok-roman", body: `{"language":"en"}`,
			header: map[string]string{"Origin": from},
		})
	}
	expect(t, patch("https://static.example"), 403, "csrf_failed")
	expect(t, patch("https://new.example"), 403, "csrf_failed")
	setOrigin("https://new.example")
	expect(t, patch("https://new.example"), 200, "")
	expect(t, patch(origin), 403, "csrf_failed")
}
