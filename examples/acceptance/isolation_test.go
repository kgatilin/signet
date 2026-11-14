package acceptance

import (
	"testing"

	"github.com/kgatilin/signet"
	"github.com/stretchr/testify/assert"
)

// TestServerPerScenario shows Background: every scenario gets its own server,
// stopped when the scenario ends. The last scenario starts a second server in
// a Given; it lives until the scenario ends as well, because Given and When
// receive a testing.TB that belongs to the scenario.
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
					s.When("a client puts "+sc.key+" and it is applied", func(t testing.TB) {
						srv.run(t, "put", sc.key, "1").OK()
						srv.awaitApplied(t, "put", sc.key, 1)
					}).
						Then("list shows only "+sc.key, func(t *testing.T) {
							assert.Equal(t, sc.key+"\n", srv.run(t, "list").OK().Stdout)
						})
				})
			}

			f.Scenario("second-server", "a server started in a Given serves the rest of the scenario", func(s *signet.Scenario) {
				var other *server
				s.Given("a second kv server", func(t testing.TB) { other = startServer(t) })
				s.When("a client puts gamma on the second server and it is applied", func(t testing.TB) {
					other.run(t, "put", "gamma", "1").OK()
					other.awaitApplied(t, "put", "gamma", 1)
				}).
					Then("the second server lists gamma", func(t *testing.T) {
						assert.Equal(t, "gamma\n", other.run(t, "list").OK().Stdout)
					}).
					Then("the first server has no keys", func(t *testing.T) {
						assert.Empty(t, srv.run(t, "list").OK().Stdout)
					})
			})
		})
}
