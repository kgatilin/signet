package signet

import (
	"bufio"
	"bytes"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/kgatilin/signet/internal/attrs"
)

// fixtureRun is the parsed `go test -json` output of testdata/sdk.
type fixtureRun struct {
	tests map[string]*fixtureTest
	marks []string
}

type fixtureTest struct {
	status  string
	attrs   map[string][]string
	errors  []string
	outputs []string
}

// runFixtures runs the fixture package with extra environment and go test
// arguments and parses its event stream.
func runFixtures(t *testing.T, env []string, args ...string) *fixtureRun {
	t.Helper()
	if testing.Short() {
		t.Skip("runs go test in a subprocess")
	}
	marks := filepath.Join(t.TempDir(), "marks")
	root, err := moduleRoot()
	if err != nil {
		t.Fatal(err)
	}
	cmd := exec.Command("go", append([]string{"test", "-json", "-count=1"}, append(args, "./testdata/sdk")...)...)
	cmd.Dir = root
	cmd.Env = append(os.Environ(), append(env, "SIGNET_FIXTURE_MARKS="+marks)...)
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	out, _ := cmd.Output()

	run := &fixtureRun{tests: map[string]*fixtureTest{}}
	scanner := bufio.NewScanner(bytes.NewReader(out))
	scanner.Buffer(make([]byte, 1<<20), 1<<20)
	for scanner.Scan() {
		var ev struct{ Action, Test, Key, Value, Output, OutputType string }
		if err := json.Unmarshal(scanner.Bytes(), &ev); err != nil || ev.Test == "" {
			continue
		}
		ft := run.tests[ev.Test]
		if ft == nil {
			ft = &fixtureTest{attrs: map[string][]string{}}
			run.tests[ev.Test] = ft
		}
		switch ev.Action {
		case "pass", "fail", "skip":
			ft.status = ev.Action
		case "attr":
			ft.attrs[ev.Key] = append(ft.attrs[ev.Key], attrs.Unescape(ev.Value))
		case "output":
			ft.outputs = append(ft.outputs, ev.Output)
			if ev.OutputType == "error" || ev.OutputType == "error-continue" {
				ft.errors = append(ft.errors, strings.TrimSpace(ev.Output))
			}
		}
	}
	if len(run.tests) == 0 {
		t.Fatalf("no test events; stderr:\n%s\nstdout:\n%s", stderr.String(), out)
	}
	if data, err := os.ReadFile(marks); err == nil {
		run.marks = strings.Fields(string(data))
	}
	return run
}

func (r *fixtureRun) expect(t *testing.T, name, status string) *fixtureTest {
	t.Helper()
	ft := r.tests[name]
	if ft == nil {
		var names []string
		for n := range r.tests {
			names = append(names, n)
		}
		slices.Sort(names)
		t.Fatalf("test %s not run; have:\n  %s", name, strings.Join(names, "\n  "))
	}
	if ft.status != status {
		t.Errorf("%s: status %s, want %s (errors: %q)", name, ft.status, status, ft.errors)
	}
	return ft
}

func (r *fixtureRun) marked(what string) bool { return slices.Contains(r.marks, what) }

