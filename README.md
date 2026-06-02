# signet

`signet` is a small CLI runner for final acceptance contracts.

An acceptance contract describes the final CLI command shape and the output
checks that prove it behaves as expected, without requiring a full e2e test
framework.

Acceptance specs use the same schema regardless of where they live. Recommended
locations are `acceptance/core/<group>/*.acceptance.yaml` for core specs,
`internal/plugins/<plugin>/acceptance/*.acceptance.yaml` for plugin-owned specs,
and `docs/features/<branch>/acceptance/**` for feature-branch deliverables.
`signet` discovers `acceptance.yaml` and `*.acceptance.yaml` recursively.

```bash
go build -o bin/signet .
signet validate acceptance.yaml
signet validate docs/features/<branch>/acceptance
signet run acceptance.yaml --yes
signet run acceptance.yaml --yes --verbose
signet run docs/features/<branch>/acceptance --yes
```

`signet validate`, `signet run`, and `signet discover` accept one or more
acceptance files or directories. Directories are searched recursively for
`acceptance.yaml` and `*.acceptance.yaml`.

The CLI also exposes discovery commands so users and agents can inspect a
contract before running it:

```bash
signet discover groups <path>...
signet discover <path>...
signet discover cases <path>... [--case <id>]
signet discover cases <path>... [--case <id>] --checks
```

Use `signet run --verbose` to print each executed command, exit code, stdout,
and stderr. Use `signet discover cases --checks` to inspect each step command
without executing it.

Each case must define a stable `id`:

```yaml
cases:
  - id: model-invoke-help
    name: invoke help exposes generic request flags
```

Supported checks:

```yaml
expect:
  exitCode: 0
  stdout:
    contains: [...]
    notContains: [...]
    orderedContains: [...]
    matches: [...]
  stderr:
    contains: [...]
```

Color is enabled automatically on terminals. Set `NO_COLOR=1` to disable it, or
`CLICOLOR_FORCE=1` to force color when output is not a terminal.
