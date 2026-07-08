package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestPrepareSetupCreatesFilesAndExpandsVariables(t *testing.T) {
	spec := minimalSetupTestSpec()
	spec.Setup.Files = []SetupFile{
		{
			Name: "appConfig",
			Path: "config/app.yaml",
			Content: "commands:\n" +
				"  - \"invoke\"\n",
		},
	}

	ctx, errs := prepareSetup(&spec, false)
	if len(errs) > 0 {
		t.Fatalf("prepareSetup returned errors: %+v", errs)
	}
	defer ctx.cleanup()

	configPath := ctx.files["appConfig"]
	if configPath == "" {
		t.Fatal("setup file path was not recorded")
	}
	content, err := os.ReadFile(configPath)
	if err != nil {
		t.Fatalf("read setup file: %v", err)
	}
	if string(content) != spec.Setup.Files[0].Content {
		t.Fatalf("setup file content mismatch: %q", string(content))
	}

	expanded, err := ctx.expandString("--config=${file.appConfig};dir=${tmp}")
	if err != nil {
		t.Fatalf("expandString returned error: %v", err)
	}
	if !strings.Contains(expanded, "--config="+configPath) {
		t.Fatalf("expanded config path missing from %q", expanded)
	}
	if !strings.Contains(expanded, "dir="+ctx.dir) {
		t.Fatalf("expanded setup dir missing from %q", expanded)
	}

	step, err := expandStep(Step{Run: Run{
		Args:  []string{"--config", "${file.appConfig}"},
		Shell: "cat ${file.appConfig}",
		Stdin: "${tmp}",
	}}, ctx)
	if err != nil {
		t.Fatalf("expandStep returned error: %v", err)
	}
	if step.Run.Args[1] != configPath {
		t.Fatalf("arg variable was not expanded: %#v", step.Run.Args)
	}
	if !strings.Contains(step.Run.Shell, configPath) {
		t.Fatalf("shell variable was not expanded: %q", step.Run.Shell)
	}
	if step.Run.Stdin != ctx.dir {
		t.Fatalf("stdin variable was not expanded: %q", step.Run.Stdin)
	}
	aliasExpanded, err := ctx.expandString("${setup.files.appConfig}:${setup.dir}")
	if err != nil {
		t.Fatalf("expandString returned alias error: %v", err)
	}
	if aliasExpanded != configPath+":"+ctx.dir {
		t.Fatalf("alias variables expanded incorrectly: %q", aliasExpanded)
	}
}

func TestSetupRejectsUnknownVariables(t *testing.T) {
	spec := minimalSetupTestSpec()
	ctx, errs := prepareSetup(&spec, false)
	if len(errs) > 0 {
		t.Fatalf("prepareSetup returned errors: %+v", errs)
	}

	if _, err := ctx.expandString("${file.missing}"); err == nil {
		t.Fatal("expected unknown setup variable error")
	}
}

func TestValidateSetupRejectsInvalidPaths(t *testing.T) {
	for _, path := range []string{
		"",
		filepath.Join(os.TempDir(), "signet-config.yaml"),
		"../config.yaml",
		"config/../../escape.yaml",
		".",
	} {
		t.Run(path, func(t *testing.T) {
			errs := validateSetup(Setup{Files: []SetupFile{{Name: "config", Path: path}}})
			if !hasValidationError(errs, "setup.files[0].path") {
				t.Fatalf("expected setup.files[0].path validation error for %q, got %+v", path, errs)
			}
		})
	}
}

func TestValidateSetupRejectsDuplicateNames(t *testing.T) {
	errs := validateSetup(Setup{Files: []SetupFile{
		{Name: "config", Path: "one.yaml"},
		{Name: "config", Path: "two.yaml"},
	}})
	if !hasValidationError(errs, "setup.files[1].name") {
		t.Fatalf("expected duplicate setup file name error, got %+v", errs)
	}
}

