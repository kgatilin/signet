# Proposal: Go SDK for acceptance tests

Status: proposal, nothing implemented.

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

1. **A description structure**: Feature → Background → Scenario → `When`
   blocks, each with its `Then` checks. Every level carries a sentence
   written for a reader.
2. **Fixtures**: the subject binary, background services, a free port.
3. **A report command** that turns the `go test -json` stream into a document.

The scenario body only registers steps. All work is inside the step closures.
The same code therefore serves two modes: a run, and a listing that produces
the document without executing anything.

Assertions are not part of signet. Step closures receive `*testing.T`, so
testify or plain `if` works.

## What already exists

| Need | Provided by |
|---|---|
| Named cases and steps with status and duration | `t.Run`; nested subtests appear in `go test -json` |
| Machine-readable description fields | `t.Attr(key, value)`, emitted as `attr` events in `-json` |
| Setup once per process | `TestMain`, `sync.OnceValue` |
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
process-group stop, log on failure; already in `service.go`), a `Result` over
`exec`, binary resolution across packages, the BDD structure, and the report
command.

## API

```go
package signet

// Main builds the subject once per test process, then runs the tests.
// With SIGNET_BINARY set, nothing is built.
func Main(m *testing.M, opts ...Option)
func Build(shell string) Option

func Feature(t *testing.T, name, description string) *F
func (f *F) Background(text string, fn func(t *testing.T))
func (f *F) Scenario(id, name string, body func(s *Scenario))

func (s *Scenario) Given(text string, fn func(t *testing.T))
func (s *Scenario) When(text string, fn func(t *testing.T)) *Block
func (b *Block) Then(text string, fn func(t *testing.T)) *Block

// Fixtures
func Binary(t testing.TB) string
func Run(t testing.TB, args ...string) *Result
func Service(t testing.TB, name string, args ...string) *Svc // stopped in t.Cleanup
func (s *Svc) ReadyHTTP(url string, status int) *Svc
func (s *Svc) ReadyLog(substring string) *Svc
func FreePort(t testing.TB) int

// Result
func (r *Result) OK() *Result
func (r *Result) Exit(code int) *Result
func (r *Result) StderrContains(s string) *Result
func JSON[T any](r *Result) T
```

How it maps onto `testing`:

- A scenario is `t.Run(id)` with the name as an attribute.
- A `When` is a subtest of the scenario; each `Then` is a subtest of its
  `When`. The keyword and the text are attributes.
- `Background` runs once before the first scenario of the feature.

Execution rules:

- A failed `Given` or `When` skips its `Then` checks and every later block
  in the scenario.
- A failed `Then` does not stop the other `Then` checks of the same block,
  so the report shows which facts hold. Later `When` blocks are skipped.

## Example

The shell step above, as a scenario. `apptest` and `events` are the
project's own helper packages (a client for the record log and typed
selections over it); they are not part of signet.

```go
func TestMain(m *testing.M) { signet.Main(m, signet.Build("make build")) }

func TestScheduling(t *testing.T) {
    f := signet.Feature(t, "Scheduling",
        "A timer firing is a channel fact that ingress routes into a scheduled conversation.")

    var log *apptest.Log
    f.Background("a running service with a timer job named reminder", func(t *testing.T) {
        log = apptest.Start(t, serviceConfig).Log()
    })

    f.Scenario("scheduling-firing-roundtrip",
        "one timer firing becomes one routed scheduled conversation", func(s *signet.Scenario) {
        var (
            fired    events.TimerFired
            received events.Record[events.MessageReceived]
            session  string
        )

        s.When("the timer fires for job reminder", func(t *testing.T) {
            require.EventuallyWithT(t, func(c *assert.CollectT) {
                fired = events.First[events.TimerFired](c, log.Records(c, "channel.scheduler.>"),
                    func(e events.TimerFired) bool { return e.Job == "reminder" })
            }, 50*time.Second, 500*time.Millisecond)
        }).
            Then("the conversation finishes without error or cancellation", func(t *testing.T) {
                require.EventuallyWithT(t, func(c *assert.CollectT) {
                    received = events.OneRecord[events.MessageReceived](c, log.Records(c, "channel.scheduler.>"), onThread(fired.Thread))
                    session = events.Last[events.SessionBound](c, log.Records(c, "ingress.scheduler.>"), onThread(fired.Thread)).Session
                    done := events.One[events.MessageProcessed](c, log.Session(c, session), forMessage(received.ID))
                    assert.Empty(c, done.Error)
                    assert.False(c, done.Cancelled)
                }, 50*time.Second, 500*time.Millisecond)
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
            signet.Run(t, "send", "cancel", "--thread", thread).OK()
        }).
            Then("the message is closed as cancelled", func(t *testing.T) { /* ... */ }).
            Then("progress reports cancelled", func(t *testing.T) { /* ... */ }).
            Then("ingress routes the cancel once", func(t *testing.T) { /* ... */ })
    })
}
```

