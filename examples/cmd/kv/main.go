// Command kv is a small key-value server and its client, used as the system
// under test by the signet SDK examples. Writes are accepted at once and
// applied by a background worker after a delay, so tests have to wait for a
// settled state.
package main

import (
	"bytes"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"os/signal"
	"slices"
	"strings"
	"sync"
	"syscall"
	"time"
)

const version = "1.0.0"

func main() {
	os.Exit(run(os.Args[1:], os.Stdin, os.Stdout, os.Stderr))
}

const usage = `usage:
  kv version [--json]
  kv serve --addr <host:port> [--apply-delay <duration>]
  kv put <key> [<value>|-]     (value from stdin when omitted or -)
  kv get <key>
  kv del <key>
  kv list [--json]
The client commands talk to $KV_SERVER.
`

func run(args []string, stdin io.Reader, stdout, stderr io.Writer) int {
	if len(args) == 0 {
		fmt.Fprint(stderr, usage)
		return 2
	}
	cmd, args := args[0], args[1:]
	switch cmd {
	case "version":
		fs := flag.NewFlagSet("version", flag.ContinueOnError)
		asJSON := fs.Bool("json", false, "print JSON")
		if fs.Parse(args) != nil {
			return 2
		}
		if *asJSON {
			json.NewEncoder(stdout).Encode(map[string]string{"name": "kv", "version": version})
		} else {
			fmt.Fprintln(stdout, "kv", version)
		}
		return 0
	case "serve":
		fs := flag.NewFlagSet("serve", flag.ContinueOnError)
		addr := fs.String("addr", "127.0.0.1:8080", "listen address")
		delay := fs.Duration("apply-delay", 200*time.Millisecond, "delay before a write is applied")
		if fs.Parse(args) != nil {
			return 2
		}
		if err := serve(*addr, *delay, stdout); err != nil {
			fmt.Fprintln(stderr, "kv:", err)
			return 1
		}
		return 0
	case "put", "get", "del", "list":
		return client(cmd, args, stdin, stdout, stderr)
	default:
		fmt.Fprintf(stderr, "kv: unknown command %q\n%s", cmd, usage)
		return 2
	}
}

// Event is one entry of the server's event log.
type Event struct {
	Seq   int    `json:"seq"`
	Type  string `json:"type"` // put.requested, put.applied, del.requested, del.applied
	Key   string `json:"key"`
	Value string `json:"value,omitempty"`
}

type store struct {
	mu     sync.Mutex
	values map[string]string
	events []Event
	delay  time.Duration
}

func (s *store) record(typ, key, value string) {
	s.events = append(s.events, Event{Seq: len(s.events) + 1, Type: typ, Key: key, Value: value})
}

// write records the request now and applies it after the delay.
func (s *store) write(op, key, value string) {
	s.mu.Lock()
	s.record(op+".requested", key, value)
	s.mu.Unlock()
	time.AfterFunc(s.delay, func() {
		s.mu.Lock()
		defer s.mu.Unlock()
		if op == "put" {
			s.values[key] = value
		} else {
			delete(s.values, key)
		}
		s.record(op+".applied", key, value)
	})
}

