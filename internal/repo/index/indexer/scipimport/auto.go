package scipimport

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path"
	"path/filepath"
	"slices"
	"strings"
	"time"

	"deepcode/internal/repo/git"
	"deepcode/internal/repo/index/indexer/store"
)

const (
	maxCheckoutBytes = 128 << 20
	maxCheckoutFiles = 20000
	maxModules       = 16
)

// Generate indexes the pinned commit, never a live checkout. The image is
// configured by the administrator, not the repository being indexed.
func Generate(ctx context.Context, s *store.Store, gitc *git.Client, dir, sha string, snapshot int64, image string) (int, error) {
	tree, err := gitc.LsTree(ctx, dir, sha)
	if err != nil {
		return 0, err
	}
	files := goBlobs(tree)
	modules := moduleRoots(tree)
	if len(modules) == 0 || len(files) == 0 {
		return 0, nil
	}
	if len(modules) > maxModules {
		return 0, fmt.Errorf("SCIP checkout exceeds %d Go modules", maxModules)
	}
	docker, err := exec.LookPath("docker")
	if err != nil {
		return 0, fmt.Errorf("SCIP requires a local Docker CLI: %w", err)
	}
	scratch, err := os.MkdirTemp("", "deepcode-scip-")
	if err != nil {
		return 0, err
	}
	defer os.RemoveAll(scratch)
	src := filepath.Join(scratch, "src")
	if err := materialize(ctx, gitc, dir, src, tree); err != nil {
		return 0, err
	}
	var rows []store.SCIPOccurrence
	definitions := make(map[string]string)
	for i, module := range modules {
		name := fmt.Sprintf("%s-%d", filepath.Base(scratch), i)
		raw, err := runSCIP(ctx, docker, image, src, module, sha, name)
		if err != nil {
			return 0, fmt.Errorf("SCIP module %q: %w", module, err)
		}
		if len(raw) == 0 {
			continue // The container found no packages for this build.
		}
		moduleRows, err := readIndex(ctx, s, snapshot, module, files, raw, nil)
		if err != nil {
			return 0, fmt.Errorf("SCIP module %q: %w", module, err)
		}
		if len(rows)+len(moduleRows) > maxOccurrences {
			return 0, fmt.Errorf("SCIP checkout exceeds %d retained occurrences", maxOccurrences)
		}
		for _, row := range moduleRows {
			if row.DefinitionName != "" {
				if other, ok := definitions[row.Symbol]; ok && other != module {
					return 0, fmt.Errorf("SCIP modules %q and %q define the same symbol", other, module)
				}
				definitions[row.Symbol] = module
			}
		}
		rows = append(rows, moduleRows...)
	}
	if len(rows) == 0 {
		return 0, nil
	}
	if err := s.IndexSCIP(ctx, snapshot, rows); err != nil {
		return 0, err
	}
	return len(rows), nil
}

func moduleRoots(tree []git.TreeEntry) []string {
	var roots []string
	for _, entry := range tree {
		if !validPath(entry.Path) || path.Base(entry.Path) != "go.mod" {
			continue
		}
		dir := path.Dir(entry.Path)
		if dir == "." {
			roots = append(roots, "")
			continue
		}
		if !slices.ContainsFunc(strings.Split(dir, "/"), func(part string) bool {
			return part == "vendor" || part == "testdata" || strings.HasPrefix(part, ".") || strings.HasPrefix(part, "_")
		}) {
			roots = append(roots, dir)
		}
	}
	return roots
}

