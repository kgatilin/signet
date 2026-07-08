//go:build unix

package main

import (
	"os/exec"
	"syscall"
)

// setProcessGroup places the command in its own process group so signals reach
// the whole tree (a shell-wrapped service and its children), not just /bin/sh.
func setProcessGroup(cmd *exec.Cmd) {
	if cmd.SysProcAttr == nil {
		cmd.SysProcAttr = &syscall.SysProcAttr{}
	}
	cmd.SysProcAttr.Setpgid = true
}

// terminateProcess sends a graceful stop signal to the whole process group.
func terminateProcess(cmd *exec.Cmd) error {
	return syscall.Kill(-cmd.Process.Pid, syscall.SIGTERM)
}

// killProcess force-kills the whole process group.
func killProcess(cmd *exec.Cmd) error {
	return syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL)
}
