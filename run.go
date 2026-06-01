package main

import (
	"bufio"
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"os/exec"
	"regexp"
	"strings"
	"time"
)

type runOptions struct {
	file           string
	yes            bool
	binaryOverride string
}

type commandResult struct {
	exitCode int
	stdout   string
	stderr   string
	timedOut bool
	err      error
}

type checkFailure struct {
	check   string
	message string
}

func runCmd(args []string, stdout, stderr io.Writer, stdin io.Reader) int {
	opts, ok := parseRunArgs(args, stdout)
	if !ok {
		return 2
	}

	spec, errs := loadSpec(opts.file)
	if len(errs) > 0 {
		printInvalid(stdout, opts.file, errs)
		return 1
	}

	binary := spec.Subject.Binary
	if opts.binaryOverride != "" {
		binary = opts.binaryOverride
	}
	if binary == "" && needsBinary(spec) {
		fmt.Fprintf(stdout, "%s subject.binary: required for steps using run.args\n", red("invalid"))
		return 1
	}

	if binary == "" {
		fmt.Fprintf(stdout, "%s shell commands\n", dim("using"))
	} else {
		fmt.Fprintf(stdout, "%s binary %s\n", dim("using"), binary)
	}
	totalSteps := countSteps(spec)
	failedSteps := 0
	confirmReader := bufio.NewReader(stdin)

	for _, c := range spec.Cases {
		fmt.Fprintf(stdout, "%s %s\n", cyan("CASE"), c.Name)
		for _, step := range c.Steps {
			fmt.Fprintf(stdout, "%s %s\n", cyan("RUN"), step.Name)
			if shouldConfirm(spec, opts) {
				if !confirmStep(confirmReader, stdout, step, binary) {
					fmt.Fprintf(stdout, "%s (use --yes to skip confirmation)\n", yellow("aborted"))
					return 130
				}
			}

			result := executeStep(step, spec, binary)
			failures := evaluateStep(step, result)
			if len(failures) == 0 {
				fmt.Fprintf(stdout, "%s %s\n", green("PASS"), step.Name)
				continue
			}

			failedSteps++
			fmt.Fprintf(stdout, "%s %s\n", red("FAIL"), step.Name)
			for _, failure := range failures {
				fmt.Fprintf(stdout, "  %s %s: %s\n", yellow("-"), failure.check, failure.message)
			}
			if result.err != nil {
				fmt.Fprintf(stderr, "%s\n", result.err)
			}
		}
	}

	if failedSteps > 0 {
		fmt.Fprintf(stdout, "%s %s: %s, %s, %s\n", red("FAIL"), opts.file, plural(len(spec.Cases), "case"), plural(totalSteps, "step"), red(fmt.Sprintf("%d failed", failedSteps)))
		return 1
	}

	fmt.Fprintf(stdout, "%s %s: %s, %s, 0 failed\n", green("PASS"), opts.file, plural(len(spec.Cases), "case"), plural(totalSteps, "step"))
	return 0
}

func parseRunArgs(args []string, stdout io.Writer) (runOptions, bool) {
	var opts runOptions
	for i := 0; i < len(args); i++ {
		switch args[i] {
		case "--yes":
			opts.yes = true
		case "--binary":
			if i+1 >= len(args) {
				fmt.Fprintln(stdout, "invalid usage: --binary requires a value")
				return opts, false
			}
			opts.binaryOverride = args[i+1]
			i++
		default:
			if opts.file == "" {
				opts.file = args[i]
				continue
			}
			fmt.Fprintln(stdout, "invalid usage: signet run <file> [--yes] [--binary <path>]")
			return opts, false
		}
	}
	if opts.file == "" {
		fmt.Fprintln(stdout, "invalid usage: signet run <file> [--yes] [--binary <path>]")
		return opts, false
	}
	return opts, true
}

func shouldConfirm(spec *Spec, opts runOptions) bool {
	if opts.yes {
		return false
	}
	if spec.Defaults.Confirm == nil {
		return true
	}
	return *spec.Defaults.Confirm
}

func needsBinary(spec *Spec) bool {
	for _, c := range spec.Cases {
		for _, step := range c.Steps {
			if step.Run.Shell == "" {
				return true
			}
		}
	}
	return false
}

func confirmStep(reader *bufio.Reader, stdout io.Writer, step Step, binary string) bool {
	fmt.Fprintf(stdout, "%s %s: %s %s %s ", yellow("Confirm"), step.Name, commandName(step, binary), strings.Join(commandArgs(step), " "), dim("[y/N] (use --yes to skip)"))
	answer, err := reader.ReadString('\n')
	if err != nil && !errors.Is(err, io.EOF) {
		return false
	}
	answer = strings.TrimSpace(strings.ToLower(answer))
	return answer == "y" || answer == "yes"
}

