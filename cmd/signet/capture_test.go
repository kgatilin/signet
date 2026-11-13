package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestExtractCapturesKeepsExactScalars(t *testing.T) {
	stdout := `{"contextId":"ctx-1","lastSeq":18446744073709551615,"big":123456789012345678901234567890,"ok":false,"a/b":{"m~n":[10,20]}}` + "\n"
	values, failures := extractCaptures(Captures{
		{Name: "contextId", Pointer: "/contextId"},
		{Name: "lastSeq", Pointer: "/lastSeq"},
		{Name: "big", Pointer: "/big"},
		{Name: "ok", Pointer: "/ok"},
		{Name: "escaped", Pointer: "/a~1b/m~0n/1"},
	}, stdout)
	if len(failures) > 0 {
		t.Fatalf("unexpected failures: %#v", failures)
	}
	want := map[string]string{
		"contextId": "ctx-1",
		"lastSeq":   "18446744073709551615",
		"big":       "123456789012345678901234567890",
		"ok":        "false",
		"escaped":   "20",
	}
	for name, value := range want {
		if values[name] != value {
			t.Errorf("capture %s = %q, want %q", name, values[name], value)
		}
	}
}

func TestExtractCapturesWholeDocumentPointer(t *testing.T) {
	values, failures := extractCaptures(Captures{{Name: "seq", Pointer: ""}}, "42\n")
	if len(failures) > 0 || values["seq"] != "42" {
		t.Fatalf("values=%#v failures=%#v", values, failures)
	}
}

func TestExtractCapturesFailuresAreExplicit(t *testing.T) {
	tests := []struct {
		name    string
		stdout  string
		pointer string
		message string
	}{
		{"malformed", "not-json", "/id", "stdout is not valid JSON"},
		{"empty", "", "/id", "stdout is not valid JSON: empty output"},
		{"trailing data", `{"id":1} {"id":2}`, "/id", "unexpected data after the JSON value"},
		{"missing key", `{"other":1}`, "/id", "/id not found"},
		{"missing nested", `{"a":{"b":1}}`, "/a/c", "/a/c not found"},
		{"index out of range", `{"items":[1]}`, "/items/3", "/items/3 not found: array has 1 items"},
		{"leading zero index", `{"items":[1,2]}`, "/items/01", `"01" is not an array index`},
		{"dash index", `{"items":[1,2]}`, "/items/-", `"-" is not an array index`},
		{"through scalar", `{"id":"x"}`, "/id/more", "parent is a string"},
		{"object", `{"id":{"x":1}}`, "/id", "value is an object"},
		{"array", `{"id":[1]}`, "/id", "value is an array"},
		{"null", `{"id":null}`, "/id", "value is null"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			values, failures := extractCaptures(Captures{{Name: "id", Pointer: tt.pointer}}, tt.stdout)
			if values != nil {
				t.Fatalf("expected no values, got %#v", values)
			}
			if len(failures) != 1 {
				t.Fatalf("expected one failure, got %#v", failures)
			}
			if !strings.Contains(failures[0].message, tt.message) {
				t.Fatalf("failure %q does not mention %q", failures[0].message, tt.message)
			}
			if !strings.HasPrefix(failures[0].check, "capture id stdout ") {
				t.Fatalf("unexpected check label %q", failures[0].check)
			}
		})
	}
}

func TestExtractCapturesIsAllOrNothing(t *testing.T) {
	values, failures := extractCaptures(Captures{
		{Name: "id", Pointer: "/id"},
		{Name: "missing", Pointer: "/missing"},
	}, `{"id":"x"}`)
	if values != nil {
		t.Fatalf("a step with a failed capture must not yield values, got %#v", values)
	}
	if len(failures) != 1 {
		t.Fatalf("expected one failure, got %#v", failures)
	}
}

func TestExpandExpectCapturesQuotesRegexp(t *testing.T) {
	captures := map[string]string{"v": "1.2+3"}
	expect := expandExpectCaptures(Expect{Stdout: StreamExpect{
		Contains: []string{"value ${capture.v}"},
		Matches:  []string{"^value ${capture.v}$"},
	}}, captures)
	if expect.Stdout.Contains[0] != "value 1.2+3" {
		t.Fatalf("contains not expanded literally: %q", expect.Stdout.Contains[0])
	}
	if expect.Stdout.Matches[0] != `^value 1\.2\+3$` {
		t.Fatalf("matches value not regexp-quoted: %q", expect.Stdout.Matches[0])
	}
}

