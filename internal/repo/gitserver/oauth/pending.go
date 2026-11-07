package oauth

import (
	"crypto/rand"
	"encoding/base64"
	"sync"
	"time"
)

// ttl is how long an admin has on the consent page.
const ttl = 10 * time.Minute

// Pending holds the authorizations begun and not yet finished, by their
// state: the value the consent page sends back, which only the admin who
// began can finish with, so no one can slip their own code into the callback.
// The zero value is ready to use.
// ponytail: in memory, per process; a restart midway means authorizing again.
type Pending struct {
	mu sync.Mutex
	m  map[string]pending
}

type pending struct {
	server, user int64
	redirect     string
	at           time.Time
}

// Begin records that user is authorizing server, to come back to redirect,
// and returns the state to send along.
func (p *Pending) Begin(server, user int64, redirect string) string {
	b := make([]byte, 32)
	rand.Read(b) // cannot fail since Go 1.24
	state := base64.RawURLEncoding.EncodeToString(b)
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.m == nil {
		p.m = map[string]pending{}
	}
	for k, v := range p.m { // the abandoned ones
		if time.Since(v.at) > ttl {
			delete(p.m, k)
		}
	}
	p.m[state] = pending{server, user, redirect, time.Now()}
	return state
}

// Finish ends the authorization user began with state, once: its server and
// redirect URI, or false when there is none, it expired, or another user
// began it.
func (p *Pending) Finish(state string, user int64) (server int64, redirect string, ok bool) {
	p.mu.Lock()
	defer p.mu.Unlock()
	v, ok := p.m[state]
	delete(p.m, state)
	if !ok || v.user != user || time.Since(v.at) > ttl {
		return 0, "", false
	}
	return v.server, v.redirect, true
}
