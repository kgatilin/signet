# signet

`signet` is a small CLI runner for final acceptance contracts.

An acceptance contract describes the final CLI command shape and the output
checks that prove it behaves as expected, without requiring a full e2e test
framework.

Contracts are written either as YAML specs run by the CLI, or as ordinary Go
tests with the signet Go SDK (see [Go SDK](#go-sdk)); both produce documents a
reader can follow without the code.

Acceptance specs use the same schema regardless of where they live. A project
can keep a single root `acceptance.yaml`, put specs under an `acceptance/`
directory, or use any other layout that fits the repo. `signet` discovers
`acceptance.yaml` and `*.acceptance.yaml` recursively.

```bash
go install github.com/kgatilin/signet/cmd/signet@latest
# or, from a clone:
go build -o bin/signet ./cmd/signet
```

> `signet run` executes commands described by the acceptance file, including
> arbitrary shell commands (`run.shell`), build commands (`setup.build`), and
> background services (`setup.services`). Review acceptance files before running
> them, and do not run untrusted contracts.

## Commands

`validate`, `run`, and `cases` accept one or more acceptance files or
directories. Directories are searched recursively for `acceptance.yaml` and
`*.acceptance.yaml`.

```bash
signet validate <path>...
signet run <path>... [--yes] [--verbose] [--keep-temp] [--no-build] [--binary <path>]
signet cases <path>... [--case <id>|--id <id>] [--checks]
signet report [<file>] [--format yaml|cases]
signet completion zsh
```

| Command    | Purpose |
|------------|---------|
| `validate` | Parse and statically validate specs. No commands run. Exit `1` on any invalid group. |
| `run`      | Build the subject, start services, execute each case's steps, and report pass/fail. Exit `1` on any failed check, `130` when a confirmation is declined. |
| `cases`    | List cases without executing anything. `--checks` also prints each step's command, expected checks, captures (`CAPTURE`) and capture references (`USES`), plus the `BUILD`/`SERVICE` setup. |
| `report`   | Read a `go test -json` stream (stdin or a file) of tests written with the Go SDK and print the feature document (`--format yaml`, default) or `GROUP`/`CASE` rows (`--format cases`). Exit `1` when a feature or package failed, `2` on invalid usage or when the input has no `go test -json` events (`go test` run without `-json`, or empty input). A stream of plain tests without features exits `0` with `features: []`. |
| `completion` | Emit a shell completion script (Cobra). |

### `run` flags

| Flag | Effect |
|------|--------|
| `--yes` | Skip the confirmation prompt for write steps. Read steps never prompt. |
| `--verbose`, `-v` | Print each executed command, its exit code, stdout, and stderr. |
| `--keep-temp` | Keep the setup temporary directory on disk after the run. |
| `--no-build` | Skip `setup.build` and run against the existing binary as-is. |
| `--binary <path>` | Override `subject.binary` for the whole run. |

### `cases` flags

| Flag | Effect |
|------|--------|
| `--checks` | Expand each case with its command and expected checks, and show `setup.build`/`setup.services`. |
| `--case <id>`, `--id <id>` | Show only the case with this id. |

## Complete spec syntax

Every field, annotated. All fields except `version` and `cases` are optional.

```yaml
version: 1                       # required, must be 1
suite: my-cli                    # label shown in `cases` group headers
description: >                    # free-text description
  What this contract proves.

subject:
  binary: ./bin/myapp            # executable used by argv steps/services;
                                 # optional if every step/service uses `shell`

setup:                           # everything prepared before cases run
  envFiles:                      # dotenv files loaded into the command env
    - ~/.myapp/.env
  requireEnv:                    # env vars that must be set and non-empty
    - MYAPP_TOKEN
  files:                         # temp files generated per acceptance file
    - name: config               # ${file.config} -> the generated path
      path: config/app.yaml      # relative, stays inside ${tmp}
      content: |
        profile: acceptance
  build:                         # command(s) run to completion before cases
    - "make build"               # a single string is also accepted
  services:                      # background processes kept alive during cases
    - name: api
      args: ["serve", "--port", "8080"]   # runs subject.binary serve --port 8080
      # shell: "${binary} serve --port 8080"   # ...or a shell command instead
      ready:                     # gate: cases wait until the service is up
        shell: "curl -sf localhost:8080/health"  # polled until it exits 0
        # log: "listening on"    # ...or wait for a substring in the output
        timeout: 10s             # how long to wait for readiness (default 10s)
      stopTimeout: 5s            # grace before SIGKILL on teardown (default 5s)

defaults:
  timeout: 10s                   # per-step timeout (default 10s)
  confirm: true                  # prompt before write steps (default true)

cases:
  - id: round-trip               # required, unique; [A-Za-z0-9][A-Za-z0-9._-]*
    name: client hits the api    # required, human-readable
    steps:
      - name: send request       # required
        run:
          kind: read             # read (default) | write
          args: ["client", "get", "--url", "localhost:8080/things"]
          # shell: "curl -s localhost:8080/things"   # ...instead of args
          stdin: ""              # piped to the command's stdin
          timeout: 5s            # overrides defaults.timeout for this step
        expect:                  # at least one check is required
          exitCode: 0
          stdout:
            contains: ["ok"]
            notContains: ["error"]
            orderedContains: ["first", "second"]
            matches: ["^ok: [0-9]+$"]
          stderr:
            notContains: ["panic"]
        capture:                 # values read from stdout JSON after the step passes
          thingId: /items/0/id   # name: JSON Pointer; later steps use ${capture.thingId}
```

## Field reference

### `subject`

- `binary` — path to the executable that argv-form steps and services invoke.
  Expanded (`${tmp}`, `${file.<name>}`), and overridable with `--binary`.
  Optional when every step and service uses `shell`.

### `setup`

Runs once per acceptance file, in this order: env files → `requireEnv` → files →
`build` → services.

- `envFiles` — list of dotenv files loaded into the environment passed to every
  command. Paths may be absolute, relative, or use `~/` for the home directory.
  A missing file fails the run with a `setup.envFiles[i]` error. Existing process
  environment variables take precedence over file values; files are loaded in
  order, so later files override earlier ones. Lines use `KEY=VALUE` syntax with
  optional `export`, comments, and single- or double-quoted values.
- `requireEnv` — env var names that must be set and non-empty after `envFiles`
  are loaded. A missing one fails the run before any file or command is created.
- `files` — temporary files created before cases run, each `{ name, path,
  content }`. `name` matches `[A-Za-z0-9][A-Za-z0-9._-]*` and must be unique;
  `path` must be relative and stay inside the temp directory. Reference a
  generated file with `${file.<name>}`. The temp directory is removed after the
  file runs unless `--keep-temp` is passed.
- `build` — a shell command **or a list of shell commands** run to completion,
  in order, from the current working directory, before any case. A non-zero exit
  fails the group and the cases do not run. signet does **not** cache builds —
  incrementality is delegated to the build tool (`make`/`go build` only recompile
  what changed). `--no-build` skips this entirely.
- `services` — long-running background processes started before cases, kept
  alive while they run, and stopped afterwards in reverse start order. Teardown
  always runs, including when a case fails or a confirmation is declined.

Each service has:

- `name` — unique label (`[A-Za-z0-9][A-Za-z0-9._-]*`).
- `args` — argv run against `subject.binary`, **or** `shell` — a shell command.
- `ready` — gate before cases start:
  - `ready.shell` — a command polled until it exits `0`, or
  - `ready.log` — a substring waited for in the service's combined output.
  - `ready.timeout` — how long to wait (default `10s`). With no `ready` block the
    service is assumed ready as soon as it starts.
- `stopTimeout` — grace period after `SIGTERM` before signet sends `SIGKILL`
  (default `5s`). Signals go to the whole process group so shell-wrapped services
  and their children are torn down.

### `defaults`

- `timeout` — per-step timeout as a Go duration (`10s`, `2m`); default `10s`.
  A step's `run.timeout` overrides it. Build commands use a fixed `5m` timeout;
  readiness probes use `5s` per attempt.
- `confirm` — whether write steps prompt before executing; default `true`.
  `--yes` skips the prompt regardless; setting `confirm: false` pre-approves
  write steps suite-wide. Read steps never prompt.

### `cases` / `steps`

- Each case needs a unique `id` (`[A-Za-z0-9][A-Za-z0-9._-]*`) and a `name`, and
  at least one step.
- Each step needs a `name`, a `run` block with `args` or `shell`, and an
  `expect` block with at least one check. `capture` is optional.

### `run`

- `kind` — `read` (default) or `write`. See [Confirmation](#confirmation).
- `args` — argv passed to `subject.binary`.
- `shell` — a command run via `/bin/sh -c`; used instead of `args`.
- `stdin` — string piped to the command's standard input.
- `timeout` — Go duration overriding `defaults.timeout` for this step.

### `expect`

`exitCode` is an integer check. `stdout` and `stderr` each support:

| Check | Passes when |
|-------|-------------|
| `contains` | every listed substring is present |
| `notContains` | none of the listed substrings are present |
| `orderedContains` | every listed substring is present **and** in the listed order |
| `matches` | every listed [RE2](https://github.com/google/re2/wiki/Syntax) regular expression matches |

### `capture`

A step can read scalar values from its stdout JSON and hand them to the steps
after it in the same case. `capture` maps a name to a
[JSON Pointer](https://www.rfc-editor.org/rfc/rfc6901):

```yaml
steps:
  - name: create session
    run:
      kind: write
      args: ["session", "create", "--json"]
    expect:
      exitCode: 0
    capture:
      contextId: /contextId      # {"contextId": "ctx-1", ...}
      lastSeq: /history/lastSeq  # nested keys; /items/0 indexes arrays

  - name: append message
    run:
      args: ["session", "send", "sessions/${capture.contextId}", "--after", "${capture.lastSeq}"]
    expect:
      stdout:
        contains: ["${capture.contextId}"]
```

- Reference a value as `${capture.<name>}` in `run.args`, `run.shell`,
  `run.stdin`, and in `expect.stdout`/`expect.stderr` values. In `matches`
  the value is regexp-quoted, so it always matches literally.
- Values are captured only after the producing step passes all its `expect`
  checks. stdout must be exactly one JSON document (surrounding whitespace is
  fine); the pointer must resolve to a string, number, or boolean. Strings are
  substituted unquoted, booleans as `true`/`false`, and numbers exactly as
  written, so integers keep full precision (uint64 sequences included).
- Malformed JSON, a pointer that does not resolve, and an object, array, or
  `null` value fail the step with a `capture <name> stdout <pointer>` line. A
  step's captures are all-or-nothing.
- Captures are scoped to one case. A later step may capture the same name again
  (for example a newer `lastSeq` cursor); references then see the newest value.
- `validate` rejects a reference that no earlier step in the same case captures,
  and pointers that are not `""` or `/...` (escape `/` as `~1` and `~` as `~0`
  inside a key).
- At run time, when the producing step failed, every step that references its
  captures fails as `unresolved` without running.
- Names use `[A-Za-z0-9][A-Za-z0-9._-]*`. Only stdout is read.

`signet cases --checks` prints `CAPTURE <name> stdout <pointer>` for each
declaration and `USES capture.<name>` for each reference.

## Variables

These expand inside `subject.binary`, `setup.build`, `setup.files` `content`,
`run.args`, `run.shell`, `run.stdin`, service `args`/`shell`, and `ready.shell`:

| Variable | Expands to | Alias |
|----------|-----------|-------|
| `${binary}` | the resolved subject binary (honors `--binary`) | `${subject.binary}` |
| `${tmp}` | the setup temporary directory | `${setup.dir}` |
| `${file.<name>}` | the path of the generated `setup.files` entry | `${setup.files.<name>}` |
| `${capture.<name>}` | a value captured by an earlier step of the same case — see [`capture`](#capture); only in `run.args`, `run.shell`, `run.stdin` and `expect` values | |

`subject.binary` is defined once and reused everywhere: argv-form services and
steps invoke it directly, and `${binary}` references its path in shell commands.
(`${binary}` is not available inside `subject.binary` itself — that field defines
it — but `${tmp}` and `${file.<name>}` are.) Referencing an unknown variable
fails the run.

Inside `setup.files` `content`, `${file.<name>}` resolves only to files declared
earlier in the list (files are written top to bottom); `${tmp}` is always
available. This lets a generated config point runtime paths — a file-backed
event log, a scratch database — into `${tmp}` so each run starts isolated.

## Confirmation

**Read steps never prompt** — they run straight through. Confirmation applies
only to steps marked `run.kind: write` (create, update, delete, deploy, and
similar mutating operations):

```yaml
steps:
  - name: create thing
    run:
      kind: write
      args: ["thing", "create", "--name", "acceptance-smoke"]
```

A write step prompts for an interactive `y`/`yes` confirmation by default.
Pass `--yes` to skip the prompt for the run, or set `defaults.confirm: false`
in the spec to pre-approve write steps suite-wide. A declined prompt aborts the
run with exit code `130`.

`setup.build` and `setup.services` are declared infrastructure and run without a
prompt. `signet cases --checks` prints `KIND write` for write steps and shows the
`BUILD`/`SERVICE` setup so risky commands are visible before running a contract.

## Execution model

For each acceptance file `signet run`:

1. Loads and validates the spec.
2. Runs setup: env files, `requireEnv`, `files`, then `build`, then `services`
   (waiting for each to be ready).
3. Runs every case's steps in order, confirming as configured, evaluates the
   `expect` checks, and records the step's `capture` values for the rest of the
   case.
4. Stops services (reverse order, `SIGTERM` then `SIGKILL` after `stopTimeout`)
   and removes the temp directory.

Use `signet run --verbose` to print each executed command, exit code, stdout, and
stderr. Use `signet cases --checks` to inspect every step command without
executing it.

## Go SDK

`github.com/kgatilin/signet` is a stdlib-only Go package for acceptance tests
that run under `go test`. A test function declares one feature; scenarios and
steps become subtests whose keyword and text are recorded with `t.Attr`, so
`signet report` can rebuild the document from `go test -json`. Assertions are
not part of the SDK: steps receive a `testing.TB` or a `*testing.T`, so
testify or plain `if` works. A complete module that uses it is in
[`examples/`](examples/).

```go
var app = signet.Subject("go build -o bin/app ./cmd/app", "bin/app")

func TestStore(t *testing.T) {
    signet.Feature(t, "Key-value store", "Writes apply asynchronously.", func(f *signet.F) {
        var url string
        f.BackgroundOnce("a running server", func(t testing.TB) {
            addr := fmt.Sprintf("127.0.0.1:%d", signet.FreePort(t))
            app.Service(t, "server", "serve", "--addr", addr).
                ReadyHTTP("http://"+addr+"/healthz", 200).
                Start()
            url = "http://" + addr
        })

        f.Scenario("put-then-get", "a write becomes readable once applied", func(s *signet.Scenario) {
            var put *signet.Result
            s.When("a client puts greeting=hello and it is applied", func(t testing.TB) {
                put = app.Cmd(t, "put", "greeting", "hello").Env("SERVER=" + url).Run().OK()
                // wait here (e.g. require.EventuallyWithT) until the state settles
            }).
                Then("the put is acknowledged", func(t *testing.T) { put.In(t).StdoutContains("accepted") }).
                Then("get returns hello", func(t *testing.T) {
                    app.Cmd(t, "get", "greeting").Env("SERVER=" + url).Run().OK().StdoutContains("hello")
                })
        })
    })
}
```

```bash
go test -json -count=1 ./acceptance/... | signet report               # run, then the document
SIGNET_LIST=1 go test -json -count=1 ./acceptance/... | signet report # the document, nothing runs
```

### Structure

| Call | Meaning |
|------|---------|
| `Feature(t, name, description, body)` | One feature per test function. `body` only registers; scenarios run after it returns. |
| `f.BackgroundOnce(text, fn)` | Shared fixture. Runs once, as a step of the first scenario that executes. `fn` gets a `testing.TB` whose `Cleanup`, `TempDir`, `Setenv`, `Chdir`, `Context` and `ArtifactDir` belong to the feature. If it fails, its scenario fails and later scenarios are skipped. |
| `f.Background(text, fn)` | Fixture run in every scenario before its steps; its resources belong to the scenario. |
| `f.Scenario(id, name, body)` | `id` is the subtest name: unique, no whitespace or slash. `body` only registers steps. |
| `s.Given(text, fn)` | Precondition. `fn` gets a `testing.TB` whose resources belong to the scenario. |
| `s.When(text, fn).Then(text, fn)...` | Action, then checks nested under it. Waiting until the system settles belongs in the `When`. The `When` gets a `testing.TB` like `Given`, each `Then` its own `*testing.T`. |
| `s.Then(text, fn)` | Check directly on the scenario, for scenarios without `When`. `fn` gets its own `*testing.T`. |

The parameter type says who owns what a step creates. `testing.TB`
(backgrounds, `Given`, `When`) means it outlives the step: `t.TempDir()`,
`t.Cleanup`, `t.Setenv`, `t.Chdir`, `t.Context()`, `t.ArtifactDir()` and a
`Service` started with that `t` belong to the scenario (to the feature for
`BackgroundOnce`) and end with it. `*testing.T` (`Then`) means the check owns
its resources, and they end with the check. Failures and logs go to the step
while it runs. A failure raised after the step has returned, in a cleanup it
registered, fails the scenario (the feature for `BackgroundOnce`).

Execution rules: a failed background, `Given` or `When` skips everything after
it in the scenario (the checks are still registered, as skipped). A failed
`Then` does not stop the other checks of its block; later `When` blocks are
skipped. Subtest names are `Given …`, `When …`, `Then …`, `Background …`, so
`go test -run 'TestStore/put-then-get'` selects one scenario.

A scenario is the smallest unit `-run` and `-skip` can select. A pattern that
reaches below it (`-run 'TestStore/put-then-get/Then'`) leaves out steps the
scenario needs: the scenario fails with a message that names the first step
that did not run, and its remaining steps are skipped.

With `SIGNET_LIST=1`, every scenario and step is registered and skipped;
backgrounds, step closures and builds do not run.

### Fixtures

| Call | Meaning |
|------|---------|
| `Subject(buildShell, binaryPath)` | The binary under test. The build runs with `/bin/sh` in the module root (nearest `go.mod`), once per test process, on first use. A relative path resolves against the same root. With `SIGNET_BINARY=<absolute path>` nothing is built. |
| `app.Path(t)`, `app.Run(t, args...)`, `app.Cmd(t, args...)`, `app.Service(t, name, args...)` | Use the subject. |
| `Cmd(t, name, args...)` | Any command. Builder: `.Env(kv...)`, `.Dir(dir)`, `.Stdin(s)`, `.Timeout(d)` (default 1m, 0 = test deadline only), then `.Run()`. Runs in its own process group. |
| `Result` | `ExitCode`, `Stdout`, `Stderr`, `Duration`, `TimedOut`, `Err`; checks `.OK()`, `.Exit(code)`, `.StdoutContains(s)`, `.StderrContains(s)` fail the test the command ran in. `res.In(t)` rebinds the checks to another step's `t`; a `Then` that checks the `Result` of a `Given` or `When` needs it. `JSON[T](res)` decodes stdout. |
| `Service(t, name, command, args...)` | Background process. Builder: `.Env`, `.Dir`, `.ReadyHTTP(url, status)` (0 = any status), `.ReadyLog(substring)`, `.ReadyTimeout(d)` (default 10s), then `.Start()`. Stopped with `SIGTERM`, then `SIGKILL` after 5s, when the owning test ends. `svc.Log()`, `svc.Stop()`. |
| `FreePort(t)` | A TCP port on 127.0.0.1 that was free when asked. |

A command is cancelled with `t.Context()`. `testing` cancels that context
before it runs `t.Cleanup` functions, so a command run inside a cleanup
(teardown through the CLI under test, `t.Cleanup(func() { app.Run(t, "delete",
id).OK() })`) runs without it and is bounded by its `Timeout` alone. With
`Timeout(0)` nothing bounds it there except the `-timeout` of the test binary.

When a test fails, its last five commands and the output of the services it
started are logged with it. The backgrounds, `Given` and `When` steps of a
scenario share one command history, logged on the scenario when it fails
(`BackgroundOnce` commands are logged on the feature); a `Then` logs its own
commands.

Services are stopped by `t.Cleanup`. When the test binary panics on `-timeout`
or is interrupted with Ctrl-C, it exits without running cleanups, and the
services, each in its own process group, keep running; so does a command
that was running at that moment. Stop them by hand (`pkill -f <binary>`). To
stay clear of the `-timeout` case, keep every wait shorter than the test
deadline (`Timeout` on commands, `ReadyTimeout` on services, the deadline of
polling loops), so a step fails and its cleanups run before the deadline.

`go test` caches results and does not track the external binary, so run
acceptance packages with `-count=1`. Go's per-package processes each build the
subject once; for many packages, build once and set `SIGNET_BINARY`:

```make
acceptance: build
	SIGNET_BINARY=$(CURDIR)/bin/app go test ./acceptance/... -count=1
```

### Report document

```yaml
features:
  - package: example.com/app/acceptance
    test: TestStore
    feature: Key-value store
    description: Writes apply asynchronously.
    status: pass
    duration: 1.2s
    background:
      - text: a running server
        scope: feature            # feature = BackgroundOnce, scenario = Background
        status: pass
    scenarios:
      - id: put-then-get
        name: a write becomes readable once applied
        status: pass
        steps:
          - when: a client puts greeting=hello and it is applied
            status: pass
            duration: 340ms
            then:
              - text: the put is acknowledged
                status: pass
              - text: get returns hello
                status: pass
```

`status` is `pass`, `fail` or `skip`; a failed step carries `error` with the
test's failure messages, and a scenario whose background failed carries the
background's message.

A failed feature, background, scenario or step also carries `log`: the plain
output `go test` attributed to that test, which is its `t.Log` lines and
whatever the test process printed while it ran. This is where the SDK's
failure output ends up: the last commands with their output, and the output
of services.

```yaml
    scenarios:
      - id: put-then-get
        name: a write becomes readable once applied
        status: fail
        log: |-
          command.go:234: signet: last 1 command(s):
          $ /work/app/bin/app put greeting hello  (exit 1, 12ms)
          stderr:
            app: server answered 500
        steps:
          - when: a client puts greeting=hello and it is applied
            status: fail
            error: |-
              store_test.go:27: /work/app/bin/app put greeting hello: exit 1, want 0
              stderr:
                app: server answered 500
```

A log line written after its test has finished is attributed by `testing` to
the nearest parent still running. Commands and services belong to the
scenario or the feature and are logged when it ends, so their output is in
the `log` of that scenario or feature, next to the failed step. Tests that
pass keep their output out of the document.

## Environment

Color is enabled automatically on terminals. Set `NO_COLOR=1` to disable it, or
`CLICOLOR_FORCE=1` to force color when output is not a terminal.

Released under the MIT License.
