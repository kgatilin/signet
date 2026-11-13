// Package attrs names the test attributes the SDK writes with t.Attr and the
// report command reads back from `go test -json`.
package attrs

import "strings"

// Attribute keys. Feature-level keys are written on the test function's t,
// scenario keys on the scenario subtest, step keys on each step subtest.
const (
	Feature            = "signet.feature"
	Description        = "signet.description"
	BackgroundFeature  = "signet.background.feature"  // a BackgroundOnce text
	BackgroundScenario = "signet.background.scenario" // a Background text
	ID                 = "signet.id"
	Scenario           = "signet.scenario"
	Keyword            = "signet.keyword" // background, given, when, then
	Text               = "signet.text"
	Scope              = "signet.scope" // feature or scenario, on background subtests
)

// Step keywords and background scopes.
const (
	KeywordBackground = "background"
	KeywordGiven      = "given"
	KeywordWhen       = "when"
	KeywordThen       = "then"

	ScopeFeature  = "feature"
	ScopeScenario = "scenario"
)

var (
	escaper   = strings.NewReplacer(`\`, `\\`, "\n", `\n`, "\r", `\r`)
	unescaper = strings.NewReplacer(`\\`, `\`, `\n`, "\n", `\r`, "\r")
)

// Escape makes a value safe for t.Attr, which rejects line breaks.
func Escape(v string) string { return escaper.Replace(v) }

// Unescape reverses Escape.
func Unescape(v string) string { return unescaper.Replace(v) }
