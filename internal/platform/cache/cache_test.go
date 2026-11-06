package cache

import (
	"context"
	"errors"
	"runtime"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

func TestCache(t *testing.T) {
	now := time.Unix(0, 0)
	c := New(20) // every entry below is 5 bytes
	c.now = func() time.Time { return now }

	v := []byte("abc")
	c.set("k1", v, time.Minute)
	v[0] = 'X'
	got, ok := c.get("k1")
	if !ok || string(got) != "abc" {
		t.Fatalf("Set did not copy its input: %q %v", got, ok)
	}
	got[0] = 'Y'
	if again, _ := c.get("k1"); string(again) != "abc" {
		t.Fatalf("Get did not copy its output: %q", again)
	}
	now = now.Add(time.Minute)
	if _, ok := c.get("k1"); ok {
		t.Fatal("expired entry returned")
	}

	for _, k := range []string{"a1", "a2", "a3", "a4"} {
		c.set(k, []byte("vvv"), time.Hour)
	}
	c.get("a1")
	c.set("a5", []byte("vvv"), time.Hour)
	if _, ok := c.get("a2"); ok {
		t.Fatal("least recently used entry survived eviction")
	}
	if _, ok := c.get("a1"); !ok {
		t.Fatal("recently used entry was evicted")
	}
	c.set("big", make([]byte, 64), time.Hour)
	if _, ok := c.get("big"); ok {
		t.Fatal("entry larger than the cache was stored")
	}
}

// TestGetOrLoadPanics: callers waiting on a load that panics get an error,
// not an empty value, and the panic goes on up the loader's stack.
func TestGetOrLoadPanics(t *testing.T) {
	ctx := context.Background()
	c := New(1 << 10)
	started, release := make(chan struct{}), make(chan struct{})
	panicked := make(chan any, 1)
	go func() {
		defer func() { panicked <- recover() }()
		c.GetOrLoad(ctx, "k", time.Minute, func() ([]byte, error) {
			close(started)
			<-release
			panic("bad file")
		})
	}()
	<-started
	waiter := make(chan error, 1)
	go func() {
		_, err := c.GetOrLoad(ctx, "k", time.Minute, func() ([]byte, error) { return []byte("v"), nil })
		waiter <- err
	}()
	// Released only once the waiter waits on the load: after a fixed sleep, a
	// slow machine may not have started it yet, and it would load for itself.
	for deadline := time.Now().Add(10 * time.Second); !joined(); time.Sleep(time.Millisecond) {
		if time.Now().After(deadline) {
			t.Fatal("the waiter never joined the load")
		}
	}
	close(release)
	if err := <-waiter; err == nil {
		t.Fatal("a waiter got a value from a load that panicked")
	}
	if r := <-panicked; r != "bad file" {
		t.Fatalf("the loader's panic: %v", r)
	}
}

// joined reports whether a goroutine waits in GetOrLoad on another caller's
// load: blocked in its select, not in a loader of its own.
func joined() bool {
	buf := make([]byte, 1<<20)
	for _, g := range strings.Split(string(buf[:runtime.Stack(buf, true)]), "\n\n") {
		head, frames, _ := strings.Cut(g, "\n")
		if strings.Contains(head, "[select") && strings.HasPrefix(frames, "deepcode/internal/platform/cache.(*Cache).GetOrLoad(") {
			return true
		}
	}
	return false
}

func TestGetOrLoadLoadsOncePerKey(t *testing.T) {
	ctx := context.Background()
	c := New(1 << 10)
	var calls atomic.Int32
	release := make(chan struct{})
	var wg sync.WaitGroup
	for range 10 {
		wg.Go(func() {
			v, err := c.GetOrLoad(ctx, "k", time.Minute, func() ([]byte, error) {
				calls.Add(1)
				<-release
				return []byte("v"), nil
			})
			if err != nil || string(v) != "v" {
				t.Errorf("got %q, %v", v, err)
			}
		})
	}
	for calls.Load() == 0 {
		time.Sleep(time.Millisecond)
	}
	close(release)
	wg.Wait()
	if n := calls.Load(); n != 1 {
		t.Fatalf("loader ran %d times, want 1", n)
	}

	boom := errors.New("boom")
	for range 2 {
		if _, err := c.GetOrLoad(ctx, "bad", time.Minute, func() ([]byte, error) { calls.Add(1); return nil, boom }); err != boom {
			t.Fatalf("got %v, want boom", err)
		}
	}
	if n := calls.Load(); n != 3 {
		t.Fatalf("a failed load was cached (%d calls)", n)
	}
}
