# Signet Development Notes

## CLI Structure

- The repository root is the Go SDK (`package signet`, stdlib only). The CLI
  is `cmd/signet`; process-group handling and the background process
  lifecycle shared by both live in `internal/proc`.
- `signet` uses Cobra commands in `cmd/signet/main.go`. Do not add new manual `os.Args`
  dispatch or hand-rolled flag parsing.
- Keep command parsing in Cobra command builders and keep behavior in the
  existing helpers:
  - validation: `validateFiles`, `validateFile`
  - execution: `runAcceptance`, `runFile`
  - case listing: `discoverCases`
- The stable public shape is:
  - `signet validate <path>...`
  - `signet run <path>... [--yes] [--verbose] [--keep-temp] [--no-build] [--binary <path>]`
  - `signet cases <path>... [--case <id>|--id <id>] [--checks]`
  - `signet report [<file>] [--format yaml|cases]`
  - `signet completion zsh`
- Build/service lifecycle lives in `cmd/signet/service.go` on top of
  `internal/proc` (process groups, start, readiness polling, stop). Keep `runFile` as the orchestrator that
  calls `runBuild` then `startServices`/`stopServices` around the case loop.

## Acceptance Contracts

- The self-contract is `acceptance.yaml`; update it with every CLI shape or
  output contract change.
- Case IDs are mandatory. Case rows should stay explicit:
  `CASE id=<id> name="<name>"`.
- `cases` uses `CASES` as the section header so actual case rows are
  visually distinct.
- Specs are discovered recursively from `acceptance.yaml` and
  `*.acceptance.yaml`.
- Acceptance command safety is explicit: `run.kind` defaults to `read`; mark
  create/update/delete/deploy style steps as `write`. Read steps never prompt.
  Write steps prompt for interactive confirmation during `signet run` by default;
  `--yes` skips the prompt, and `defaults.confirm: false` pre-approves write steps
  suite-wide. `cases --checks` should make write steps visible with `KIND write`.
- `setup.files` creates per-acceptance-file temporary files. Keep variable
  expansion limited to explicit setup variables. Canonical variables are
  `${file.<name>}` and `${tmp}`; `${setup.files.<name>}` and `${setup.dir}` are
  compatibility aliases only. `${binary}` (alias `${subject.binary}`) expands to
  the resolved subject binary, honoring `--binary`.
- `setup.build` (string or list) runs to completion before cases; a non-zero
  exit fails the group. signet does not cache builds — incrementality is the
  build tool's job. `--no-build` skips it.
- `setup.services` are background processes started before cases and stopped
  after (reverse order, `SIGTERM`→`SIGKILL` after `stopTimeout`). Teardown always
  runs. Each service uses `args` (argv against `subject.binary`) or `shell`;
  `ready.shell` (polled to exit 0) or `ready.log` (substring) gates case start.
  `cases --checks` shows `BUILD`/`SERVICE`/`COMMAND`/`READY` so setup is visible.
- Step `capture` maps a name to a JSON Pointer over stdout JSON (`capture.go`).
  Values exist only after the step passes, are scoped to the case, and expand
  as `${capture.<name>}` in `run.args`/`run.shell`/`run.stdin` and expect
  values (regexp-quoted in `matches`). `validate` rejects references not
  captured by an earlier step in the case; numbers keep their literal text.
  Self-acceptance strings must not contain a literal `${capture.` — use an
  escaped `matches` pattern instead.
- Update `README.md` with every new public feature, CLI shape change, YAML
  field, or output contract that users need to understand.

## Go SDK

- The root package is stdlib only: `go list -deps .` must show no module
  outside the standard library besides `internal/*`. YAML rendering lives in
  `internal/report`, used only by the CLI.
- Attribute keys shared by the SDK and `signet report` are in
  `internal/attrs`; change both sides together.
- `testdata/sdk` holds fixture features (some fail on purpose). Root tests
  drive them through `go test -json`. `testdata/report/*.jsonl` are recorded
  streams of that package; after changing the fixtures, re-record them
  (commands in `internal/report/report_test.go`), refresh goldens with
  `go test ./internal/report -update`, and update counts in `acceptance.yaml`.
- `examples/` is a separate module (replace → `..`) that tests its own `kv`
  binary with the SDK. `make examples` runs it; `acceptance.yaml` checks its
  list mode and a full run through `signet report`.

## Verification

- Run `go test ./...` for compile-level coverage, and `make examples` after
  SDK changes.
- Run `make acceptance` before considering CLI changes complete. It rebuilds
  `./bin/signet` and runs `acceptance.yaml`.
- Run `go install ./cmd/signet` when the user needs the shell-visible `signet` binary
  updated.

## Shell Completion

- Cobra provides completion through `signet completion zsh`.
- Do not add a generated static completion file unless the user asks for that
  style explicitly.
