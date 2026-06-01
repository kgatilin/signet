package main

import (
	"fmt"
	"os"
	"strings"
	"time"

	"gopkg.in/yaml.v3"
)

type Spec struct {
	Version     int      `yaml:"version"`
	Suite       string   `yaml:"suite"`
	Description string   `yaml:"description"`
	Subject     Subject  `yaml:"subject"`
	Defaults    Defaults `yaml:"defaults"`
	Cases       []Case   `yaml:"cases"`
}

type Subject struct {
	Binary string `yaml:"binary"`
}

type Defaults struct {
	Timeout string `yaml:"timeout"`
	Confirm *bool  `yaml:"confirm"`
}

type Case struct {
	Name  string `yaml:"name"`
	Steps []Step `yaml:"steps"`
}

type Step struct {
	Name   string `yaml:"name"`
	Run    Run    `yaml:"run"`
	Expect Expect `yaml:"expect"`
}

type Run struct {
	Args    []string `yaml:"args"`
	Shell   string   `yaml:"shell"`
	Stdin   string   `yaml:"stdin"`
	Timeout string   `yaml:"timeout"`
}

type Expect struct {
	ExitCode *int         `yaml:"exitCode"`
	Stdout   StreamExpect `yaml:"stdout"`
	Stderr   StreamExpect `yaml:"stderr"`
}

type StreamExpect struct {
	Contains        []string `yaml:"contains"`
	NotContains     []string `yaml:"notContains"`
	OrderedContains []string `yaml:"orderedContains"`
	Matches         []string `yaml:"matches"`
}

type validationError struct {
	Path    string
	Message string
}

func loadSpec(path string) (*Spec, []validationError) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, []validationError{{Message: err.Error()}}
	}

	var doc yaml.Node
	if err := yaml.Unmarshal(data, &doc); err != nil {
		return nil, []validationError{{Message: err.Error()}}
	}
	if len(doc.Content) == 0 {
		return nil, []validationError{{Message: "file is empty"}}
	}

	root := doc.Content[0]
	errs := validateNode(root)
	if len(errs) > 0 {
		return nil, errs
	}

	var spec Spec
	if err := root.Decode(&spec); err != nil {
		return nil, []validationError{{Message: err.Error()}}
	}
	errs = validateSpec(&spec)
	if len(errs) > 0 {
		return nil, errs
	}

	return &spec, nil
}

func validateNode(root *yaml.Node) []validationError {
	if root.Kind != yaml.MappingNode {
		return []validationError{{Message: "root must be a mapping"}}
	}

	var errs []validationError
	cases := mapLookup(root, "cases")
	if cases == nil {
		return errs
	}
	if cases.Kind != yaml.SequenceNode {
		return append(errs, validationError{Path: "cases", Message: "must be a list"})
	}

	for caseIndex, caseNode := range cases.Content {
		steps := mapLookup(caseNode, "steps")
		if steps == nil {
			continue
		}
		if steps.Kind != yaml.SequenceNode {
			errs = append(errs, validationError{Path: fmt.Sprintf("cases[%d].steps", caseIndex), Message: "must be a list"})
			continue
		}
		for stepIndex, stepNode := range steps.Content {
			expect := mapLookup(stepNode, "expect")
			if expect == nil {
				continue
			}
			exitCode := mapLookup(expect, "exitCode")
			if exitCode != nil && exitCode.Tag != "!!int" {
				errs = append(errs, validationError{
					Path:    fmt.Sprintf("cases[%d].steps[%d].expect.exitCode", caseIndex, stepIndex),
					Message: "must be an integer",
				})
			}
		}
	}
	return errs
}

func validateSpec(spec *Spec) []validationError {
	var errs []validationError
	if spec.Version != 1 {
		errs = append(errs, validationError{Path: "version", Message: "must be 1"})
	}
	if len(spec.Cases) == 0 {
		errs = append(errs, validationError{Path: "cases", Message: "must contain at least one case"})
	}

	for caseIndex, c := range spec.Cases {
		if strings.TrimSpace(c.Name) == "" {
			errs = append(errs, validationError{Path: fmt.Sprintf("cases[%d].name", caseIndex), Message: "is required"})
		}
		if len(c.Steps) == 0 {
			errs = append(errs, validationError{Path: fmt.Sprintf("cases[%d].steps", caseIndex), Message: "must contain at least one step"})
		}
		for stepIndex, step := range c.Steps {
			path := fmt.Sprintf("cases[%d].steps[%d]", caseIndex, stepIndex)
			if strings.TrimSpace(step.Name) == "" {
				errs = append(errs, validationError{Path: path + ".name", Message: "is required"})
			}
			if len(step.Run.Args) == 0 && strings.TrimSpace(step.Run.Shell) == "" {
				errs = append(errs, validationError{Path: path + ".run.args", Message: "must contain at least one argument unless run.shell is set"})
			}
			if step.Expect.ExitCode == nil && countStreamChecks(step.Expect.Stdout) == 0 && countStreamChecks(step.Expect.Stderr) == 0 {
				errs = append(errs, validationError{Path: path + ".expect", Message: "must contain at least one check"})
			}
			if step.Run.Timeout != "" {
				if _, err := time.ParseDuration(step.Run.Timeout); err != nil {
					errs = append(errs, validationError{Path: path + ".run.timeout", Message: "must be a duration"})
				}
			}
		}
	}

	if spec.Defaults.Timeout != "" {
		if _, err := time.ParseDuration(spec.Defaults.Timeout); err != nil {
			errs = append(errs, validationError{Path: "defaults.timeout", Message: "must be a duration"})
		}
	}

	return errs
}

func mapLookup(node *yaml.Node, key string) *yaml.Node {
	if node == nil || node.Kind != yaml.MappingNode {
		return nil
	}
	for i := 0; i < len(node.Content)-1; i += 2 {
		if node.Content[i].Value == key {
			return node.Content[i+1]
		}
	}
	return nil
}

func countSteps(spec *Spec) int {
	total := 0
	for _, c := range spec.Cases {
		total += len(c.Steps)
	}
	return total
}

func countChecks(spec *Spec) int {
	total := 0
	for _, c := range spec.Cases {
		for _, step := range c.Steps {
			total += countStepChecks(step)
		}
	}
	return total
}

func countStepChecks(step Step) int {
	total := 0
	if step.Expect.ExitCode != nil {
		total++
	}
	total += countStreamChecks(step.Expect.Stdout)
	total += countStreamChecks(step.Expect.Stderr)
	return total
}

func countStreamChecks(expect StreamExpect) int {
	return len(expect.Contains) + len(expect.NotContains) + len(expect.OrderedContains) + len(expect.Matches)
}

func plural(n int, word string) string {
	if n == 1 {
		return fmt.Sprintf("%d %s", n, word)
	}
	return fmt.Sprintf("%d %ss", n, word)
}
