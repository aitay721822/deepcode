package session

import (
	"sync"
	"time"
)

// limiter counts failures per key in fixed windows. An attempt is counted as
// it starts and given back (undo) when it does not fail, so parallel attempts
// cannot all pass before the first failure is counted.
// ponytail: in memory, so a restart forgets the counts; keep them in SQLite if
// several processes ever serve one database. Every key holds an attempt that
// cost a PBKDF2 check within the window, so the map is as large as the checks
// the CPU runs in fifteen minutes; cap it if that ever matters.
type limiter struct {
	mu     sync.Mutex
	max    int
	window time.Duration
	counts map[string]*count
	swept  time.Time // when windows that have passed were last dropped
}

type count struct {
	n     int
	since time.Time
}

// take counts an attempt for key in its current window and returns that
// window, or returns nil and counts nothing when key already has max.
func (l *limiter) take(key string) *count {
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.counts == nil {
		l.counts = map[string]*count{}
	}
	if len(l.counts) > 10000 && time.Since(l.swept) >= time.Minute { // at most once a minute: a full scan
		for k, c := range l.counts {
			if time.Since(c.since) >= l.window {
				delete(l.counts, k)
			}
		}
		l.swept = time.Now()
	}
	c := l.counts[key]
	if c == nil || time.Since(c.since) >= l.window {
		c = &count{since: time.Now()}
		l.counts[key] = c
	}
	if c.n >= l.max {
		return nil
	}
	c.n++
	return c
}

// undo gives back an attempt that take counted in window c and that did not
// fail. Once key's window has turned, nothing of c is left to give back.
func (l *limiter) undo(key string, c *count) {
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.counts[key] != c {
		return
	}
	if c.n--; c.n <= 0 {
		delete(l.counts, key)
	}
}
