// Package report rebuilds feature documents from the `go test -json` stream
// of tests written with the signet SDK.
package report

import (
	"bufio"
	"encoding/json"
	"io"
	"strings"
	"time"

	"github.com/kgatilin/signet/internal/attrs"
)

// Document is every feature found in one test stream, in run order.
type Document struct {
	Features []*Feature `yaml:"features"`
	// FailedPackages lists packages whose run failed, including failures
	// outside features (build errors, plain tests).
	FailedPackages []string `yaml:"-"`
}

// Log, here and on Background, Scenario and Step, is the plain output go test
// attributed to a failed test: t.Log lines and what the test process printed
// while it ran. It is empty for a test that did not fail.
type Feature struct {
	Package     string        `yaml:"package"`
	Test        string        `yaml:"test"`
	Feature     string        `yaml:"feature"`
	Description string        `yaml:"description,omitempty"`
	Status      string        `yaml:"status"`
	Duration    string        `yaml:"duration,omitempty"`
	Error       string        `yaml:"error,omitempty"`
	Log         string        `yaml:"log,omitempty"`
	Background  []*Background `yaml:"background,omitempty"`
	Scenarios   []*Scenario   `yaml:"scenarios"`
}

// Background is a BackgroundOnce (scope feature) or Background (scope
// scenario). Status is fail when any run of it failed, pass when it ran and
// never failed, skip when it never ran. Error and Log are those of its first
// failed run.
type Background struct {
	Text   string `yaml:"text"`
	Scope  string `yaml:"scope"`
	Status string `yaml:"status"`
	Error  string `yaml:"error,omitempty"`
	Log    string `yaml:"log,omitempty"`
}

type Scenario struct {
	ID       string  `yaml:"id"`
	Name     string  `yaml:"name"`
	Status   string  `yaml:"status"`
	Duration string  `yaml:"duration,omitempty"`
	Error    string  `yaml:"error,omitempty"`
	Log      string  `yaml:"log,omitempty"`
	Steps    []*Step `yaml:"steps"`
}

// Step is a Given, When or Then. Thens of a When are nested under it.
type Step struct {
	Keyword  string
	Text     string
	Status   string
	Duration string
	Error    string
	Log      string
	Then     []*Step
}

// node is one test from the stream.
type node struct {
	name     string
	pkg      string
	status   string
	elapsed  float64
	attrs    map[string][]string
	order    [][2]string // attributes in the order they were written
	errors   []string
	logs     []string
	children []*node
}

func (n *node) attr(key string) string {
	if values := n.attrs[key]; len(values) > 0 {
		return values[0]
	}
	return ""
}

type event struct {
	Action     string
	Package    string
	Test       string
	Key        string
	Value      string
	Output     string
	OutputType string
	Elapsed    float64
}

// Parse reads a `go test -json` stream. Lines that are not JSON events are
// ignored.
func Parse(r io.Reader) (*Document, error) {
	doc := &Document{}
	nodes := map[string]*node{} // package + "\x00" + test name
	var roots []*node

	scanner := bufio.NewScanner(r)
	scanner.Buffer(make([]byte, 64*1024), 16*1024*1024)
	for scanner.Scan() {
		var ev event
		if err := json.Unmarshal(scanner.Bytes(), &ev); err != nil || ev.Action == "" {
			continue
		}
		if ev.Test == "" {
			if ev.Action == "fail" {
				doc.FailedPackages = append(doc.FailedPackages, ev.Package)
			}
			continue
		}
		key := ev.Package + "\x00" + ev.Test
		n := nodes[key]
		if n == nil {
			n = &node{name: ev.Test, pkg: ev.Package, attrs: map[string][]string{}}
			nodes[key] = n
			if parent := findParent(nodes, ev.Package, ev.Test); parent != nil {
				parent.children = append(parent.children, n)
			} else {
				roots = append(roots, n)
			}
		}
		switch ev.Action {
		case "pass", "fail", "skip":
			n.status = ev.Action
			n.elapsed = ev.Elapsed
		case "attr":
			value := attrs.Unescape(ev.Value)
			n.attrs[ev.Key] = append(n.attrs[ev.Key], value)
			n.order = append(n.order, [2]string{ev.Key, value})
		case "output":
			// go test types an output line as frame ("=== RUN", "--- FAIL"),
			// error, error-continue, or leaves the type empty for t.Log
			// lines and for what the test process printed itself.
			line := strings.TrimRight(ev.Output, "\n")
			switch ev.OutputType {
			case "error":
				n.errors = append(n.errors, strings.TrimPrefix(line, firstIndent))
			case "error-continue":
				n.errors = append(n.errors, strings.TrimPrefix(line, nextIndent))
			case "":
				// Without trailing blanks the YAML encoder keeps the log
				// a block of lines.
				n.logs = append(n.logs, strings.TrimRight(unindent(line), " \t\r"))
			}
		}
	}
	if err := scanner.Err(); err != nil {
		return nil, err
	}

	for _, root := range roots {
		collectFeatures(doc, root)
	}
	return doc, nil
}

// go test indents the first line of a message by four spaces and its other
// lines by eight.
const (
	firstIndent = "    "
	nextIndent  = "        "
)

