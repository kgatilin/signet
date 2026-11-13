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
	if len(doc.Features) != 4 {
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
	if broken.Background[0].Status != "fail" || !strings.Contains(broken.Background[0].Error, "service refused to start") {
		t.Errorf("background = %+v", broken.Background[0])
	}
	if broken.Scenarios[0].Status != "fail" || !strings.Contains(broken.Scenarios[0].Error, `background "a service that does not start" failed`) {
		t.Errorf("first scenario = %+v", broken.Scenarios[0])
	}
	if broken.Scenarios[1].Status != "skip" {
		t.Errorf("second scenario = %+v", broken.Scenarios[1])
	}

	whenFails := doc.Features[1].Scenarios[0].Steps[0]
	if !strings.HasSuffix(whenFails.Error, ": action broke\n    second line") {
		t.Errorf("error = %q", whenFails.Error)
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
