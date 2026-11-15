package auth

import (
	"context"
	"encoding/json"
	"net/http"
	"slices"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/devXpro/gpt-home/internal/auth/wire"
	"github.com/devXpro/gpt-home/internal/store"
)

func TestStepUp(t *testing.T) {
	e := newEnv(t)
	phone := newDevice()
	b := e.owner(phone)

	// Signing in counts: dangerous actions pass for 5 minutes.
	wantStatus(t, b.do(http.MethodPost, "/test/dangerous", map[string]any{}), http.StatusNoContent)
	e.clock.Add(StepUpWindow - time.Millisecond)
	wantStatus(t, b.do(http.MethodPost, "/test/dangerous", map[string]any{}), http.StatusNoContent)
	e.clock.Add(time.Millisecond)
	wantError(t, b.do(http.MethodPost, "/test/dangerous", map[string]any{}), http.StatusForbidden, "step_up_required")

	rec := b.stepUp(phone)
	wantStatus(t, rec, http.StatusOK)
	until := decodeAs[map[string]string](t, rec)["stepUpUntil"]
	if until != wire.Time(e.clock.Now().Add(StepUpWindow)) {
		t.Fatalf("stepUpUntil %q", until)
	}
	wantStatus(t, b.do(http.MethodPost, "/test/dangerous", map[string]any{}), http.StatusNoContent)
	if got := e.actions(); got[len(got)-1] != "auth.step_up" {
		t.Fatalf("audit %v", got)
	}

	// The step-up options allow only the person's own passkeys.
	raw, _ := b.begin("/api/admin/auth/stepup/begin", map[string]any{})
	if !slices.Contains(allowed(t, raw), passkeyID(phone.cred.ID)) || len(allowed(t, raw)) != 1 {
		t.Fatalf("allowCredentials %v", allowed(t, raw))
	}
}

func TestStepUpRejected(t *testing.T) {
	e := newEnv(t)
	phone := newDevice()
	b := e.owner(phone)
	e.clock.Add(StepUpWindow)

	// Another person's passkey does not step up Roman's session.
	tanyaPhone := newDevice()
	tanya := e.addPerson("tanya", "Tanya", store.RoleMember)
	tanyaPhone.auth.Options.UserHandle = tanya.UserHandle
	token := newToken()
	ctx := context.Background()
	if err := e.st.Invites().Create(ctx, store.Invite{ID: "i_t", TokenHash: hashToken(token), Kind: store.InviteMember,
		PersonID: "tanya", CreatedAt: e.clock.Now(), ExpiresAt: e.clock.Now().Add(time.Hour)}); err != nil {
		t.Fatal(err)
	}
	wantStatus(t, e.browser().register(tanyaPhone, token, nil), http.StatusCreated)
	wantError(t, b.stepUp(tanyaPhone), http.StatusUnauthorized, "login_failed")

	// A step-up ceremony begun in one session cannot be finished in another.
	other := e.browser()
	wantStatus(t, other.login(phone), http.StatusOK)
	raw, id := b.begin("/api/admin/auth/stepup/begin", map[string]any{})
	answer := phone.get(t, raw)
	wantError(t, other.do(http.MethodPost, "/api/admin/auth/stepup/finish",
		map[string]any{"ceremonyId": id, "credential": answer}), http.StatusBadRequest, "webauthn_failed")
	wantStatus(t, b.do(http.MethodPost, "/api/admin/auth/stepup/finish",
		map[string]any{"ceremonyId": id, "credential": answer}), http.StatusOK)

	// Without a session there is no step-up.
	wantError(t, e.browser().do(http.MethodPost, "/api/admin/auth/stepup/begin", map[string]any{}), http.StatusUnauthorized, "unauthenticated")
}

