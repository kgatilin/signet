//go:build !unix

package proc

import "os/exec"

// SetGroup is a no-op on platforms without POSIX process groups.
func SetGroup(cmd *exec.Cmd) {}

// Terminate asks the process to stop.
func Terminate(cmd *exec.Cmd) error {
	return cmd.Process.Kill()
}

// Kill force-kills the process.
func Kill(cmd *exec.Cmd) error {
	return cmd.Process.Kill()
}
