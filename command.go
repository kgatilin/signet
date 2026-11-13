package signet

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/kgatilin/signet/internal/proc"
)

// DefaultTimeout bounds a command that sets no Timeout.
const DefaultTimeout = time.Minute

// Command is a command prepared by Cmd or Subj.Cmd. Configure it with the
// builder methods, then call Run.
type Command struct {
	t       testing.TB
	name    string
	args    []string
	env     []string
	dir     string
	stdin   *string
	timeout time.Duration
}

// Cmd prepares an arbitrary command. name is resolved through PATH unless it
// contains a path separator.
func Cmd(t testing.TB, name string, args ...string) *Command {
	return &Command{t: t, name: name, args: args, timeout: DefaultTimeout}
}

// Env adds KEY=VALUE pairs on top of the test process environment.
func (c *Command) Env(kv ...string) *Command { c.env = append(c.env, kv...); return c }

// Dir sets the working directory; the default is the test's working directory.
func (c *Command) Dir(dir string) *Command { c.dir = dir; return c }

// Stdin sets the command's standard input.
func (c *Command) Stdin(in string) *Command { c.stdin = &in; return c }

// Timeout bounds the command; 0 leaves only the test's own deadline.
func (c *Command) Timeout(d time.Duration) *Command { c.timeout = d; return c }

// Run runs the command to completion. A command that cannot start, exits
// non-zero or times out still returns a Result; checks on the Result decide
// whether the step fails. When the test fails, the last commands it ran are
// logged with their output.
func (c *Command) Run() *Result {
	c.t.Helper()
	ctx := c.t.Context()
	if c.timeout > 0 {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, c.timeout)
		defer cancel()
	}

	cmd := exec.CommandContext(ctx, c.name, c.args...)
	proc.SetGroup(cmd)
	cmd.Cancel = func() error { return proc.Kill(cmd) }
	cmd.WaitDelay = 2 * time.Second
	if len(c.env) > 0 {
		cmd.Env = append(os.Environ(), c.env...)
	}
	cmd.Dir = c.dir
	if c.stdin != nil {
		cmd.Stdin = strings.NewReader(*c.stdin)
	}
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr

	start := time.Now()
	err := cmd.Run()
	r := &Result{
		t:        c.t,
		Args:     append([]string{c.name}, c.args...),
		Stdout:   stdout.String(),
		Stderr:   stderr.String(),
		Duration: time.Since(start),
	}
	var exitErr *exec.ExitError
	switch {
	case errors.Is(ctx.Err(), context.DeadlineExceeded):
		r.ExitCode, r.TimedOut = -1, true
	case err == nil:
	case errors.As(err, &exitErr):
		r.ExitCode = exitErr.ExitCode()
	default:
		r.ExitCode, r.Err = -1, err
	}
	record(c.t, r)
	return r
}

// Result is a finished command. Its checks fail the test the command ran in;
// use In to check it from another step.
type Result struct {
	t testing.TB

	Args     []string
	ExitCode int // -1 when the command did not start or timed out
	Stdout   string
	Stderr   string
	Duration time.Duration
	TimedOut bool
	Err      error // set when the command could not start
}

// In returns the result bound to t, so a Then step can check a result that
// its When produced.
func (r *Result) In(t testing.TB) *Result {
	c := *r
	c.t = t
	return &c
}

// OK requires exit code 0.
func (r *Result) OK() *Result {
	r.t.Helper()
	return r.Exit(0)
}

// Exit requires the given exit code.
func (r *Result) Exit(code int) *Result {
	r.t.Helper()
	if err := r.checkExit(code); err != nil {
		r.t.Fatal(err)
	}
	return r
}

// StdoutContains requires s in standard output.
func (r *Result) StdoutContains(s string) *Result {
	r.t.Helper()
	if !strings.Contains(r.Stdout, s) {
		r.t.Fatalf("%s: stdout does not contain %q\nstdout:\n%s", r.Command(), s, indent(tail(r.Stdout)))
	}
	return r
}