func serve(addr string, delay time.Duration, stdout io.Writer) error {
	s := &store{values: map[string]string{}, delay: delay}
	mux := http.NewServeMux()
	mux.HandleFunc("GET /healthz", func(w http.ResponseWriter, r *http.Request) { fmt.Fprintln(w, "ok") })
	mux.HandleFunc("GET /keys", func(w http.ResponseWriter, r *http.Request) {
		s.mu.Lock()
		keys := make([]string, 0, len(s.values))
		for k := range s.values {
			keys = append(keys, k)
		}
		s.mu.Unlock()
		slices.Sort(keys)
		writeJSON(w, http.StatusOK, map[string]any{"keys": keys})
	})
	mux.HandleFunc("GET /keys/{key}", func(w http.ResponseWriter, r *http.Request) {
		key := r.PathValue("key")
		s.mu.Lock()
		value, ok := s.values[key]
		s.mu.Unlock()
		if !ok {
			writeJSON(w, http.StatusNotFound, map[string]string{"error": fmt.Sprintf("key %q not found", key)})
			return
		}
		writeJSON(w, http.StatusOK, map[string]string{"key": key, "value": value})
	})
	mux.HandleFunc("PUT /keys/{key}", func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		s.write("put", r.PathValue("key"), string(body))
		writeJSON(w, http.StatusAccepted, map[string]string{"key": r.PathValue("key")})
	})
	mux.HandleFunc("DELETE /keys/{key}", func(w http.ResponseWriter, r *http.Request) {
		s.write("del", r.PathValue("key"), "")
		writeJSON(w, http.StatusAccepted, map[string]string{"key": r.PathValue("key")})
	})
	mux.HandleFunc("GET /events", func(w http.ResponseWriter, r *http.Request) {
		s.mu.Lock()
		events := slices.Clone(s.events)
		s.mu.Unlock()
		writeJSON(w, http.StatusOK, map[string]any{"events": events})
	})

	l, err := net.Listen("tcp", addr)
	if err != nil {
		return err
	}
	srv := &http.Server{Handler: mux}
	stop := make(chan os.Signal, 1)
	signal.Notify(stop, syscall.SIGTERM, os.Interrupt)
	go func() {
		<-stop
		srv.Close()
	}()
	fmt.Fprintln(stdout, "kv listening on", l.Addr())
	if err := srv.Serve(l); !errors.Is(err, http.ErrServerClosed) {
		return err
	}
	fmt.Fprintln(stdout, "kv stopped")
	return nil
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	json.NewEncoder(w).Encode(v)
}

func client(cmd string, args []string, stdin io.Reader, stdout, stderr io.Writer) int {
	server := strings.TrimRight(os.Getenv("KV_SERVER"), "/")
	if server == "" {
		fmt.Fprintln(stderr, "kv: KV_SERVER is not set")
		return 2
	}
	asJSON := len(args) > 0 && args[len(args)-1] == "--json"
	if asJSON {
		args = args[:len(args)-1]
	}

	var method, path string
	var body io.Reader
	switch {
	case cmd == "list" && len(args) == 0:
		method, path = http.MethodGet, "/keys"
	case cmd == "get" && len(args) == 1:
		method, path = http.MethodGet, "/keys/"+args[0]
	case cmd == "del" && len(args) == 1:
		method, path = http.MethodDelete, "/keys/"+args[0]
	case cmd == "put" && (len(args) == 1 || len(args) == 2):
		method, path = http.MethodPut, "/keys/"+args[0]
		if len(args) == 2 && args[1] != "-" {
			body = strings.NewReader(args[1])
		} else {
			data, _ := io.ReadAll(stdin)
			body = bytes.NewReader(data)
		}
	default:
		fmt.Fprintf(stderr, "kv: wrong arguments for %s\n%s", cmd, usage)
		return 2
	}

	req, _ := http.NewRequest(method, server+path, body)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		fmt.Fprintln(stderr, "kv: cannot reach server:", err)
		return 3
	}
	defer resp.Body.Close()
	data, _ := io.ReadAll(resp.Body)

	if resp.StatusCode >= 300 {
		var failure struct{ Error string }
		json.Unmarshal(data, &failure)
		fmt.Fprintln(stderr, "kv:", failure.Error)
		return 1
	}
	if asJSON {
		stdout.Write(data)
		return 0
	}
	switch cmd {
	case "put", "del":
		fmt.Fprintf(stdout, "accepted %s %s\n", cmd, args[0])
	case "get":
		var kv struct{ Value string }
		json.Unmarshal(data, &kv)
		fmt.Fprintln(stdout, kv.Value)
	case "list":
		var list struct{ Keys []string }
		json.Unmarshal(data, &list)
		for _, k := range list.Keys {
			fmt.Fprintln(stdout, k)
		}
	}
	return 0
}
