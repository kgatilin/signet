// Package proc runs background processes in their own process group and stops
// them gracefully. It is shared by the YAML runner and the Go SDK.
package proc

import (
	"bytes"
	"errors"
	"os/exec"
	"sync"
	"time"
)

// Buffer is a goroutine-safe buffer for capturing a process's output while
// another goroutine scans it.
type Buffer struct {
	mu  sync.Mutex
	buf bytes.Buffer
}

func (b *Buffer) Write(p []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.Write(p)
}

func (b *Buffer) String() string {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.String()
}

// Process is a started background command with combined stdout and stderr.
type Process struct {
	Cmd    *exec.Cmd
	Output *Buffer

	done    chan struct{}
	waitErr error
}

// Start starts cmd in its own process group, capturing its combined output.
func Start(cmd *exec.Cmd) (*Process, error) {
	SetGroup(cmd)
	output := &Buffer{}
	cmd.Stdout = output
	cmd.Stderr = output
	if err := cmd.Start(); err != nil {
		return nil, err
	}
	p := &Process{Cmd: cmd, Output: output, done: make(chan struct{})}
	go func() {
		p.waitErr = cmd.Wait()
		close(p.done)
	}()
	return p, nil
}

// Done is closed when the process has exited.
func (p *Process) Done() <-chan struct{} { return p.done }

// Exited reports whether the process has exited.
func (p *Process) Exited() bool {
	select {
	case <-p.done:
		return true
	default:
		return false
	}
}

// WaitErr is the result of cmd.Wait; valid once Done is closed.
func (p *Process) WaitErr() error { return p.waitErr }

// Stop sends SIGTERM to the process group and SIGKILL if it is still running
// after timeout. It returns false when the process had already exited.
func (p *Process) Stop(timeout time.Duration) bool {
	if p.Exited() {
		return false
	}
	_ = Terminate(p.Cmd)
	select {
	case <-p.done:
	case <-time.After(timeout):
		_ = Kill(p.Cmd)
		<-p.done
	}
	return true
}

// ErrExited is returned by WaitReady when the process exits before it is ready.
var ErrExited = errors.New("exited before it was ready")

// ErrNotReady is returned by WaitReady when the timeout elapses.
var ErrNotReady = errors.New("not ready")

// WaitReady polls ready every interval until it returns true, the process
// exits, or timeout elapses.
func (p *Process) WaitReady(timeout, interval time.Duration, ready func() bool) error {
	deadline := time.Now().Add(timeout)
	for {
		if p.Exited() {
			return ErrExited
		}
		if ready() {
			return nil
		}
		if time.Now().After(deadline) {
			return ErrNotReady
		}
		select {
		case <-p.done:
		case <-time.After(interval):
		}
	}
}
