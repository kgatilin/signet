// Package sdk holds features run by the SDK's own tests through
// `go test -json`. Several of them fail on purpose.
package sdk

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/kgatilin/signet"
)

// mark appends a line to $SIGNET_FIXTURE_MARKS so the driver can see which
// closures executed.
func mark(t testing.TB, what string) {
	path := os.Getenv("SIGNET_FIXTURE_MARKS")
	if path == "" {
		return
	}
	f, err := os.OpenFile(path, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	f.WriteString(what + "\n")
}

func TestPassing(t *testing.T) {
	signet.Feature(t, "Passing feature", "First line.\nSecond line.", func(f *signet.F) {
		var shared string
		onceRuns := 0

		f.BackgroundOnce("a shared file", func(t testing.TB) {
			mark(t, "once")
			onceRuns++
			shared = filepath.Join(t.TempDir(), "shared")
			if err := os.WriteFile(shared, []byte("x"), 0o644); err != nil {
				t.Fatal(err)
			}
		})
		var scratch string
		f.Background("a fresh counter", func(t testing.TB) {
			mark(t, "each")
			scratch = filepath.Join(t.TempDir(), "scratch")
			if err := os.WriteFile(scratch, nil, 0o644); err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() { mark(t, "each-cleanup") })
		})

		f.Scenario("one", "every step kind passes", func(s *signet.Scenario) {
			var n int
			s.Given("n is one", func(t *testing.T) { mark(t, "one/given"); n = 1 })
			s.When("n doubles", func(t *testing.T) { mark(t, "one/when"); n *= 2 }).
				Then("n is two", func(t *testing.T) {
					if n != 2 {
						t.Fatalf("n = %d", n)
					}
				}).
				Then("n is even", func(t *testing.T) {
					if n%2 != 0 {
						t.Fatal("odd")
					}
				})
			s.Then("the shared file exists", func(t *testing.T) {
				if _, err := os.Stat(shared); err != nil {
					t.Fatal(err)
				}
			})
			s.Then("the per-scenario file outlives its background step", func(t *testing.T) {
				if _, err := os.Stat(scratch); err != nil {
					t.Fatal(err)
				}
				mark(t, "one/scratch-checked")
			})
		})

		f.Scenario("two", "the shared background ran once", func(s *signet.Scenario) {
			s.When("nothing happens", func(t *testing.T) { mark(t, "two/when") }).
				Then("the shared file still exists", func(t *testing.T) {
					if _, err := os.Stat(shared); err != nil {
						t.Fatal(err)
					}
				})
			s.When("it is checked again", func(t *testing.T) {}).
				Then("the background ran once", func(t *testing.T) {
					if onceRuns != 1 {
						t.Fatalf("BackgroundOnce ran %d times", onceRuns)
					}
				})
		})
	})
}

func TestStepFailures(t *testing.T) {
	signet.Feature(t, "Step failures", "", func(f *signet.F) {
		f.Scenario("when-fails", "a failed action skips its checks and later actions", func(s *signet.Scenario) {
			s.When("the action fails", func(t *testing.T) { t.Fatal("action broke\nsecond line") }).
				Then("its check", func(t *testing.T) { mark(t, "when-fails/then") })
			s.When("a later action", func(t *testing.T) { mark(t, "when-fails/later") }).
				Then("a later check", func(t *testing.T) { mark(t, "when-fails/later-then") })
		})

		f.Scenario("then-fails", "a failed check keeps its siblings and skips later actions", func(s *signet.Scenario) {
			s.When("the action passes", func(t *testing.T) {}).
				Then("the first check fails", func(t *testing.T) { t.Error("first check broke") }).
				Then("the second check runs", func(t *testing.T) { mark(t, "then-fails/second") })
			s.When("a later action", func(t *testing.T) { mark(t, "then-fails/later") })
		})

		f.Scenario("given-fails", "a failed precondition skips everything after it", func(s *signet.Scenario) {
			s.Given("a broken precondition", func(t *testing.T) { t.Fatal("precondition broke") })
			s.Then("a direct check", func(t *testing.T) { mark(t, "given-fails/then") })
			s.When("an action", func(t *testing.T) { mark(t, "given-fails/when") })
		})

		f.Scenario("skip-in-when", "a skipped action skips its checks", func(s *signet.Scenario) {
			s.When("the action skips", func(t *testing.T) { t.Skip("not today") }).
				Then("its check", func(t *testing.T) { mark(t, "skip-in-when/then") })
		})
	})
}

func TestBackgroundOnceFails(t *testing.T) {
	signet.Feature(t, "Broken background", "", func(f *signet.F) {
		f.BackgroundOnce("a service that does not start", func(t testing.TB) { t.Fatal("service refused to start") })
		f.Scenario("first", "runs the background", func(s *signet.Scenario) {
			s.When("an action", func(t *testing.T) { mark(t, "bg/first") })
		})
		f.Scenario("second", "is skipped", func(s *signet.Scenario) {
			s.When("an action", func(t *testing.T) { mark(t, "bg/second") }).
				Then("a check", func(t *testing.T) { mark(t, "bg/second-then") })
		})
	})
}

func TestBackgroundFails(t *testing.T) {
	signet.Feature(t, "Broken per-scenario background", "", func(f *signet.F) {
		f.Background("a fixture that fails in one scenario", func(t testing.TB) {
			if strings.HasPrefix(t.Name(), "TestBackgroundFails/bad/") {
				t.Fatal("fixture broke")
			}
		})
		f.Scenario("bad", "its background fails", func(s *signet.Scenario) {
			s.When("an action", func(t *testing.T) { mark(t, "bgeach/bad") })
		})
		f.Scenario("good", "its background passes", func(s *signet.Scenario) {
			s.When("an action", func(t *testing.T) { mark(t, "bgeach/good") })
		})
	})
}
