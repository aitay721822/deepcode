package git

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"time"
)

// ErrTooLarge is a download that added more than the client's MaxBytes to a
// clone.
var ErrTooLarge = errors.New("the download passed the size limit")

// measureEvery is how often a running download's size is looked at; a test
// slows it to reach the look at the end.
var measureEvery = 250 * time.Millisecond

// diskUsage is the size of the files under dir. A file git renames or removes
// while it counts is left out.
func diskUsage(dir string) int64 {
	var n int64
	filepath.Walk(dir, func(_ string, info os.FileInfo, err error) error {
		if err == nil && info.Mode().IsRegular() {
			n += info.Size()
		}
		return nil
	})
	return n
}

// watch stops ctx with ErrTooLarge once the files under dir pass limit bytes,
// looking every so often. It returns when ctx ends.
func watch(ctx context.Context, stop context.CancelCauseFunc, dir string, limit int64, every time.Duration) {
	tick := time.NewTicker(every)
	defer tick.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-tick.C:
			if diskUsage(dir) > limit {
				stop(ErrTooLarge)
				return
			}
		}
	}
}
