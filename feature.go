package signet

import (
	"context"
	"flag"
	"fmt"
	"io"
	"os"
	"strings"
	"sync"
	"sync/atomic"
	"testing"

	"github.com/kgatilin/signet/internal/attrs"
)

// ListEnv names the environment variable that switches every Feature into
// list mode: scenarios and steps are registered and skipped, and backgrounds,
// step closures and Subject builds do not run.
const ListEnv = "SIGNET_LIST"

func listMode() bool {
	v := os.Getenv(ListEnv)
	return v != "" && v != "0" && v != "false"
}

// features guards the one-Feature-per-test-function rule.
var features sync.Map // *testing.T -> struct{}

// F registers the backgrounds and scenarios of one feature.
type F struct {
	t           *testing.T
	backgrounds []*background
	scenarios   []*scenarioDef
	ids         map[string]bool
	sealed      bool
	onceFailed  bool
}

type background struct {
	text string
	once bool
	fn   func(testing.TB)
	ran  bool
}

type scenarioDef struct {
	id, name string
	body     func(*Scenario)
}

// Feature declares the feature tested by this test function and runs its
// scenarios as subtests. body only registers backgrounds and scenarios; it
// runs once, before any of them. A test function holds one Feature.
func Feature(t *testing.T, name, description string, body func(f *F)) {
	t.Helper()
	if _, dup := features.LoadOrStore(t, struct{}{}); dup {
		t.Fatalf("signet: %s already declared a Feature; use one test function per feature", t.Name())
	}
	t.Cleanup(func() { features.Delete(t) })
	if strings.TrimSpace(name) == "" || body == nil {
		t.Fatal("signet: Feature needs a name and a body")
	}
	t.Attr(attrs.Feature, attrs.Escape(name))
	if description != "" {
		t.Attr(attrs.Description, attrs.Escape(description))
	}

	f := &F{t: t, ids: map[string]bool{}}
	body(f)
	f.sealed = true

	for _, bg := range f.backgrounds {
		key := attrs.BackgroundScenario
		if bg.once {
			key = attrs.BackgroundFeature
		}
		t.Attr(key, attrs.Escape(bg.text))
	}
	for _, sc := range f.scenarios {
		t.Run(sc.id, func(t *testing.T) { f.runScenario(t, sc) })
	}
	if listMode() {
		t.SkipNow()
	}
}

// BackgroundOnce registers a fixture shared by the feature's scenarios. It
// runs once, as a step of the first scenario that executes, so a -run filter
// that selects no scenario starts nothing. fn receives a testing.TB whose
// Cleanup, TempDir, Setenv, Chdir, Context and ArtifactDir belong to the
// feature, so what it starts lives until the feature ends. If it fails, the
// scenario that ran it fails and every later scenario is skipped.
func (f *F) BackgroundOnce(text string, fn func(t testing.TB)) {
	f.checkOpen("BackgroundOnce")
	if strings.TrimSpace(text) == "" || fn == nil {
		f.t.Fatal("signet: BackgroundOnce needs a text and a function")
	}
	f.backgrounds = append(f.backgrounds, &background{text: text, once: true, fn: fn})
}

// Background registers a fixture that runs inside every scenario, before its
// steps. fn receives a testing.TB whose Cleanup, TempDir, Setenv, Chdir,
// Context and ArtifactDir belong to the scenario, so what it starts lives
// until the scenario ends.
func (f *F) Background(text string, fn func(t testing.TB)) {
	f.checkOpen("Background")
	if strings.TrimSpace(text) == "" || fn == nil {
		f.t.Fatal("signet: Background needs a text and a function")
	}
	f.backgrounds = append(f.backgrounds, &background{text: text, fn: fn})
}

// Scenario registers a scenario. id names its subtest, so it must be unique
// in the feature and contain no whitespace or slash. body only registers
// steps; it runs inside the scenario's subtest before any step.
func (f *F) Scenario(id, name string, body func(s *Scenario)) {
	f.checkOpen("Scenario")
	switch {
	case id == "" || strings.ContainsAny(id, " \t\n\r/"):
		f.t.Fatalf("signet: scenario id %q must be non-empty without whitespace or slash", id)
	case f.ids[id]:
		f.t.Fatalf("signet: duplicate scenario id %q", id)
	case strings.TrimSpace(name) == "" || body == nil:
		f.t.Fatalf("signet: scenario %q needs a name and a body", id)
	}
	f.ids[id] = true
	f.scenarios = append(f.scenarios, &scenarioDef{id: id, name: name, body: body})
}

func (f *F) checkOpen(method string) {
	if f.sealed {
		panic(fmt.Sprintf("signet: %s called after the feature body returned", method))
	}
}

func (f *F) runScenario(t *testing.T, sc *scenarioDef) {
	t.Attr(attrs.ID, attrs.Escape(sc.id))
	t.Attr(attrs.Scenario, attrs.Escape(sc.name))

	s := &Scenario{t: t}
	sc.body(s)
	s.sealed = true

	skipped := listMode() || f.onceFailed
	blocked := skipped
	if !skipped {
		blocked = !f.runBackgrounds(t)
	}
	runSteps(t, s.steps, blocked)
	if skipped {
		t.SkipNow()
	}
}

