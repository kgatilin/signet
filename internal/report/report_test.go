package report

import (
	"bytes"
	"flag"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

var update = flag.Bool("update", false, "rewrite golden files in testdata/report")

// The inputs are go test -json streams of testdata/sdk, recorded with:
//
//	go test -json -count=1 ./testdata/sdk > testdata/report/run.jsonl
//	SIGNET_LIST=1 go test -json -count=1 ./testdata/sdk > testdata/report/list.jsonl
const dir = "../../testdata/report"

func parseFile(t *testing.T, name string) *Document {
	t.Helper()
	f, err := os.Open(filepath.Join(dir, name))
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	doc, err := Parse(f)
	if err != nil {
		t.Fatal(err)
	}
	return doc
}

func golden(t *testing.T, name string, got []byte) {
	t.Helper()
	path := filepath.Join(dir, name)
	if *update {
		if err := os.WriteFile(path, got, 0o644); err != nil {
			t.Fatal(err)
		}
	}
	want, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(got, want) {
		t.Errorf("%s differs; rerun with -update and review the diff\n--- got ---\n%s", name, got)
	}
}

func TestGoldenDocuments(t *testing.T) {
	for _, stream := range []string{"run", "list"} {
		t.Run(stream, func(t *testing.T) {
			doc := parseFile(t, stream+".jsonl")
			var yamlOut, casesOut bytes.Buffer
			if err := WriteYAML(&yamlOut, doc); err != nil {
				t.Fatal(err)
			}
			WriteCases(&casesOut, doc)
			golden(t, stream+".yaml", yamlOut.Bytes())
			golden(t, stream+".cases", casesOut.Bytes())
		})
	}
}

func TestRunDocument(t *testing.T) {
	doc := parseFile(t, "run.jsonl")
	if !doc.Failed() || len(doc.FailedPackages) != 1 {
		t.Fatalf("Failed=%v packages=%v", doc.Failed(), doc.FailedPackages)
	}
	if len(doc.Features) != 6 {
		t.Fatalf("features = %d", len(doc.Features))
	}

	passing := doc.Features[0]
	if passing.Feature != "Passing feature" || passing.Description != "First line.\nSecond line." || passing.Status != "pass" {
		t.Errorf("feature = %+v", passing)
	}
	if len(passing.Background) != 2 || passing.Background[0].Scope != "feature" || passing.Background[0].Status != "pass" {
		t.Errorf("background = %+v", passing.Background)
	}
	one := passing.Scenarios[0]
	if one.StepCount() != 6 || one.Steps[1].Keyword != "when" || len(one.Steps[1].Then) != 2 {
		t.Errorf("scenario one = %+v", one)
	}

	broken := doc.Features[2]
	if broken.Background[0].Status != "fail" || !strings.Contains(broken.Background[0].Error, "service dying exited before it was ready") {
		t.Errorf("background = %+v", broken.Background[0])
	}
	// The service is stopped and its output logged when its owner, the
	// feature, ends: that output has to be in the document.
	if broken.Log != "service.go:77: signet: service dying output:\n  listen: address already in use" {
		t.Errorf("feature log = %q", broken.Log)
	}
	if broken.Scenarios[0].Status != "fail" || !strings.Contains(broken.Scenarios[0].Error, `background "a service that does not start" failed`) {
		t.Errorf("first scenario = %+v", broken.Scenarios[0])
	}
	if broken.Scenarios[1].Status != "skip" {
		t.Errorf("second scenario = %+v", broken.Scenarios[1])
	}

	whenFails := doc.Features[1].Scenarios[0].Steps[0]
	if !strings.HasSuffix(whenFails.Error, ": action broke\nsecond line") {
		t.Errorf("error = %q", whenFails.Error)
	}
}

// The output types are those of a real stream: frame for "=== RUN" and
// "--- FAIL" lines, error and error-continue for failures, none for t.Log
// lines and for what the test process printed.
func TestFailedNodesCarryTheirLog(t *testing.T) {
	out := func(test, typ, text string) string {
		if typ != "" {
			typ = `,"OutputType":"` + typ + `"`
		}
		return `{"Action":"output","Package":"p","Test":"` + test + `","Output":"` + text + `\n"` + typ + `}`
	}
	keyword := func(test, kw, text string) string {
		return `{"Action":"attr","Package":"p","Test":"` + test + `","Key":"signet.keyword","Value":"` + kw + `"}` + "\n" +
			`{"Action":"attr","Package":"p","Test":"` + test + `","Key":"signet.text","Value":"` + text + `"}`
	}
	stream := strings.Join([]string{
		`{"Action":"attr","Package":"p","Test":"TestF","Key":"signet.feature","Value":"F"}`,
		`{"Action":"attr","Package":"p","Test":"TestF","Key":"signet.background.scenario","Value":"bg"}`,
		`{"Action":"attr","Package":"p","Test":"TestF/s","Key":"signet.id","Value":"s"}`,

		keyword("TestF/s/Background_bg", "background", "bg"),
		`{"Action":"attr","Package":"p","Test":"TestF/s/Background_bg","Key":"signet.scope","Value":"scenario"}`,
		out("TestF/s/Background_bg", "", "    x_test.go:1: background log"),
		out("TestF/s/Background_bg", "error", "    x_test.go:2: background broke"),
		`{"Action":"fail","Package":"p","Test":"TestF/s/Background_bg"}`,

		keyword("TestF/s/Given_g", "given", "g"),
		out("TestF/s/Given_g", "", "    x_test.go:3: log of a passing step"),
		`{"Action":"pass","Package":"p","Test":"TestF/s/Given_g"}`,

		keyword("TestF/s/When_w", "when", "w"),
		out("TestF/s/When_w", "frame", "=== RUN   TestF/s/When_w"),
		out("TestF/s/When_w", "", "printed by the test process  "),
		out("TestF/s/When_w", "", "    x_test.go:4: first line"),
		out("TestF/s/When_w", "", "          indented second line"),
		out("TestF/s/When_w", "error", "    x_test.go:5: step broke"),
		out("TestF/s/When_w", "error-continue", "        more"),
		out("TestF/s/When_w", "frame", "--- FAIL: TestF/s/When_w (0.00s)"),
		`{"Action":"fail","Package":"p","Test":"TestF/s/When_w"}`,

		out("TestF/s", "", "    command.go:1: signet: last 1 command(s):"),
		out("TestF/s", "", "        $ app run  (exit 1, 5ms)"),
		`{"Action":"fail","Package":"p","Test":"TestF/s"}`,
		out("TestF", "", "    service.go:1: signet: service api output:"),
		out("TestF", "", "          bind: address already in use"),
		`{"Action":"fail","Package":"p","Test":"TestF"}`,
	}, "\n")
	doc, err := Parse(strings.NewReader(stream))
	if err != nil {
		t.Fatal(err)
	}
	f := doc.Features[0]
	s := f.Scenarios[0]
	for _, c := range []struct{ where, got, want string }{
		{"feature", f.Log, "service.go:1: signet: service api output:\n  bind: address already in use"},
		{"scenario", s.Log, "command.go:1: signet: last 1 command(s):\n$ app run  (exit 1, 5ms)"},
		{"background", f.Background[0].Log, "x_test.go:1: background log"},
		{"passing step", s.Steps[0].Log, ""},
		{"failed step", s.Steps[1].Log, "printed by the test process\nx_test.go:4: first line\n  indented second line"},
		{"failed step error", s.Steps[1].Error, "x_test.go:5: step broke\nmore"},
	} {
		if c.got != c.want {
			t.Errorf("%s = %q, want %q", c.where, c.got, c.want)
		}
	}

	var yamlOut bytes.Buffer
	if err := WriteYAML(&yamlOut, doc); err != nil {
		t.Fatal(err)
	}
	want := `          - when: w
            status: fail
            error: |-
              x_test.go:5: step broke
              more
            log: |-
              printed by the test process
              x_test.go:4: first line
                indented second line
`
	if !strings.Contains(yamlOut.String(), want) {
		t.Errorf("step is not rendered as\n%s\ngot\n%s", want, yamlOut.String())
	}
}

func TestListDocument(t *testing.T) {
	doc := parseFile(t, "list.jsonl")
	if doc.Failed() {
		t.Fatal("list run reported a failure")
	}
	for _, f := range doc.Features {
		for _, bg := range f.Background {
			if bg.Status != "skip" {
				t.Errorf("%s background %q status %s", f.Test, bg.Text, bg.Status)
			}
		}
		for _, s := range f.Scenarios {
			if s.Status != "skip" {
				t.Errorf("%s/%s status %s", f.Test, s.ID, s.Status)
			}
		}
	}
	run := parseFile(t, "run.jsonl")
	for i, f := range doc.Features {
		if got, want := f.Scenarios, run.Features[i].Scenarios; len(got) != len(want) {
			t.Fatalf("%s: %d scenarios in list mode, %d in a run", f.Test, len(got), len(want))
		}
		for j, s := range f.Scenarios {
			if s.StepCount() != run.Features[i].Scenarios[j].StepCount() {
				t.Errorf("%s/%s: list and run step counts differ", f.Test, s.ID)
			}
		}
	}
}

func TestParentWithSlashInStepText(t *testing.T) {
	stream := strings.Join([]string{
		`{"Action":"run","Package":"p","Test":"TestF"}`,
		`{"Action":"attr","Package":"p","Test":"TestF","Key":"signet.feature","Value":"F"}`,
		`{"Action":"run","Package":"p","Test":"TestF/s"}`,
		`{"Action":"attr","Package":"p","Test":"TestF/s","Key":"signet.id","Value":"s"}`,
		`{"Action":"run","Package":"p","Test":"TestF/s/When_GET_/api/x"}`,
		`{"Action":"attr","Package":"p","Test":"TestF/s/When_GET_/api/x","Key":"signet.keyword","Value":"when"}`,
		`{"Action":"attr","Package":"p","Test":"TestF/s/When_GET_/api/x","Key":"signet.text","Value":"GET /api/x"}`,
		`{"Action":"run","Package":"p","Test":"TestF/s/When_GET_/api/x/Then_ok"}`,
		`{"Action":"attr","Package":"p","Test":"TestF/s/When_GET_/api/x/Then_ok","Key":"signet.keyword","Value":"then"}`,
		`{"Action":"attr","Package":"p","Test":"TestF/s/When_GET_/api/x/Then_ok","Key":"signet.text","Value":"ok"}`,
		`not json`,
	}, "\n")
	doc, err := Parse(strings.NewReader(stream))
	if err != nil {
		t.Fatal(err)
	}
	steps := doc.Features[0].Scenarios[0].Steps
	if len(steps) != 1 || steps[0].Text != "GET /api/x" || len(steps[0].Then) != 1 {
		t.Fatalf("steps = %+v", steps)
	}
}
