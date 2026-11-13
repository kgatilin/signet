// Package acceptance tests the kv example with the signet SDK. Each test
// function is one feature; run `go test -json ./... | signet report` for the
// document, or set SIGNET_LIST=1 to get it without running anything.
package acceptance

import (
	"encoding/json"
	"fmt"
	"net/http"
	"testing"
	"time"

	"github.com/kgatilin/signet"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// kv is the binary under test. It is built once per test process, in the
// examples module root, the first time a step needs it.
var kv = signet.Subject("go build -o bin/kv ./cmd/kv", "bin/kv")

// server is a running kv server and the client environment pointing at it.
type server struct {
	url string
	svc *signet.Svc
}

func startServer(t testing.TB) *server {
	addr := fmt.Sprintf("127.0.0.1:%d", signet.FreePort(t))
	svc := kv.Service(t, "kv", "serve", "--addr", addr, "--apply-delay", "300ms").
		ReadyHTTP("http://"+addr+"/healthz", http.StatusOK).
		ReadyLog("kv listening").
		Start()
	return &server{url: "http://" + addr, svc: svc}
}

// cmd prepares a kv client command against this server.
func (s *server) cmd(t testing.TB, args ...string) *signet.Command {
	return kv.Cmd(t, args...).Env("KV_SERVER=" + s.url)
}

func (s *server) run(t testing.TB, args ...string) *signet.Result {
	return s.cmd(t, args...).Run()
}

type event struct {
	Seq   int    `json:"seq"`
	Type  string `json:"type"`
	Key   string `json:"key"`
	Value string `json:"value"`
}

// events reads the event log over HTTP. Failures go to c, so it works both
// inside require.EventuallyWithT and in a Then step.
func (s *server) events(c assert.TestingT) []event {
	var log struct{ Events []event }
	resp, err := http.Get(s.url + "/events")
	if !assert.NoError(c, err) {
		return nil
	}
	defer resp.Body.Close()
	assert.NoError(c, json.NewDecoder(resp.Body).Decode(&log))
	return log.Events
}

// awaitApplied waits until the event log has n applied events of op for key.
func (s *server) awaitApplied(t *testing.T, op, key string, n int) {
	require.EventuallyWithT(t, func(c *assert.CollectT) {
		assert.Len(c, filter(s.events(c), op+".applied", key), n)
	}, 5*time.Second, 50*time.Millisecond)
}

func filter(events []event, typ, key string) []event {
	var out []event
	for _, e := range events {
		if e.Type == typ && e.Key == key {
			out = append(out, e)
		}
	}
	return out
}
