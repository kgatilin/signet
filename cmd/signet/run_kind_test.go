package main

import (
	"bufio"
	"bytes"
	"strings"
	"testing"
)

func TestConfirmationSemantics(t *testing.T) {
	confirmTrue := true
	confirmFalse := false

	readStep := Step{Run: Run{Kind: runKindRead}}
	writeStep := Step{Run: Run{Kind: runKindWrite}}

	// Read steps never prompt, regardless of --yes or defaults.confirm.
	unset := &Spec{}
	if shouldConfirm(unset, runOptions{}, readStep) {
		t.Fatal("read step must never prompt")
	}
	if shouldConfirm(unset, runOptions{yes: true}, readStep) {
		t.Fatal("read step must never prompt with --yes")
	}
	if shouldConfirm(&Spec{Defaults: Defaults{Confirm: &confirmTrue}}, runOptions{}, readStep) {
		t.Fatal("read step must never prompt even with defaults.confirm=true")
	}

	// Write steps prompt by default.
	if !shouldConfirm(unset, runOptions{}, writeStep) {
		t.Fatal("write step must prompt by default")
	}
	// --yes skips the write prompt.
	if shouldConfirm(unset, runOptions{yes: true}, writeStep) {
		t.Fatal("write step should honor --yes")
	}
	// defaults.confirm=false pre-approves writes suite-wide.
	if shouldConfirm(&Spec{Defaults: Defaults{Confirm: &confirmFalse}}, runOptions{}, writeStep) {
		t.Fatal("write step should honor defaults.confirm=false")
	}
	// defaults.confirm=true keeps the write prompt.
	if !shouldConfirm(&Spec{Defaults: Defaults{Confirm: &confirmTrue}}, runOptions{}, writeStep) {
		t.Fatal("write step must prompt with defaults.confirm=true")
	}
}

func TestWriteConfirmationPromptSuggestsYesBypass(t *testing.T) {
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
	if !strings.Contains(output, "--yes") {
		t.Fatalf("write prompt must advertise the --yes bypass, got %q", output)
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
