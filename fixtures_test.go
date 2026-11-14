package signet

import (
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// helperEnv switches the test binary into a helper process for service tests.
const helperEnv = "SIGNET_TEST_HELPER"

func TestMain(m *testing.M) {
	if os.Getenv(helperEnv) == "http" {
		serveHelper(os.Getenv("ADDR"))
		return
	}
	os.Exit(m.Run())
}

func serveHelper(addr string) {
	http.HandleFunc("/healthz", func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(http.StatusNoContent) })
	fmt.Println("helper listening on", addr)
	if err := http.ListenAndServe(addr, nil); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

func TestModuleRootFindsGoMod(t *testing.T) {
	root, err := moduleRoot()
	if err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(root, "doc.go")); err != nil {
		t.Fatalf("module root %s is not the signet root: %v", root, err)
	}

	t.Chdir(t.TempDir())
	if _, err := moduleRoot(); err == nil {
		t.Fatal("expected an error outside any module")
	}
}

func TestSubjectBinaryEnvMustBeAbsolute(t *testing.T) {
	t.Setenv(BinaryEnv, "bin/app")
	_, err := Subject("", "bin/app").resolve()
	if err == nil || !strings.Contains(err.Error(), "must be an absolute path") {
		t.Fatalf("err = %v", err)
	}
}

func TestSubjectBinaryEnvSkipsBuild(t *testing.T) {
	bin := writeScript(t, "echo from-env")
	t.Setenv(BinaryEnv, bin)
	path, err := Subject("exit 1", "does-not-exist").resolve()
	if err != nil {
		t.Fatal(err)
	}
	if path != bin {
		t.Fatalf("path = %s, want %s", path, bin)
	}
}

func TestSubjectBuildsOnceInModuleRoot(t *testing.T) {
	t.Setenv(BinaryEnv, "")
	dir := t.TempDir()
	count := filepath.Join(dir, "count")
	bin := filepath.Join(dir, "app")
	build := fmt.Sprintf(`test -f go.mod && echo built >> %q && printf '#!/bin/sh\necho hello "$@"\n' > %q && chmod +x %q`, count, bin, bin)
	s := Subject(build, bin)

	if got := s.Path(t); got != bin {
		t.Fatalf("path = %s, want %s", got, bin)
	}
	s.Run(t, "world").OK().StdoutContains("hello world")
	s.Path(t)

	data, err := os.ReadFile(count)
	if err != nil {
		t.Fatal(err)
	}
	if n := strings.Count(string(data), "built"); n != 1 {
		t.Fatalf("build ran %d times, want 1", n)
	}
}

func TestSubjectBuildFailureKeepsOutput(t *testing.T) {
	t.Setenv(BinaryEnv, "")
	_, err := Subject("echo boom; exit 4", filepath.Join(t.TempDir(), "app")).resolve()
	if err == nil || !strings.Contains(err.Error(), "boom") || !strings.Contains(err.Error(), "exit status 4") {
		t.Fatalf("err = %v", err)
	}
}

func TestSubjectRelativePathResolvesAgainstModuleRoot(t *testing.T) {
	t.Setenv(BinaryEnv, "")
	_, err := Subject("", "bin/does-not-exist").resolve()
	root, _ := moduleRoot()
	if err == nil || !strings.Contains(err.Error(), filepath.Join(root, "bin/does-not-exist")) {
		t.Fatalf("err = %v", err)
	}
}

func TestCmdCapturesStreamsAndExitCode(t *testing.T) {
	dir := t.TempDir()
	r := Cmd(t, "/bin/sh", "-c", `echo "out $GREETING $(pwd)"; cat; echo err >&2; exit 3`).
		Env("GREETING=hi").
		Dir(dir).
		Stdin("from-stdin").
		Run()

	r.Exit(3).StderrContains("err").StdoutContains("from-stdin")
	resolved, _ := filepath.EvalSymlinks(dir)
	if !strings.Contains(r.Stdout, "out hi "+resolved) && !strings.Contains(r.Stdout, "out hi "+dir) {
		t.Fatalf("stdout = %q", r.Stdout)
	}
	if err := r.checkExit(0); err == nil || !strings.Contains(err.Error(), "exit 3, want 0") {
		t.Fatalf("checkExit(0) = %v", err)
	}
}