func TestSetupKeepTempOptionPreservesDirectory(t *testing.T) {
	spec := minimalSetupTestSpec()
	spec.Setup.Files = []SetupFile{{Name: "config", Path: "config.yaml", Content: "kept: true\n"}}

	ctx, errs := prepareSetup(&spec, true)
	if len(errs) > 0 {
		t.Fatalf("prepareSetup returned errors: %+v", errs)
	}
	tempDir := ctx.dir
	ctx.cleanup()
	defer os.RemoveAll(tempDir)

	if _, err := os.Stat(tempDir); err != nil {
		t.Fatalf("expected keep-temp directory to remain: %v", err)
	}
}

func TestPrepareSetupRequireEnv(t *testing.T) {
	t.Run("missing or empty", func(t *testing.T) {
		const name = "SIGNET_TEST_REQUIRED_EMPTY"
		t.Setenv(name, "")
		spec := minimalSetupTestSpec()
		spec.Setup.RequireEnv = []string{name}
		spec.Setup.Files = []SetupFile{{Name: "config", Path: "config.yaml"}}

		ctx, errs := prepareSetup(&spec, false)
		if ctx != nil {
			t.Fatalf("expected no setup context, got %#v", ctx)
		}
		if !hasValidationError(errs, "setup.requireEnv[0]") {
			t.Fatalf("expected requireEnv validation error, got %+v", errs)
		}
	})

	t.Run("set", func(t *testing.T) {
		const name = "SIGNET_TEST_REQUIRED_SET"
		t.Setenv(name, "present")
		spec := minimalSetupTestSpec()
		spec.Setup.RequireEnv = []string{name}
		spec.Setup.Files = []SetupFile{{Name: "config", Path: "config.yaml"}}

		ctx, errs := prepareSetup(&spec, false)
		if len(errs) > 0 {
			t.Fatalf("prepareSetup returned errors: %+v", errs)
		}
		defer ctx.cleanup()
		if ctx.files["config"] == "" {
			t.Fatal("setup file was not created")
		}
	})
}

func TestPrepareSetupLoadsEnvFilesBeforeRequireEnvAndExpandsHome(t *testing.T) {
	const name = "SIGNET_TEST_DOTENV_REQUIRED"
	unsetEnvForTest(t, name)
	home := t.TempDir()
	t.Setenv("HOME", home)
	if err := os.WriteFile(filepath.Join(home, ".env"), []byte(name+"=from-home-env\n"), 0600); err != nil {
		t.Fatalf("write env file: %v", err)
	}

	spec := minimalSetupTestSpec()
	spec.Setup.EnvFiles = []string{"~/.env"}
	spec.Setup.RequireEnv = []string{name}

	ctx, errs := prepareSetup(&spec, false)
	if len(errs) > 0 {
		t.Fatalf("prepareSetup returned errors: %+v", errs)
	}
	defer ctx.cleanup()

	value, ok := ctx.lookupEnv(name)
	if !ok || value != "from-home-env" {
		t.Fatalf("expected loaded env value, got %q ok=%v", value, ok)
	}
}

func TestPrepareSetupEnvFileProcessEnvPrecedence(t *testing.T) {
	const name = "SIGNET_TEST_DOTENV_PRECEDENCE"
	t.Setenv(name, "from-process")
	envFile := filepath.Join(t.TempDir(), "dotenv.env")
	if err := os.WriteFile(envFile, []byte(name+"=from-file\n"), 0600); err != nil {
		t.Fatalf("write env file: %v", err)
	}

	spec := minimalSetupTestSpec()
	spec.Setup.EnvFiles = []string{envFile}
	spec.Setup.RequireEnv = []string{name}

	ctx, errs := prepareSetup(&spec, false)
	if len(errs) > 0 {
		t.Fatalf("prepareSetup returned errors: %+v", errs)
	}
	defer ctx.cleanup()

	value, ok := ctx.lookupEnv(name)
	if !ok || value != "from-process" {
		t.Fatalf("expected process env to win, got %q ok=%v", value, ok)
	}
	result := executeStep(Step{Run: Run{Args: []string{name}}}, &spec, "/usr/bin/printenv", ctx.commandEnv())
	if result.exitCode != 0 {
		t.Fatalf("printenv failed: exit=%d stderr=%q", result.exitCode, result.stderr)
	}
	if strings.TrimSpace(result.stdout) != "from-process" {
		t.Fatalf("expected command to see process env value, got %q", result.stdout)
	}
}

