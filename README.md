# signet

`signet` is a small CLI runner for final acceptance contracts.

An acceptance contract describes the final CLI command shape and the output
checks that prove it behaves as expected, without requiring a full e2e test
framework.

```bash
go build -o bin/signet .
signet validate acceptance.yaml
signet run acceptance.yaml --yes
```

The CLI also exposes discovery commands so users and agents can inspect a
contract before running it:

```bash
signet discover groups <path>
signet discover <path>
signet discover cases <file>
signet discover cases <file> --checks
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
