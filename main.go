package main

import (
	"errors"
	"fmt"
	"io"
	"os"

	"github.com/spf13/cobra"
)

type cliExitError struct {
	code int
}

func (err cliExitError) Error() string {
	return fmt.Sprintf("exit code %d", err.code)
}

func main() {
	configureColor(os.Stdout)
	os.Exit(runCLI(os.Args[1:], os.Stdout, os.Stderr, os.Stdin))
}

func runCLI(args []string, stdout, stderr io.Writer, stdin io.Reader) int {
	root := newRootCommand(stdout, stderr, stdin)
	root.SetArgs(args)

	if err := root.Execute(); err != nil {
		var exitErr cliExitError
		if errors.As(err, &exitErr) {
			return exitErr.code
		}
		fmt.Fprintln(stderr, err)
		return 2
	}
	return 0
}

func newRootCommand(stdout, stderr io.Writer, stdin io.Reader) *cobra.Command {
	root := &cobra.Command{
		Use:           "signet",
		Short:         "Run final acceptance contracts.",
		SilenceUsage:  true,
		SilenceErrors: true,
	}
	root.SetOut(stdout)
	root.SetErr(stderr)
	root.SetIn(stdin)
	root.SetHelpFunc(func(cmd *cobra.Command, args []string) {
		printHelp(cmd.OutOrStdout())
	})

	root.AddCommand(
		newValidateCommand(stdout),
		newRunCommand(stdout, stderr, stdin),
		newDiscoverCommand(stdout),
	)
	return root
}

func newValidateCommand(stdout io.Writer) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "validate <path>...",
		Short: "Validate acceptance YAML files.",
		RunE: func(cmd *cobra.Command, args []string) error {
			if len(args) == 0 {
				fmt.Fprintln(stdout, "invalid usage: signet validate <path>...")
				return cliExitError{code: 2}
			}

			files, err := acceptanceFilesForPaths(args)
			if err != nil {
				fmt.Fprintf(stdout, "%s %s:\n%s %s\n", red("invalid"), pathListLabel(args), yellow("-"), err)
				return cliExitError{code: 1}
			}

			return exitCode(validateFiles(files, pathListLabel(args), stdout))
		},
	}
	cmd.SetHelpFunc(func(cmd *cobra.Command, args []string) {
		printValidateHelp(cmd.OutOrStdout())
	})
	return cmd
}

func newRunCommand(stdout, stderr io.Writer, stdin io.Reader) *cobra.Command {
	opts := runOptions{}
	cmd := &cobra.Command{
		Use:   "run <path>...",
		Short: "Run acceptance YAML files against their subject binaries.",
		RunE: func(cmd *cobra.Command, args []string) error {
			if len(args) == 0 {
				fmt.Fprintln(stdout, "invalid usage: signet run <path>... [--yes] [--verbose] [--binary <path>]")
				return cliExitError{code: 2}
			}
			opts.paths = args
			return exitCode(runAcceptance(opts, stdout, stderr, stdin))
		},
	}
	cmd.Flags().BoolVar(&opts.yes, "yes", false, "run without per-command confirmation")
	cmd.Flags().BoolVarP(&opts.verbose, "verbose", "v", false, "print command traces")
	cmd.Flags().StringVar(&opts.binaryOverride, "binary", "", "override subject.binary")
	cmd.SetHelpFunc(func(cmd *cobra.Command, args []string) {
		printRunHelp(cmd.OutOrStdout())
	})
	return cmd
}

func newDiscoverCommand(stdout io.Writer) *cobra.Command {
	discover := &cobra.Command{
		Use:   "discover <path>...",
		Short: "List acceptance groups and cases without running commands.",
		RunE: func(cmd *cobra.Command, args []string) error {
			if len(args) == 0 {
				fmt.Fprintln(stdout, "invalid usage: signet discover groups <path>... | signet discover cases <path>... [--checks]")
				return cliExitError{code: 2}
			}
			return exitCode(discoverGroups(args, stdout))
		},
	}
	discover.SetHelpFunc(func(cmd *cobra.Command, args []string) {
		printDiscoverHelp(cmd.OutOrStdout())
	})

	groups := &cobra.Command{
		Use:   "groups <path>...",
		Short: "List acceptance files discovered under files or directories.",
		RunE: func(cmd *cobra.Command, args []string) error {
			if len(args) == 0 {
				fmt.Fprintln(stdout, "invalid usage: signet discover groups <path>...")
				return cliExitError{code: 2}
			}
			return exitCode(discoverGroups(args, stdout))
		},
	}
	groups.SetHelpFunc(func(cmd *cobra.Command, args []string) {
		printDiscoverGroupsHelp(cmd.OutOrStdout())
	})

	opts := discoverCaseOptions{}
	cases := &cobra.Command{
		Use:   "cases <path>...",
		Short: "List cases under acceptance files or directories.",
		RunE: func(cmd *cobra.Command, args []string) error {
			if len(args) == 0 {
				fmt.Fprintln(stdout, "invalid usage: signet discover cases <path>... [--case <id>] [--checks]")
				return cliExitError{code: 2}
			}
			return exitCode(discoverCases(args, opts, stdout))
		},
	}
	cases.Flags().BoolVar(&opts.showChecks, "checks", false, "include expected checks")
	cases.Flags().StringVar(&opts.caseID, "case", "", "select one case id")
	cases.Flags().StringVar(&opts.caseID, "id", "", "alias for --case")
	cases.SetHelpFunc(func(cmd *cobra.Command, args []string) {
		printDiscoverCasesHelp(cmd.OutOrStdout())
	})

	discover.AddCommand(groups, cases)
	return discover
}

func exitCode(code int) error {
	if code == 0 {
		return nil
	}
	return cliExitError{code: code}
}

func printHelp(w io.Writer) {
	fmt.Fprint(w, `Usage:
  signet validate <path>...
  signet run <path>... [--yes] [--verbose] [--binary <path>]
  signet discover groups <path>...
  signet discover <path>...
  signet discover cases <path>... [--case <id>]
  signet discover cases <path>... [--case <id>] --checks
  signet completion zsh

Commands:
  validate     Validate acceptance YAML files.
  run          Run acceptance YAML files against their subject binaries.
  discover     List acceptance groups and cases without running commands.
  completion   Generate shell completion scripts.

Options:
  --yes            Run without per-command confirmation.
  --verbose        Print executed command, exit code, stdout, and stderr.
  --binary <path>  Override subject.binary for run.
`)
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
