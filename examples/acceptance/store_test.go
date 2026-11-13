package acceptance

import (
	"testing"

	"github.com/kgatilin/signet"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestStore shows one server shared by all scenarios (BackgroundOnce), waiting
// in When until the asynchronous write settles, several When blocks in one
// scenario, a Given precondition, and stdin input.
func TestStore(t *testing.T) {
	signet.Feature(t, "Key-value store",
		"Writes are accepted at once and applied by a background worker after a delay; reads see applied values only.",
		func(f *signet.F) {
			var srv *server
			f.BackgroundOnce("a kv server applying writes after 300ms", func(t testing.TB) {
				srv = startServer(t)
			})

			f.Scenario("put-then-get", "an accepted write becomes readable once applied", func(s *signet.Scenario) {
				var put *signet.Result
				s.When("a client puts greeting=hello and the write is applied", func(t *testing.T) {
					put = srv.run(t, "put", "greeting", "hello").OK()
					srv.awaitApplied(t, "put", "greeting", 1)
				}).
					Then("the put was acknowledged as accepted", func(t *testing.T) {
						assert.Equal(t, "accepted put greeting\n", put.Stdout)
					}).
					Then("get returns hello", func(t *testing.T) {
						assert.Equal(t, "hello\n", srv.run(t, "get", "greeting").OK().Stdout)
					}).
					Then("the request is logged before it is applied", func(t *testing.T) {
						requested := filter(srv.events(t), "put.requested", "greeting")
						applied := filter(srv.events(t), "put.applied", "greeting")
						require.Len(t, requested, 1)
						require.Len(t, applied, 1)
						assert.Less(t, requested[0].Seq, applied[0].Seq)
					})
			})

			f.Scenario("missing-key", "reading a key nobody wrote fails with the key in the message", func(s *signet.Scenario) {
				var res *signet.Result
				s.When("a client gets a key nobody wrote", func(t *testing.T) {
					res = srv.run(t, "get", "nobody")
				}).
					Then("it exits 1", func(t *testing.T) { res.In(t).Exit(1) }).
					Then("stderr names the key", func(t *testing.T) { res.In(t).StderrContains(`key "nobody" not found`) })
			})

			f.Scenario("overwrite", "a second write replaces the first", func(s *signet.Scenario) {
				s.When("a client puts colour=red and it is applied", func(t *testing.T) {
					srv.run(t, "put", "colour", "red").OK()
					srv.awaitApplied(t, "put", "colour", 1)
				}).
					Then("get returns red", func(t *testing.T) {
						assert.Equal(t, "red\n", srv.run(t, "get", "colour").OK().Stdout)
					})
				s.When("a client puts colour=blue and it is applied", func(t *testing.T) {
					srv.run(t, "put", "colour", "blue").OK()
					srv.awaitApplied(t, "put", "colour", 2)
				}).
					Then("get returns blue", func(t *testing.T) {
						assert.Equal(t, "blue\n", srv.run(t, "get", "colour").OK().Stdout)
					}).
					Then("both writes are in the event log in order", func(t *testing.T) {
						applied := filter(srv.events(t), "put.applied", "colour")
						require.Len(t, applied, 2)
						assert.Equal(t, []string{"red", "blue"}, []string{applied[0].Value, applied[1].Value})
					})
			})

			f.Scenario("delete", "a deleted key is gone once the delete is applied", func(s *signet.Scenario) {
				s.Given("shape=circle is applied", func(t *testing.T) {
					srv.run(t, "put", "shape", "circle").OK()
					srv.awaitApplied(t, "put", "shape", 1)
				})
				s.When("a client deletes shape and the delete is applied", func(t *testing.T) {
					srv.run(t, "del", "shape").OK().StdoutContains("accepted del shape")
					srv.awaitApplied(t, "del", "shape", 1)
				}).
					Then("get exits 1", func(t *testing.T) { srv.run(t, "get", "shape").Exit(1) }).
					Then("list does not show shape", func(t *testing.T) {
						keys := signet.JSON[struct{ Keys []string }](srv.run(t, "list", "--json").OK()).Keys
						assert.NotContains(t, keys, "shape")
					})
			})

			f.Scenario("value-from-stdin", "put reads a multi-line value from stdin", func(s *signet.Scenario) {
				s.When("a client pipes two lines into put note", func(t *testing.T) {
					srv.cmd(t, "put", "note", "-").Stdin("line one\nline two").Run().OK()
					srv.awaitApplied(t, "put", "note", 1)
				}).
					Then("get returns both lines", func(t *testing.T) {
						assert.Equal(t, "line one\nline two\n", srv.run(t, "get", "note").OK().Stdout)
					})
			})
		})
}