// StderrContains requires s in standard error.
func (r *Result) StderrContains(s string) *Result {
	r.t.Helper()
	if !strings.Contains(r.Stderr, s) {
		r.t.Fatalf("%s: stderr does not contain %q\nstderr:\n%s", r.Command(), s, indent(tail(r.Stderr)))
	}
	return r
}

// JSON decodes the result's standard output into T.
func JSON[T any](r *Result) T {
	r.t.Helper()
	var v T
	if err := json.Unmarshal([]byte(r.Stdout), &v); err != nil {
		r.t.Fatalf("%s: stdout is not %T: %v\nstdout:\n%s", r.Command(), v, err, indent(tail(r.Stdout)))
	}
	return v
}

// Command renders the argv as a shell-like line.
func (r *Result) Command() string {
	parts := make([]string, len(r.Args))
	for i, arg := range r.Args {
		if arg == "" || strings.ContainsAny(arg, " \t\n'\"\\$`") {
			arg = strconv.Quote(arg)
		}
		parts[i] = arg
	}
	return strings.Join(parts, " ")
}

func (r *Result) checkExit(code int) error {
	if r.ExitCode == code && !r.TimedOut && r.Err == nil {
		return nil
	}
	var b strings.Builder
	switch {
	case r.Err != nil:
		fmt.Fprintf(&b, "%s: did not run: %v", r.Command(), r.Err)
	case r.TimedOut:
		fmt.Fprintf(&b, "%s: timed out after %s, want exit %d", r.Command(), r.Duration.Round(time.Millisecond), code)
	default:
		fmt.Fprintf(&b, "%s: exit %d, want %d", r.Command(), r.ExitCode, code)
	}
	if r.Stderr != "" {
		fmt.Fprintf(&b, "\nstderr:\n%s", indent(tail(r.Stderr)))
	}
	return errors.New(b.String())
}

const (
	dumpCommands = 5
	dumpBytes    = 2048
)

// histories keeps the last commands run by each test, to log them when it fails.
var histories sync.Map // testing.TB -> *history

type history struct {
	mu      sync.Mutex
	results []*Result
}

func record(t testing.TB, r *Result) {
	value, loaded := histories.LoadOrStore(t, &history{})
	h := value.(*history)
	if !loaded {
		t.Cleanup(func() {
			histories.Delete(t)
			if t.Failed() {
				h.mu.Lock()
				defer h.mu.Unlock()
				t.Log(h.dump())
			}
		})
	}
	h.mu.Lock()
	defer h.mu.Unlock()
	h.results = append(h.results, r)
	if len(h.results) > dumpCommands {
		h.results = h.results[len(h.results)-dumpCommands:]
	}
}

func (h *history) dump() string {
	var b strings.Builder
	fmt.Fprintf(&b, "signet: last %d command(s):", len(h.results))
	for _, r := range h.results {
		status := fmt.Sprintf("exit %d", r.ExitCode)
		if r.TimedOut {
			status = "timed out"
		} else if r.Err != nil {
			status = r.Err.Error()
		}
		fmt.Fprintf(&b, "\n$ %s  (%s, %s)", r.Command(), status, r.Duration.Round(time.Millisecond))
		if r.Stdout != "" {
			fmt.Fprintf(&b, "\nstdout:\n%s", indent(tail(r.Stdout)))
		}
		if r.Stderr != "" {
			fmt.Fprintf(&b, "\nstderr:\n%s", indent(tail(r.Stderr)))
		}
	}
	return b.String()
}

func tail(s string) string {
	s = strings.TrimRight(s, "\n")
	if len(s) <= dumpBytes {
		return s
	}
	return "..." + s[len(s)-dumpBytes:]
}

func indent(s string) string {
	if s == "" {
		return "  (empty)"
	}
	return "  " + strings.ReplaceAll(s, "\n", "\n  ")
}