// unindent strips go test's indentation from a plain output line, whose type
// does not say whether it starts a t.Log message or continues one. A line the
// test process printed itself has no such indentation; if it starts with four
// or more spaces of its own, it loses them.
func unindent(line string) string {
	if rest, ok := strings.CutPrefix(line, nextIndent); ok {
		return rest
	}
	return strings.TrimPrefix(line, firstIndent)
}

// findParent returns the nearest known ancestor. Subtest names may contain
// slashes of their own (a step text such as "GET /api"), so every slash is
// tried from the right.
func findParent(nodes map[string]*node, pkg, name string) *node {
	for i := strings.LastIndex(name, "/"); i > 0; i = strings.LastIndex(name[:i], "/") {
		if n := nodes[pkg+"\x00"+name[:i]]; n != nil {
			return n
		}
	}
	return nil
}

func collectFeatures(doc *Document, n *node) {
	if _, ok := n.attrs[attrs.Feature]; ok {
		doc.Features = append(doc.Features, buildFeature(n))
		return
	}
	for _, child := range n.children {
		collectFeatures(doc, child)
	}
}

func buildFeature(n *node) *Feature {
	f := &Feature{
		Package:     n.pkg,
		Test:        n.name,
		Feature:     n.attr(attrs.Feature),
		Description: n.attr(attrs.Description),
		Status:      status(n),
		Duration:    duration(n),
		Error:       errorText(n.errors),
		Log:         logText(n),
		Scenarios:   []*Scenario{},
	}
	for _, kv := range n.order {
		switch kv[0] {
		case attrs.BackgroundFeature:
			f.Background = append(f.Background, &Background{Text: kv[1], Scope: attrs.ScopeFeature, Status: "skip"})
		case attrs.BackgroundScenario:
			f.Background = append(f.Background, &Background{Text: kv[1], Scope: attrs.ScopeScenario, Status: "skip"})
		}
	}
	for _, child := range n.children {
		if _, ok := child.attrs[attrs.ID]; ok {
			f.Scenarios = append(f.Scenarios, buildScenario(f, child))
		}
	}
	return f
}

func buildScenario(f *Feature, n *node) *Scenario {
	s := &Scenario{
		ID:       n.attr(attrs.ID),
		Name:     n.attr(attrs.Scenario),
		Status:   status(n),
		Duration: duration(n),
		Log:      logText(n),
		Steps:    []*Step{},
	}
	var errs []string
	if own := errorText(n.errors); own != "" {
		errs = append(errs, own)
	}
	for _, child := range n.children {
		switch child.attr(attrs.Keyword) {
		case attrs.KeywordBackground:
			bg := f.background(child.attr(attrs.Scope), child.attr(attrs.Text))
			if bg == nil {
				continue
			}
			switch child.status {
			case "fail":
				msg := errorText(child.errors)
				if bg.Status != "fail" {
					bg.Status, bg.Error, bg.Log = "fail", msg, logText(child)
				}
				errs = append(errs, "background "+quote(bg.Text)+" failed: "+msg)
			case "pass":
				if bg.Status == "skip" {
					bg.Status = "pass"
				}
			}
		case attrs.KeywordGiven, attrs.KeywordWhen, attrs.KeywordThen:
			s.Steps = append(s.Steps, buildStep(child))
		}
	}
	s.Error = strings.Join(errs, "\n")
	return s
}

func (f *Feature) background(scope, text string) *Background {
	for _, bg := range f.Background {
		if bg.Scope == scope && bg.Text == text {
			return bg
		}
	}
	return nil
}

func buildStep(n *node) *Step {
	st := &Step{
		Keyword:  n.attr(attrs.Keyword),
		Text:     n.attr(attrs.Text),
		Status:   status(n),
		Duration: duration(n),
		Error:    errorText(n.errors),
		Log:      logText(n),
	}
	for _, child := range n.children {
		if child.attr(attrs.Keyword) == attrs.KeywordThen {
			st.Then = append(st.Then, buildStep(child))
		}
	}
	return st
}

func status(n *node) string {
	if n.status == "" {
		return "unknown"
	}
	return n.status
}

func duration(n *node) string {
	if n.elapsed <= 0 {
		return ""
	}
	return time.Duration(n.elapsed * float64(time.Second)).Round(time.Millisecond).String()
}

// errorText joins failure lines recorded from error / error-continue events.
func errorText(lines []string) string {
	return strings.Join(lines, "\n")
}

// logText joins the plain output of a failed test; a test that did not fail
// keeps its output out of the document.
func logText(n *node) string {
	if n.status != "fail" {
		return ""
	}
	return strings.Trim(strings.Join(n.logs, "\n"), "\n")
}

func quote(s string) string { return `"` + s + `"` }

// StepCount counts the steps of a scenario, nested Thens included.
func (s *Scenario) StepCount() int {
	total := 0
	for _, st := range s.Steps {
		total += 1 + len(st.Then)
	}
	return total
}

// Failed reports whether any feature or package failed.
func (d *Document) Failed() bool {
	if len(d.FailedPackages) > 0 {
		return true
	}
	for _, f := range d.Features {
		if f.Status == "fail" {
			return true
		}
	}
	return false
}