func TestSessionLifetime(t *testing.T) {
	ctx := context.Background()

	t.Run("idle", func(t *testing.T) {
		e := newEnv(t)
		b := e.owner(newDevice())
		sessions, _ := e.st.Sessions().ListByPerson(ctx, "roman")
		e.clock.Add(IdleTimeout - time.Millisecond)
		wantStatus(t, b.do(http.MethodGet, "/test/session", nil), http.StatusOK) // touches
		e.clock.Add(IdleTimeout - time.Millisecond)
		wantStatus(t, b.do(http.MethodGet, "/test/session", nil), http.StatusOK)
		e.clock.Add(IdleTimeout)
		wantError(t, b.do(http.MethodGet, "/test/session", nil), http.StatusUnauthorized, "unauthenticated")
		if left, _ := e.st.Sessions().ListByPerson(ctx, "roman"); len(left) != 0 {
			t.Fatalf("expired session kept: %+v", left)
		}
		if !slices.Equal(e.endedSessions(), []string{sessions[0].ID}) {
			t.Fatalf("OnSessionEnd %v", e.endedSessions())
		}
	})

	t.Run("absolute", func(t *testing.T) {
		e := newEnv(t)
		b := e.owner(newDevice())
		for elapsed := time.Duration(0); elapsed+11*time.Hour < AbsoluteTimeout; elapsed += 11 * time.Hour {
			e.clock.Add(11 * time.Hour)
			wantStatus(t, b.do(http.MethodGet, "/test/session", nil), http.StatusOK)
		}
		e.clock.Add(11 * time.Hour)
		wantError(t, b.do(http.MethodGet, "/test/session", nil), http.StatusUnauthorized, "unauthenticated")
	})

	t.Run("touch at most once a minute, never by an event stream", func(t *testing.T) {
		e := newEnv(t)
		b := e.owner(newDevice())
		start := e.clock.Now()
		lastSeen := func() time.Time {
			s, _ := e.st.Sessions().ListByPerson(ctx, "roman")
			return s[0].LastSeenAt
		}
		e.clock.Add(59 * time.Second)
		b.do(http.MethodGet, "/test/session", nil)
		if !lastSeen().Equal(start) {
			t.Fatalf("touched after 59 s: %v", lastSeen())
		}
		e.clock.Add(time.Minute)
		b.header.Set("Accept", "text/event-stream")
		b.do(http.MethodGet, "/test/session", nil)
		if !lastSeen().Equal(start) {
			t.Fatal("an event stream touched the session")
		}
		b.header.Del("Accept")
		b.do(http.MethodGet, "/test/session", nil)
		if !lastSeen().Equal(e.clock.Now()) {
			t.Fatalf("not touched after 2 minutes: %v", lastSeen())
		}
	})

	t.Run("bad cookies", func(t *testing.T) {
		e := newEnv(t)
		b := e.owner(newDevice())
		tampered := []byte(b.cookie)
		tampered[0] ^= 'A' ^ 'B' // flips between two base64url letters, so the cookie stays well formed
		if tampered[0] == b.cookie[0] || !wellFormedToken(string(tampered)) {
			tampered[0] = 'x'
		}
		for _, c := range []string{"", "nope", newToken(), string(tampered)} {
			x := e.browser()
			x.cookie = c
			wantError(t, x.do(http.MethodGet, "/test/session", nil), http.StatusUnauthorized, "unauthenticated")
		}
	})

	t.Run("person removed", func(t *testing.T) {
		e := newEnv(t)
		b := e.owner(newDevice())
		if err := e.st.People().Delete(ctx, "roman"); err != nil {
			t.Fatal(err)
		}
		wantError(t, b.do(http.MethodGet, "/test/session", nil), http.StatusUnauthorized, "unauthenticated")
	})

	t.Run("sweep", func(t *testing.T) {
		e := newEnv(t)
		phone := newDevice()
		e.owner(phone)
		e.clock.Add(IdleTimeout - time.Minute)
		fresh := e.browser()
		wantStatus(t, fresh.login(phone), http.StatusOK)
		e.clock.Add(time.Minute + time.Millisecond)
		e.a.sweep(ctx)
		left, _ := e.st.Sessions().ListByPerson(ctx, "roman")
		if len(left) != 1 || left[0].TokenHash != hashToken(fresh.cookie) {
			t.Fatalf("sessions left: %+v", left)
		}
	})
}

