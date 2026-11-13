package signet

import (
	"errors"
	"fmt"
	"net"
	"net/http"
	"os"
	"os/exec"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/kgatilin/signet/internal/proc"
)

const (
	defaultReadyTimeout = 10 * time.Second
	readyPollInterval   = 100 * time.Millisecond
	readyProbeTimeout   = time.Second
	serviceStopTimeout  = 5 * time.Second
)

// ServiceBuilder is a background service prepared by Service or Subj.Service.
// Configure it, then call Start.
type ServiceBuilder struct {
	t       testing.TB
	name    string
	command string
	args    []string
	env     []string
	dir     string

	readyURL     string
	readyStatus  int
	readyLog     string
	readyTimeout time.Duration
}

// Service prepares a background process. name labels it in failure output.
func Service(t testing.TB, name, command string, args ...string) *ServiceBuilder {
	return &ServiceBuilder{t: t, name: name, command: command, args: args, readyTimeout: defaultReadyTimeout}
}

// Env adds KEY=VALUE pairs on top of the test process environment.
func (b *ServiceBuilder) Env(kv ...string) *ServiceBuilder { b.env = append(b.env, kv...); return b }

// Dir sets the working directory; the default is the test's working directory.
func (b *ServiceBuilder) Dir(dir string) *ServiceBuilder { b.dir = dir; return b }

// ReadyHTTP makes Start wait until GET url answers with status; 0 accepts any
// HTTP response.
func (b *ServiceBuilder) ReadyHTTP(url string, status int) *ServiceBuilder {
	b.readyURL, b.readyStatus = url, status
	return b
}

// ReadyLog makes Start wait until the combined output contains substring.
// With both ReadyHTTP and ReadyLog, both must hold.
func (b *ServiceBuilder) ReadyLog(substring string) *ServiceBuilder { b.readyLog = substring; return b }

// ReadyTimeout bounds the wait for readiness; the default is 10s.
func (b *ServiceBuilder) ReadyTimeout(d time.Duration) *ServiceBuilder { b.readyTimeout = d; return b }

// Start starts the process in its own process group and waits until it is
// ready. The service is stopped (SIGTERM, then SIGKILL after 5s) when the test
// that started it ends; if that test failed, the service output is logged.
func (b *ServiceBuilder) Start() *Svc {
	b.t.Helper()
	svc, err := b.start()
	if svc != nil {
		t := b.t
		t.Cleanup(func() {
			svc.Stop()
			if t.Failed() {
				t.Logf("signet: service %s output:\n%s", svc.name, indent(tail(svc.Log())))
			}
		})
	}
	if err != nil {
		b.t.Fatal(err)
	}
	return svc
}

func (b *ServiceBuilder) start() (*Svc, error) {
	cmd := exec.Command(b.command, b.args...)
	if len(b.env) > 0 {
		cmd.Env = append(os.Environ(), b.env...)
	}
	cmd.Dir = b.dir
	p, err := proc.Start(cmd)
	if err != nil {
		return nil, fmt.Errorf("signet: service %s: %w", b.name, err)
	}
	svc := &Svc{name: b.name, process: p}

	if b.readyURL == "" && b.readyLog == "" {
		if p.Exited() {
			return svc, fmt.Errorf("signet: service %s exited at start: %v", b.name, p.WaitErr())
		}
		return svc, nil
	}
	client := &http.Client{Timeout: readyProbeTimeout}
	err = p.WaitReady(b.readyTimeout, readyPollInterval, func() bool {
		if b.readyLog != "" && !strings.Contains(p.Output.String(), b.readyLog) {
			return false
		}
		return b.readyURL == "" || b.httpReady(client)
	})
	switch {
	case errors.Is(err, proc.ErrExited):
		return svc, fmt.Errorf("signet: service %s exited before it was ready (%v); %s", b.name, p.WaitErr(), b.readiness())
	case err != nil:
		return svc, fmt.Errorf("signet: service %s not ready after %s; %s", b.name, b.readyTimeout, b.readiness())
	}
	return svc, nil
}

func (b *ServiceBuilder) httpReady(client *http.Client) bool {
	resp, err := client.Get(b.readyURL)
	if err != nil {
		return false
	}
	resp.Body.Close()
	return b.readyStatus == 0 || resp.StatusCode == b.readyStatus
}

func (b *ServiceBuilder) readiness() string {
	var parts []string
	if b.readyURL != "" {
		want := "any status"
		if b.readyStatus != 0 {
			want = fmt.Sprintf("status %d", b.readyStatus)
		}
		parts = append(parts, fmt.Sprintf("waiting for GET %s to answer %s", b.readyURL, want))
	}
	if b.readyLog != "" {
		parts = append(parts, fmt.Sprintf("waiting for %q in the output", b.readyLog))
	}
	return strings.Join(parts, " and ")
}

// Svc is a started background service.
type Svc struct {
	name    string
	process *proc.Process
	stop    sync.Once
}

// Name is the label given to Service.
func (s *Svc) Name() string { return s.name }

// Log returns the combined stdout and stderr written so far.
func (s *Svc) Log() string { return s.process.Output.String() }

// Stop stops the service now; the cleanup registered by Start then does
// nothing. It is safe to call more than once.
func (s *Svc) Stop() {
	s.stop.Do(func() { s.process.Stop(serviceStopTimeout) })
}

// FreePort returns a TCP port on 127.0.0.1 that was free at the time of the
// call. Another process can take it before the service binds it.
func FreePort(t testing.TB) int {
	t.Helper()
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("signet: free port: %v", err)
	}
	defer l.Close()
	return l.Addr().(*net.TCPAddr).Port
}
