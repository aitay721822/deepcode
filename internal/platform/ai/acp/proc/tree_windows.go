package proc

import (
	"os"
	"os/exec"
	"unsafe"

	"golang.org/x/sys/windows"
)

// tree is a process's job object: every process it starts joins it, and
// closing it ends them all, as when deepcode itself ends.
type tree struct{ job windows.Handle }

// group does nothing on Windows: the job object holds the tree.
func group(*exec.Cmd) {}

// hold puts p in a new job object that ends its processes once closed.
// ponytail: p runs for a moment before it joins, so a child it starts in that
// moment escapes; starting it suspended needs its thread, which os/exec keeps.
func hold(p *os.Process) (tree, error) {
	job, err := windows.CreateJobObject(nil, nil)
	if err != nil {
		return tree{}, err
	}
	info := windows.JOBOBJECT_EXTENDED_LIMIT_INFORMATION{
		BasicLimitInformation: windows.JOBOBJECT_BASIC_LIMIT_INFORMATION{LimitFlags: windows.JOB_OBJECT_LIMIT_KILL_ON_JOB_CLOSE}}
	h, err := windows.OpenProcess(windows.PROCESS_SET_QUOTA|windows.PROCESS_TERMINATE, false, uint32(p.Pid))
	if err == nil {
		defer windows.CloseHandle(h)
		_, err = windows.SetInformationJobObject(job, windows.JobObjectExtendedLimitInformation, uintptr(unsafe.Pointer(&info)), uint32(unsafe.Sizeof(info)))
	}
	if err == nil {
		err = windows.AssignProcessToJobObject(job, h)
	}
	if err != nil {
		windows.CloseHandle(job)
		return tree{}, err
	}
	return tree{job}, nil
}

// kill ends every process in the tree.
func (t tree) kill() {
	_ = windows.TerminateJobObject(t.job, 1)
	_ = windows.CloseHandle(t.job)
}