func TestNewPrincipal(t *testing.T) {
	t0 := time.Date(2026, 11, 15, 12, 0, 0, 0, time.UTC)
	p := newPrincipal(store.Person{ID: "roman"}, store.Session{CreatedAt: t0, LastSeenAt: t0.Add(AbsoluteTimeout - 2*time.Hour)})
	if !p.ExpiresAt.Equal(t0.Add(AbsoluteTimeout)) || !p.IdleExpiresAt.Equal(p.ExpiresAt) || !p.StepUpUntil.IsZero() ||
		p.SteppedUp(t0) {
		t.Fatalf("principal %+v", p)
	}
}

func TestAuthenticatePrincipal(t *testing.T) {
	e := newEnv(t)
	b := e.owner(newDevice())
	start := e.clock.Now()
	e.clock.Add(time.Hour)
	req, _ := http.NewRequest(http.MethodGet, "https://"+testDomain+"/api/admin/session", http.NoBody)
	req.Header.Set("Cookie", CookieName+"="+b.cookie)
	p, err := e.a.Authenticate(req)
	if err != nil {
		t.Fatal(err)
	}
	now := e.clock.Now()
	if p.Person.ID != "roman" || !p.ExpiresAt.Equal(start.Add(AbsoluteTimeout)) || !p.IdleExpiresAt.Equal(now.Add(IdleTimeout)) ||
		!p.StepUpUntil.Equal(start.Add(StepUpWindow)) || p.SteppedUp(now) || p.Session.PasskeyID == "" {
		t.Fatalf("principal %+v", p)
	}
}

func TestAddPasskey(t *testing.T) {
	e := newEnv(t)
	ctx := context.Background()
	phone := newDevice()
	b := e.owner(phone)
	mac := newDevice()

	e.clock.Add(2 * time.Minute)
	raw, id := b.begin("/api/admin/me/passkeys/begin", map[string]any{})
	if ex := excluded(t, raw); !slices.Equal(ex, []string{passkeyID(phone.cred.ID)}) {
		t.Fatalf("excludeCredentials %v", ex)
	}
	answer := mac.create(t, raw)

	// Step-up expired between begin and finish: 403, and the ceremony survives for the retry after step-up.
	e.clock.Add(StepUpWindow - 2*time.Minute)
	finish := map[string]any{"ceremonyId": id, "credential": answer, "name": "Mac mini"}
	wantError(t, b.do(http.MethodPost, "/api/admin/me/passkeys/finish", finish), http.StatusForbidden, "step_up_required")
	wantError(t, b.do(http.MethodPost, "/api/admin/me/passkeys/begin", map[string]any{}), http.StatusForbidden, "step_up_required")
	wantStatus(t, b.stepUp(phone), http.StatusOK)
	rec := b.do(http.MethodPost, "/api/admin/me/passkeys/finish", finish)
	wantStatus(t, rec, http.StatusCreated)
	pk := decodeAs[map[string]any](t, rec)
	if pk["id"] != passkeyID(mac.cred.ID) || pk["name"] != "Mac mini" || pk["current"] != false || pk["lastUsedAt"] != nil {
		t.Fatalf("passkey %v", pk)
	}
	if pks, _ := e.st.Passkeys().ListByPerson(ctx, "roman"); len(pks) != 2 {
		t.Fatalf("passkeys %+v", pks)
	}
	if got := e.actions(); got[len(got)-1] != "passkey.add" || e.audit()[len(got)-1].Target != "Mac mini" {
		t.Fatalf("audit %v", got)
	}
	// The new passkey signs in.
	wantStatus(t, e.browser().login(mac), http.StatusOK)

	// The same authenticator again.
	raw, id = b.begin("/api/admin/me/passkeys/begin", map[string]any{})
	wantError(t, b.do(http.MethodPost, "/api/admin/me/passkeys/finish",
		map[string]any{"ceremonyId": id, "credential": mac.create(t, raw)}), http.StatusConflict, "passkey_exists")

	// Without a session.
	wantError(t, e.browser().do(http.MethodPost, "/api/admin/me/passkeys/begin", map[string]any{}), http.StatusUnauthorized, "unauthenticated")
}

