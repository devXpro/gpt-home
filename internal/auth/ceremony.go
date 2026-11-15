package auth

import (
	"sync"
	"time"

	"github.com/go-webauthn/webauthn/webauthn"

	"github.com/devXpro/gpt-home/internal/auth/wire"
	"github.com/devXpro/gpt-home/internal/store"
)

// ceremonyKind tells the four passkey ceremonies apart; a ceremony can only be finished by its own endpoint.
type ceremonyKind uint8

const (
	kindRegister   ceremonyKind = iota + 1 // POST /auth/register: a setup link or an invite
	kindLogin                              // POST /auth/login: discoverable sign-in
	kindStepUp                             // POST /auth/stepup: a fresh assertion in a session
	kindAddPasskey                         // POST /me/passkeys: another passkey for the signed-in person
)

// maxCeremonies bounds the pending ceremonies; the rate limits keep the public ones far below it.
const maxCeremonies = 1000

// ceremony is the server side of a begun passkey ceremony (ADMIN-API §8: in memory, 5 minutes, single use).
type ceremony struct {
	kind    ceremonyKind
	rp      RelyingParty // the relying party it began with; finish verifies against it
	data    webauthn.SessionData
	expires time.Time

	person    store.Person // the person registering or asserting; for a new owner, the person to create (no ID yet)
	newPerson bool         // kindRegister: person does not exist yet and is created at finish
	sessionID string       // kindStepUp, kindAddPasskey: the session that began it

	tokenHash  string // kindRegister: the setup or invite token, consumed at finish
	personName string // kindRegister: a new name for the person; "" keeps it
	homeName   string // kindRegister with a setup token: a new home name; "" keeps it
}

// ceremonies holds the pending ceremonies by id.
type ceremonies struct {
	mu sync.Mutex
	m  map[string]*ceremony
}

func newCeremonies() *ceremonies { return &ceremonies{m: map[string]*ceremony{}} }

// add stores c and returns its random id. ok is false when too many ceremonies are pending.
func (cs *ceremonies) add(c *ceremony, now time.Time) (id string, ok bool) {
	cs.mu.Lock()
	defer cs.mu.Unlock()
	for k, v := range cs.m {
		if !now.Before(v.expires) {
			delete(cs.m, k)
		}
	}
	if len(cs.m) >= maxCeremonies {
		return "", false
	}
	id = wire.Random(18)
	cs.m[id] = c
	return id, true
}

// take removes and returns the ceremony with id when it is unexpired and match accepts it. A ceremony that match
// refuses (another kind or session) stays, so a stray request cannot burn somebody else's ceremony.
func (cs *ceremonies) take(id string, now time.Time, match func(*ceremony) bool) (*ceremony, bool) {
	cs.mu.Lock()
	defer cs.mu.Unlock()
	c, ok := cs.m[id]
	if !ok || !match(c) {
		return nil, false
	}
	delete(cs.m, id)
	if !now.Before(c.expires) {
		return nil, false
	}
	return c, true
}

// clear drops every pending ceremony (reset-access).
func (cs *ceremonies) clear() {
	cs.mu.Lock()
	defer cs.mu.Unlock()
	clear(cs.m)
}