func TestFeatureRunsScenariosAsSubtests(t *testing.T) {
	run := runFixtures(t, nil, "-run", "^TestPassing$")

	feature := run.expect(t, "TestPassing", "pass")
	if got := feature.attrs[attrs.Feature]; !slices.Equal(got, []string{"Passing feature"}) {
		t.Errorf("feature attr = %q", got)
	}
	if got := feature.attrs[attrs.Description]; !slices.Equal(got, []string{"First line.\nSecond line."}) {
		t.Errorf("description attr = %q", got)
	}
	if got := feature.attrs[attrs.BackgroundFeature]; !slices.Equal(got, []string{"a shared file"}) {
		t.Errorf("background feature attr = %q", got)
	}
	if got := feature.attrs[attrs.BackgroundScenario]; !slices.Equal(got, []string{"a fresh counter"}) {
		t.Errorf("background scenario attr = %q", got)
	}

	one := run.expect(t, "TestPassing/one", "pass")
	if one.attrs[attrs.ID][0] != "one" || one.attrs[attrs.Scenario][0] != "every step kind passes" {
		t.Errorf("scenario attrs = %v", one.attrs)
	}
	once := run.expect(t, "TestPassing/one/Background_a_shared_file", "pass")
	if once.attrs[attrs.Scope][0] != attrs.ScopeFeature || once.attrs[attrs.Keyword][0] != attrs.KeywordBackground {
		t.Errorf("background attrs = %v", once.attrs)
	}
	run.expect(t, "TestPassing/one/Background_a_fresh_counter", "pass")
	given := run.expect(t, "TestPassing/one/Given_n_is_one", "pass")
	if given.attrs[attrs.Keyword][0] != attrs.KeywordGiven || given.attrs[attrs.Text][0] != "n is one" {
		t.Errorf("given attrs = %v", given.attrs)
	}
	run.expect(t, "TestPassing/one/When_n_doubles", "pass")
	run.expect(t, "TestPassing/one/When_n_doubles/Then_n_is_two", "pass")
	run.expect(t, "TestPassing/one/When_n_doubles/Then_n_is_even", "pass")
	run.expect(t, "TestPassing/one/Then_the_shared_file_exists", "pass")
	run.expect(t, "TestPassing/one/Then_the_per-scenario_file_outlives_its_background_step", "pass")

	run.expect(t, "TestPassing/two", "pass")
	if _, ran := run.tests["TestPassing/two/Background_a_shared_file"]; ran {
		t.Error("BackgroundOnce ran again in the second scenario")
	}
	run.expect(t, "TestPassing/two/Background_a_fresh_counter", "pass")
	run.expect(t, "TestPassing/two/When_nothing_happens/Then_the_shared_file_still_exists", "pass")
	run.expect(t, "TestPassing/two/When_it_is_checked_again/Then_the_background_ran_once", "pass")

	want := []string{"once", "each", "one/given", "one/when", "one/scratch-checked", "each-cleanup", "each", "two/when", "each-cleanup"}
	if !slices.Equal(run.marks, want) {
		t.Errorf("marks = %q", run.marks)
	}
}

func TestFeatureStepFailureRules(t *testing.T) {
	run := runFixtures(t, nil, "-run", "^TestStepFailures$")
	run.expect(t, "TestStepFailures", "fail")

	run.expect(t, "TestStepFailures/when-fails", "fail")
	when := run.expect(t, "TestStepFailures/when-fails/When_the_action_fails", "fail")
	if !strings.Contains(strings.Join(when.errors, "\n"), "action broke\nsecond line") {
		t.Errorf("errors = %q", when.errors)
	}
	run.expect(t, "TestStepFailures/when-fails/When_the_action_fails/Then_its_check", "skip")
	run.expect(t, "TestStepFailures/when-fails/When_a_later_action", "skip")
	run.expect(t, "TestStepFailures/when-fails/When_a_later_action/Then_a_later_check", "skip")

	run.expect(t, "TestStepFailures/then-fails/When_the_action_passes", "fail")
	run.expect(t, "TestStepFailures/then-fails/When_the_action_passes/Then_the_first_check_fails", "fail")
	run.expect(t, "TestStepFailures/then-fails/When_the_action_passes/Then_the_second_check_runs", "pass")
	run.expect(t, "TestStepFailures/then-fails/When_a_later_action", "skip")

	run.expect(t, "TestStepFailures/given-fails/Given_a_broken_precondition", "fail")
	run.expect(t, "TestStepFailures/given-fails/Then_a_direct_check", "skip")
	run.expect(t, "TestStepFailures/given-fails/When_an_action", "skip")

	run.expect(t, "TestStepFailures/skip-in-when", "pass")
	run.expect(t, "TestStepFailures/skip-in-when/When_the_action_skips", "skip")
	run.expect(t, "TestStepFailures/skip-in-when/When_the_action_skips/Then_its_check", "skip")

	for _, m := range []string{"when-fails/then", "when-fails/later", "when-fails/later-then", "then-fails/later", "given-fails/then", "given-fails/when", "skip-in-when/then"} {
		if run.marked(m) {
			t.Errorf("%s ran but should have been skipped", m)
		}
	}
	if !run.marked("then-fails/second") {
		t.Error("the sibling check of a failed check did not run")
	}
}

