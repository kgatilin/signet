package acceptance

import (
	"fmt"
	"testing"

	"github.com/kgatilin/signet"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestCommandLine shows scenarios without a server: direct Then checks,
// a When whose result is checked by its Thens, and an arbitrary command.
func TestCommandLine(t *testing.T) {
	signet.Feature(t, "kv command line",
		"Commands that need no server, and how the client reports usage and connection errors.",
		func(f *signet.F) {
			f.Scenario("version", "version reports the tool name and version", func(s *signet.Scenario) {
				s.Then("version --json names the tool and its version", func(t *testing.T) {
					got := signet.JSON[map[string]string](kv.Run(t, "version", "--json").OK())
					assert.Equal(t, map[string]string{"name": "kv", "version": "1.0.0"}, got)
				})
				s.Then("plain version prints one line", func(t *testing.T) {
					assert.Equal(t, "kv 1.0.0\n", kv.Run(t, "version").OK().Stdout)
				})
			})

			f.Scenario("unknown-command", "an unknown command is a usage error", func(s *signet.Scenario) {
				var res *signet.Result
				s.When("kv runs an unknown command", func(t testing.TB) {
					res = kv.Run(t, "frobnicate")
				}).
					Then("it exits 2", func(t *testing.T) { res.In(t).Exit(2) }).
					Then("stderr names the command and shows usage", func(t *testing.T) {
						res.In(t).StderrContains(`unknown command "frobnicate"`).StderrContains("usage:")
					}).
					Then("stdout stays empty", func(t *testing.T) { assert.Empty(t, res.Stdout) })
			})

			f.Scenario("server-unreachable", "a client without a server reports it and exits 3", func(s *signet.Scenario) {
				var url string
				var res *signet.Result
				s.Given("KV_SERVER points at a port nobody listens on", func(t testing.TB) {
					url = fmt.Sprintf("http://127.0.0.1:%d", signet.FreePort(t))
				})
				s.When("the client reads a key", func(t testing.TB) {
					res = kv.Cmd(t, "get", "anything").Env("KV_SERVER=" + url).Run()
				}).
					Then("it exits 3", func(t *testing.T) { res.In(t).Exit(3) }).
					Then("stderr says the server cannot be reached", func(t *testing.T) {
						res.In(t).StderrContains("kv: cannot reach server")
					})
			})

			f.Scenario("build-info", "the binary under test is built from this module", func(s *signet.Scenario) {
				s.Then("go version -m shows the kv main package", func(t *testing.T) {
					out := signet.Cmd(t, "go", "version", "-m", kv.Path(t)).Run().OK().Stdout
					require.Contains(t, out, "github.com/kgatilin/signet/examples/cmd/kv")
				})
			})
		})
}
