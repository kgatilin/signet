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

type Feature struct {
	Package     string        `yaml:"package"`
	Test        string        `yaml:"test"`
	Feature     string        `yaml:"feature"`
	Description string        `yaml:"description,omitempty"`
	Status      string        `yaml:"status"`
	Duration    string        `yaml:"duration,omitempty"`
	Error       string        `yaml:"error,omitempty"`
	Background  []*Background `yaml:"background,omitempty"`
	Scenarios   []*Scenario   `yaml:"scenarios"`
}

// Background is a BackgroundOnce (scope feature) or Background (scope
// scenario). Status is fail when any run of it failed, pass when it ran and
// never failed, skip when it never ran.
type Background struct {
	Text   string `yaml:"text"`
	Scope  string `yaml:"scope"`
	Status string `yaml:"status"`
	Error  string `yaml:"error,omitempty"`
}

type Scenario struct {
	ID       string  `yaml:"id"`
	Name     string  `yaml:"name"`
	Status   string  `yaml:"status"`
	Duration string  `yaml:"duration,omitempty"`
	Error    string  `yaml:"error,omitempty"`
	Steps    []*Step `yaml:"steps"`
}

// Step is a Given, When or Then. Thens of a When are nested under it.
type Step struct {
	Keyword  string
	Text     string
	Status   string
	Duration string
	Error    string
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
			if ev.OutputType == "error" || ev.OutputType == "error-continue" {
				n.errors = append(n.errors, strings.TrimRight(ev.Output, "\n"))
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
				bg.Status = "fail"
				msg := errorText(child.errors)
				if bg.Error == "" {
					bg.Error = msg
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

// errorText joins failure lines and strips the indentation go test adds.
func errorText(lines []string) string {
	if len(lines) == 0 {
		return ""
	}
	first := lines[0]
	prefix := first[:len(first)-len(strings.TrimLeft(first, " \t"))]
	out := make([]string, len(lines))
	for i, line := range lines {
		out[i] = strings.TrimPrefix(line, prefix)
	}
	return strings.Join(out, "\n")
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