// runBackgrounds runs the pending BackgroundOnce fixtures and every
// Background; it returns false when one of them failed. Each runs as a
// subtest of the scenario, with its resources owned by the feature t
// (BackgroundOnce) or the scenario t (Background).
func (f *F) runBackgrounds(t *testing.T) bool {
	for _, bg := range f.backgrounds {
		owner := t
		if bg.once {
			if bg.ran {
				continue
			}
			owner = f.t
		}
		title := "Background " + bg.text
		called, ok := subtest(t, title, func(bt *testing.T) {
			backgroundAttrs(bt, bg)
			runScoped(bt, owner, bg.fn)
		})
		if !called {
			notStarted(t, t, title)
			return false
		}
		bg.ran = true
		if !ok {
			if bg.once {
				f.onceFailed = true
			}
			return false
		}
	}
	return true
}

func backgroundAttrs(t *testing.T, bg *background) {
	scope := attrs.ScopeScenario
	if bg.once {
		scope = attrs.ScopeFeature
	}
	t.Attr(attrs.Keyword, attrs.KeywordBackground)
	t.Attr(attrs.Scope, scope)
	t.Attr(attrs.Text, attrs.Escape(bg.text))
}

// scopedTB is the testing.TB given to backgrounds, Given and When: what the
// step creates belongs to its owner (the feature for BackgroundOnce, the
// scenario for the others), so Cleanup, TempDir, Setenv, Chdir, Context and
// ArtifactDir outlive the step. Failures and logs go to the step while it
// runs and to the owner once it has returned, which is where a Cleanup
// registered through it runs.
type scopedTB struct {
	*testing.T
	owner *testing.T
	done  *atomic.Bool
}

// runScoped runs fn as the step t with resources owned by owner.
func runScoped(t, owner *testing.T, fn func(testing.TB)) {
	var done atomic.Bool
	defer done.Store(true)
	fn(scopedTB{T: t, owner: owner, done: &done})
}

func (w scopedTB) live() *testing.T {
	if w.done.Load() {
		return w.owner
	}
	return w.T
}

func (w scopedTB) Cleanup(fn func())               { w.owner.Cleanup(fn) }
func (w scopedTB) TempDir() string                 { return w.owner.TempDir() }
func (w scopedTB) Setenv(key, value string)        { w.owner.Setenv(key, value) }
func (w scopedTB) Chdir(dir string)                { w.owner.Chdir(dir) }
func (w scopedTB) Context() context.Context        { return w.owner.Context() }
func (w scopedTB) ArtifactDir() string             { return w.owner.ArtifactDir() }
func (w scopedTB) Failed() bool                    { return w.T.Failed() || w.owner.Failed() }
func (w scopedTB) Log(args ...any)                 { w.live().Helper(); w.live().Log(args...) }
func (w scopedTB) Logf(format string, args ...any) { w.live().Helper(); w.live().Logf(format, args...) }
func (w scopedTB) Output() io.Writer               { return w.live().Output() }
func (w scopedTB) Fail()                           { w.live().Fail() }
func (w scopedTB) FailNow()                        { w.live().FailNow() }
func (w scopedTB) Error(args ...any)               { w.live().Helper(); w.live().Error(args...) }
func (w scopedTB) Fatal(args ...any)               { w.live().Helper(); w.live().Fatal(args...) }

func (w scopedTB) Errorf(format string, args ...any) {
	w.live().Helper()
	w.live().Errorf(format, args...)
}

func (w scopedTB) Fatalf(format string, args ...any) {
	w.live().Helper()
	w.live().Fatalf(format, args...)
}

// Scenario registers the steps of one scenario.
type Scenario struct {
	t      *testing.T
	steps  []*step
	sealed bool
}

// step is a Given or When (scoped is set) or a Then (fn is set).
type step struct {
	keyword string
	text    string
	scoped  func(testing.TB)
	fn      func(*testing.T)
	thens   []*step
}

// run calls the step's function as the subtest t of the scenario owner.
func (st *step) run(t, owner *testing.T) {
	if st.scoped != nil {
		runScoped(t, owner, st.scoped)
		return
	}
	st.fn(t)
}

// Block is a When step; Then adds checks that run after it succeeds.
type Block struct {
	s    *Scenario
	when *step
}

// Given registers a precondition step. fn receives a testing.TB whose
// Cleanup, TempDir, Setenv, Chdir, Context and ArtifactDir belong to the
// scenario, so what it creates lives until the scenario ends.
func (s *Scenario) Given(text string, fn func(t testing.TB)) {
	s.steps = append(s.steps, s.newStep(&step{keyword: attrs.KeywordGiven, text: text, scoped: fn}))
}

