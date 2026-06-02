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
  - `signet run <path>... [--yes] [--verbose] [--binary <path>]`
  - `signet cases <path>... [--case <id>|--id <id>] [--checks]`
  - `signet completion zsh`

## Acceptance Contracts

- The self-contract is `acceptance.yaml`; update it with every CLI shape or
  output contract change.
- Case IDs are mandatory. Case rows should stay explicit:
  `CASE id=<id> name="<name>"`.
- `cases` uses `CASES` as the section header so actual case rows are
  visually distinct.
- Specs are discovered recursively from `acceptance.yaml` and
  `*.acceptance.yaml`.

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
