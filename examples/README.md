# signet Go SDK examples

A separate Go module that tests its own binary, `kv`, with the signet Go SDK.
`kv` is a small key-value server and client: writes are accepted at once and
applied by a background worker after a delay, so the tests have to wait for
a settled state, much like tests of an event-driven service.

```sh
make examples            # from the repository root: vet + go test
make examples-report     # run, then print the feature document
SIGNET_LIST=1 go -C examples test -json ./... | signet report   # document only, nothing runs
```

| File | Shows |
|---|---|
| `acceptance/kv_test.go` | `Subject` built once from this module's root, a `Service` with HTTP and log readiness, helpers that poll the event log with `require.EventuallyWithT` |
| `acceptance/cli_test.go` | scenarios with only `Then` checks, a `When` whose `Result` its `Then` checks read through `res.In(t)`, `Cmd` with `Env`, an arbitrary command (`go version -m`) |
| `acceptance/store_test.go` | `BackgroundOnce` sharing one server across scenarios, waiting in `When`, several `When` blocks in one scenario, `Given`, `Stdin` |
| `acceptance/isolation_test.go` | `Background` starting a fresh server for every scenario |

The module points at the SDK in the parent directory through a `replace`
directive, so it always tests the current checkout.