func TestCSRF(t *testing.T) {
	e := newEnv(t)
	for _, tc := range []struct {
		name   string
		mutate func(b *browser)
		want   int
	}{
		{"same origin", func(*browser) {}, http.StatusOK},
		{"no Origin header", func(b *browser) { b.origin = "" }, http.StatusOK},
		{"same-origin fetch", func(b *browser) { b.header.Set("Sec-Fetch-Site", "same-origin") }, http.StatusOK},
		{"no X-GPT-Home", func(b *browser) { b.header.Set("X-GPT-Home", "0") }, http.StatusForbidden},
		{"foreign Origin", func(b *browser) { b.origin = "https://evil.example.com" }, http.StatusForbidden},
		{"http Origin", func(b *browser) { b.origin = "http://" + testDomain }, http.StatusForbidden},
		{"cross-site fetch", func(b *browser) { b.header.Set("Sec-Fetch-Site", "cross-site") }, http.StatusForbidden},
		{"same-site fetch", func(b *browser) { b.header.Set("Sec-Fetch-Site", "same-site") }, http.StatusForbidden},
	} {
		t.Run(tc.name, func(t *testing.T) {
			b := e.browser()
			tc.mutate(b)
			rec := b.do(http.MethodPost, "/api/admin/auth/login/begin", map[string]any{})
			if tc.want == http.StatusForbidden {
				wantError(t, rec, tc.want, "csrf_failed")
				return
			}
			wantStatus(t, rec, tc.want)
		})
	}
}

func TestRequestFormat(t *testing.T) {
	e := newEnv(t)
	b := e.browser()
	rec := b.do(http.MethodGet, "/api/admin/auth/login/begin", nil)
	wantError(t, rec, http.StatusMethodNotAllowed, "method_not_allowed")
	if rec.Header().Get("Allow") != http.MethodPost {
		t.Fatalf("Allow %q", rec.Header().Get("Allow"))
	}
	wantError(t, b.do(http.MethodPost, "/api/admin/auth/nope", map[string]any{}), http.StatusNotFound, "not_found")
	wantError(t, b.do(http.MethodPost, "/api/admin/auth/token", "[1]"), http.StatusBadRequest, "invalid_json")
	wantError(t, b.do(http.MethodPost, "/api/admin/auth/token", "{"), http.StatusBadRequest, "invalid_json")
	wantError(t, b.do(http.MethodPost, "/api/admin/auth/token", `{"token":"a"} {}`), http.StatusBadRequest, "invalid_json")
	b.header.Set("Content-Type", "text/plain")
	wantError(t, b.do(http.MethodPost, "/api/admin/auth/token", `{"token":"a"}`), http.StatusUnsupportedMediaType, "unsupported_media_type")
	b.header.Del("Content-Type")
	big := `{"token":"` + string(make([]byte, wire.MaxBody)) + `"}`
	wantError(t, b.do(http.MethodPost, "/api/admin/auth/token", big), http.StatusRequestEntityTooLarge, "too_large")

	rec = b.do(http.MethodPost, "/api/admin/auth/login/begin", nil) // an empty body is {}
	wantStatus(t, rec, http.StatusOK)
	if rec.Header().Get("Cache-Control") != "no-store" || rec.Header().Get("X-Content-Type-Options") != "nosniff" {
		t.Fatalf("headers %v", rec.Header())
	}
}

