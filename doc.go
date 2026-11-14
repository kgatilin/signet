// Package signet writes acceptance tests as ordinary Go tests with a
// description a reader can follow without the code.
//
// A test function declares one Feature. The feature body registers
// backgrounds and scenarios; a scenario body registers Given, When and Then
// steps. Each step is a subtest whose keyword and text are recorded with
// t.Attr, so `go test -json | signet report` turns a run into a YAML
// document. With SIGNET_LIST=1 the same tests register every step and skip
// it, which produces the document without running anything.
//
// Fixtures run the system under test: Subject builds the binary once per test
// process, Cmd and Subj.Run execute commands and return a Result, Service
// starts a background process that is stopped when the test ends, and
// FreePort picks a TCP port.
//
// Backgrounds, Given and When receive a testing.TB: what they create (a
// temporary directory, a service, a Cleanup) lives until the scenario ends,
// or the feature for BackgroundOnce. Then receives its own *testing.T, and
// what a check creates ends with the check.
//
// Assertions are not part of the package: testify or plain if statements
// work with either parameter.
package signet
