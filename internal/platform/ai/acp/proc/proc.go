// Package proc runs an agent's process for package acp: its command line
// split as a shell splits one, a double-quoted part kept whole, and the name
// it gives the agent; its environment only what the OS, Node, npm and a proxy
// need, and what acp adds; and its whole process tree ended with it (a Windows
// job object, a process group elsewhere), since an agent started by npx runs
// node, which runs the agent's own CLI, which starts more (Codex's git).
package proc

import (
	"cmp"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"time"
	"unicode"
)

// Process is a running agent: Stdin and Stdout are its pipes.
type Process struct {
	Stdin  io.WriteCloser
	Stdout io.ReadCloser
	tree   tree
	done   chan struct{}
	stop   sync.Once
}

// Start runs line with env, nothing else of this process's environment, its
// standard error written to stderr (nil discards it), and its tree held so
// that Stop ends every process it starts. No shell runs line: one starting
// with a variable set (assignment: KEY=k3y, $env:KEY=k3y) or a URL is ErrLine,
// without the word, which may hold a key; a program that cannot start is its
// cause, without its path (unquoted).
func Start(line string, env []string, stderr *os.File) (*Process, error) {
	args, err := Split(line)
	if err != nil {
		return nil, err
	}
	if len(args) == 0 {
		return nil, fmt.Errorf("%w: no command", ErrLine)
	}
	if assignment(args[0]) || strings.Contains(args[0], "://") {
		return nil, fmt.Errorf("%w: a variable or a URL for the command, which no shell runs", ErrLine)
	}
	cmd := exec.Command(args[0], args[1:]...)
	cmd.Env = env
	if stderr != nil { // a file, not a writer: Wait copies a writer's, and a child holding the pipe would hold Wait
		cmd.Stderr = stderr
	}
	group(cmd)
	p := &Process{done: make(chan struct{})}
	if p.Stdin, err = cmd.StdinPipe(); err != nil {
		return nil, err
	}
	out, w, err := os.Pipe() // not StdoutPipe, which Wait closes once the process ends, maybe before its last words are read
	if err != nil {
		p.Stdin.Close()
		return nil, err
	}
	cmd.Stdout, p.Stdout = w, out
	err = cmd.Start()
	w.Close() // the process holds its own
	if err != nil {
		out.Close()
		return nil, unquoted(err)
	}
	if p.tree, err = hold(cmd.Process); err != nil {
		_ = cmd.Process.Kill()
		_ = cmd.Wait()
		out.Close()
		return nil, err
	}
	go func() {
		_ = cmd.Wait()
		close(p.done)
	}()
	return p, nil
}

// unquoted is a failed start's cause without the program's path, which exec's
// errors (*exec.Error, *fs.PathError) quote whole and which may hold a key
// (/x/a=k3y, //user:k3y@host/agent); the caller names the agent. A
// *fs.PathError is looked for first, as an *exec.Error may hold one (Windows'
// stat of the program).
func unquoted(err error) error {
	var pe *fs.PathError
	if errors.As(err, &pe) {
		return fmt.Errorf("the program: %s: %w", pe.Op, pe.Err)
	}
	var ee *exec.Error
	if errors.As(err, &ee) {
		return fmt.Errorf("the program: %w", ee.Err)
	}
	return err
}

// Done is closed once the process has ended.
func (p *Process) Done() <-chan struct{} { return p.done }

// Stop ends the process, once: it closes its standard input, which an ACP
// agent ends on, waits up to grace for it to end, then ends its whole tree,
// whatever the process left running, and closes its standard output.
func (p *Process) Stop(grace time.Duration) {
	p.stop.Do(func() {
		_ = p.Stdin.Close()
		select {
		case <-p.done:
		case <-time.After(grace):
		}
		p.tree.kill()
		<-p.done
		_ = p.Stdout.Close()
	})
}

// ErrLine is a command line that names no program to run: the setting is
// wrong.
var ErrLine = errors.New("the agent's command line is not one")

// Split is line's words: split at spaces, a double-quoted part kept whole
// without its quotes ("C:\Program Files\nodejs\npx.cmd" -y pkg). A backslash
// is a character, as in a Windows path.
func Split(line string) ([]string, error) {
	var args []string
	var word strings.Builder
	quoted, started := false, false
	for _, r := range line {
		switch {
		case r == '"':
			quoted, started = !quoted, true
		case unicode.IsSpace(r) && !quoted:
			if started {
				args, started = append(args, word.String()), false
				word.Reset()
			}
		default:
			word.WriteRune(r)
			started = true
		}
	}
	if quoted {
		return nil, fmt.Errorf("%w: a quote is not closed", ErrLine)
	}
	if started {
		args = append(args, word.String())
	}
	return args, nil
}