func TestRateLimit(t *testing.T) {
	e := newEnv(t)
	b := e.browser()
	b.xff = "203.0.113.7, 172.18.0.2" // the client as Caddy appended it, then Caddy as frps saw it
	for range authPerAddress {
		wantStatus(t, b.do(http.MethodPost, "/api/admin/auth/login/begin", map[string]any{}), http.StatusOK)
	}
	rec := b.do(http.MethodPost, "/api/admin/auth/login/begin", map[string]any{})
	wantError(t, rec, http.StatusTooManyRequests, "rate_limited")
	if after, _ := strconv.Atoi(rec.Header().Get("Retry-After")); after != 3 {
		t.Fatalf("Retry-After %q", rec.Header().Get("Retry-After"))
	}
	// A spoofed left-most value does not give the same client a new bucket.
	b.xff = "198.51.100.1, 203.0.113.7, 172.18.0.2"
	wantError(t, b.do(http.MethodPost, "/api/admin/auth/token", map[string]any{"token": "x"}), http.StatusTooManyRequests, "rate_limited")
	// Another client is not affected; a token comes back after 3 s.
	other := e.browser()
	other.xff = "203.0.113.8, 172.18.0.2"
	wantStatus(t, other.do(http.MethodPost, "/api/admin/auth/login/begin", map[string]any{}), http.StatusOK)
	e.clock.Add(3*time.Second + time.Millisecond)
	wantStatus(t, b.do(http.MethodPost, "/api/admin/auth/login/begin", map[string]any{}), http.StatusOK)

	// 60 a minute in total, whatever the addresses: 60 - 20 (b) - 1 (other) + 3 (refilled in 3 s) - 1 (b) = 41 left.
	for i := range 41 {
		c := e.browser()
		c.xff = "198.51.100." + strconv.Itoa(i) + ", 172.18.0.2"
		wantStatus(t, c.do(http.MethodPost, "/api/admin/auth/login/begin", map[string]any{}), http.StatusOK)
	}
	c := e.browser()
	c.xff = "192.0.2.1, 172.18.0.2"
	wantError(t, c.do(http.MethodPost, "/api/admin/auth/login/begin", map[string]any{}), http.StatusTooManyRequests, "rate_limited")
	// Logout is not an auth ceremony and is not limited.
	wantError(t, c.do(http.MethodPost, "/api/admin/auth/logout", map[string]any{}), http.StatusUnauthorized, "unauthenticated")
}

func TestClientAddr(t *testing.T) {
	for _, tc := range []struct {
		remote string
		xff    []string
		want   string
	}{
		{"127.0.0.1:5000", []string{"203.0.113.7, 172.18.0.2"}, "203.0.113.7"},
		{"127.0.0.1:5000", []string{"1.1.1.1, 203.0.113.7, 172.18.0.2"}, "203.0.113.7"},
		{"127.0.0.1:5000", []string{"203.0.113.7", "172.18.0.2"}, "203.0.113.7"}, // two header lines
		{"127.0.0.1:5000", []string{"2001:db8:1:2:3:4:5:6, 172.18.0.2"}, "2001:db8:1:2::/64"},
		{"127.0.0.1:5000", []string{"::ffff:203.0.113.7, 172.18.0.2"}, "203.0.113.7"},
		{"127.0.0.1:5000", []string{"172.18.0.2"}, ""}, // cannot tell
		{"127.0.0.1:5000", nil, ""},
		{"127.0.0.1:5000", []string{"junk, 172.18.0.2"}, ""},
		{"[::1]:5000", []string{"203.0.113.7, 172.18.0.2"}, "203.0.113.7"},
		{"192.168.68.20:5000", []string{"1.2.3.4, 5.6.7.8"}, "192.168.68.20"}, // not via the relay: the peer
		{"garbage", nil, ""},
	} {
		r, _ := http.NewRequest(http.MethodGet, "http://x/", http.NoBody)
		r.RemoteAddr = tc.remote
		for _, v := range tc.xff {
			r.Header.Add("X-Forwarded-For", v)
		}
		if got := ClientAddr(r); got != tc.want {
			t.Errorf("%s %v: %q, want %q", tc.remote, tc.xff, got, tc.want)
		}
	}
}

