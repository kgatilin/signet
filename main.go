package main

import (
	"fmt"
	"io"
	"os"
)

func main() {
	configureColor(os.Stdout)
	os.Exit(runCLI(os.Args[1:], os.Stdout, os.Stderr, os.Stdin))
}

func runCLI(args []string, stdout, stderr io.Writer, stdin io.Reader) int {
	if len(args) == 0 || isHelpArg(args[0]) {
		printHelp(stdout)
		return 0
	}

	switch args[0] {
	case "validate":
		return validateCmd(args[1:], stdout)
	case "run":
		return runCmd(args[1:], stdout, stderr, stdin)
	case "discover":
		return discoverCmd(args[1:], stdout)
	default:
		fmt.Fprintf(stderr, "unknown command: %s\n\n", args[0])
		printHelp(stderr)
		return 2
	}
}

func printHelp(w io.Writer) {
	fmt.Fprint(w, `Usage:
  signet validate <path>...
  signet run <path>... [--yes] [--verbose] [--binary <path>]
  signet discover groups <path>...
  signet discover <path>...
  signet discover cases <path>... [--case <id>]
  signet discover cases <path>... [--case <id>] --checks

Commands:
  validate   Validate acceptance YAML files.
  run        Run acceptance YAML files against their subject binaries.
  discover   List acceptance groups and cases without running commands.

Options:
  --yes            Run without per-command confirmation.
  --verbose        Print executed command, exit code, stdout, and stderr.
  --binary <path>  Override subject.binary for run.
`)
}

func isHelpArg(arg string) bool {
	return arg == "--help" || arg == "-h" || arg == "help"
}

func validateCmd(args []string, stdout io.Writer) int {
	if len(args) == 1 && isHelpArg(args[0]) {
		printValidateHelp(stdout)
		return 0
	}
	if len(args) == 0 {
		fmt.Fprintln(stdout, "invalid usage: signet validate <path>...")
		return 2
	}

	files, err := acceptanceFilesForPaths(args)
	if err != nil {
		fmt.Fprintf(stdout, "%s %s:\n%s %s\n", red("invalid"), pathListLabel(args), yellow("-"), err)
		return 1
	}

	return validateFiles(files, pathListLabel(args), stdout)
}

func printValidateHelp(w io.Writer) {
	fmt.Fprint(w, `Usage:
  signet validate <path>...

Validate acceptance YAML files. Paths may be files or directories; directories
are searched recursively for acceptance.yaml and *.acceptance.yaml.
`)
}

type validateSummary struct {
	groups        int
	cases         int
	steps         int
	invalidGroups int
}

func validateFiles(files []string, target string, stdout io.Writer) int {
	summary := validateSummary{}
	for _, file := range files {
		fileSummary := validateFile(file, stdout)
		summary.add(fileSummary)
	}
	if len(files) == 1 {
		if summary.invalidGroups > 0 {
			return 1
		}
		return 0
	}
	if summary.invalidGroups > 0 {
		fmt.Fprintf(stdout, "%s %s: %s, %s\n", red("invalid"), target, plural(summary.groups, "group"), red(plural(summary.invalidGroups, "invalid group")))
		return 1
	}
	fmt.Fprintf(stdout, "%s %s: %s, %s, %s\n", green("valid"), target, plural(summary.groups, "group"), plural(summary.cases, "case"), plural(summary.steps, "step"))
	return 0
}

func validateFile(file string, stdout io.Writer) validateSummary {
	spec, errs := loadSpec(file)
	if len(errs) > 0 {
		printInvalid(stdout, file, errs)
		return validateSummary{invalidGroups: 1}
	}

	summary := validateSummary{
		groups: 1,
		cases:  len(spec.Cases),
		steps:  countSteps(spec),
	}
	fmt.Fprintf(stdout, "%s %s: %s, %s\n", green("valid"), file, plural(summary.cases, "case"), plural(summary.steps, "step"))
	return summary
}

func (summary *validateSummary) add(other validateSummary) {
	summary.groups += other.groups
	summary.cases += other.cases
	summary.steps += other.steps
	summary.invalidGroups += other.invalidGroups
}

func printInvalid(w io.Writer, path string, errs []validationError) {
	fmt.Fprintf(w, "%s %s:\n", red("invalid"), path)
	for _, err := range errs {
		if err.Path == "" {
			fmt.Fprintf(w, "%s %s\n", yellow("-"), err.Message)
			continue
		}
		fmt.Fprintf(w, "%s %s %s\n", yellow("-"), err.Path, err.Message)
	}
}