## Exported document

```
go test ./acceptance/... -json -count=1 | signet report > report.yaml   # after a run
go test ./acceptance/... -json -signet.list | signet report             # without running
```

With `-signet.list` every step is registered and skipped; `Background` and
the step closures do not execute. Both commands produce the same document,
with `status: skip` in the second case.

```yaml
feature: Scheduling
description: A timer firing is a channel fact that ingress routes into a scheduled conversation.
background: a running service with a timer job named reminder
scenarios:
  - id: scheduling-firing-roundtrip
    name: one timer firing becomes one routed scheduled conversation
    status: pass
    steps:
      - when: the timer fires for job reminder
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

The document contains only the text written in `Feature`, `Background`,
`Scenario`, `Given`, `When` and `Then`, plus status, duration and the failure
message. Commands, JSON and assertions stay in the code.

## Build

`signet.Main` runs the build once per test process. `go test` starts one
process per package, so a suite spread over several packages sets
`SIGNET_BINARY` after building once:

```make
acceptance: build
	SIGNET_BINARY=$(CURDIR)/bin/app go test ./acceptance/... -count=1
```

`-count=1` is required: `go test` caches results and does not track a
binary the test executes.

## What this design does not do

- The text and number of steps cannot depend on the result of an earlier
  step. A loop over constants works; a loop over a server response does not.
- Code in a scenario body outside the step closures runs at registration,
  including in list mode. Keeping the body free of side effects is a
  convention, not something the API enforces.
- Executed commands are not in the document. Adding them means `Result`
  writes them as attributes, and the document stops being description only.
- The BDD document has a different shape from `acceptance.yaml` (no `run`
  or `expect`). A UI needs a second reader, or the YAML specs need a
  converter into this shape.
- The report is not runnable: `signet run` cannot replay it.
- No Gherkin output. It can be a second format of `signet report`.
- `kind: write` and the confirmation prompt have no equivalent under
  `go test`.

## Open decisions

- **Fail-fast waiting.** `EventuallyWithT` cannot stop early on a condition
  that makes success impossible (the conversation was cancelled while the
  test waits for it to finish). The verdict is the same, reached only at the
  timeout. A `signet.Await` with a fatal condition would fix it.
- **Order among `Then` checks.** In the example the first `Then` waits and
  the rest read a settled log; the API does not show that dependency.
  `When(...).Within(d)` could retry every `Then` until it passes, but
  absence checks then pass immediately and prove nothing, so they would need
  their own step kind.
- **Failure diagnostics.** The shell version dumps every fetched payload.
  Project helpers should write what they fetched into `t.ArtifactDir()` or
  log it on failure; signet could offer a hook for that on `Result`.

## Prior art

| Tool | Shape | Why not adopted as is |
|---|---|---|
| godog | Gherkin `.feature` files bound to Go functions by regular expressions | The text is the source; code goes through text → regexp → function, with state in a context |
| Ginkgo v2 | `Describe/It/By`, dry run, JSON report | Own runner and CLI; `By` steps exist only during a run |
| allure-go | `provider.T` with nested steps, Allure results | Wrapper over `testing.T`, Allure format, structure known only after a run |
| gogiven | Given/When/Then over `testing`, HTML report | HTML only |
| clarify/subtest, GoConvey | Nested `t.Run` in a given-when-then style | No export |

## Iterations

1. Move the service lifecycle, process handling and setup files out of
   `package main` into an importable package; the CLI becomes `cmd/signet`.
2. Fixtures: `Main`, `Build`, `Binary`, `Run`, `Result`, `Service`, `FreePort`.
3. Structure: `Feature`, `Background`, `Scenario`, `Given`, `When`, `Then`
   over `t.Run` and `t.Attr`, with the skip rules above.
4. `signet report` and `-signet.list`.
5. Rewrite one existing YAML suite as a Go feature and compare the documents.
