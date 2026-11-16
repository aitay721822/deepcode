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
	"io"
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
// that Stop ends every process it starts.
func Start(line string, env []string, stderr *os.File) (*Process, error) {
	args, err := Split(line)
	if err != nil {
		return nil, err
	}
	if len(args) == 0 {
		return nil, errors.New("no command")
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
		return nil, err
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
		return nil, errors.New("a quote is not closed")
	}
	if started {
		args = append(args, word.String())
	}
	return args, nil
}

// Name is the package a runner (npx, bunx, pnpm dlx, uvx) runs in line, the
// first scoped one (@scope/pkg) else the first word after the runner, without
// its scope and version; else the program's name; acp when line names none.
// No word with :// in it is taken, nor after a runner one with a colon, as a
// flag's value (a registry's URL, user:k3y@host) may hold a key; an unscoped
// package after a flag's value given apart from it (--token k3y pkg) is not
// found, so give that one as --token=k3y.
func Name(line string) string {
	var words []string
	args, _ := Split(line)
	for _, w := range args {
		if !strings.HasPrefix(w, "-") && w != "dlx" && w != "x" && !strings.Contains(w, "://") {
			words = append(words, w)
		}
	}
	base := func(w string) string { return w[strings.LastIndexAny(w, `/\`)+1:] }
	name := ""
	if len(words) > 0 {
		name = strings.ToLower(base(words[0]))
		name = strings.TrimSuffix(name, filepath.Ext(name))
	}
	if slices.Contains([]string{"npx", "bunx", "pnpx", "pnpm", "yarn", "bun", "uvx"}, name) {
		pkgs := slices.DeleteFunc(words[1:], func(w string) bool { return strings.Contains(w, ":") })
		if i := slices.IndexFunc(pkgs, func(w string) bool { return strings.HasPrefix(w, "@") && strings.Contains(w, "/") }); i > 0 {
			pkgs = pkgs[i:]
		}
		if len(pkgs) > 0 {
			name, _, _ = strings.Cut(base(strings.TrimPrefix(pkgs[0], "@")), "@")
		}
	}
	return cmp.Or(name, "acp")
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