func TestFeatureBackgroundOnceFailureSkipsLaterScenarios(t *testing.T) {
	run := runFixtures(t, nil, "-run", "^TestBackgroundOnceFails$")

	run.expect(t, "TestBackgroundOnceFails/first", "fail")
	bg := run.expect(t, "TestBackgroundOnceFails/first/Background_a_service_that_does_not_start", "fail")
	if !strings.Contains(strings.Join(bg.errors, "\n"), "service refused to start") {
		t.Errorf("errors = %q", bg.errors)
	}
	run.expect(t, "TestBackgroundOnceFails/first/When_an_action", "skip")
	run.expect(t, "TestBackgroundOnceFails/second", "skip")
	run.expect(t, "TestBackgroundOnceFails/second/When_an_action", "skip")
	run.expect(t, "TestBackgroundOnceFails/second/When_an_action/Then_a_check", "skip")
	if len(run.marks) != 0 {
		t.Errorf("marks = %q", run.marks)
	}
}

func TestFeatureBackgroundFailureStaysInItsScenario(t *testing.T) {
	run := runFixtures(t, nil, "-run", "^TestBackgroundFails$")

	run.expect(t, "TestBackgroundFails/bad", "fail")
	run.expect(t, "TestBackgroundFails/bad/When_an_action", "skip")
	run.expect(t, "TestBackgroundFails/good", "pass")
	run.expect(t, "TestBackgroundFails/good/When_an_action", "pass")
	if !slices.Equal(run.marks, []string{"bgeach/good"}) {
		t.Errorf("marks = %q", run.marks)
	}
}

func TestFeatureRunFilterStartsBackgroundOnceInSelectedScenario(t *testing.T) {
	run := runFixtures(t, nil, "-run", "^TestPassing$/^two$")

	run.expect(t, "TestPassing/two", "pass")
	run.expect(t, "TestPassing/two/Background_a_shared_file", "pass")
	if _, ran := run.tests["TestPassing/one"]; ran {
		t.Error("filtered scenario ran")
	}
}

func TestFeatureListModeRegistersWithoutRunning(t *testing.T) {
	run := runFixtures(t, []string{ListEnv + "=1"})

	for name, ft := range run.tests {
		if ft.status != "skip" {
			t.Errorf("%s: status %s, want skip", name, ft.status)
		}
		if strings.Contains(name, "/Background_") {
			t.Errorf("%s: background registered as a subtest in list mode", name)
		}
	}
	run.expect(t, "TestPassing/one/When_n_doubles/Then_n_is_even", "skip")
	run.expect(t, "TestStepFailures/when-fails/When_the_action_fails/Then_its_check", "skip")
	run.expect(t, "TestBackgroundOnceFails/second/When_an_action/Then_a_check", "skip")
	if got := run.tests["TestPassing"].attrs[attrs.BackgroundFeature]; !slices.Equal(got, []string{"a shared file"}) {
		t.Errorf("background feature attr = %q", got)
	}
	if len(run.marks) != 0 {
		t.Errorf("closures ran in list mode: %q", run.marks)
	}
}

func TestFeatureBackgroundServiceAndCmdDumpLogsWithoutPanic(t *testing.T) {
	run := runFixtures(t, []string{"SIGNET_TEST_BG_CLEANUP=1"}, "-run", "^TestBackgroundCleanupLogsOnStepFailure$")

	feat := run.expect(t, "TestBackgroundCleanupLogsOnStepFailure", "fail")
	scen := run.expect(t, "TestBackgroundCleanupLogsOnStepFailure/later-step-fails", "fail")
	run.expect(t, "TestBackgroundCleanupLogsOnStepFailure/later-step-fails/When_the_step_fails", "fail")

	featOut := strings.Join(feat.outputs, "")
	scenOut := strings.Join(scen.outputs, "")
	for _, out := range []string{featOut, scenOut} {
		if strings.Contains(out, "panic:") {
			t.Fatalf("unexpected panic in output:\n%s", out)
		}
	}
	if !strings.Contains(featOut, "bg-once-svc-out") || !strings.Contains(featOut, "bg-once-cmd-out") {
		t.Errorf("feature output missing BackgroundOnce dumps:\n%s", featOut)
	}
	if !strings.Contains(scenOut, "bg-each-cmd-out") {
		t.Errorf("scenario output missing Background dump:\n%s", scenOut)
	}
}