func TestPrepareSetupMissingEnvFile(t *testing.T) {
	spec := minimalSetupTestSpec()
	spec.Setup.EnvFiles = []string{filepath.Join(t.TempDir(), "missing.env")}

	ctx, errs := prepareSetup(&spec, false)
	if ctx != nil {
		t.Fatalf("expected no setup context, got %#v", ctx)
	}
	if !hasValidationError(errs, "setup.envFiles[0]") {
		t.Fatalf("expected env file validation error, got %+v", errs)
	}
}

func TestPrepareSetupRejectsInvalidDotenvName(t *testing.T) {
	envFile := filepath.Join(t.TempDir(), "dotenv.env")
	if err := os.WriteFile(envFile, []byte("BAD-NAME=value\n"), 0600); err != nil {
		t.Fatalf("write env file: %v", err)
	}

	spec := minimalSetupTestSpec()
	spec.Setup.EnvFiles = []string{envFile}

	ctx, errs := prepareSetup(&spec, false)
	if ctx != nil {
		t.Fatalf("expected no setup context, got %#v", ctx)
	}
	if !hasValidationError(errs, "setup.envFiles[0]") {
		t.Fatalf("expected env file syntax validation error, got %+v", errs)
	}
}

func TestExecuteStepReceivesSetupEnvFileValues(t *testing.T) {
	const name = "SIGNET_TEST_DOTENV_COMMAND_ENV"
	unsetEnvForTest(t, name)
	envFile := filepath.Join(t.TempDir(), "dotenv.env")
	if err := os.WriteFile(envFile, []byte(name+"=from-file\n"), 0600); err != nil {
		t.Fatalf("write env file: %v", err)
	}

	spec := minimalSetupTestSpec()
	spec.Setup.EnvFiles = []string{envFile}

	ctx, errs := prepareSetup(&spec, false)
	if len(errs) > 0 {
		t.Fatalf("prepareSetup returned errors: %+v", errs)
	}
	defer ctx.cleanup()

	result := executeStep(Step{Run: Run{Args: []string{name}}}, &spec, "/usr/bin/printenv", ctx.commandEnv())
	if result.exitCode != 0 {
		t.Fatalf("printenv failed: exit=%d stderr=%q", result.exitCode, result.stderr)
	}
	if strings.TrimSpace(result.stdout) != "from-file" {
		t.Fatalf("expected command to see env file value, got %q", result.stdout)
	}
}

func TestValidateSetupRejectsInvalidEnvFiles(t *testing.T) {
	for _, path := range []string{"", "~other/.env", "bad\x00path"} {
		t.Run(path, func(t *testing.T) {
			errs := validateSetup(Setup{EnvFiles: []string{path}})
			if !hasValidationError(errs, "setup.envFiles[0]") {
				t.Fatalf("expected setup.envFiles[0] validation error for %q, got %+v", path, errs)
			}
		})
	}
}

func minimalSetupTestSpec() Spec {
	exitCode := 0
	return Spec{
		Version: 1,
		Cases: []Case{
			{
				ID:   "case",
				Name: "case",
				Steps: []Step{
					{
						Name: "step",
						Run:  Run{Shell: "true"},
						Expect: Expect{
							ExitCode: &exitCode,
						},
					},
				},
			},
		},
	}
}

func unsetEnvForTest(t *testing.T, name string) {
	t.Helper()
	previous, hadPrevious := os.LookupEnv(name)
	if err := os.Unsetenv(name); err != nil {
		t.Fatalf("unset env %s: %v", name, err)
	}
	t.Cleanup(func() {
		if hadPrevious {
			_ = os.Setenv(name, previous)
		} else {
			_ = os.Unsetenv(name)
		}
	})
}

func hasValidationError(errs []validationError, path string) bool {
	for _, err := range errs {
		if err.Path == path {
			return true
		}
	}
	return false
}
