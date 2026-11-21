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
func Generate(ctx context.Context, s *store.Store, gitc *git.Client, dir, sha string, snapshot int64, images Images, logf func(string, ...any)) (int, error) {
	tree, err := gitc.LsTree(ctx, dir, sha)
	if err != nil {
		return 0, err
	}
	if !slices.ContainsFunc(specs, func(spec languageSpec) bool {
		return spec.image(images) != "" && len(spec.blobs(tree)) > 0 && len(spec.roots(tree)) > 0
	}) {
		return 0, nil
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
	for i, spec := range specs {
		image := spec.image(images)
		if image == "" {
			continue
		}
		languageCtx, cancel := context.WithTimeout(ctx, 2*time.Minute)
		languageRows, err := generateLanguage(languageCtx, s, snapshot, tree, docker, image, src, sha, fmt.Sprintf("%s-%d", filepath.Base(scratch), i), spec)
		cancel()
		if ctx.Err() != nil {
			return 0, ctx.Err()
		}
		if err != nil {
			logf("%s SCIP unavailable; kept the base index: %v", spec.name, err)
			continue
		}
		rows = append(rows, languageRows...)
		if len(languageRows) > 0 {
			logf("automatically indexed %d %s SCIP occurrences", len(languageRows), spec.name)
		}
	}
	if len(rows) == 0 {
		return 0, nil
	}
	if err := s.IndexSCIP(ctx, snapshot, rows); err != nil {
		return 0, err
	}
	return len(rows), nil
}

func generateLanguage(ctx context.Context, s *store.Store, snapshot int64, tree []git.TreeEntry, docker, image, src, sha, prefix string, spec languageSpec) ([]store.SCIPOccurrence, error) {
	files, projects := spec.blobs(tree), spec.roots(tree)
	if len(files) == 0 || len(projects) == 0 {
		return nil, nil
	}
	if len(projects) > maxModules {
		return nil, fmt.Errorf("SCIP checkout exceeds %d %s projects", maxModules, spec.name)
	}
	if spec.deepest {
		slices.SortStableFunc(projects, func(a, b project) int {
			return strings.Count(b.root, "/") - strings.Count(a.root, "/") + boolDepth(b.root) - boolDepth(a.root)
		})
	}
	var rows []store.SCIPOccurrence
	owners := map[string]bool{}
	definitions := map[string]string{}
	for i, p := range projects {
		raw, err := runContainer(ctx, docker, image, src, p, sha, fmt.Sprintf("%s-%d", prefix, i), spec)
		if err != nil {
			return nil, fmt.Errorf("SCIP project %q: %w", p.root, err)
		}
		if len(raw) == 0 {
			if spec.allowEmpty {
				continue
			}
			return nil, fmt.Errorf("SCIP project %q returned no index", p.root)
		}
		documents := map[string]bool{}
		projectRows, err := readLanguageIndex(ctx, s, snapshot, p.root, files, raw, nil, spec, documents)
		if err != nil {
			return nil, fmt.Errorf("SCIP project %q: %w", p.root, err)
		}
		for _, row := range projectRows {
			if spec.deepest && owners[row.Path] {
				continue
			}
			if row.DefinitionName != "" {
				if other, ok := definitions[row.Symbol]; ok && other != p.root {
					return nil, fmt.Errorf("SCIP projects %q and %q define the same symbol", other, p.root)
				}
				definitions[row.Symbol] = p.root
			}
			rows = append(rows, row)
			if len(rows) > maxOccurrences {
				return nil, fmt.Errorf("SCIP exceeds %d retained occurrences", maxOccurrences)
			}
		}
		for name := range documents {
			owners[name] = true
		}
	}
	return rows, nil
}

func boolDepth(root string) int {
	if root != "" {
		return 1
	}
	return 0
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
	return runContainer(ctx, docker, image, src, project{root: module}, sha, name, specs[0])
}

func runContainer(ctx context.Context, docker, image, src string, p project, sha, name string, spec languageSpec) ([]byte, error) {
	if strings.HasPrefix(image, "-") || image == "" {
		return nil, fmt.Errorf("invalid SCIP image name")
	}
	if strings.ContainsAny(src, ",\"\r\n") {
		return nil, fmt.Errorf("SCIP checkout path contains unsupported Docker mount characters")
	}
	runCtx, stop := context.WithCancel(ctx)
	defer stop()
	args := []string{"run", "--pull=never", "--rm", "--name", name,
		"--network=none", "--read-only", "--user", "65534:65534", "--cap-drop=ALL", "--security-opt=no-new-privileges",
		"--memory=1g", "--memory-swap=1g", "--cpus=2", "--pids-limit=128", "--log-driver=none",
		"--tmpfs", "/tmp:rw,noexec,nosuid,nodev,size=512m,mode=1777",
		"--mount", "type=bind,source=" + src + ",target=/src,readonly",
		"--env", "HOME=/tmp", "--env", "XDG_CACHE_HOME=/tmp/cache", "--env", "npm_config_cache=/tmp/npm-cache"}
	for _, env := range spec.env {
		args = append(args, "--env", env)
	}
	infer := ""
	if p.infer {
		infer = "infer"
	}
	args = append(args, "--workdir", path.Join("/src", p.root), "--entrypoint", "/bin/sh", image, "-c", spec.script, "index", sha, infer)
	cmd := exec.CommandContext(runCtx, docker, args...)
	cmd.WaitDelay = 2 * time.Second
	out := indexOutput{stop: stop}
	cmd.Stdout = &out
	var stderr bytes.Buffer
	cmd.Stderr = &limitedErrors{buf: &stderr}
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
		return nil, fmt.Errorf("%s container failed (verify the local image, Docker permissions and offline dependencies): %w: %s", spec.tool, err, stderr.String())
	}
	return out.buf.Bytes(), nil
}

type limitedErrors struct{ buf *bytes.Buffer }

func (w *limitedErrors) Write(p []byte) (int, error) {
	n := len(p)
	if left := 4096 - w.buf.Len(); left > 0 {
		_, _ = w.buf.Write(p[:min(left, n)])
	}
	return n, nil
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
