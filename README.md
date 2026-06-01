# signet

`signet` is a small CLI runner for final acceptance contracts.

The first artifact is [`acceptance.yaml`](acceptance.yaml): a self-acceptance
spec that defines the intended command shape and report shape before the tool is
implemented.

The CLI also exposes discovery commands so users and agents can inspect a
contract before running it:

```bash
signet discover groups <path>
signet discover cases <file>
signet discover cases <file> --checks
```
