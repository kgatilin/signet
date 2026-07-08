package main

import (
	"bytes"
	"strings"
	"testing"
	"time"

	"gopkg.in/yaml.v3"
)

func TestBuildCommandsUnmarshalYAML(t *testing.T) {
	t.Run("string", func(t *testing.T) {
		var setup Setup
		if err := yaml.Unmarshal([]byte("build: make build\n"), &setup); err != nil {
			t.Fatalf("unmarshal: %v", err)
		}
		if len(setup.Build) != 1 || setup.Build[0] != "make build" {
			t.Fatalf("expected single build command, got %#v", setup.Build)
		}
	})

	t.Run("list", func(t *testing.T) {
		var setup Setup
		if err := yaml.Unmarshal([]byte("build:\n  - make build\n  - make seed\n"), &setup); err != nil {
			t.Fatalf("unmarshal: %v", err)
		}
		if len(setup.Build) != 2 || setup.Build[1] != "make seed" {
			t.Fatalf("expected two build commands, got %#v", setup.Build)
		}
	})

	t.Run("rejects mapping", func(t *testing.T) {
		var setup Setup
		if err := yaml.Unmarshal([]byte("build:\n  cmd: make\n"), &setup); err == nil {
			t.Fatal("expected error for mapping build")
		}
	})
}

func TestBinaryVariableExpansion(t *testing.T) {
	ctx := &setupContext{binary: "./bin/tool"}
	expanded, err := ctx.expandString("${binary} serve; ${subject.binary} check")
	if err != nil {
		t.Fatalf("expandString: %v", err)
	}
	if expanded != "./bin/tool serve; ./bin/tool check" {
		t.Fatalf("binary variables expanded incorrectly: %q", expanded)
	}
}

func TestBinaryVariableUnknownWhenUnset(t *testing.T) {
	ctx := &setupContext{}
	if _, err := ctx.expandString("${binary}"); err == nil {
		t.Fatal("expected unknown variable error when binary is unset")
	}
}

func TestRunBuildSuccessAndFailure(t *testing.T) {
	ctx := &setupContext{binary: "/bin/echo"}

	t.Run("success", func(t *testing.T) {
		spec := &Spec{Setup: Setup{Build: BuildCommands{"true", "${binary} ok"}}}
		var stdout, stderr bytes.Buffer
		if !runBuild(spec, ctx, &stdout, &stderr) {
			t.Fatalf("expected build to succeed, stdout=%q", stdout.String())
		}
		if !strings.Contains(stdout.String(), "BUILD /bin/echo ok") {
			t.Fatalf("expected expanded build line, got %q", stdout.String())
		}
	})

	t.Run("failure stops the group", func(t *testing.T) {
		spec := &Spec{Setup: Setup{Build: BuildCommands{"exit 4"}}}
		var stdout, stderr bytes.Buffer
		if runBuild(spec, ctx, &stdout, &stderr) {
			t.Fatal("expected build to fail")
		}
		if !strings.Contains(stdout.String(), "FAIL build: exit 4") {
			t.Fatalf("expected build failure line, got %q", stdout.String())
		}
	})
}

func TestValidateSetupServices(t *testing.T) {
	t.Run("empty build command", func(t *testing.T) {
		errs := validateSetup(Setup{Build: BuildCommands{"  "}})
		if !hasValidationError(errs, "setup.build[0]") {
			t.Fatalf("expected empty build error, got %+v", errs)
		}
	})

	t.Run("missing name", func(t *testing.T) {
		errs := validateSetup(Setup{Services: []Service{{Shell: "serve"}}})
		if !hasValidationError(errs, "setup.services[0].name") {
			t.Fatalf("expected missing name error, got %+v", errs)
		}
	})

	t.Run("duplicate name", func(t *testing.T) {
		errs := validateSetup(Setup{Services: []Service{
			{Name: "api", Shell: "serve"},
			{Name: "api", Shell: "serve2"},
		}})
		if !hasValidationError(errs, "setup.services[1].name") {
			t.Fatalf("expected duplicate name error, got %+v", errs)
		}
	})

	t.Run("no args or shell", func(t *testing.T) {
		errs := validateSetup(Setup{Services: []Service{{Name: "api"}}})
		if !hasValidationError(errs, "setup.services[0].args") {
			t.Fatalf("expected args/shell error, got %+v", errs)
		}
	})

	t.Run("bad durations", func(t *testing.T) {
		errs := validateSetup(Setup{Services: []Service{{
			Name:        "api",
			Shell:       "serve",
			StopTimeout: "later",
			Ready:       Ready{Timeout: "soon"},
		}}})
		if !hasValidationError(errs, "setup.services[0].stopTimeout") {
			t.Fatalf("expected stopTimeout error, got %+v", errs)
		}
		if !hasValidationError(errs, "setup.services[0].ready.timeout") {
			t.Fatalf("expected ready.timeout error, got %+v", errs)
		}
	})
}

func TestServiceLifecycleStartsReadyAndStops(t *testing.T) {
	ctx := &setupContext{}
	service := Service{
		Name:  "svc",
		Shell: "echo ready; sleep 30",
		Ready: Ready{Log: "ready", Timeout: "3s"},
	}
	var stdout, stderr bytes.Buffer

	rs, err := startService(service, ctx)
	if err != nil {
		t.Fatalf("startService: %v", err)
	}
	if !waitServiceReady(rs, service, ctx, &stdout, &stderr) {
		t.Fatalf("service never became ready, out=%q", stdout.String())
	}

	select {
	case <-rs.done:
		t.Fatal("service exited before it was stopped")
	default:
	}

	stopService(rs, &stdout)

	select {
	case <-rs.done:
	case <-time.After(5 * time.Second):
		t.Fatal("service was not stopped")
	}
}

func TestServiceReadyFailsWhenProcessExitsEarly(t *testing.T) {
	ctx := &setupContext{}
	service := Service{
		Name:  "crasher",
		Shell: "echo boom; exit 1",
		Ready: Ready{Shell: "false", Timeout: "2s"},
	}
	var stdout, stderr bytes.Buffer

	rs, err := startService(service, ctx)
	if err != nil {
		t.Fatalf("startService: %v", err)
	}
	if waitServiceReady(rs, service, ctx, &stdout, &stderr) {
		t.Fatal("expected readiness to fail for a crashing service")
	}
	if !strings.Contains(stdout.String(), "exited before it was ready") {
		t.Fatalf("expected early-exit report, got %q", stdout.String())
	}
}
