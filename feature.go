package signet

import (
	"context"
	"fmt"
	"os"
	"strings"
	"sync"
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
			bg.ran = true
			owner = f.t
		}
		ok := t.Run("Background "+bg.text, func(bt *testing.T) {
			backgroundAttrs(bt, bg)
			bg.fn(scopedTB{T: bt, owner: owner})
		})
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

// scopedTB is the testing.TB given to backgrounds: failures and logs go to the
// background step, resources and cleanups to the owner (feature or scenario),
// so a service started in a background outlives the background step.
type scopedTB struct {
	*testing.T
	owner *testing.T
}

func (w scopedTB) Cleanup(fn func())        { w.owner.Cleanup(fn) }
func (w scopedTB) TempDir() string          { return w.owner.TempDir() }
func (w scopedTB) Setenv(key, value string) { w.owner.Setenv(key, value) }
func (w scopedTB) Chdir(dir string)         { w.owner.Chdir(dir) }
func (w scopedTB) Context() context.Context { return w.owner.Context() }
func (w scopedTB) ArtifactDir() string      { return w.owner.ArtifactDir() }
func (w scopedTB) Failed() bool             { return w.T.Failed() || w.owner.Failed() }

// Scenario registers the steps of one scenario.
type Scenario struct {
	t      *testing.T
	steps  []*step
	sealed bool
}

type step struct {
	keyword string
	text    string
	fn      func(*testing.T)
	thens   []*step
}

// Block is a When step; Then adds checks that run after it succeeds.
type Block struct {
	s    *Scenario
	when *step
}

// Given registers a precondition step.
func (s *Scenario) Given(text string, fn func(t *testing.T)) {
	s.steps = append(s.steps, s.newStep(attrs.KeywordGiven, text, fn))
}

// When registers an action. Waiting until the system settles belongs here, so
// its Then checks read a settled state.
func (s *Scenario) When(text string, fn func(t *testing.T)) *Block {
	st := s.newStep(attrs.KeywordWhen, text, fn)
	s.steps = append(s.steps, st)
	return &Block{s: s, when: st}
}

// Then registers a check directly on the scenario, for scenarios without When.
func (s *Scenario) Then(text string, fn func(t *testing.T)) {
	s.steps = append(s.steps, s.newStep(attrs.KeywordThen, text, fn))
}

// Then registers a check that runs as a subtest of the When after it
// succeeds. A failed check does not stop the other checks of the block.
func (b *Block) Then(text string, fn func(t *testing.T)) *Block {
	b.when.thens = append(b.when.thens, b.s.newStep(attrs.KeywordThen, text, fn))
	return b
}

func (s *Scenario) newStep(keyword, text string, fn func(*testing.T)) *step {
	if s.sealed {
		panic(fmt.Sprintf("signet: %s step %q registered after the scenario body returned", keyword, text))
	}
	if strings.TrimSpace(text) == "" || fn == nil {
		s.t.Fatalf("signet: %s step needs a text and a function", keyword)
	}
	return &step{keyword: keyword, text: text, fn: fn}
}

// runSteps runs the steps in order. After a failure, later Given and When
// steps are skipped; a Then runs only when its own block's action succeeded.
// blocked registers every step as skipped.
func runSteps(t *testing.T, steps []*step, blocked bool) {
	failed := blocked
	blockOK := !blocked
	for _, st := range steps {
		run := !failed
		if st.keyword == attrs.KeywordThen {
			run = blockOK
		}
		ok := t.Run(stepTitle(st), func(t *testing.T) {
			if st.keyword == attrs.KeywordWhen {
				runWhen(t, st, run)
				return
			}
			stepAttrs(t, st)
			if !run {
				t.SkipNow()
			}
			st.fn(t)
		})
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
func runWhen(t *testing.T, st *step, run bool) {
	stepAttrs(t, st)
	returned := false
	defer func() {
		thenRun := returned && !t.Failed() && !t.Skipped()
		for _, th := range st.thens {
			t.Run(stepTitle(th), func(t *testing.T) {
				stepAttrs(t, th)
				if !thenRun {
					t.SkipNow()
				}
				th.fn(t)
			})
		}
	}()
	if !run {
		t.SkipNow()
	}
	st.fn(t)
	returned = true
}

func stepAttrs(t *testing.T, st *step) {
	t.Attr(attrs.Keyword, st.keyword)
	t.Attr(attrs.Text, attrs.Escape(st.text))
}

func stepTitle(st *step) string {
	return strings.ToUpper(st.keyword[:1]) + st.keyword[1:] + " " + st.text
}
