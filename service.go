package main

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"os/exec"
	"strings"
	"sync"
	"time"
)

const (
	buildTimeout        = 5 * time.Minute
	defaultReadyTimeout = 10 * time.Second
	defaultStopTimeout  = 5 * time.Second
	readyProbeTimeout   = 5 * time.Second
	readyPollInterval   = 200 * time.Millisecond
)

// runBuild executes setup.build commands to completion before any case runs.
// It returns false when a build command fails, so cases do not run against a
// stale or missing binary.
func runBuild(spec *Spec, setup *setupContext, stdout, stderr io.Writer) bool {
	for index, raw := range spec.Setup.Build {
		command, err := setup.expandString(raw)
		if err != nil {
			fmt.Fprintf(stdout, "%s setup.build[%d]: %s\n", red("FAIL"), index, err)
			return false
		}
		fmt.Fprintf(stdout, "%s %s\n", cyan("BUILD"), command)
		result := executeShellCommand(command, setup.commandEnv(), buildTimeout)
		if result.exitCode != 0 {
			fmt.Fprintf(stdout, "%s build: exit %d\n", red("FAIL"), result.exitCode)
			printCapturedStream(stdout, "STDOUT", result.stdout)
			printCapturedStream(stdout, "STDERR", result.stderr)
			if result.err != nil {
				fmt.Fprintf(stderr, "%s\n", result.err)
			}
			return false
		}
	}
	return true
}

// executeShellCommand runs a shell command to completion with a timeout. It is
// shared by build commands and service readiness probes.
func executeShellCommand(command string, env []string, timeout time.Duration) commandResult {
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()

	cmd := exec.CommandContext(ctx, "/bin/sh", "-c", command)
	if env != nil {
		cmd.Env = env
	}
	var out bytes.Buffer
	var errOut bytes.Buffer
	cmd.Stdout = &out
	cmd.Stderr = &errOut

	err := cmd.Run()
	return finishCommandResult(ctx, out.String(), errOut.String(), err)
}

// syncBuffer is a goroutine-safe buffer for capturing a background service's
// combined output while the readiness loop scans it.
type syncBuffer struct {
	mu  sync.Mutex
	buf bytes.Buffer
}

func (b *syncBuffer) Write(p []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.Write(p)
}

func (b *syncBuffer) String() string {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.String()
}

// runningService tracks a started background service and its lifecycle state.
type runningService struct {
	name        string
	label       string
	cmd         *exec.Cmd
	output      *syncBuffer
	stopTimeout time.Duration
	done        chan struct{}
	waitErr     error
}

// startServices starts each declared service in order, waiting for each to be
// ready before starting the next. On any failure it returns the services that
// were already started so the caller can tear them down.
func startServices(spec *Spec, setup *setupContext, stdout, stderr io.Writer) ([]*runningService, bool) {
	var started []*runningService
	for _, service := range spec.Setup.Services {
		rs, err := startService(service, setup)
		if err != nil {
			fmt.Fprintf(stdout, "%s service %s: %s\n", red("FAIL"), service.Name, err)
			return started, false
		}
		started = append(started, rs)
		fmt.Fprintf(stdout, "%s %s (%s)\n", cyan("SERVICE"), service.Name, rs.label)
		if !waitServiceReady(rs, service, setup, stdout, stderr) {
			return started, false
		}
	}
	return started, true
}

func startService(service Service, setup *setupContext) (*runningService, error) {
	name, args, label, err := resolveServiceCommand(service, setup)
	if err != nil {
		return nil, err
	}

	cmd := exec.Command(name, args...)
	setProcessGroup(cmd)
	if env := setup.commandEnv(); env != nil {
		cmd.Env = env
	}
	output := &syncBuffer{}
	cmd.Stdout = output
	cmd.Stderr = output
	if err := cmd.Start(); err != nil {
		return nil, err
	}

	rs := &runningService{
		name:        service.Name,
		label:       label,
		cmd:         cmd,
		output:      output,
		stopTimeout: serviceStopTimeout(service),
		done:        make(chan struct{}),
	}
	go func() {
		rs.waitErr = cmd.Wait()
		close(rs.done)
	}()
	return rs, nil
}

