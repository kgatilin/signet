package main

import (
	"fmt"
	"io"
	"os"
	"strings"

	"github.com/kgatilin/signet/internal/report"
	"github.com/spf13/cobra"
)

func newReportCommand(stdout, stderr io.Writer, stdin io.Reader) *cobra.Command {
	format := "yaml"
	cmd := &cobra.Command{
		Use:   "report [<file>]",
		Short: "Turn a go test -json stream of SDK features into a document.",
		Args:  cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			if format != "yaml" && format != "cases" {
				fmt.Fprintf(stderr, "invalid usage: unknown format %q (want yaml or cases)\n", format)
				return cliExitError{code: 2}
			}
			in := stdin
			if len(args) == 1 && args[0] != "-" {
				file, err := os.Open(args[0])
				if err != nil {
					fmt.Fprintln(stderr, err)
					return cliExitError{code: 2}
				}
				defer file.Close()
				in = file
			}

			doc, err := report.Parse(in)
			if err != nil {
				fmt.Fprintln(stderr, err)
				return cliExitError{code: 2}
			}
			if format == "cases" {
				report.WriteCases(stdout, doc)
			} else if err := report.WriteYAML(stdout, doc); err != nil {
				fmt.Fprintln(stderr, err)
				return cliExitError{code: 2}
			}
			if doc.Failed() {
				if len(doc.FailedPackages) > 0 {
					fmt.Fprintf(stderr, "failed packages: %s\n", strings.Join(doc.FailedPackages, ", "))
				}
				return cliExitError{code: 1}
			}
			return nil
		},
	}
	cmd.Flags().StringVar(&format, "format", "yaml", "output format: yaml or cases")
	cmd.SetHelpFunc(func(cmd *cobra.Command, args []string) {
		printReportHelp(cmd.OutOrStdout())
	})
	return cmd
}

func printReportHelp(w io.Writer) {
	fmt.Fprint(w, `Usage:
  go test -json ./... | signet report [--format yaml|cases]
  signet report <file> [--format yaml|cases]

Read the go test -json stream of tests written with the signet Go SDK and
write one document for every Feature: scenarios, steps, status, duration and
failure messages. Run the tests with SIGNET_LIST=1 to get the document
without executing any step.

Formats:
  yaml   the feature document (default)
  cases  GROUP and CASE rows, as printed by signet cases

Exit codes: 0 when nothing failed, 1 when a feature or package failed,
2 on invalid usage, unreadable input, or input without go test -json events
(go test run without -json, or no input at all).
`)
}
