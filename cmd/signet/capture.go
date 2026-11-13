package main

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"regexp"
	"strconv"
	"strings"

	"gopkg.in/yaml.v3"
)

// Capture names one scalar value read from a step's stdout JSON with a JSON
// Pointer (RFC 6901). Later steps in the same case reference it as
// ${capture.<name>}.
type Capture struct {
	Name    string
	Pointer string
}

// Captures keeps the YAML mapping order of `capture: {name: pointer}` so the
// declarations are listed and extracted in the order they were written.
type Captures []Capture

func (c *Captures) UnmarshalYAML(node *yaml.Node) error {
	if node.Kind != yaml.MappingNode {
		return fmt.Errorf("capture must be a mapping of name to JSON Pointer")
	}
	captures := make(Captures, 0, len(node.Content)/2)
	for i := 0; i+1 < len(node.Content); i += 2 {
		var pointer string
		if err := node.Content[i+1].Decode(&pointer); err != nil {
			return err
		}
		captures = append(captures, Capture{Name: node.Content[i].Value, Pointer: pointer})
	}
	*c = captures
	return nil
}

var captureRefPattern = regexp.MustCompile(`\$\{capture\.([A-Za-z0-9._-]+)\}`)

// validateCaptureNode checks the raw `capture` node shape so errors carry a
// field path; Captures.UnmarshalYAML alone would report them without one.
func validateCaptureNode(node *yaml.Node, path string) []validationError {
	if node.Kind != yaml.MappingNode {
		return []validationError{{Path: path, Message: "must be a mapping of name to JSON Pointer"}}
	}
	var errs []validationError
	for i := 0; i+1 < len(node.Content); i += 2 {
		value := node.Content[i+1]
		if value.Kind != yaml.ScalarNode || value.Tag != "!!str" {
			errs = append(errs, validationError{Path: path + "." + node.Content[i].Value, Message: "must be a JSON Pointer string"})
		}
	}
	return errs
}

// validateCaseCaptures checks capture declarations and that every
// ${capture.<name>} reference is declared by an earlier step of the same case.
func validateCaseCaptures(c Case, caseIndex int) []validationError {
	var errs []validationError
	declared := map[string]bool{}
	for stepIndex, step := range c.Steps {
		path := fmt.Sprintf("cases[%d].steps[%d]", caseIndex, stepIndex)
		for _, name := range stepCaptureRefs(step) {
			if !declared[name] {
				errs = append(errs, validationError{
					Path:    path,
					Message: fmt.Sprintf("references ${capture.%s} but no earlier step in this case captures %s", name, name),
				})
			}
		}
		for _, capture := range step.Capture {
			capturePath := path + ".capture." + capture.Name
			if !setupNamePattern.MatchString(capture.Name) {
				errs = append(errs, validationError{Path: capturePath, Message: "name must use letters, numbers, dot, dash, or underscore"})
			}
			if _, err := parseJSONPointer(capture.Pointer); err != nil {
				errs = append(errs, validationError{Path: capturePath, Message: err.Error()})
			}
			declared[capture.Name] = true
		}
	}
	return errs
}

// stepCaptureRefs lists the capture names a step references in its run and
// expect fields, deduplicated in first-use order.
func stepCaptureRefs(step Step) []string {
	values := append([]string{step.Run.Shell, step.Run.Stdin}, step.Run.Args...)
	for _, stream := range []StreamExpect{step.Expect.Stdout, step.Expect.Stderr} {
		values = append(values, stream.Contains...)
		values = append(values, stream.NotContains...)
		values = append(values, stream.OrderedContains...)
		values = append(values, stream.Matches...)
	}
	seen := map[string]bool{}
	var names []string
	for _, value := range values {
		for _, match := range captureRefPattern.FindAllStringSubmatch(value, -1) {
			if !seen[match[1]] {
				seen[match[1]] = true
				names = append(names, match[1])
			}
		}
	}
	return names
}

// unresolvedCaptureRefs returns referenced capture names that have no value,
// which happens when the producing step did not pass.
func unresolvedCaptureRefs(step Step, captures map[string]string) []string {
	var missing []string
	for _, name := range stepCaptureRefs(step) {
		if _, ok := captures[name]; !ok {
			missing = append(missing, name)
		}
	}
	return missing
}

// forgetCaptures drops the names a failed step declares, so later references
// fail as unresolved instead of using a value from an earlier producer.
func forgetCaptures(captures map[string]string, declared Captures) {
	for _, capture := range declared {
		delete(captures, capture.Name)
	}
}

// expandExpectCaptures substitutes ${capture.<name>} in expectation values.
// Only captures expand here; inside `matches` the value is regexp-quoted so a
// captured datum is always matched literally.
func expandExpectCaptures(expect Expect, captures map[string]string) Expect {
	expect.Stdout = expandStreamCaptures(expect.Stdout, captures)
	expect.Stderr = expandStreamCaptures(expect.Stderr, captures)
	return expect
}

func expandStreamCaptures(stream StreamExpect, captures map[string]string) StreamExpect {
	literal := func(value string) string { return value }
	stream.Contains = replaceCaptureRefs(stream.Contains, captures, literal)
	stream.NotContains = replaceCaptureRefs(stream.NotContains, captures, literal)
	stream.OrderedContains = replaceCaptureRefs(stream.OrderedContains, captures, literal)
	stream.Matches = replaceCaptureRefs(stream.Matches, captures, regexp.QuoteMeta)
	return stream
}