func TestLocalEndpoints(t *testing.T) {
	e := newEnv(t)
	ctx := context.Background()
	phone := newDevice()
	b := e.owner(phone)
	other := e.browser()
	wantStatus(t, other.login(phone), http.StatusOK)
	sessions, _ := e.st.Sessions().ListByPerson(ctx, "roman")

	local := e.browser()
	local.origin = ""
	rec := local.do(http.MethodPost, "/local/api/admin/local/setup-link", map[string]any{})
	wantStatus(t, rec, http.StatusOK)
	link := decodeAs[map[string]any](t, rec)
	if url, _ := link["url"].(string); len(url) != len(testOrigin+"/admin/setup#t=")+43 || link["expiresAt"] != wire.Time(e.clock.Now().Add(SetupLinkTTL)) {
		t.Fatalf("link %v", link)
	}

	rec = local.do(http.MethodPost, "/local/api/admin/local/reset-access", nil)
	wantStatus(t, rec, http.StatusOK)
	res := decodeAs[map[string]any](t, rec)
	if res["revokedPasskeys"] != float64(1) || res["revokedSessions"] != float64(2) || res["url"] == link["url"] {
		t.Fatalf("reset %v", res)
	}
	if ready, _ := OwnerReady(ctx, e.st); ready {
		t.Fatal("owner still ready after reset")
	}
	wantError(t, b.do(http.MethodGet, "/test/session", nil), http.StatusUnauthorized, "unauthenticated")
	wantError(t, e.browser().login(phone), http.StatusUnauthorized, "login_failed")
	ended := e.endedSessions()
	if len(ended) != 2 || !slices.Contains(ended, sessions[0].ID) || !slices.Contains(ended, sessions[1].ID) {
		t.Fatalf("OnSessionEnd %v, sessions %+v", ended, sessions)
	}
	// The earlier link is revoked; the new one registers a passkey for the same owner.
	_, oldToken, _ := strings.Cut(link["url"].(string), "#t=")
	wantError(t, e.browser().do(http.MethodPost, "/api/admin/auth/token", map[string]string{"token": oldToken}), http.StatusGone, "token_used")
	_, newTokenValue, _ := strings.Cut(res["url"].(string), "#t=")
	rec = e.browser().register(newDevice(), newTokenValue, nil)
	wantStatus(t, rec, http.StatusCreated)
	if m := decodeAs[me](t, rec); m.Person.ID != "roman" {
		t.Fatalf("Me %+v", m)
	}
	recs := e.audit()
	var reset store.AuditRecord
	for _, r := range recs {
		if r.Action == "access.reset" {
			reset = r
		}
	}
	if reset.Via != "cli" || string(reset.Args) != `{"credentials":1,"sessions":2}` {
		t.Fatalf("access.reset record %+v %s", reset, reset.Args)
	}

	wantError(t, local.do(http.MethodGet, "/local/api/admin/local/setup-link", nil), http.StatusMethodNotAllowed, "method_not_allowed")
	wantError(t, local.do(http.MethodPost, "/local/api/admin/local/other", nil), http.StatusNotFound, "not_found")
}

func TestOwnerReady(t *testing.T) {
	e := newEnv(t)
	ctx := context.Background()
	check := func(want bool) {
		t.Helper()
		if got, err := OwnerReady(ctx, e.st); err != nil || got != want {
			t.Fatalf("OwnerReady %v %v, want %v", got, err, want)
		}
	}
	check(false)
	e.addPerson("roman", "Roman", store.RoleOwner) // imported, no passkey yet
	check(false)
	wantStatus(t, e.browser().register(newDevice(), e.link(), nil), http.StatusCreated)
	check(true)
}

func TestNewSetupLinkChecksOrigin(t *testing.T) {
	st := newEnv(t).st
	for _, origin := range []string{"", "homie.example.com", "https://homie.example.com/", "https://h/x", "ftp://h", "https://u@h"} {
		if _, err := NewSetupLink(context.Background(), st, nil, origin, "cli", time.Now()); err == nil {
			t.Errorf("origin %q accepted", origin)
		}
	}
}

// allowed returns the allowCredentials ids of request options.
func allowed(t *testing.T, raw []byte) []string {
	t.Helper()
	var v struct {
		PublicKey struct {
			AllowCredentials []struct {
				ID string `json:"id"`
			} `json:"allowCredentials"`
		} `json:"publicKey"`
	}
	if err := json.Unmarshal(raw, &v); err != nil {
		t.Fatal(err)
	}
	var ids []string
	for _, c := range v.PublicKey.AllowCredentials {
		ids = append(ids, c.ID)
	}
	return ids
}

// excluded returns the excludeCredentials ids of creation options.
func excluded(t *testing.T, raw []byte) []string {
	t.Helper()
	var v struct {
		PublicKey struct {
			ExcludeCredentials []struct {
				ID string `json:"id"`
			} `json:"excludeCredentials"`
		} `json:"publicKey"`
	}
	if err := json.Unmarshal(raw, &v); err != nil {
		t.Fatal(err)
	}
	var ids []string
	for _, c := range v.PublicKey.ExcludeCredentials {
		ids = append(ids, c.ID)
	}
	return ids
}