// assignment is whether w sets a variable, as a shell's NAME=value or
// PowerShell's $env:NAME=value would (KEY=k3y): a word with = and no / or \
// before it, which a path to a program has (C:\a=b\npx.cmd, ./a=b/x); a
// program on the PATH named a=b is taken for one.
func assignment(w string) bool {
	name, _, ok := strings.Cut(w, "=")
	return ok && !strings.ContainsAny(name, `/\`)
}

// switches are the runners' flags that take no value.
var switches = []string{"--", "-y", "--yes", "-q", "--quiet", "-s", "--silent", "--offline", "--prefer-offline", "--bun", "--no-install", "--ignore-scripts"}

// Name is the package a runner (npx, bunx, pnpm dlx, uvx) runs in line, the
// first scoped one (@scope/pkg) else the first word after the runner, without
// its scope and version (@1.2, ==1.2); else the program's name, up to an =.
// The program is line's first word, as Start runs it; Name is acp when there
// is none, its name is empty, or it is no program's (a flag, an assignment, a
// URL), and then takes none of the words after it.
//
// A flag's value may hold a key (--token k3y, a registry's URL, user:k3y@host),
// so none is taken: the word after a flag that is not one of the runners'
// switches (and --) and has no =, unless a scoped package, any word with a
// colon, and one starting with @ that is no scope.
func Name(line string) string {
	args, _ := Split(line)
	base := func(w string) string { return w[strings.LastIndexAny(w, `/\`)+1:] }
	if len(args) == 0 || strings.HasPrefix(args[0], "-") || assignment(args[0]) || strings.Contains(args[0], "://") {
		return "acp"
	}
	name, _, _ := strings.Cut(strings.ToLower(base(args[0])), "=")
	name = strings.TrimSuffix(name, filepath.Ext(name))
	if !slices.Contains([]string{"npx", "bunx", "pnpx", "pnpm", "yarn", "bun", "uvx"}, name) {
		return cmp.Or(name, "acp")
	}
	scoped := func(w string) bool { return strings.HasPrefix(w, "@") && strings.Contains(w, "/") }
	var pkgs []string
	for rest := args[1:]; len(rest) > 0; rest = rest[1:] {
		switch w := rest[0]; {
		case strings.HasPrefix(w, "-"):
			if !strings.Contains(w, "=") && !slices.Contains(switches, w) && len(rest) > 1 && !scoped(rest[1]) {
				rest = rest[1:] // its value
			}
		case w != "dlx" && w != "x" && !strings.Contains(w, ":") && (scoped(w) || !strings.HasPrefix(w, "@")):
			pkgs = append(pkgs, w)
		}
	}
	if j := slices.IndexFunc(pkgs, scoped); j > 0 {
		pkgs = pkgs[j:]
	}
	if len(pkgs) == 0 {
		return name
	}
	pkg := pkgs[0]
	if assignment(pkg) { // KEY=x/k3y: what follows = is no package's
		pkg, _, _ = strings.Cut(pkg, "=")
	}
	pkg = base(pkg)
	if k := strings.IndexAny(pkg, "@=<>~!"); k >= 0 {
		pkg = pkg[:k]
	}
	return cmp.Or(pkg, name)
}

// passed are the variables an agent gets from this process's environment:
// what the OS, Node, npm (NPM_CONFIG_*) and a proxy need. Never deepcode's
// own settings and keys (DEEPCODE_*, DS_APIKEY, OPENAI_*, which would also turn
// Codex to API billing), nor a host agent's (CLAUDE*, ANTHROPIC_*), but for
// CLAUDE_CODE_GIT_BASH_PATH, the bash Claude Code needs on Windows, a path.
var passed = []string{"PATH", "PATHEXT", "SYSTEMROOT", "SYSTEMDRIVE", "WINDIR", "COMSPEC", "TEMP", "TMP", "TMPDIR", "USERPROFILE",
	"HOMEDRIVE", "HOMEPATH", "HOME", "USER", "LOGNAME", "SHELL", "APPDATA", "LOCALAPPDATA", "PROGRAMDATA", "ALLUSERSPROFILE",
	"PROGRAMFILES", "PROGRAMFILES(X86)", "PROGRAMW6432", "COMMONPROGRAMFILES", "COMMONPROGRAMFILES(X86)", "COMMONPROGRAMW6432",
	"PUBLIC", "USERNAME", "USERDOMAIN", "COMPUTERNAME", "OS", "NUMBER_OF_PROCESSORS", "PROCESSOR_ARCHITECTURE", "LANG", "LC_ALL",
	"XDG_CONFIG_HOME", "XDG_DATA_HOME", "XDG_CACHE_HOME", "XDG_STATE_HOME", "XDG_RUNTIME_DIR",
	"HTTP_PROXY", "HTTPS_PROXY", "NO_PROXY", "NODE_EXTRA_CA_CERTS", "SSL_CERT_FILE", "SSL_CERT_DIR", "CLAUDE_CODE_GIT_BASH_PATH"}

// Env is the passed variables of this process's environment, then extra
// (NAME=value), which win over them.
func Env(extra ...string) []string {
	var out []string
	for _, kv := range os.Environ() {
		name, _, _ := strings.Cut(kv, "=")
		if name = strings.ToUpper(name); slices.Contains(passed, name) || strings.HasPrefix(name, "NPM_CONFIG_") {
			out = append(out, kv)
		}
	}
	return append(out, extra...)
}
