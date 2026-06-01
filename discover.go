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
	if len(args) < 2 {
		fmt.Fprintln(stdout, "invalid usage: signet discover groups <path> | signet discover cases <file> [--checks]")
		return 2
	}

	switch args[0] {
	case "groups":
		if len(args) != 2 {
			fmt.Fprintln(stdout, "invalid usage: signet discover groups <path>")
			return 2
		}
		return discoverGroups(args[1], stdout)
	case "cases":
		showChecks := false
		var file string
		for _, arg := range args[1:] {
			if arg == "--checks" {
				showChecks = true
				continue
			}
			if file == "" {
				file = arg
				continue
			}
			fmt.Fprintln(stdout, "invalid usage: signet discover cases <file> [--checks]")
			return 2
		}
		if file == "" {
			fmt.Fprintln(stdout, "invalid usage: signet discover cases <file> [--checks]")
			return 2
		}
		return discoverCases(file, showChecks, stdout)
	default:
		fmt.Fprintf(stdout, "unknown discover target: %s\n", args[0])
		return 2
	}
}

func discoverGroups(path string, stdout io.Writer) int {
	files, err := acceptanceFiles(path)
	if err != nil {
		fmt.Fprintf(stdout, "invalid %s:\n- %s\n", path, err)
		return 1
	}

	fmt.Fprintln(stdout, "GROUP FILE SUITE CASES STEPS")
	for _, file := range files {
		spec, errs := loadSpec(file)
		if len(errs) > 0 {
			printInvalid(stdout, file, errs)
			return 1
		}
		fmt.Fprintf(stdout, "GROUP %s %s %s %s\n", file, spec.Suite, plural(len(spec.Cases), "case"), plural(countSteps(spec), "step"))
	}
	fmt.Fprintln(stdout, plural(len(files), "group"))
	return 0
}

func discoverCases(file string, showChecks bool, stdout io.Writer) int {
	spec, errs := loadSpec(file)
	if len(errs) > 0 {
		printInvalid(stdout, file, errs)
		return 1
	}

	fmt.Fprintf(stdout, "GROUP %s %s\n", file, spec.Suite)
	fmt.Fprintln(stdout, "CASE")
	for _, c := range spec.Cases {
		fmt.Fprintf(stdout, "CASE %s (%s)\n", c.Name, plural(len(c.Steps), "step"))
		if !showChecks {
			continue
		}
		for _, step := range c.Steps {
			fmt.Fprintf(stdout, "STEP %s\n", step.Name)
			for _, check := range describeChecks(step) {
				fmt.Fprintf(stdout, "CHECK %s\n", check)
			}
		}
	}

	if showChecks {
		fmt.Fprintf(stdout, "%s, %s, %s\n", plural(len(spec.Cases), "case"), plural(countSteps(spec), "step"), plural(countChecks(spec), "check"))
		return 0
	}
	fmt.Fprintf(stdout, "%s, %s\n", plural(len(spec.Cases), "case"), plural(countSteps(spec), "step"))
	return 0
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
