package fanout

import (
	"context"
	"errors"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
)

// TestEach: every i runs, never more than parallel at once (one for 0); the
// first error, or a panic, ends it, none starting after; a cancelled ctx
// stops it with its cause.
func TestEach(t *testing.T) {
	for _, parallel := range []int{0, 1, 3, 50} {
		var running, most atomic.Int32
		var mu sync.Mutex
		seen := map[int]bool{}
		err := Each(context.Background(), 10, parallel, func(_ context.Context, i int) error {
			now := running.Add(1)
			defer running.Add(-1)
			for m := most.Load(); now > m && !most.CompareAndSwap(m, now); m = most.Load() {
			}
			mu.Lock()
			defer mu.Unlock()
			seen[i] = true
			return nil
		})
		if err != nil || len(seen) != 10 || int(most.Load()) > max(parallel, 1) {
			t.Errorf("parallel %d: %v, %d ran, %d at once", parallel, err, len(seen), most.Load())
		}
	}
	boom := errors.New("boom")
	var started atomic.Int32
	if err := Each(context.Background(), 10, 1, func(_ context.Context, i int) error {
		started.Add(1)
		if i == 2 {
			return boom
		}
		return nil
	}); !errors.Is(err, boom) || started.Load() != 3 {
		t.Errorf("an error: %v after %d started", err, started.Load())
	}
	if err := Each(context.Background(), 3, 2, func(context.Context, int) error { panic("bad") }); err == nil || !strings.Contains(err.Error(), "panic: bad") {
		t.Errorf("a panic: %v", err)
	}
	ctx, cancel := context.WithCancelCause(context.Background())
	cancel(boom)
	if err := Each(ctx, 3, 2, func(context.Context, int) error { return nil }); !errors.Is(err, boom) {
		t.Errorf("cancelled: %v", err)
	}
}