func resolveServiceCommand(service Service, setup *setupContext) (name string, args []string, label string, err error) {
	if strings.TrimSpace(service.Shell) != "" {
		shell, expandErr := setup.expandString(service.Shell)
		if expandErr != nil {
			return "", nil, "", expandErr
		}
		return "/bin/sh", []string{"-c", shell}, shell, nil
	}
	if setup.binary == "" {
		return "", nil, "", fmt.Errorf("subject.binary is required for service args")
	}
	expanded, expandErr := setup.expandStrings(service.Args)
	if expandErr != nil {
		return "", nil, "", expandErr
	}
	return setup.binary, expanded, formatArgv(setup.binary, expanded), nil
}

func serviceStopTimeout(service Service) time.Duration {
	if service.StopTimeout != "" {
		if parsed, err := time.ParseDuration(service.StopTimeout); err == nil {
			return parsed
		}
	}
	return defaultStopTimeout
}

// waitServiceReady blocks until the service signals readiness, crashes, or the
// readiness timeout elapses. With no ready gate it only checks for an immediate
// crash, then proceeds.
func waitServiceReady(rs *runningService, service Service, setup *setupContext, stdout, stderr io.Writer) bool {
	hasShell := strings.TrimSpace(service.Ready.Shell) != ""
	hasLog := strings.TrimSpace(service.Ready.Log) != ""
	if !hasShell && !hasLog {
		select {
		case <-rs.done:
			reportServiceExit(stdout, stderr, rs)
			return false
		default:
			return true
		}
	}

	readyShell := ""
	if hasShell {
		expanded, err := setup.expandString(service.Ready.Shell)
		if err != nil {
			fmt.Fprintf(stdout, "%s service %s: %s\n", red("FAIL"), rs.name, err)
			return false
		}
		readyShell = expanded
	}

	deadline := time.Now().Add(serviceReadyTimeout(service))
	for {
		select {
		case <-rs.done:
			reportServiceExit(stdout, stderr, rs)
			return false
		default:
		}
		if hasLog && strings.Contains(rs.output.String(), service.Ready.Log) {
			return true
		}
		if readyShell != "" {
			if executeShellCommand(readyShell, setup.commandEnv(), readyProbeTimeout).exitCode == 0 {
				return true
			}
		}
		if time.Now().After(deadline) {
			fmt.Fprintf(stdout, "%s service %s: not ready after %s\n", red("FAIL"), rs.name, serviceReadyTimeout(service))
			printServiceOutput(stdout, rs)
			return false
		}
		time.Sleep(readyPollInterval)
	}
}

func serviceReadyTimeout(service Service) time.Duration {
	if service.Ready.Timeout != "" {
		if parsed, err := time.ParseDuration(service.Ready.Timeout); err == nil {
			return parsed
		}
	}
	return defaultReadyTimeout
}

// stopServices stops services in reverse start order so dependents go down
// before their dependencies.
func stopServices(services []*runningService, stdout io.Writer) {
	for index := len(services) - 1; index >= 0; index-- {
		stopService(services[index], stdout)
	}
}

func stopService(rs *runningService, stdout io.Writer) {
	if rs == nil || rs.cmd == nil || rs.cmd.Process == nil {
		return
	}
	select {
	case <-rs.done:
		return
	default:
	}

	fmt.Fprintf(stdout, "%s %s\n", dim("STOP"), rs.name)
	_ = terminateProcess(rs.cmd)
	select {
	case <-rs.done:
	case <-time.After(rs.stopTimeout):
		_ = killProcess(rs.cmd)
		<-rs.done
	}
}

func reportServiceExit(stdout, stderr io.Writer, rs *runningService) {
	fmt.Fprintf(stdout, "%s service %s exited before it was ready\n", red("FAIL"), rs.name)
	printServiceOutput(stdout, rs)
	if rs.waitErr != nil {
		fmt.Fprintf(stderr, "%s\n", rs.waitErr)
	}
}

func printServiceOutput(stdout io.Writer, rs *runningService) {
	printCapturedStream(stdout, "OUTPUT", rs.output.String())
}

// serviceCommandLabel renders a service's command for display in cases --checks.
func serviceCommandLabel(service Service, binary string) string {
	if strings.TrimSpace(service.Shell) != "" {
		return service.Shell
	}
	if binary == "" {
		return formatArgv("<subject.binary>", service.Args)
	}
	return formatArgv(binary, service.Args)
}

func formatArgv(binary string, args []string) string {
	parts := append([]string{binary}, args...)
	formatted := make([]string, 0, len(parts))
	for _, part := range parts {
		formatted = append(formatted, quoteCommandPart(part))
	}
	return strings.Join(formatted, " ")
}
