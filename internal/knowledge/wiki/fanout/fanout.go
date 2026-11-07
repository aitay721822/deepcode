// Package fanout runs the wiki's model calls a few at a time: the sections'
// plans, then the pages.
package fanout

import (
	"context"
	"fmt"
	"sync"
)

// Each runs fn for each i from 0 to n-1, parallel at a time (one when that
// is less), and returns the first error, after which no more start and the
// ctx fn was given is cancelled. A panic in fn is that error, as a caller's
// recover cannot reach another goroutine.
func Each(ctx context.Context, n, parallel int, fn func(ctx context.Context, i int) error) error {
	ctx, cancel := context.WithCancelCause(ctx)
	defer cancel(nil)
	slots := make(chan struct{}, max(parallel, 1))
	var wg sync.WaitGroup
	for i := 0; i < n; i++ {
		select {
		case slots <- struct{}{}:
		case <-ctx.Done():
		}
		if ctx.Err() != nil {
			break
		}
		wg.Add(1)
		go func() {
			defer wg.Done()
			defer func() { <-slots }()
			defer func() {
				if v := recover(); v != nil {
					cancel(fmt.Errorf("panic: %v", v))
				}
			}()
			if err := fn(ctx, i); err != nil {
				cancel(err)
			}
		}()
	}
	wg.Wait()
	return context.Cause(ctx)
}