func commandName(step Step, binary string) string {
	if step.Run.Shell != "" {
		return "sh"
	}
	return binary
}

func commandArgs(step Step) []string {
	if step.Run.Shell != "" {
		return []string{"-c", step.Run.Shell}
	}
	return step.Run.Args
}

func executeStep(step Step, spec *Spec, binary string) commandResult {
	timeout := 10 * time.Second
	if spec.Defaults.Timeout != "" {
		if parsed, err := time.ParseDuration(spec.Defaults.Timeout); err == nil {
			timeout = parsed
		}
	}
	if step.Run.Timeout != "" {
		if parsed, err := time.ParseDuration(step.Run.Timeout); err == nil {
			timeout = parsed
		}
	}

	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()

	var cmd *exec.Cmd
	if step.Run.Shell != "" {
		cmd = exec.CommandContext(ctx, "/bin/sh", "-c", step.Run.Shell)
	} else {
		cmd = exec.CommandContext(ctx, binary, step.Run.Args...)
	}
	if step.Run.Stdin != "" {
		cmd.Stdin = strings.NewReader(step.Run.Stdin)
	}

	var out bytes.Buffer
	var errOut bytes.Buffer
	cmd.Stdout = &out
	cmd.Stderr = &errOut

	err := cmd.Run()
	result := commandResult{
		exitCode: 0,
		stdout:   out.String(),
		stderr:   errOut.String(),
		err:      err,
	}
	if ctx.Err() == context.DeadlineExceeded {
		result.exitCode = -1
		result.timedOut = true
		if result.stderr != "" && !strings.HasSuffix(result.stderr, "\n") {
			result.stderr += "\n"
		}
		result.stderr += "command timed out"
		return result
	}
	if err == nil {
		return result
	}

	var exitErr *exec.ExitError
	if errors.As(err, &exitErr) {
		result.exitCode = exitErr.ExitCode()
		return result
	}
	result.exitCode = -1
	if result.stderr != "" && !strings.HasSuffix(result.stderr, "\n") {
		result.stderr += "\n"
	}
	result.stderr += err.Error()
	return result
}

func evaluateStep(step Step, result commandResult) []checkFailure {
	var failures []checkFailure
	if step.Expect.ExitCode != nil && result.exitCode != *step.Expect.ExitCode {
		failures = append(failures, checkFailure{
			check:   fmt.Sprintf("exitCode == %d", *step.Expect.ExitCode),
			message: fmt.Sprintf("got %d", result.exitCode),
		})
	}
	failures = append(failures, evaluateStream("stdout", result.stdout, step.Expect.Stdout)...)
	failures = append(failures, evaluateStream("stderr", result.stderr, step.Expect.Stderr)...)
	return failures
}

func evaluateStream(name, actual string, expect StreamExpect) []checkFailure {
	var failures []checkFailure
	for _, value := range expect.Contains {
		if !strings.Contains(actual, value) {
			failures = append(failures, checkFailure{check: fmt.Sprintf("%s contains %q", name, value), message: "not found"})
		}
	}
	for _, value := range expect.NotContains {
		if strings.Contains(actual, value) {
			failures = append(failures, checkFailure{check: fmt.Sprintf("%s notContains %q", name, value), message: "found"})
		}
	}
	for _, value := range expect.OrderedContains {
		if !orderedContains(actual, []string{value}) {
			failures = append(failures, checkFailure{check: fmt.Sprintf("%s orderedContains %q", name, value), message: "not found in order"})
		}
	}
	if len(expect.OrderedContains) > 1 && !orderedContains(actual, expect.OrderedContains) {
		failures = append(failures, checkFailure{check: fmt.Sprintf("%s orderedContains", name), message: "values are not in order"})
	}
	for _, pattern := range expect.Matches {
		matched, err := regexp.MatchString(pattern, actual)
		if err != nil {
			failures = append(failures, checkFailure{check: fmt.Sprintf("%s matches %q", name, pattern), message: err.Error()})
			continue
		}
		if !matched {
			failures = append(failures, checkFailure{check: fmt.Sprintf("%s matches %q", name, pattern), message: "not matched"})
		}
	}
	return failures
}

func orderedContains(actual string, values []string) bool {
	pos := 0
	for _, value := range values {
		index := strings.Index(actual[pos:], value)
		if index < 0 {
			return false
		}
		pos += index + len(value)
	}
	return true
}
