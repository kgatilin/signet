package acceptance

import (
	"testing"

	"github.com/kgatilin/signet"
	"github.com/stretchr/testify/assert"
)

// TestServerPerScenario shows Background: every scenario gets its own server,
// started with the scenario's t and stopped when the scenario ends.
func TestServerPerScenario(t *testing.T) {
	signet.Feature(t, "Server per scenario",
		"Each scenario starts from an empty store, whatever earlier scenarios wrote.",
		func(f *signet.F) {
			var srv *server
			f.Background("a fresh kv server", func(t testing.TB) { srv = startServer(t) })

			for _, sc := range []struct{ id, key string }{{"first-write", "alpha"}, {"second-write", "beta"}} {
				f.Scenario(sc.id, "a scenario that writes "+sc.key+" starts empty", func(s *signet.Scenario) {
					s.Then("the store has no keys", func(t *testing.T) {
						assert.Empty(t, srv.run(t, "list").OK().Stdout)
					})
					s.When("a client puts "+sc.key+" and it is applied", func(t *testing.T) {
						srv.run(t, "put", sc.key, "1").OK()
						srv.awaitApplied(t, "put", sc.key, 1)
					}).
						Then("list shows only "+sc.key, func(t *testing.T) {
							assert.Equal(t, sc.key+"\n", srv.run(t, "list").OK().Stdout)
						})
				})
			}
		})
}
