package scipimport

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"
)

func TestSCIPContainerCancellation(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("fixture uses a POSIX shell")
	}
	dir := t.TempDir()
	docker := filepath.Join(dir, "docker")
	t.Setenv("TEST_DOCKER_ARGS", filepath.Join(dir, "args"))
	if err := os.WriteFile(docker, []byte(`#!/bin/sh
if [ "$1" = run ]; then
  printf '%s\n' "$@" > "$TEST_DOCKER_ARGS"
  exec sleep 60
fi
printf '%s\n' "$@" >> "$TEST_DOCKER_ARGS"
`), 0700); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(t.Context(), time.Second)
	defer cancel()
	if _, err := runSCIP(ctx, docker, "local-test-image", dir, "", "commit", "test-scip"); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("expected timeout, got %v", err)
	}
	raw, err := os.ReadFile(filepath.Join(dir, "args"))
	if err != nil {
		t.Fatal(err)
	}
	for _, flag := range []string{
		"\n--pull=never\n", "\n--network=none\n", "\n--read-only\n", "\n--user\n65534:65534\n", "\n--cap-drop=ALL\n",
		"\n--memory=1g\n", "\n--pids-limit=128\n", "\nGOPROXY=off\n", ",target=/src,readonly\n", "\nrm\n-f\ntest-scip\n",
	} {
		if !strings.Contains(string(raw), flag) {
			t.Errorf("missing Docker argument sequence %q", flag)
		}
	}
}
