package main

import (
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

func discoverCmd(args []string, stdout io.Writer) int {
	if len(args) == 1 && isHelpArg(args[0]) {
		printDiscoverHelp(stdout)
		return 0
	}
	if len(args) == 0 {
		fmt.Fprintln(stdout, "invalid usage: signet discover groups <path>... | signet discover cases <path>... [--checks]")
		return 2
	}
	if len(args) == 1 {
		return discoverGroups([]string{args[0]}, stdout)
	}

	switch args[0] {
	case "groups":
		if len(args) == 2 && isHelpArg(args[1]) {
			printDiscoverGroupsHelp(stdout)
			return 0
		}
		if len(args) < 2 {
			fmt.Fprintln(stdout, "invalid usage: signet discover groups <path>...")
			return 2
		}
		return discoverGroups(args[1:], stdout)
	case "cases":
		if len(args) == 2 && isHelpArg(args[1]) {
			printDiscoverCasesHelp(stdout)
			return 0
		}
		opts := discoverCaseOptions{}
		var paths []string
		for i := 1; i < len(args); i++ {
			switch args[i] {
			case "--checks":
				opts.showChecks = true
				continue
			case "--case", "--id":
				if i+1 >= len(args) {
					fmt.Fprintf(stdout, "invalid usage: %s requires a value\n", args[i])
					return 2
				}
				opts.caseID = args[i+1]
				i++
				continue
			}
			paths = append(paths, args[i])
		}
		if len(paths) == 0 {
			fmt.Fprintln(stdout, "invalid usage: signet discover cases <path>... [--case <id>] [--checks]")
			return 2
		}
		return discoverCases(paths, opts, stdout)
	default:
		return discoverGroups(args, stdout)
	}
}

func printDiscoverHelp(w io.Writer) {
	fmt.Fprint(w, `Usage:
  signet discover groups <path>...
  signet discover <path>...
  signet discover cases <path>... [--case <id>]
  signet discover cases <path>... [--case <id>] --checks

List acceptance groups and cases without running commands.
`)
}

func printDiscoverGroupsHelp(w io.Writer) {
	fmt.Fprint(w, `Usage:
  signet discover groups <path>...
  signet discover <path>...

List acceptance files discovered under files or directories.
`)
}

func printDiscoverCasesHelp(w io.Writer) {
	fmt.Fprint(w, `Usage:
  signet discover cases <path>... [--case <id>]
  signet discover cases <path>... [--case <id>] --checks

List cases under acceptance files or directories. Use --checks to include
expected checks. Use --case, or its --id alias, to select one case id.
`)
}

func discoverGroups(paths []string, stdout io.Writer) int {
	files, err := acceptanceFilesForPaths(paths)
	if err != nil {
		fmt.Fprintf(stdout, "%s %s:\n%s %s\n", red("invalid"), pathListLabel(paths), yellow("-"), err)
		return 1
	}

	fmt.Fprintln(stdout, bold("GROUP FILE SUITE CASES STEPS STATUS"))
	validGroups := 0
	invalidGroups := 0
	for _, file := range files {
		spec, errs := loadSpec(file)
		if len(errs) > 0 {
			invalidGroups++
			fmt.Fprintf(stdout, "%s %s\n", red("INVALID"), file)
			for _, err := range errs {
				if err.Path == "" {
					fmt.Fprintf(stdout, "  %s %s\n", yellow("-"), err.Message)
					continue
				}
				fmt.Fprintf(stdout, "  %s %s %s\n", yellow("-"), err.Path, err.Message)
			}
			continue
		}
		validGroups++
		fmt.Fprintf(stdout, "%s %s %s %s %s %s\n", cyan("GROUP"), file, spec.Suite, plural(len(spec.Cases), "case"), plural(countSteps(spec), "step"), green("valid"))
	}
	if invalidGroups > 0 {
		fmt.Fprintf(stdout, "%s, %s\n", plural(validGroups, "group"), red(plural(invalidGroups, "invalid")))
		return 1
	}
	fmt.Fprintln(stdout, green(plural(validGroups, "group")))
	return 0
}

type discoverSummary struct {
	groups        int
	cases         int
	steps         int
	checks        int
	invalidGroups int
}

type discoverCaseOptions struct {
	showChecks bool
	caseID     string
}

func discoverCases(paths []string, opts discoverCaseOptions, stdout io.Writer) int {
	files, err := acceptanceFilesForPaths(paths)
	if err != nil {
		fmt.Fprintf(stdout, "%s %s:\n%s %s\n", red("invalid"), pathListLabel(paths), yellow("-"), err)
		return 1
	}

	total := discoverSummary{}
	failed := false
	for fileIndex, file := range files {
		if fileIndex > 0 {
			fmt.Fprintln(stdout)
		}
		summary, code := discoverCasesFile(file, opts, stdout)
		total.add(summary)
		if code != 0 {
			failed = true
		}
	}
	if total.cases == 0 && total.invalidGroups == 0 && opts.caseID != "" {
		fmt.Fprintf(stdout, "%s %s:\n%s case id %q not found\n", red("invalid"), pathListLabel(paths), yellow("-"), opts.caseID)
		return 1
	}
	if len(files) == 1 {
		if failed {
			return 1
		}
		return 0
	}

	if failed {
		fmt.Fprintf(stdout, "%s, %s\n", plural(total.groups, "group"), red(plural(total.invalidGroups, "invalid group")))
		return 1
	}
	if opts.showChecks {
		fmt.Fprintf(stdout, "%s, %s, %s, %s\n", green(plural(total.groups, "group")), green(plural(total.cases, "case")), green(plural(total.steps, "step")), green(plural(total.checks, "check")))
		return 0
	}
	fmt.Fprintf(stdout, "%s, %s, %s\n", green(plural(total.groups, "group")), green(plural(total.cases, "case")), green(plural(total.steps, "step")))
	return 0
}

func discoverCasesFile(file string, opts discoverCaseOptions, stdout io.Writer) (discoverSummary, int) {
	spec, errs := loadSpec(file)
	if len(errs) > 0 {
		printInvalid(stdout, file, errs)
		return discoverSummary{invalidGroups: 1}, 1
	}

	cases := selectedCases(spec.Cases, opts.caseID)
	if len(cases) == 0 {
		return discoverSummary{}, 0
	}

	summary := discoverSummary{
		groups: 1,
		cases:  len(cases),
		steps:  countCaseSteps(cases),
		checks: countCaseChecks(cases),
	}

	fmt.Fprintf(stdout, "%s %s %s\n", cyan("GROUP"), file, spec.Suite)
	fmt.Fprintln(stdout, bold("CASES"))
	for caseIndex, c := range cases {
		if caseIndex > 0 {
			printCaseSeparator(stdout)
		}
		fmt.Fprintf(stdout, "%s %s %s\n", cyan("CASE"), caseDisplay(c), dim("("+plural(len(c.Steps), "step")+")"))
		if !opts.showChecks {
			continue
		}
		for _, step := range c.Steps {
			fmt.Fprintf(stdout, "%s %s\n", cyan("STEP"), step.Name)
			fmt.Fprintf(stdout, "%s %s\n", cyan("COMMAND"), formatCommand(step, spec.Subject.Binary))
			for _, check := range describeChecks(step) {
				fmt.Fprintf(stdout, "%s %s\n", yellow("CHECK"), check)
			}
		}
	}

	if opts.showChecks {
		fmt.Fprintf(stdout, "%s, %s, %s\n", green(plural(summary.cases, "case")), green(plural(summary.steps, "step")), green(plural(summary.checks, "check")))
		return summary, 0
	}
	fmt.Fprintf(stdout, "%s, %s\n", green(plural(summary.cases, "case")), green(plural(summary.steps, "step")))
	return summary, 0
}

func (summary *discoverSummary) add(other discoverSummary) {
	summary.groups += other.groups
	summary.cases += other.cases
	summary.steps += other.steps
	summary.checks += other.checks
	summary.invalidGroups += other.invalidGroups
}

func selectedCases(cases []Case, caseID string) []Case {
	if caseID == "" {
		return cases
	}
	var selected []Case
	for _, c := range cases {
		if c.ID == caseID {
			selected = append(selected, c)
		}
	}
	return selected
}

func caseDisplay(c Case) string {
	return fmt.Sprintf("id=%s name=%q", c.ID, c.Name)
}

func countCaseSteps(cases []Case) int {
	total := 0
	for _, c := range cases {
		total += len(c.Steps)
	}
	return total
}

func countCaseChecks(cases []Case) int {
	total := 0
	for _, c := range cases {
		for _, step := range c.Steps {
			total += countStepChecks(step)
		}
	}
	return total
}

func acceptanceFiles(path string) ([]string, error) {
	info, err := os.Stat(path)
	if err != nil {
		return nil, err
	}
	if !info.IsDir() {
		if !isAcceptanceFile(path) {
			return nil, fmt.Errorf("%s is not an acceptance YAML file", path)
		}
		return []string{path}, nil
	}

	var files []string
	err = filepath.WalkDir(path, func(candidate string, entry fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if entry.IsDir() {
			switch entry.Name() {
			case ".git", "bin":
				return filepath.SkipDir
			}
			return nil
		}
		if isAcceptanceFile(candidate) {
			files = append(files, candidate)
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	sort.Strings(files)
	return files, nil
}

func acceptanceFilesForPaths(paths []string) ([]string, error) {
	seen := map[string]bool{}
	var files []string
	for _, path := range paths {
		matches, err := acceptanceFiles(path)
		if err != nil {
			return nil, err
		}
		for _, file := range matches {
			if seen[file] {
				continue
			}
			seen[file] = true
			files = append(files, file)
		}
	}
	sort.Strings(files)
	if len(files) == 0 {
		return nil, fmt.Errorf("no acceptance YAML files found in %s", pathListLabel(paths))
	}
	return files, nil
}

func pathListLabel(paths []string) string {
	if len(paths) == 1 {
		return paths[0]
	}
	return strings.Join(paths, ", ")
}

func isAcceptanceFile(path string) bool {
	base := filepath.Base(path)
	return base == "acceptance.yaml" || strings.HasSuffix(base, ".acceptance.yaml")
}

func describeChecks(step Step) []string {
	var checks []string
	if step.Expect.ExitCode != nil {
		checks = append(checks, fmt.Sprintf("exitCode == %d", *step.Expect.ExitCode))
	}
	checks = append(checks, describeStreamChecks("stdout", step.Expect.Stdout)...)
	checks = append(checks, describeStreamChecks("stderr", step.Expect.Stderr)...)
	return checks
}

func describeStreamChecks(name string, expect StreamExpect) []string {
	var checks []string
	for _, value := range expect.Contains {
		checks = append(checks, fmt.Sprintf("%s contains %q", name, value))
	}
	for _, value := range expect.OrderedContains {
		checks = append(checks, fmt.Sprintf("%s orderedContains %q", name, value))
	}
	for _, value := range expect.NotContains {
		checks = append(checks, fmt.Sprintf("%s notContains %q", name, value))
	}
	for _, value := range expect.Matches {
		checks = append(checks, fmt.Sprintf("%s matches %q", name, value))
	}
	return checks
}
