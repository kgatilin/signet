# Signet Development Notes

## CLI Structure

- `signet` uses Cobra commands in `main.go`. Do not add new manual `os.Args`
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
  - `signet completion zsh`
- Build/service lifecycle lives in `service.go`; process-group signals are in
  `process_unix.go`/`process_other.go`. Keep `runFile` as the orchestrator that
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
  create/update/delete/deploy style steps as `write`. Write steps always require
  interactive confirmation during `signet run`, even when `--yes` is passed or
  `defaults.confirm: false` is set. `cases --checks` should make write steps
  visible with `KIND write`.
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
- Update `README.md` with every new public feature, CLI shape change, YAML
  field, or output contract that users need to understand.

## Verification

- Run `go test ./...` for compile-level coverage.
- Run `make acceptance` before considering CLI changes complete. It rebuilds
  `./bin/signet` and runs `acceptance.yaml`.
- Run `go install .` when the user needs the shell-visible `signet` binary
  updated.

## Shell Completion

- Cobra provides completion through `signet completion zsh`.
- Do not add a generated static completion file unless the user asks for that
  style explicitly.
