package cache

import (
	"bytes"
	"context"
	"errors"
	"time"
)

type flight struct {
	done chan struct{}
	val  []byte
	err  error
}

// errPanicked is what callers waiting on a load get when it panics; the panic
// itself goes on up the loading caller's stack.
var errPanicked = errors.New("loading the value failed")

// GetOrLoad returns the cached value for key, or runs load once no matter how
// many callers miss at the same time, caching a successful result for ttl. A
// caller whose ctx ends stops waiting; the load goes on for the others.
func (c *Cache) GetOrLoad(ctx context.Context, key string, ttl time.Duration, load func() ([]byte, error)) ([]byte, error) {
	if v, ok := c.get(key); ok {
		return v, nil
	}
	c.flightMu.Lock()
	if f, ok := c.flights[key]; ok {
		c.flightMu.Unlock()
		select {
		case <-f.done:
			return bytes.Clone(f.val), f.err
		case <-ctx.Done():
			return nil, ctx.Err()
		}
	}
	f := &flight{done: make(chan struct{}), err: errPanicked} // until load returns
	c.flights[key] = f
	c.flightMu.Unlock()

	defer func() {
		c.flightMu.Lock()
		delete(c.flights, key)
		c.flightMu.Unlock()
		close(f.done)
	}()
	f.val, f.err = load()
	if f.err == nil {
		c.set(key, f.val, ttl)
	}
	return bytes.Clone(f.val), f.err
}