func materialize(ctx context.Context, gitc *git.Client, dir, dst string, tree []git.TreeEntry) error {
	var size int64
	for _, entry := range tree {
		if entry.Size < 0 || entry.Size > maxCheckoutBytes-size {
			return fmt.Errorf("SCIP checkout exceeds %d MiB", maxCheckoutBytes>>20)
		}
		size += entry.Size
	}
	if len(tree) > maxCheckoutFiles {
		return fmt.Errorf("SCIP checkout exceeds %d files", maxCheckoutFiles)
	}
	if err := os.Mkdir(dst, 0755); err != nil {
		return err
	}
	root, err := os.OpenRoot(dst)
	if err != nil {
		return err
	}
	defer root.Close()
	blobs, err := gitc.OpenBlobs(ctx, dir)
	if err != nil {
		return err
	}
	defer blobs.Close()
	for _, entry := range tree {
		if !validPath(entry.Path) || slices.ContainsFunc(strings.Split(entry.Path, "/"), func(part string) bool {
			return strings.EqualFold(part, ".git")
		}) {
			return fmt.Errorf("unsupported path in SCIP checkout: %q", entry.Path)
		}
		name, err := filepath.Localize(entry.Path)
		if err != nil {
			return err
		}
		if err := root.MkdirAll(filepath.Dir(name), 0755); err != nil {
			return err
		}
		raw, err := blobs.Read(entry.SHA)
		if err != nil {
			return err
		}
		f, err := root.OpenFile(name, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0644)
		if err != nil {
			return err
		}
		_, err = f.Write(raw)
		closeErr := f.Close()
		if err != nil {
			return err
		}
		if closeErr != nil {
			return closeErr
		}
	}
	return nil
}

func runSCIP(ctx context.Context, docker, image, src, module, sha, name string) ([]byte, error) {
	if strings.HasPrefix(image, "-") || image == "" {
		return nil, fmt.Errorf("invalid SCIP image name")
	}
	if strings.ContainsAny(src, ",\"\r\n") {
		return nil, fmt.Errorf("SCIP checkout path contains unsupported Docker mount characters")
	}
	runCtx, stop := context.WithCancel(ctx)
	defer stop()
	cmd := exec.CommandContext(runCtx, docker, "run", "--pull=never", "--rm", "--name", name,
		"--network=none", "--read-only", "--user", "65534:65534", "--cap-drop=ALL", "--security-opt=no-new-privileges",
		"--memory=1g", "--memory-swap=1g", "--cpus=2", "--pids-limit=128", "--log-driver=none",
		"--tmpfs", "/tmp:rw,noexec,nosuid,nodev,size=512m,mode=1777",
		"--mount", "type=bind,source="+src+",target=/src,readonly",
		"--env", "HOME=/tmp", "--env", "GOCACHE=/tmp/go-cache", "--env", "GOMODCACHE=/opt/go-mod",
		"--env", "GOENV=off", "--env", "GOWORK=off", "--env", "GOPACKAGESDRIVER=off", "--env", "GOTOOLCHAIN=local", "--env", "GOTELEMETRY=off",
		"--env", "GOFLAGS=-buildvcs=false", "--env", "GOPROXY=off", "--env", "GOSUMDB=off", "--env", "CGO_ENABLED=0",
		"--workdir", path.Join("/src", module), "--entrypoint", "/bin/sh", image, "-c",
		`packages=$(go list ./...) || exit $?
[ -n "$packages" ] || exit 0
go list -deps ./... >/dev/null || exit $?
exec /usr/local/bin/scip-go index --quiet --skip-tests --skip-implementations --module-version="$1" --output=- ./...`,
		"index", sha)
	cmd.WaitDelay = 2 * time.Second
	out := indexOutput{stop: stop}
	cmd.Stdout = &out
	if err := cmd.Run(); err != nil {
		// Killing a Docker client does not necessarily stop its container.
		cleanup, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_ = exec.CommandContext(cleanup, docker, "rm", "-f", name).Run()
		if ctx.Err() != nil {
			return nil, ctx.Err()
		}
		if out.over {
			return nil, fmt.Errorf("SCIP output exceeds %d MiB", maxIndex>>20)
		}
		return nil, fmt.Errorf("scip-go container failed (verify the local image, Docker permissions and offline dependencies): %w", err)
	}
	return out.buf.Bytes(), nil
}

type indexOutput struct {
	buf  bytes.Buffer
	over bool
	stop context.CancelFunc
}

func (o *indexOutput) Write(p []byte) (int, error) {
	if len(p) > maxIndex-o.buf.Len() {
		o.over = true
		o.stop()
		return 0, errors.New("SCIP output limit reached")
	}
	return o.buf.Write(p)
}