func TestCmdTimeoutKillsProcessGroup(t *testing.T) {
	start := time.Now()
	r := Cmd(t, "/bin/sh", "-c", "sleep 30 & sleep 30").Timeout(200 * time.Millisecond).Run()
	if !r.TimedOut || r.ExitCode != -1 {
		t.Fatalf("TimedOut=%v ExitCode=%d", r.TimedOut, r.ExitCode)
	}
	if elapsed := time.Since(start); elapsed > 5*time.Second {
		t.Fatalf("timed out command took %s", elapsed)
	}
	if err := r.checkExit(0); err == nil || !strings.Contains(err.Error(), "timed out") {
		t.Fatalf("checkExit = %v", err)
	}
}

func TestCmdThatCannotStart(t *testing.T) {
	r := Cmd(t, "/nonexistent/binary").Run()
	if r.Err == nil || r.ExitCode != -1 {
		t.Fatalf("Err=%v ExitCode=%d", r.Err, r.ExitCode)
	}
	if err := r.checkExit(0); err == nil || !strings.Contains(err.Error(), "did not run") {
		t.Fatalf("checkExit = %v", err)
	}
}

func TestCmdExitsZeroWithBackgroundChildHoldingPipe(t *testing.T) {
	// Child inherits stdout and sleeps beyond WaitDelay (2s); parent exits 0 immediately.
	r := Cmd(t, "/bin/sh", "-c", "sleep 5 & echo parent-done").Run()
	r.OK().StdoutContains("parent-done")
}

func TestJSONDecodesStdout(t *testing.T) {
	type item struct {
		Name  string `json:"name"`
		Count int    `json:"count"`
	}
	got := JSON[[]item](Cmd(t, "/bin/sh", "-c", `echo '[{"name":"a","count":2}]'`).Run().OK())
	if len(got) != 1 || got[0] != (item{Name: "a", Count: 2}) {
		t.Fatalf("got %+v", got)
	}
}

func TestResultInRebindsTest(t *testing.T) {
	r := Cmd(t, "/bin/sh", "-c", "exit 0").Run()
	t.Run("other", func(t *testing.T) {
		if bound := r.In(t); bound.t != t || r.t == t {
			t.Fatal("In did not rebind the result")
		}
	})
}

func TestHistoryDumpShowsLastCommands(t *testing.T) {
	h := &history{}
	for i := range dumpCommands + 2 {
		h.results = append(h.results, &Result{Args: []string{"app", fmt.Sprint(i)}, Stdout: "line\n"})
	}
	h.results = h.results[2:]
	dump := h.dump()
	if strings.Contains(dump, "$ app 0 ") || !strings.Contains(dump, "$ app 6 ") || !strings.Contains(dump, "stdout:\n  line") {
		t.Fatalf("dump:\n%s", dump)
	}
}

func TestServiceReadyHTTPAndLog(t *testing.T) {
	addr := fmt.Sprintf("127.0.0.1:%d", FreePort(t))
	svc := Service(t, "helper", os.Args[0], "-test.run=^$").
		Env(helperEnv+"=http", "ADDR="+addr).
		ReadyHTTP("http://"+addr+"/healthz", http.StatusNoContent).
		ReadyLog("helper listening").
		Start()

	resp, err := http.Get("http://" + addr + "/healthz")
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if !strings.Contains(svc.Log(), "helper listening on "+addr) {
		t.Fatalf("log = %q", svc.Log())
	}

	svc.Stop()
	if !svc.process.Exited() {
		t.Fatal("service still running after Stop")
	}
	svc.Stop()
}

func TestServiceExitsBeforeReady(t *testing.T) {
	svc, err := Service(t, "quitter", "/bin/sh", "-c", "echo bye; exit 3").ReadyLog("never").start()
	if err == nil || !strings.Contains(err.Error(), "exited before it was ready") {
		t.Fatalf("err = %v", err)
	}
	if !strings.Contains(svc.Log(), "bye") {
		t.Fatalf("log = %q", svc.Log())
	}
}

func TestServiceNotReadyInTime(t *testing.T) {
	svc, err := Service(t, "sleeper", "/bin/sh", "-c", "sleep 30").
		ReadyLog("ready").
		ReadyTimeout(300 * time.Millisecond).
		start()
	defer svc.Stop()
	if err == nil || !strings.Contains(err.Error(), "not ready after 300ms") || !strings.Contains(err.Error(), `waiting for "ready"`) {
		t.Fatalf("err = %v", err)
	}
}

func TestFreePort(t *testing.T) {
	if port := FreePort(t); port <= 0 {
		t.Fatalf("port = %d", port)
	}
}

func writeScript(t *testing.T, body string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "app")
	if err := os.WriteFile(path, []byte("#!/bin/sh\n"+body+"\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	return path
}
