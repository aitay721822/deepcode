//go:build unix

package proc

import (
	"os"
	"os/exec"
	"syscall"
)

// tree is a process's process group, which its children join.
type tree struct{ group int }

// group starts cmd in a process group of its own.
func group(cmd *exec.Cmd) { cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true} }

// hold is p's group, which it leads.
func hold(p *os.Process) (tree, error) { return tree{p.Pid}, nil }

// kill ends every process in the group.
func (t tree) kill() { _ = syscall.Kill(-t.group, syscall.SIGKILL) }