func replaceCaptureRefs(values []string, captures map[string]string, quote func(string) string) []string {
	if values == nil {
		return nil
	}
	replaced := make([]string, len(values))
	for i, value := range values {
		replaced[i] = captureRefPattern.ReplaceAllStringFunc(value, func(token string) string {
			name := captureRefPattern.FindStringSubmatch(token)[1]
			if captured, ok := captures[name]; ok {
				return quote(captured)
			}
			return token
		})
	}
	return replaced
}

// extractCaptures reads every declared capture from stdout. It returns either
// all values or the failures; a step never yields a partial set.
func extractCaptures(captures Captures, stdout string) (map[string]string, []checkFailure) {
	if len(captures) == 0 {
		return nil, nil
	}
	check := func(capture Capture) string {
		return fmt.Sprintf("capture %s stdout %s", capture.Name, displayPointer(capture.Pointer))
	}

	document, err := decodeJSONDocument(stdout)
	if err != nil {
		var failures []checkFailure
		for _, capture := range captures {
			failures = append(failures, checkFailure{check: check(capture), message: "stdout is not valid JSON: " + err.Error()})
		}
		return nil, failures
	}

	values := map[string]string{}
	var failures []checkFailure
	for _, capture := range captures {
		resolved, err := resolveJSONPointer(document, capture.Pointer)
		var value string
		if err == nil {
			value, err = scalarString(resolved)
		}
		if err != nil {
			failures = append(failures, checkFailure{check: check(capture), message: err.Error()})
			continue
		}
		values[capture.Name] = value
	}
	if len(failures) > 0 {
		return nil, failures
	}
	return values, nil
}

// decodeJSONDocument parses exactly one JSON value, keeping numbers as their
// literal text so integers such as uint64 sequences keep full precision.
func decodeJSONDocument(text string) (any, error) {
	decoder := json.NewDecoder(strings.NewReader(text))
	decoder.UseNumber()
	var document any
	if err := decoder.Decode(&document); err != nil {
		if errors.Is(err, io.EOF) {
			return nil, fmt.Errorf("empty output")
		}
		return nil, err
	}
	if _, err := decoder.Token(); !errors.Is(err, io.EOF) {
		return nil, fmt.Errorf("unexpected data after the JSON value")
	}
	return document, nil
}

func parseJSONPointer(pointer string) ([]string, error) {
	if pointer == "" {
		return nil, nil
	}
	if !strings.HasPrefix(pointer, "/") {
		return nil, fmt.Errorf("JSON Pointer %q must be empty or start with /", pointer)
	}
	tokens := strings.Split(pointer[1:], "/")
	for i, token := range tokens {
		for j := 0; j < len(token); j++ {
			if token[j] == '~' && (j+1 == len(token) || (token[j+1] != '0' && token[j+1] != '1')) {
				return nil, fmt.Errorf("JSON Pointer %q has an invalid ~ escape (use ~0 or ~1)", pointer)
			}
		}
		tokens[i] = strings.ReplaceAll(strings.ReplaceAll(token, "~1", "/"), "~0", "~")
	}
	return tokens, nil
}

func resolveJSONPointer(document any, pointer string) (any, error) {
	tokens, err := parseJSONPointer(pointer)
	if err != nil {
		return nil, err
	}
	current := document
	for depth, token := range tokens {
		at := displayPointer(joinPointer(tokens[:depth+1]))
		switch node := current.(type) {
		case map[string]any:
			next, ok := node[token]
			if !ok {
				return nil, fmt.Errorf("%s not found", at)
			}
			current = next
		case []any:
			index, ok := arrayIndex(token)
			if !ok {
				return nil, fmt.Errorf("%s: %q is not an array index", at, token)
			}
			if index >= len(node) {
				return nil, fmt.Errorf("%s not found: array has %d items", at, len(node))
			}
			current = node[index]
		default:
			return nil, fmt.Errorf("%s not found: parent is %s", at, jsonKind(current))
		}
	}
	return current, nil
}

func arrayIndex(token string) (int, bool) {
	if token == "" || (len(token) > 1 && token[0] == '0') {
		return 0, false
	}
	for _, r := range token {
		if r < '0' || r > '9' {
			return 0, false
		}
	}
	index, err := strconv.Atoi(token)
	return index, err == nil
}

// scalarString renders a JSON scalar as the text substituted for
// ${capture.<name>}: strings unquoted, numbers exactly as written.
func scalarString(value any) (string, error) {
	switch v := value.(type) {
	case string:
		return v, nil
	case json.Number:
		return v.String(), nil
	case bool:
		return strconv.FormatBool(v), nil
	default:
		return "", fmt.Errorf("value is %s, want a string, number, or boolean", jsonKind(value))
	}
}

func jsonKind(value any) string {
	switch value.(type) {
	case nil:
		return "null"
	case map[string]any:
		return "an object"
	case []any:
		return "an array"
	case string:
		return "a string"
	case json.Number:
		return "a number"
	case bool:
		return "a boolean"
	default:
		return fmt.Sprintf("%T", value)
	}
}

func joinPointer(tokens []string) string {
	var buf bytes.Buffer
	for _, token := range tokens {
		buf.WriteByte('/')
		buf.WriteString(strings.ReplaceAll(strings.ReplaceAll(token, "~", "~0"), "/", "~1"))
	}
	return buf.String()
}

func displayPointer(pointer string) string {
	if pointer == "" {
		return `""`
	}
	return pointer
}