func TestExpandStepResolvesCaptures(t *testing.T) {
	step, err := expandStep(Step{
		Run: Run{
			Args:  []string{"sessions/${capture.id}"},
			Stdin: "${capture.id}",
		},
	}, &setupContext{}, map[string]string{"id": "ctx-9"})
	if err != nil {
		t.Fatalf("expandStep: %v", err)
	}
	if step.Run.Args[0] != "sessions/ctx-9" || step.Run.Stdin != "ctx-9" {
		t.Fatalf("captures not expanded: %#v", step.Run)
	}

	_, err = expandStep(Step{Run: Run{Args: []string{"${capture.gone}"}}}, &setupContext{}, nil)
	if err == nil || !strings.Contains(err.Error(), "${capture.gone} is unresolved") {
		t.Fatalf("expected unresolved capture error, got %v", err)
	}

	// Outside a step (setup files, services) captures do not exist at all.
	if _, err := (&setupContext{}).expandString("${capture.id}"); err == nil || !strings.Contains(err.Error(), "unknown setup variable") {
		t.Fatalf("expected unknown setup variable, got %v", err)
	}
}

func TestLoadSpecValidatesCaptures(t *testing.T) {
	tests := []struct {
		name    string
		steps   string
		message string
	}{
		{
			name: "pointer must be a string",
			steps: `
      - name: produce
        run: {shell: "echo 1"}
        expect: {exitCode: 0}
        capture: {seq: 7}`,
			message: "cases[0].steps[0].capture.seq: must be a JSON Pointer string",
		},
		{
			name: "capture must be a mapping",
			steps: `
      - name: produce
        run: {shell: "echo 1"}
        expect: {exitCode: 0}
        capture: [/id]`,
			message: "cases[0].steps[0].capture: must be a mapping",
		},
		{
			name: "pointer syntax",
			steps: `
      - name: produce
        run: {shell: "echo 1"}
        expect: {exitCode: 0}
        capture: {id: "/a~2b"}`,
			message: "cases[0].steps[0].capture.id: JSON Pointer \"/a~2b\" has an invalid ~ escape",
		},
		{
			name: "reference before declaration",
			steps: `
      - name: consume
        run: {shell: "echo ${capture.id}"}
        expect: {exitCode: 0}
      - name: produce
        run: {shell: "echo 1"}
        expect: {exitCode: 0}
        capture: {id: /id}`,
			message: "cases[0].steps[0]: references ${capture.id} but no earlier step in this case captures id",
		},
		{
			name: "reference in expectation",
			steps: `
      - name: consume
        run: {shell: "echo 1"}
        expect:
          stdout: {contains: ["${capture.id}"]}`,
			message: "references ${capture.id}",
		},
		{
			name: "self reference",
			steps: `
      - name: produce
        run: {shell: "echo ${capture.id}"}
        expect: {exitCode: 0}
        capture: {id: /id}`,
			message: "references ${capture.id}",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "acceptance.yaml")
			spec := "version: 1\ncases:\n  - id: c\n    name: c\n    steps:" + tt.steps + "\n"
			if err := os.WriteFile(path, []byte(spec), 0600); err != nil {
				t.Fatal(err)
			}
			_, errs := loadSpec(path)
			var got []string
			for _, e := range errs {
				got = append(got, e.Path+": "+e.Message)
			}
			if !strings.Contains(strings.Join(got, "\n"), tt.message) {
				t.Fatalf("errors %q do not contain %q", got, tt.message)
			}
		})
	}
}

func TestCapturesKeepDeclarationOrder(t *testing.T) {
	path := filepath.Join(t.TempDir(), "acceptance.yaml")
	spec := `version: 1
cases:
  - id: c
    name: c
    steps:
      - name: produce
        run: {shell: "echo 1"}
        expect: {exitCode: 0}
        capture:
          zeta: /z
          alpha: /a
          mid: /m
`
	if err := os.WriteFile(path, []byte(spec), 0600); err != nil {
		t.Fatal(err)
	}
	loaded, errs := loadSpec(path)
	if len(errs) > 0 {
		t.Fatalf("loadSpec: %#v", errs)
	}
	var names []string
	for _, capture := range loaded.Cases[0].Steps[0].Capture {
		names = append(names, capture.Name)
	}
	if strings.Join(names, ",") != "zeta,alpha,mid" {
		t.Fatalf("declaration order lost: %v", names)
	}
}
