package signet

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/kgatilin/signet/internal/proc"
)

const buildTimeout = 5 * time.Minute

// BinaryEnv names the environment variable that points every Subject at an
// already built binary. Its value must be an absolute path.
const BinaryEnv = "SIGNET_BINARY"

// Subj is the binary under test: a build command and the path it produces.
type Subj struct {
	build   string
	binary  string
	resolve func() (string, error)
}

// Subject declares the binary under test. buildShell runs with /bin/sh in the
// module root (the nearest directory with go.mod above the test's working
// directory); a relative binaryPath resolves against the same root. The build
// runs once per test process, on the first call that needs the binary, so a
// test run that never reaches a step does not build. When SIGNET_BINARY is
// set, nothing is built and that path is used instead.
func Subject(buildShell, binaryPath string) *Subj {
	s := &Subj{build: buildShell, binary: binaryPath}
	s.resolve = sync.OnceValues(s.buildAndResolve)
	return s
}

// Path returns the absolute path of the subject binary, building it first if
// needed. A failed build fails t.
func (s *Subj) Path(t testing.TB) string {
	t.Helper()
	path, err := s.resolve()
	if err != nil {
		t.Fatalf("signet: subject: %v", err)
	}
	return path
}

// Cmd prepares a command that runs the subject binary with args.
func (s *Subj) Cmd(t testing.TB, args ...string) *Command {
	t.Helper()
	return Cmd(t, s.Path(t), args...)
}

// Run runs the subject binary with args; a shortcut for s.Cmd(t, args...).Run().
func (s *Subj) Run(t testing.TB, args ...string) *Result {
	t.Helper()
	return s.Cmd(t, args...).Run()
}

// Service prepares the subject binary as a background service.
func (s *Subj) Service(t testing.TB, name string, args ...string) *ServiceBuilder {
	t.Helper()
	return Service(t, name, s.Path(t), args...)
}

func (s *Subj) buildAndResolve() (string, error) {
	if env := os.Getenv(BinaryEnv); env != "" {
		if !filepath.IsAbs(env) {
			return "", fmt.Errorf("%s must be an absolute path, got %q: each test package runs in its own directory", BinaryEnv, env)
		}
		return env, checkExecutable(env)
	}

	root, err := moduleRoot()
	if err != nil {
		return "", err
	}
	if strings.TrimSpace(s.build) != "" {
		ctx, cancel := context.WithTimeout(context.Background(), buildTimeout)
		defer cancel()
		cmd := exec.CommandContext(ctx, "/bin/sh", "-c", s.build)
		proc.SetGroup(cmd)
		cmd.Cancel = func() error { return proc.Kill(cmd) }
		cmd.WaitDelay = 2 * time.Second
		cmd.Dir = root
		if out, err := cmd.CombinedOutput(); err != nil {
			return "", fmt.Errorf("build %q in %s: %v\n%s", s.build, root, err, strings.TrimRight(string(out), "\n"))
		}
	}
	path := s.binary
	if !filepath.IsAbs(path) {
		path = filepath.Join(root, path)
	}
	return path, checkExecutable(path)
}

func checkExecutable(path string) error {
	info, err := os.Stat(path)
	if err != nil {
		return fmt.Errorf("binary %s: %w", path, err)
	}
	if info.IsDir() {
		return fmt.Errorf("binary %s is a directory", path)
	}
	return nil
}

// moduleRoot walks up from the working directory to the nearest go.mod.
func moduleRoot() (string, error) {
	dir, err := os.Getwd()
	if err != nil {
		return "", err
	}
	for {
		if _, err := os.Stat(filepath.Join(dir, "go.mod")); err == nil {
			return dir, nil
		} else if !errors.Is(err, os.ErrNotExist) {
			return "", err
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			return "", errors.New("no go.mod found above the working directory")
		}
		dir = parent
	}
}
