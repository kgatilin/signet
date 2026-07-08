package main

import (
	"bufio"
	"bytes"
	"strings"
	"testing"
)

func TestWriteStepAlwaysRequiresConfirmation(t *testing.T) {
	confirm := false
	spec := &Spec{Defaults: Defaults{Confirm: &confirm}}

	readStep := Step{Run: Run{Kind: runKindRead}}
	writeStep := Step{Run: Run{Kind: runKindWrite}}

	if shouldConfirm(spec, runOptions{yes: true}, readStep) {
		t.Fatal("read step should honor --yes")
	}
	if shouldConfirm(spec, runOptions{}, readStep) {
		t.Fatal("read step should honor defaults.confirm=false")
	}
	if !shouldConfirm(spec, runOptions{yes: true}, writeStep) {
		t.Fatal("write step must require confirmation even with --yes")
	}
	if !shouldConfirm(spec, runOptions{}, writeStep) {
		t.Fatal("write step must require confirmation even with defaults.confirm=false")
	}
}

func TestWriteConfirmationPromptDoesNotSuggestYesBypass(t *testing.T) {
	step := Step{
		Name: "create sample",
		Run:  Run{Kind: runKindWrite, Args: []string{"create"}},
	}
	var stdout bytes.Buffer

	ok := confirmStep(bufio.NewReader(strings.NewReader("y\n")), &stdout, step, "./bin/tool")

	if !ok {
		t.Fatal("expected confirmation to be accepted")
	}
	output := stdout.String()
	if !strings.Contains(output, "Confirm WRITE") {
		t.Fatalf("expected write prompt marker, got %q", output)
	}
	if strings.Contains(output, "--yes") {
		t.Fatalf("write prompt must not suggest --yes bypass, got %q", output)
	}
}

func TestValidateSpecRejectsUnknownRunKind(t *testing.T) {
	exitCode := 0
	spec := Spec{
		Version: 1,
		Cases: []Case{{
			ID:   "case",
			Name: "case",
			Steps: []Step{{
				Name: "step",
				Run:  Run{Kind: "mutate", Args: []string{"ok"}},
				Expect: Expect{
					ExitCode: &exitCode,
				},
			}},
		}},
	}

	errs := validateSpec(&spec)
	if !hasValidationError(errs, "cases[0].steps[0].run.kind") {
		t.Fatalf("expected run.kind validation error, got %+v", errs)
	}
}
