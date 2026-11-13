//go:build unix

package proc

import (
	"os/exec"
	"syscall"
)

// SetGroup places the command in its own process group so signals reach the
// whole tree (a shell-wrapped service and its children), not just /bin/sh.
func SetGroup(cmd *exec.Cmd) {
	if cmd.SysProcAttr == nil {
		cmd.SysProcAttr = &syscall.SysProcAttr{}
	}
	cmd.SysProcAttr.Setpgid = true
}

// Terminate sends a graceful stop signal to the whole process group.
func Terminate(cmd *exec.Cmd) error {
	return syscall.Kill(-cmd.Process.Pid, syscall.SIGTERM)
}

// Kill force-kills the whole process group.
func Kill(cmd *exec.Cmd) error {
	return syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL)
}
