# Proposal: Go SDK for acceptance tests

Status: implemented. The SDK is the root package, the report is
`signet report`, and `examples/` is a module that uses it. The reference for
the current API is the README's Go SDK section; this document keeps the
reasoning. Changes made while implementing are listed under
[Implementation notes](#implementation-notes).

## Why

A YAML spec works while a step is one command and a few `contains` checks.
Suites that test a running service outgrow it:

- A step becomes a shell script: a polling loop with `sleep`, several
  `jq` selections over JSON output, counters printed as `NAME=1` so that
  `stdout.contains` can check them.
- The step name describes the script ("wait for one firing and inspect its
  records"), and the eight facts the script checks have no names at all.
- Setup repeats per file: the build command, a free-port search patched into
  config files with `sed`, readiness probes written with `curl`.
- The same arguments are copied into every step, because YAML has no
  variables or loops.

A condensed step of this kind:

```yaml
- name: wait for one timer firing and inspect only its causal records
  run:
    timeout: 90s
    shell: |
      for attempt in $(seq 1 100); do
        ./bin/app api records --pattern "channel.scheduler.>" >"$channel"
        thread="$(jq -r '[.records[] | select(.type == "timer.fired" and .data.job == "reminder")] | first | .data.thread // empty' "$channel")"
        if [ -n "$thread" ]; then
          ./bin/app api records --pattern "ingress.scheduler.>" >"$ingress"
          session="$(jq -r --arg t "$thread" '[.records[] | select(.type == "session.bound" and .data.thread == $t)] | last | .data.session // empty' "$ingress")"
          ...
        fi
        sleep 0.5
      done
      fired="$(jq -r --arg t "$thread" '[.records[] | select(.type == "timer.fired" and .data.thread == $t)] | length' "$channel")"
      [ "$fired" -eq 1 ] || status=1
      printf 'FIRED_COUNT=%s\n' "$fired"
      ...
      exit "$status"
  expect:
    exitCode: 0
    stdout:
      contains: ["FIRED_COUNT=1", "ROUTED_COUNT=1", "REPLY_COUNT=1"]
```

Go has typed JSON, loops, helpers and an assertion library. The reason these
tests were not written in Go is the description: a YAML spec is readable by
someone who does not read the code, and a UI can list its cases. The SDK keeps
that property for tests written in Go.

## Model

A test is ordinary Go code under `go test`. signet adds three things:

1. **A description structure**: Feature → Background / BackgroundOnce →
   Scenario → `Given` / `When` / `Then`. Every level carries a sentence written
   for a reader.
2. **Fixtures**: a `Subject` value (lazy module-root build, command runner,
   background services) and `FreePort`.
3. **A report command** that turns the `go test -json` stream into a document.

The feature and scenario bodies only register steps; all work happens inside
the step closures. The same code therefore serves two modes: a normal run, and
a listing (`SIGNET_LIST=1`) that produces the document without executing
anything or building the binary.

Assertions are not part of signet. Step closures receive `*testing.T`, so
testify or plain `if` works.

## What already exists

| Need | Provided by |
|---|---|
| Named cases and steps with status and duration | `t.Run`; nested subtests appear in `go test -json` |
| Machine-readable description fields | `t.Attr(key, value)`, emitted as `attr` events in `-json` |
| Lazy build once per process | `sync.OnceValues` |
| Temp files, teardown | `t.TempDir`, `t.Cleanup`, `t.Setenv`, `t.Context` |
| Files kept from a run | `t.ArtifactDir()` with `-artifacts` |
| Free port | `net.Listen("127.0.0.1:0")` |
| Stub HTTP dependencies | `httptest.NewServer` |
| Commands with timeouts | `exec.CommandContext`, `cmd.WaitDelay` |
| JSON selection | `encoding/json` into types, `slices` |
| Polling | `require.EventuallyWithT` |
| Comparisons | `require.Len`, `Contains`, `ElementsMatch`, `JSONEq`, `Regexp` |
| Selecting one case | `go test -run 'TestX/case-id'` |

What signet has to provide: background service lifecycle (start, readiness,
process-group stop, log dump on failure), a `Command` / `Result` builder over
`exec`, module-root subject resolution across packages, the BDD structure, and
the report command.

## API

```go
package signet

// Subject binds a build command and binary path, both resolved from the
// nearest module root (walking up to go.mod). Built lazily once per test
// process on first use, or resolved directly from SIGNET_BINARY.
func Subject(buildShell, binaryPath string) *Subj
func (b *Subj) Path(t testing.TB) string
func (b *Subj) Run(t testing.TB, args ...string) *Result // shortcut for b.Cmd(t, args...).Run()
func (b *Subj) Cmd(t testing.TB, args ...string) *Command
func (b *Subj) Service(t testing.TB, name string, args ...string) *ServiceBuilder

// Arbitrary commands & services (not bound to a Subject)
func Cmd(t testing.TB, name string, args ...string) *Command
func Service(t testing.TB, name string, cmd string, args ...string) *ServiceBuilder
func FreePort(t testing.TB) int

// Command builder & Result checks
func (c *Command) Env(kv ...string) *Command
func (c *Command) Dir(dir string) *Command
func (c *Command) Stdin(in string) *Command
func (c *Command) Timeout(d time.Duration) *Command
func (c *Command) Run() *Result

func (r *Result) OK() *Result
func (r *Result) Exit(code int) *Result
func (r *Result) StdoutContains(s string) *Result
func (r *Result) StderrContains(s string) *Result
func (r *Result) In(t testing.TB) *Result // checks report to another step's t
func JSON[T any](r *Result) T

// Service builder (process starts in Start and stops in t.Cleanup)
func (s *ServiceBuilder) Env(kv ...string) *ServiceBuilder
func (s *ServiceBuilder) Dir(dir string) *ServiceBuilder
func (s *ServiceBuilder) ReadyHTTP(url string, status int) *ServiceBuilder
func (s *ServiceBuilder) ReadyLog(substring string) *ServiceBuilder
func (s *ServiceBuilder) ReadyTimeout(d time.Duration) *ServiceBuilder
func (s *ServiceBuilder) Start() *Svc
func (s *Svc) Name() string
func (s *Svc) Log() string
func (s *Svc) Stop()

// Structure
// One Feature per test function.
func Feature(t *testing.T, name, description string, body func(f *F))
func (f *F) BackgroundOnce(text string, fn func(t testing.TB))
func (f *F) Background(text string, fn func(t testing.TB))
func (f *F) Scenario(id, name string, body func(s *Scenario))

func (s *Scenario) Given(text string, fn func(t *testing.T))
func (s *Scenario) When(text string, fn func(t *testing.T)) *Block
func (s *Scenario) Then(text string, fn func(t *testing.T)) // for scenarios with no When
func (b *Block) Then(text string, fn func(t *testing.T)) *Block
```

How it maps onto `testing`:

- `Feature` emits feature metadata via `t.Attr`, runs `body(f)` to collect
  `BackgroundOnce`, `Background`, and `Scenario` registrations, then executes
  scenarios.
- Each scenario runs inside `t.Run(id)` with its human-readable name as an
  attribute.
- Two explicit background scopes cover shared vs isolated fixtures:
  - `BackgroundOnce` runs lazily once, inside the `t.Run(id)` of the first
    scenario that actually executes, so a `-run` filter that selects no
    scenario starts nothing and `go test -run TestX/case-id` still gets its
    fixture. Its `fn` receives a `testing.TB` that wraps the feature's `t`:
    `Cleanup` registers on the feature, so services live until the feature
    ends, and `FailNow` is intercepted. The signature differs from steps
    (`*testing.T`) for that reason: a raw feature `t` failing inside a
    scenario's goroutine makes `testing` report "subtest may have called
    FailNow on a parent test" instead of the cause.
  - `Background` runs inside each scenario's `t.Run(id)` before its steps,
    as a subtest of the scenario. Its `fn` receives a `testing.TB` whose
    resources belong to the scenario, the same way `BackgroundOnce` resources
    belong to the feature.
- A `Given` or `When` is a subtest of the scenario; each `Then` on a `Block`
  is a subtest of its `When`. The keyword and text are attributes.

Execution rules:

- Action and waiting belong in `Given` / `When` (reaching a settled state and
  populating scenario variables). `Then` closures assert independent facts over
  that settled state. (Trade-off: if `When` times out waiting for the settled
  state, its `Then` checks are skipped rather than individually evaluated.)
- If `BackgroundOnce` fails, the scenario that triggered it fails with the
  background's message, and every later scenario is registered and skipped,
  so the report keeps the full scenario list.
- A test function holds one `Feature`: feature metadata is written as
  attributes on that function's `t`, and a second call would mix them.
- A failed `Background`, `Given`, or `When` skips its `Then` checks and every
  later block in the scenario.
- A failed `Then` does not stop the other `Then` checks of the same block,
  so the report shows all facts that hold or fail. Later `When` blocks in the
  scenario are skipped.
- On `t.Failed()`, `Command` stdout/stderr and `Svc` logs are dumped
  automatically via `t.Cleanup`.

## Example

The shell step above, as a scenario. `apptest` and `events` are the
project's own helper packages (a client for the record log and typed
selections over it); they are not part of signet.

```go
var app = signet.Subject("make build", "./bin/app")

func TestScheduling(t *testing.T) {
    signet.Feature(t, "Scheduling",
        "A timer firing is a channel fact that ingress routes into a scheduled conversation.",
        func(f *signet.F) {
            var log *apptest.Log

            f.BackgroundOnce("a running service with a timer job named reminder", func(t testing.TB) {
                log = apptest.Start(t, app, serviceConfig).Log()
            })

            f.Scenario("scheduling-firing-roundtrip",
                "one timer firing becomes one routed scheduled conversation", func(s *signet.Scenario) {
                var (
                    fired    events.TimerFired
                    received events.Record[events.MessageReceived]
                    done     events.MessageProcessed
                    session  string
                )

                s.When("the timer fires for job reminder and the conversation settles", func(t *testing.T) {
                    require.EventuallyWithT(t, func(c *assert.CollectT) {
                        fired = events.First[events.TimerFired](c, log.Records(c, "channel.scheduler.>"),
                            func(e events.TimerFired) bool { return e.Job == "reminder" })
                        received = events.OneRecord[events.MessageReceived](c, log.Records(c, "channel.scheduler.>"), onThread(fired.Thread))
                        session = events.Last[events.SessionBound](c, log.Records(c, "ingress.scheduler.>"), onThread(fired.Thread)).Session
                        done = events.One[events.MessageProcessed](c, log.Session(c, session), forMessage(received.ID))
                    }, 50*time.Second, 500*time.Millisecond)
                }).
                    Then("the conversation finishes without error or cancellation", func(t *testing.T) {
                        assert.Empty(t, done.Error)
                        assert.False(t, done.Cancelled)
                    }).
                    Then("the firing is recorded once on the scheduler channel", func(t *testing.T) {
                        events.One[events.TimerFired](t, log.Records(t, "channel.scheduler.>"), onThread(fired.Thread))
                    }).
                    Then("ingress routes the message once", func(t *testing.T) {
                        events.One[events.MessageRouted](t, log.Records(t, "ingress.scheduler.>"), onThread(fired.Thread))
                    }).
                    Then("the agent replies SCHEDULED_OK once on the firing thread", func(t *testing.T) {
                        events.One[events.MessageSent](t, log.Records(t, "channel.scheduler.>"), func(e events.MessageSent) bool {
                            return e.Thread == fired.Thread && e.ReplyTo == received.ID && e.Content == "SCHEDULED_OK"
                        })
                    }).
                    Then("the runner channel receives nothing for the thread", func(t *testing.T) {
                        events.None[events.MessageReceived](t, log.Records(t, "channel.runner.>"), onThread(fired.Thread))
                    }).
                    Then("the session is listed with trigger scheduler", func(t *testing.T) {
                        require.Contains(t, log.Sessions(t),
                            apptest.Session{ID: session, Trigger: "scheduler", Thread: fired.Thread})
                    })
            })

            f.Scenario("scheduling-cancel-pending",
                "operator cancellation closes a conversation waiting on an offline worker", func(s *signet.Scenario) {
                var thread string

                s.When("the timer fires for job slow-reminder", func(t *testing.T) { /* sets thread */ }).
                    Then("the turn is pending on the offline worker", func(t *testing.T) { /* ... */ })

                s.When("the operator requests cancellation of the thread", func(t *testing.T) {
                    app.Run(t, "send", "cancel", "--thread", thread).OK()
                }).
                    Then("the message is closed as cancelled", func(t *testing.T) { /* ... */ }).
                    Then("progress reports cancelled", func(t *testing.T) { /* ... */ }).
                    Then("ingress routes the cancel once", func(t *testing.T) { /* ... */ })
            })
        })
}
```

## Exported document

```sh
go test ./acceptance/... -json -count=1 | signet report > report.yaml        # after a run
SIGNET_LIST=1 go test ./acceptance/... -json -count=1 | signet report        # without running
```

`SIGNET_LIST=1` is an environment variable rather than a custom `go test` flag
so that running `SIGNET_LIST=1 go test ./...` across mixed unit and acceptance
packages never fails with `flag provided but not defined` in packages that do
not import `signet`.

With `SIGNET_LIST=1`, `BackgroundOnce`, `Background`, `Subject` builds, and
step closures do not execute. Scenarios and `When` blocks still enter their
`t.Run` subtests and emit `t.Attr` metadata so child subtests are registered;
leaf steps emit their attributes and call `t.SkipNow()`, followed by
`t.SkipNow()` at the end of each parent block/scenario after its children have
registered. Both commands therefore produce the exact same document tree, with
`status: skip` in list mode:

```yaml
features:
  - package: example.com/app/acceptance
    test: TestScheduling
    feature: Scheduling
    description: A timer firing is a channel fact that ingress routes into a scheduled conversation.
    status: pass
    background:
      - text: a running service with a timer job named reminder
        scope: feature
        status: pass
    scenarios:
      - id: scheduling-firing-roundtrip
        name: one timer firing becomes one routed scheduled conversation
        status: pass
        steps:
          - when: the timer fires for job reminder and the conversation settles
            status: pass
            then:
              - text: the conversation finishes without error or cancellation
                status: pass
              - text: the firing is recorded once on the scheduler channel
                status: pass
              - text: ingress routes the message once
                status: pass
              - text: the agent replies SCHEDULED_OK once on the firing thread
                status: pass
              - text: the runner channel receives nothing for the thread
                status: pass
              - text: the session is listed with trigger scheduler
                status: pass
```

The document contains only the text written in `Feature`, `BackgroundOnce` /
`Background`, `Scenario`, `Given`, `When` and `Then`, plus status, duration and
any failure message. Commands, JSON and assertions stay in the code.
`background` is a list: `scope: feature` for `BackgroundOnce`, `scope:
scenario` for `Background`.

`signet report --format cases` renders the `CASE id=<id> name="<name>"` table
from the same `-json` stream so scripts get a consistent case inventory format
across YAML specs and Go suites.

## Build and working directory

`go test` runs each test binary with its working directory set to that test
package's directory (e.g. `acceptance/scheduling`), not the repository root.
`signet.Subject(buildShell, binaryPath)` walks up from the current directory to
the nearest `go.mod` to find the module root:

- `buildShell` executes with `cmd.Dir` set to the module root.
- Relative `binaryPath` values (such as `./bin/app`) resolve against the module
  root into an absolute path.
- Individual `app.Cmd` / `app.Service` calls continue to run in the test's
  working directory unless overridden with `.Dir(...)`.

`Subject` builds lazily via `sync.OnceValues` on the first call to `Path`,
`Run`, `Cmd`, or `Service`:

- During `go test ./acceptance`, the binary is built once on first use and
  reused across scenarios in that package.
- In `SIGNET_LIST=1` mode, step closures never run, so nothing is built.
- When `SIGNET_BINARY` is set in the environment, `Subject` skips `buildShell`
  and uses that path. It must be absolute: the working directory differs per
  test package, so a relative value would point somewhere else in each of
  them, and `Subject` fails the test on one. Because `go test` starts one
  process per package, a multi-package suite builds once in `make`:

```make
acceptance: build
	SIGNET_BINARY=$(CURDIR)/bin/app go test ./acceptance/... -count=1
```

`-count=1` is required: `go test` caches results and does not track external
binaries executed by tests.

## Package layout

The SDK lives at the module root (`package signet`, imported as
`github.com/kgatilin/signet`, stdlib-only), and the CLI moves to `cmd/signet`
(`go install github.com/kgatilin/signet/cmd/signet@latest`). The import path is
written in every test file, whereas installing the CLI happens once. Process
handling and service readiness shared between the CLI runner and the SDK live
in `internal/proc`.

## What this design does not do

- The module root is the nearest `go.mod`. When acceptance tests live in
  their own module (to keep test dependencies out of the main `go.mod`),
  that is the test module, not the repository root; such a suite sets
  `SIGNET_BINARY` or passes absolute paths to `Subject`.
- The text and number of steps cannot depend on the result of an earlier
  step. A loop over constants works; a loop over a server response does not.
- Code in a feature or scenario body outside the step closures runs at
  registration, including in `SIGNET_LIST=1` mode. Keeping registration bodies
  free of side effects (only `var` declarations and step calls) is a
  convention, not something the API enforces.
- Executed commands are not in the document. Adding them means `Result`
  writes them as attributes, and the document stops being description only.
- The BDD report YAML has a different shape (`when` / `then`) from
  `acceptance.yaml` (`run` / `expect`), though scenario IDs and names map
  1-to-1 onto `CASE id=<id> name="<name>"` via `signet report --format cases`.
  A UI either reads this report schema or converts YAML specs into it.
- The report is not runnable: `signet run` cannot replay it.
- No Gherkin `.feature` output yet; it can be added as `--format gherkin` on
  `signet report`.
- `kind: write` and the interactive confirmation prompt have no equivalent
  under `go test`.

## Open decisions

- **Fail-fast waiting.** `EventuallyWithT` cannot stop early on a condition
  that makes success impossible (e.g., the conversation was cancelled while the
  test waits for it to finish). The verdict is the same, reached only at the
  timeout. A `signet.Await` helper with a terminal/fatal predicate could
  short-circuit immediately.
- **Payload artifacts.** `Command` stdout/stderr and `Svc` logs are dumped
  automatically on `t.Failed()`, while project-specific HTTP/API helpers should
  write fetched payloads into `t.ArtifactDir()` or log them on failure.

## Implementation notes

What changed against the design above while building it:

- **`Background` receives `testing.TB`, not `*testing.T`.** Every background
  runs as its own subtest, so with the subtest's `t` a service it started was
  stopped by `t.Cleanup` before the scenario's steps ran. Both background
  kinds now get a `testing.TB` whose `Cleanup`, `TempDir`, `Setenv`, `Chdir`,
  `Context` and `ArtifactDir` belong to their scope; failures still go to the
  background step.
- **`Result.In(t)`.** A `Then` that checks the result of its `When` would
  otherwise call `Fatal` on the `When`'s `t` from another goroutine. `In`
  rebinds the checks to the `Then`'s `t`.
- **The report is one document with a `features` list**, each entry carrying
  `package` and `test`, so one stream from many packages is one file.
  Backgrounds carry `status` and `error`; a scenario whose background failed
  carries the message in its own `error`.
- **`signet report` exits 1** when a feature or package failed, so
  `go test -json | signet report` fails a CI step like `go test` would.
- **Commands default to a 1 minute timeout** (`Timeout(0)` leaves only the
  test deadline) and run in their own process group. A failing test logs its
  last five commands with their output; services log their output when the
  test that owns them failed.
- **Polling inside `require.EventuallyWithT`.** testify runs the condition in
  another goroutine, so `Result` checks (which call `Fatal`) must not be used
  there; read the `Result` fields, or poll an HTTP endpoint with `assert`
  against the `CollectT`, as `examples/acceptance/kv_test.go` does. This is
  the same gap as the fail-fast question above, and a `signet.Await` would
  close both.

## Prior art

| Tool | Shape | Why not adopted as is |
|---|---|---|
| godog | Gherkin `.feature` files bound to Go functions by regular expressions | The text is the source; code goes through text → regexp → function, with state in a context |
| Ginkgo v2 | `Describe/It/By`, dry run, JSON report | Own runner and CLI; `By` steps exist only during a run |
| allure-go | `provider.T` with nested steps, Allure results | Wrapper over `testing.T`, Allure format, structure known only after a run |
| gogiven | Given/When/Then over `testing`, HTML report | HTML only |
| clarify/subtest, GoConvey | Nested `t.Run` in a given-when-then style | No export |

## Iterations

1. Move the CLI into `cmd/signet` and extract process-group handling and
   service readiness/lifecycle into `internal/proc`, freeing the root
   `package signet` for the stdlib-only SDK.
2. Implement `Subject` (with `go.mod` root resolution and `SIGNET_BINARY`),
   `Command`/`Result`, `ServiceBuilder`/`Svc` (with auto-dump on `t.Failed()`),
   and `FreePort`.
3. Implement structure (`Feature`, `BackgroundOnce`, `Background`, `Scenario`,
   `Given`, `When`, `Then`) over `t.Run` and `t.Attr`, with `SIGNET_LIST=1`
   and the skip rules above.
4. Add `signet report` (`yaml` default + `--format cases`) consuming
   `go test -json`.
5. Rewrite one existing complex YAML suite as a Go feature and compare the
   resulting documents and failure diagnostics.