// When registers an action. Waiting until the system settles belongs here, so
// its Then checks read a settled state. fn receives a testing.TB that owns
// resources the way Given's does: what it starts lives until the scenario
// ends.
func (s *Scenario) When(text string, fn func(t testing.TB)) *Block {
	st := s.newStep(&step{keyword: attrs.KeywordWhen, text: text, scoped: fn})
	s.steps = append(s.steps, st)
	return &Block{s: s, when: st}
}

// Then registers a check directly on the scenario, for scenarios without
// When. fn receives the check's own *testing.T: what it creates ends with the
// check.
func (s *Scenario) Then(text string, fn func(t *testing.T)) {
	s.steps = append(s.steps, s.newStep(&step{keyword: attrs.KeywordThen, text: text, fn: fn}))
}

// Then registers a check that runs as a subtest of the When after it
// succeeds. A failed check does not stop the other checks of the block.
func (b *Block) Then(text string, fn func(t *testing.T)) *Block {
	b.when.thens = append(b.when.thens, b.s.newStep(&step{keyword: attrs.KeywordThen, text: text, fn: fn}))
	return b
}

func (s *Scenario) newStep(st *step) *step {
	if s.sealed {
		panic(fmt.Sprintf("signet: %s step %q registered after the scenario body returned", st.keyword, st.text))
	}
	if strings.TrimSpace(st.text) == "" || st.fn == nil && st.scoped == nil {
		s.t.Fatalf("signet: %s step needs a text and a function", st.keyword)
	}
	return st
}

// subtest runs fn as a subtest of t. called is false when go test did not
// start it: t.Run returns true without calling fn for a name that -run or
// -skip leaves out, and for every name once -failfast has seen a failure.
func subtest(t *testing.T, name string, fn func(*testing.T)) (called, ok bool) {
	ok = t.Run(name, func(t *testing.T) {
		called = true
		fn(t)
	})
	return called, ok
}

// notStarted fails parent when go test did not start its step title although
// the scenario sc needed it to run. A scenario whose fixture or action is
// filtered out would otherwise run its remaining steps against a state that
// was never set up, and pass or fail for the wrong reason. Nothing is
// reported when the parent has already failed or no filter is set: then
// -failfast stopped the run.
func notStarted(parent, sc *testing.T, title string) {
	if parent.Failed() || !filtered() {
		return
	}
	parts := strings.Split(sc.Name(), "/")
	for i, part := range parts {
		parts[i] = "^" + part + "$"
	}
	parent.Errorf("signet: step %q did not run: -run or -skip leaves it out, and the rest of the scenario is skipped. "+
		"A scenario is the smallest unit they can select: -run '%s'", title, strings.Join(parts, "/"))
}

// filtered reports whether go test was given a -run or -skip pattern.
func filtered() bool {
	for _, name := range []string{"test.run", "test.skip"} {
		if f := flag.Lookup(name); f != nil && f.Value.String() != "" {
			return true
		}
	}
	return false
}

// runSteps runs the steps in order. After a failure, later Given and When
// steps are skipped; a Then runs only when its own block's action succeeded.
// blocked registers every step as skipped.
func runSteps(sc *testing.T, steps []*step, blocked bool) {
	failed := blocked
	blockOK := !blocked
	for _, st := range steps {
		run := !failed
		if st.keyword == attrs.KeywordThen {
			run = blockOK
		}
		called, ok := subtest(sc, stepTitle(st), func(t *testing.T) {
			if st.keyword == attrs.KeywordWhen {
				runWhen(t, sc, st, run)
				return
			}
			stepAttrs(t, st)
			if !run {
				t.SkipNow()
			}
			st.run(t, sc)
		})
		if run && !called {
			notStarted(sc, sc, stepTitle(st))
			failed, blockOK = true, false
			continue
		}
		if run && !ok {
			failed = true
		}
		if st.keyword != attrs.KeywordThen {
			blockOK = run && ok
		}
	}
}

// runWhen runs the action and then its Then checks. The checks are started
// from a deferred call so that they are registered, as skipped, even when the
// action stops the subtest with FailNow or SkipNow.
func runWhen(t, sc *testing.T, st *step, run bool) {
	stepAttrs(t, st)
	returned := false
	defer func() {
		thenRun := returned && !t.Failed() && !t.Skipped()
		for _, th := range st.thens {
			run := thenRun
			called, _ := subtest(t, stepTitle(th), func(t *testing.T) {
				stepAttrs(t, th)
				if !run {
					t.SkipNow()
				}
				th.fn(t)
			})
			if run && !called {
				notStarted(t, sc, stepTitle(th))
				thenRun = false
			}
		}
	}()
	if !run {
		t.SkipNow()
	}
	st.run(t, sc)
	returned = true
}

func stepAttrs(t *testing.T, st *step) {
	t.Attr(attrs.Keyword, st.keyword)
	t.Attr(attrs.Text, attrs.Escape(st.text))
}

func stepTitle(st *step) string {
	return strings.ToUpper(st.keyword[:1]) + st.keyword[1:] + " " + st.text
}
