package main

import (
	"fmt"
	"io"
	"os"
)

func main() {
	os.Exit(runCLI(os.Args[1:], os.Stdout, os.Stderr, os.Stdin))
}

func runCLI(args []string, stdout, stderr io.Writer, stdin io.Reader) int {
	if len(args) == 0 || args[0] == "--help" || args[0] == "-h" || args[0] == "help" {
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
  signet validate <file>
  signet run <file> [--yes] [--binary <path>]
  signet discover groups <path>
  signet discover <path>
  signet discover cases <file>
  signet discover cases <file> --checks

Commands:
  validate   Validate an acceptance YAML file.
  run        Run an acceptance YAML file against its subject binary.
  discover   List acceptance groups and cases without running commands.

Options:
  --yes           Run without per-command confirmation.
  --binary <path> Override subject.binary for run.
`)
}

func validateCmd(args []string, stdout io.Writer) int {
	if len(args) != 1 {
		fmt.Fprintln(stdout, "invalid usage: signet validate <file>")
		return 2
	}

	spec, errs := loadSpec(args[0])
	if len(errs) > 0 {
		printInvalid(stdout, args[0], errs)
		return 1
	}

	fmt.Fprintf(stdout, "valid %s: %s, %s\n", args[0], plural(len(spec.Cases), "case"), plural(countSteps(spec), "step"))
	return 0
}

func printInvalid(w io.Writer, path string, errs []validationError) {
	fmt.Fprintf(w, "invalid %s:\n", path)
	for _, err := range errs {
		if err.Path == "" {
			fmt.Fprintf(w, "- %s\n", err.Message)
			continue
		}
		fmt.Fprintf(w, "- %s %s\n", err.Path, err.Message)
	}
}
