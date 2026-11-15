package firstrun

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"time"

	"github.com/devXpro/gpt-home/internal/auth"
	"github.com/devXpro/gpt-home/internal/auth/wire"
	"github.com/devXpro/gpt-home/internal/relay"
	"github.com/devXpro/gpt-home/internal/store"
)

// connectingGrace: a disconnected relay counts as connecting this long after its last change (ADMIN-API §2).
const connectingGrace = 30 * time.Second

// relayJSON is RelayStatus (ADMIN-API §2), built the way internal/admin builds it.
type relayJSON struct {
	Configured bool           `json:"configured"`
	Domain     string         `json:"domain,omitempty"`
	PublicURL  string         `json:"publicUrl,omitempty"`
	State      string         `json:"state"`
	Since      string         `json:"since,omitempty"`
	LastError  string         `json:"lastError,omitempty"`
	SelfCheck  *selfCheckJSON `json:"selfCheck,omitempty"`
}

type selfCheckJSON struct {
	OK        bool   `json:"ok"`
	CheckedAt string `json:"checkedAt"`
	Error     string `json:"error,omitempty"`
}

// relayStatus returns RelayStatus and whether the setup link can be made: connected, and the last self-check proved
// that https://<domain> reaches this hub.
func (s *Server) relayStatus() (out relayJSON, ready bool) {
	domain := s.relay.Domain()
	if domain == "" {
		return relayJSON{State: "not_configured"}, false
	}
	st := s.relay.Status()
	out = relayJSON{
		Configured: true, Domain: domain, PublicURL: "https://" + domain, Since: wire.OptTime(st.Since),
		LastError: st.LastError,
	}
	switch {
	case st.Connected:
		out.State = "connected"
	case st.LastError == "" || !st.Since.IsZero() && s.now().Sub(st.Since) < connectingGrace:
		out.State = "connecting"
	default:
		out.State = "offline"
	}
	checkedAt, failure := s.relay.SelfCheckResult()
	if !checkedAt.IsZero() {
		out.SelfCheck = &selfCheckJSON{OK: failure == "", CheckedAt: wire.Time(checkedAt), Error: failure}
	}
	return out, st.Connected && out.SelfCheck != nil && out.SelfCheck.OK
}

type stateJSON struct {
	Step     string     `json:"step"`
	HomeName string     `json:"homeName,omitempty"`
	Relay    *relayJSON `json:"relay,omitempty"`
}

// state is GET /api/firstrun/state: "pin" without the cookie, else relay → connecting → link.
func (s *Server) state(w http.ResponseWriter, r *http.Request) error {
	if !s.signedIn(r) {
		wire.WriteJSON(w, http.StatusOK, stateJSON{Step: "pin"})
		return nil
	}
	hs, err := s.st.Settings().Get(r.Context())
	if err != nil {
		return err
	}
	out := stateJSON{HomeName: hs.HomeName}
	rs, ready := s.relayStatus()
	switch {
	case !rs.Configured:
		out.Step = "relay"
	case ready:
		out.Step, out.Relay = "link", &rs
	default:
		out.Step, out.Relay = "connecting", &rs
	}
	wire.WriteJSON(w, http.StatusOK, out)
	return nil
}

// connect is POST /api/firstrun/relay. A code for another domain is fine here: nobody has a passkey yet, so no
// passkey can be locked out by the new relying party ID.
func (s *Server) connect(w http.ResponseWriter, r *http.Request) error {
	var body struct {
		Code string `json:"code"`
	}
	if err := wire.Decode(w, r, &body); err != nil {
		return err
	}
	code, err := relay.ParseCode(body.Code)
	if err != nil {
		return wire.Invalid("/code", "invalid", err.Error()) // ParseCode errors never contain the token
	}
	ctx := r.Context()
	if err := s.relay.Connect(context.WithoutCancel(ctx), code); err != nil {
		return fmt.Errorf("firstrun: connect the relay: %w", err)
	}
	s.record(ctx, store.AuditRecord{At: s.now(), Via: "admin", Action: "relay.change", Target: code.Domain, OK: true})
	s.log.Info("firstrun: relay code applied", "domain", code.Domain)
	rs, _ := s.relayStatus()
	wire.WriteJSON(w, http.StatusAccepted, rs)
	return nil
}

var errRelayNotReady = wire.Errorf(http.StatusConflict, "relay_not_ready",
	"the relay is not connected yet, or https://<domain> does not reach this hub")

// link is POST /api/firstrun/link: a new setup link for the relay domain. Every call revokes the older links.
func (s *Server) link(w http.ResponseWriter, r *http.Request) error {
	if err := wire.Decode(w, r, &struct{}{}); err != nil {
		return err
	}
	rs, ready := s.relayStatus()
	if !ready {
		return errRelayNotReady
	}
	l, err := auth.NewSetupLink(r.Context(), s.st, s.audit, auth.ForDomain(rs.Domain).Origin, "admin", s.now())
	if err != nil {
		return err
	}
	wire.WriteJSON(w, http.StatusOK, map[string]string{"url": l.URL, "expiresAt": wire.Time(l.ExpiresAt)})
	return nil
}

// events is GET /api/firstrun/events: server-sent "relay" events with the full RelayStatus whenever it changes. It
// ends when the client goes, the cookie expires or the home gets its owner.
func (s *Server) events(w http.ResponseWriter, r *http.Request) error {
	flusher, ok := w.(http.Flusher)
	if !ok {
		return errors.New("firstrun: the connection cannot stream")
	}
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("X-Accel-Buffering", "no")
	w.WriteHeader(http.StatusOK)
	if _, err := io.WriteString(w, "retry: 3000\n\n"); err != nil {
		return nil //nolint:nilerr // the client is gone; nothing to answer
	}
	var (
		seq  int
		last []byte
	)
	send := func() error {
		rs, _ := s.relayStatus()
		b, err := wire.Marshal(rs)
		if err != nil || bytes.Equal(b, last) {
			return err
		}
		last, seq = b, seq+1
		if _, err := fmt.Fprintf(w, "id: %d\nevent: relay\ndata: %s\n\n", seq, b); err != nil {
			return err
		}
		flusher.Flush()
		return nil
	}
	if send() != nil {
		return nil
	}
	poll, ping := time.NewTicker(s.pollEvery), time.NewTicker(s.pingEvery)
	defer poll.Stop()
	defer ping.Stop()
	ctx := r.Context()
	for {
		select {
		case <-ctx.Done():
			return nil
		case <-poll.C:
			if !s.signedIn(r) || send() != nil {
				return nil
			}
		case <-ping.C:
			if ready, err := auth.OwnerReady(ctx, s.st); err != nil || ready {
				return nil
			}
			if _, err := io.WriteString(w, ": ping\n\n"); err != nil {
				return nil //nolint:nilerr // the client is gone
			}
			flusher.Flush()
		}
	}
}

// record writes an admin audit record; it outlives the request.
func (s *Server) record(ctx context.Context, rec store.AuditRecord) {
	rec.Kind = store.AuditAdmin
	ctx = context.WithoutCancel(ctx)
	if s.audit != nil {
		s.audit.RecordAdmin(ctx, rec)
		return
	}
	if _, err := s.st.Audit().Append(ctx, []store.AuditRecord{rec}); err != nil {
		s.log.Error("firstrun: cannot store an audit record", "action", rec.Action, "err", err)
	}
}
