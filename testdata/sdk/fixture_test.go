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
			s.Given("n is one", func(t testing.TB) { mark(t, "one/given"); n = 1 })
			s.When("n doubles", func(t testing.TB) { mark(t, "one/when"); n *= 2 }).
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
			s.When("nothing happens", func(t testing.TB) { mark(t, "two/when") }).
				Then("the shared file still exists", func(t *testing.T) {
					if _, err := os.Stat(shared); err != nil {
						t.Fatal(err)
					}
				})
			s.When("it is checked again", func(t testing.TB) {}).
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
			s.When("the action fails", func(t testing.TB) { t.Fatal("action broke\nsecond line") }).
				Then("its check", func(t *testing.T) { mark(t, "when-fails/then") })
			s.When("a later action", func(t testing.TB) { mark(t, "when-fails/later") }).
				Then("a later check", func(t *testing.T) { mark(t, "when-fails/later-then") })
		})

		f.Scenario("then-fails", "a failed check keeps its siblings and skips later actions", func(s *signet.Scenario) {
			s.When("the action passes", func(t testing.TB) {}).
				Then("the first check fails", func(t *testing.T) { t.Error("first check broke") }).
				Then("the second check runs", func(t *testing.T) { mark(t, "then-fails/second") })
			s.When("a later action", func(t testing.TB) { mark(t, "then-fails/later") })
		})

		f.Scenario("given-fails", "a failed precondition skips everything after it", func(s *signet.Scenario) {
			s.Given("a broken precondition", func(t testing.TB) { t.Fatal("precondition broke") })
			s.Then("a direct check", func(t *testing.T) { mark(t, "given-fails/then") })
			s.When("an action", func(t testing.TB) { mark(t, "given-fails/when") })
		})

		f.Scenario("skip-in-when", "a skipped action skips its checks", func(s *signet.Scenario) {
			s.When("the action skips", func(t testing.TB) { t.Skip("not today") }).
				Then("its check", func(t *testing.T) { mark(t, "skip-in-when/then") })
		})

		f.Scenario("cleanup-fails", "a failed cleanup registered in a Given fails the scenario", func(s *signet.Scenario) {
			s.Given("a resource whose teardown fails", func(t testing.TB) {
				t.Cleanup(func() { t.Error("teardown broke") })
			})
			s.Then("a check that passes", func(t *testing.T) { mark(t, "cleanup-fails/then") })
		})
	})
}

func TestBackgroundOnceFails(t *testing.T) {
	signet.Feature(t, "Broken background", "", func(f *signet.F) {
		f.BackgroundOnce("a service that does not start", func(t testing.TB) { t.Fatal("service refused to start") })
		f.Scenario("first", "runs the background", func(s *signet.Scenario) {
			s.When("an action", func(t testing.TB) { mark(t, "bg/first") })
		})
		f.Scenario("second", "is skipped", func(s *signet.Scenario) {
			s.When("an action", func(t testing.TB) { mark(t, "bg/second") }).
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
			s.When("an action", func(t testing.TB) { mark(t, "bgeach/bad") })
		})
		f.Scenario("good", "its background passes", func(s *signet.Scenario) {
			s.When("an action", func(t testing.TB) { mark(t, "bgeach/good") })
		})
	})
}

func TestFailureOutput(t *testing.T) {
	signet.Feature(t, "Failure output", "", func(f *signet.F) {
		f.BackgroundOnce("a shared service and command", func(t testing.TB) {
			signet.Cmd(t, "/bin/sh", "-c", "echo bg-once-cmd-out").Run().OK()
			signet.Service(t, "once-svc", "/bin/sh", "-c", "echo bg-once-svc-out; exec sleep 30").
				ReadyLog("bg-once-svc-out").
				Start()
		})
		f.Background("a per-scenario command", func(t testing.TB) {
			signet.Cmd(t, "/bin/sh", "-c", "echo bg-each-cmd-out").Run().OK()
		})
		f.Scenario("later-step-fails", "background fixtures log their commands and service output when a later step fails", func(s *signet.Scenario) {
			s.When("the step fails", func(t testing.TB) {
				t.Fatal("boom after background")
			})
		})
		f.Scenario("commands-in-steps", "a failed check logs its own commands, and the scenario logs those of its steps once", func(s *signet.Scenario) {
			s.Given("a command in a Given", func(t testing.TB) {
				signet.Cmd(t, "/bin/sh", "-c", "echo given-cmd-out").Run().OK()
			})
			s.When("a command in a When", func(t testing.TB) {
				signet.Cmd(t, "/bin/sh", "-c", "echo when-cmd-out").Run().OK()
			}).
				Then("a check with a command fails", func(t *testing.T) {
					signet.Cmd(t, "/bin/sh", "-c", "echo then-cmd-out; exit 3").Run().OK()
				})
		})
	})
}

func TestStepResources(t *testing.T) {
	signet.Feature(t, "Step resources", "", func(f *signet.F) {
		f.Scenario("outlive-step", "what a Given or When creates lives until the scenario ends", func(s *signet.Scenario) {
			var dir, pid string
			s.Given("a temporary directory", func(t testing.TB) {
				dir = t.TempDir()
				t.Cleanup(func() { mark(t, "resources/given-cleanup") })
			})
			s.When("a service starts", func(t testing.TB) {
				svc := signet.Service(t, "sleeper", "/bin/sh", "-c", "echo pid=$$.; exec sleep 30").
					ReadyLog(".").
					Start()
				pid = strings.TrimSuffix(strings.TrimPrefix(strings.TrimSpace(svc.Log()), "pid="), ".")
			}).
				Then("the directory of the Given still exists", func(t *testing.T) {
					if _, err := os.Stat(dir); err != nil {
						t.Fatal(err)
					}
				})
			s.When("a later action runs", func(t testing.TB) { mark(t, "resources/later-when") }).
				Then("the service of the earlier When is still running", func(t *testing.T) {
					signet.Cmd(t, "kill", "-0", pid).Run().OK()
				})
		})
	})
}
