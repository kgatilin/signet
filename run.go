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
	"strconv"
	"strings"
	"time"
)

type runOptions struct {
	paths          []string
	yes            bool
	verbose        bool
	binaryOverride string
}

type runSummary struct {
	cases         int
	steps         int
	failedSteps   int
	invalidGroups int
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

func runAcceptance(opts runOptions, stdout, stderr io.Writer, stdin io.Reader) int {
	files, err := acceptanceFilesForPaths(opts.paths)
	if err != nil {
		fmt.Fprintf(stdout, "%s %s\n", red("invalid"), err)
		return 1
	}

	multiple := len(files) > 1
	total := runSummary{}
	confirmReader := bufio.NewReader(stdin)

	for fileIndex, file := range files {
		if multiple && fileIndex > 0 {
			fmt.Fprintln(stdout)
		}
		if multiple {
			fmt.Fprintf(stdout, "%s %s\n", cyan("GROUP"), file)
		}
		summary, code := runFile(file, opts, stdout, stderr, confirmReader)
		total.add(summary)
		if code == 130 {
			return code
		}
		if !multiple {
			return code
		}
	}

	return printRunSummary(stdout, pathListLabel(opts.paths), len(files), total)
}

func printRunHelp(w io.Writer) {
	fmt.Fprint(w, `Usage:
  signet run <path>... [--yes] [--verbose] [--binary <path>]

Run acceptance YAML files against their subject binaries. Paths may be files or
directories; directories are searched recursively for acceptance.yaml and
*.acceptance.yaml.

Options:
  --yes            Run without per-command confirmation.
  --verbose        Print executed command, exit code, stdout, and stderr.
  --binary <path>  Override subject.binary for run.
`)
}

func runFile(file string, opts runOptions, stdout, stderr io.Writer, confirmReader *bufio.Reader) (runSummary, int) {
	spec, errs := loadSpec(file)
	if len(errs) > 0 {
		printInvalid(stdout, file, errs)
		return runSummary{invalidGroups: 1}, 1
	}

	summary := runSummary{
		cases: len(spec.Cases),
		steps: countSteps(spec),
	}

	binary := spec.Subject.Binary
	if opts.binaryOverride != "" {
		binary = opts.binaryOverride
	}
	if binary == "" && needsBinary(spec) {
		fmt.Fprintf(stdout, "%s subject.binary: required for steps using run.args\n", red("invalid"))
		summary.invalidGroups = 1
		return summary, 1
	}

	if binary == "" {
		fmt.Fprintf(stdout, "%s shell commands\n", dim("using"))
	} else {
		fmt.Fprintf(stdout, "%s binary %s\n", dim("using"), binary)
	}

	for caseIndex, c := range spec.Cases {
		if caseIndex > 0 {
			printCaseSeparator(stdout)
		}
		fmt.Fprintf(stdout, "%s %s\n", cyan("CASE"), caseDisplay(c))
		for _, step := range c.Steps {
			fmt.Fprintf(stdout, "%s %s\n", cyan("RUN"), step.Name)
			if shouldConfirm(spec, opts) {
				if !confirmStep(confirmReader, stdout, step, binary) {
					fmt.Fprintf(stdout, "%s (use --yes to skip confirmation)\n", yellow("aborted"))
					return summary, 130
				}
			}

			result := executeStep(step, spec, binary)
			if opts.verbose {
				printStepTrace(stdout, step, binary, result)
			}
			failures := evaluateStep(step, result)
			if len(failures) == 0 {
				fmt.Fprintf(stdout, "%s %s\n", green("PASS"), step.Name)
				continue
			}

			summary.failedSteps++
			fmt.Fprintf(stdout, "%s %s\n", red("FAIL"), step.Name)
			for _, failure := range failures {
				fmt.Fprintf(stdout, "  %s %s: %s\n", yellow("-"), failure.check, failure.message)
			}
			if result.err != nil {
				fmt.Fprintf(stderr, "%s\n", result.err)
			}
		}
	}

	if summary.failedSteps > 0 {
		fmt.Fprintf(stdout, "%s %s: %s, %s, %s\n", red("FAIL"), file, plural(summary.cases, "case"), plural(summary.steps, "step"), red(fmt.Sprintf("%d failed", summary.failedSteps)))
		return summary, 1
	}

	fmt.Fprintf(stdout, "%s %s: %s, %s, 0 failed\n", green("PASS"), file, plural(summary.cases, "case"), plural(summary.steps, "step"))
	return summary, 0
}

func printCaseSeparator(stdout io.Writer) {
	fmt.Fprintln(stdout, "-----")
}

func (summary *runSummary) add(other runSummary) {
	summary.cases += other.cases
	summary.steps += other.steps
	summary.failedSteps += other.failedSteps
	summary.invalidGroups += other.invalidGroups
}

func printRunSummary(stdout io.Writer, target string, groups int, summary runSummary) int {
	if summary.failedSteps > 0 || summary.invalidGroups > 0 {
		parts := []string{
			plural(groups, "group"),
			plural(summary.cases, "case"),
			plural(summary.steps, "step"),
			red(fmt.Sprintf("%d failed", summary.failedSteps)),
		}
		if summary.invalidGroups > 0 {
			parts = append(parts, red(plural(summary.invalidGroups, "invalid group")))
		}
		fmt.Fprintf(stdout, "%s %s: %s\n", red("FAIL"), target, strings.Join(parts, ", "))
		return 1
	}

	fmt.Fprintf(stdout, "%s %s: %s, %s, %s, 0 failed\n", green("PASS"), target, plural(groups, "group"), plural(summary.cases, "case"), plural(summary.steps, "step"))
	return 0
}

func printStepTrace(stdout io.Writer, step Step, binary string, result commandResult) {
	fmt.Fprintf(stdout, "%s %s\n", cyan("COMMAND"), formatCommand(step, binary))
	fmt.Fprintf(stdout, "%s %d\n", cyan("EXIT"), result.exitCode)
	printCapturedStream(stdout, "STDOUT", result.stdout)
	printCapturedStream(stdout, "STDERR", result.stderr)
}

func printCapturedStream(stdout io.Writer, name, value string) {
	if value == "" {
		fmt.Fprintf(stdout, "%s %s\n", cyan(name), dim("(empty)"))
		return
	}
	fmt.Fprintf(stdout, "%s\n", cyan(name))
	fmt.Fprint(stdout, value)
	if !strings.HasSuffix(value, "\n") {
		fmt.Fprintln(stdout)
	}
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
	fmt.Fprintf(stdout, "%s %s: %s %s ", yellow("Confirm"), step.Name, formatCommand(step, binary), dim("[y/N] (use --yes to skip)"))
	answer, err := reader.ReadString('\n')
	if err != nil && !errors.Is(err, io.EOF) {
		return false
	}
	answer = strings.TrimSpace(strings.ToLower(answer))
	return answer == "y" || answer == "yes"
}

func formatCommand(step Step, binary string) string {
	parts := append([]string{commandName(step, binary)}, commandArgs(step)...)
	formatted := make([]string, 0, len(parts))
	for _, part := range parts {
		formatted = append(formatted, quoteCommandPart(part))
	}
	return strings.Join(formatted, " ")
}

func quoteCommandPart(part string) string {
	if part == "" {
		return `""`
	}
	if strings.ContainsAny(part, " \t\n\"'\\$`!*?[]{}();&|<>") {
		return strconv.Quote(part)
	}
	return part
}

func commandName(step Step, binary string) string {
	if step.Run.Shell != "" {
		return "/bin/sh"
	}
	if binary == "" {
		return "<subject.binary>"
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
