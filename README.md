# signet

`signet` is a small CLI runner for final acceptance contracts.

An acceptance contract describes the final CLI command shape and the output
checks that prove it behaves as expected, without requiring a full e2e test
framework.

Acceptance specs use the same schema regardless of where they live. A project
can keep a single root `acceptance.yaml`, put specs under an `acceptance/`
directory, or use any other layout that fits the repo. `signet` discovers
`acceptance.yaml` and `*.acceptance.yaml` recursively.

```bash
go install github.com/kgatilin/signet@latest
# or, from a clone:
go build -o bin/signet .
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
signet completion zsh
```

| Command    | Purpose |
|------------|---------|
| `validate` | Parse and statically validate specs. No commands run. Exit `1` on any invalid group. |
| `run`      | Build the subject, start services, execute each case's steps, and report pass/fail. Exit `1` on any failed check, `130` when a confirmation is declined. |
| `cases`    | List cases without executing anything. `--checks` also prints each step's command and expected checks, plus the `BUILD`/`SERVICE` setup. |
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
  `expect` block with at least one check.

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

## Variables

These expand inside `subject.binary`, `setup.build`, `run.args`, `run.shell`,
`run.stdin`, service `args`/`shell`, and `ready.shell`:

| Variable | Expands to | Alias |
|----------|-----------|-------|
| `${binary}` | the resolved subject binary (honors `--binary`) | `${subject.binary}` |
| `${tmp}` | the setup temporary directory | `${setup.dir}` |
| `${file.<name>}` | the path of the generated `setup.files` entry | `${setup.files.<name>}` |

`subject.binary` is defined once and reused everywhere: argv-form services and
steps invoke it directly, and `${binary}` references its path in shell commands.
(`${binary}` is not available inside `subject.binary` itself — that field defines
it — but `${tmp}` and `${file.<name>}` are.) Referencing an unknown variable
fails the run.

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
3. Runs every case's steps in order, confirming as configured, and evaluates the
   `expect` checks.
4. Stops services (reverse order, `SIGTERM` then `SIGKILL` after `stopTimeout`)
   and removes the temp directory.

Use `signet run --verbose` to print each executed command, exit code, stdout, and
stderr. Use `signet cases --checks` to inspect every step command without
executing it.

## Environment

Color is enabled automatically on terminals. Set `NO_COLOR=1` to disable it, or
`CLICOLOR_FORCE=1` to force color when output is not a terminal.

Released under the MIT License.
