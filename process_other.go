//go:build !unix

package main

import "os/exec"

// setProcessGroup is a no-op on platforms without POSIX process groups.
func setProcessGroup(cmd *exec.Cmd) {}

// terminateProcess asks the process to stop.
func terminateProcess(cmd *exec.Cmd) error {
	return cmd.Process.Kill()
}

// killProcess force-kills the process.
func killProcess(cmd *exec.Cmd) error {
	return cmd.Process.Kill()
}
