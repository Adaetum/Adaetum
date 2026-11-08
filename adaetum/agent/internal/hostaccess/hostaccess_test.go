package hostaccess

import (
	"bytes"
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
)

type fakeExec struct {
	mu     sync.Mutex
	calls  []string
	gate   chan struct{}
	output string
	exit   int
	err    error
}

func (f *fakeExec) Run(ctx context.Context, host, command string, out io.Writer) (int, error) {
	f.mu.Lock()
	f.calls = append(f.calls, host+": "+command)
	f.mu.Unlock()
	if f.gate != nil {
		select {
		case <-f.gate:
		case <-ctx.Done():
			return -1, nil
		}
	}
	io.WriteString(out, f.output)
	return f.exit, f.err
}

type record struct {
	mu      sync.Mutex
	entries []AuditEntry
	failOn  string
}

func (r *record) add(e AuditEntry) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.failOn == e.Event {
		return errors.New("disk full")
	}
	r.entries = append(r.entries, e)
	return nil
}

func manager(f *fakeExec, r *record, allowed []string, allowedErr error) *Manager {
	return &Manager{
		Exec:    f,
		Audit:   r.add,
		Allowed: func(context.Context) ([]string, error) { return allowed, allowedErr },
	}
}

func TestACommandOnAnAllowedServerRunsAndIsRecordedBeforeAndAfter(t *testing.T) {
	f, r := &fakeExec{output: "hello\n", exit: 0}, &record{}
	m := manager(f, r, []string{"node-a", "node-b"}, nil)
	result, err := m.Run(context.Background(), Request{Host: "node-a", Command: "uptime"}, "operator")
	if err != nil || result.ExitCode != 0 || result.Output != "hello\n" {
		t.Fatalf("%+v %v", result, err)
	}
	if len(f.calls) != 1 || f.calls[0] != "node-a: uptime" {
		t.Fatalf("unexpected calls: %v", f.calls)
	}
	if len(r.entries) != 2 || r.entries[0].Event != "start" || r.entries[1].Event != "finish" || r.entries[0].Command != "uptime" || r.entries[0].By != "operator" {
		t.Fatalf("a start and a finish must be recorded: %+v", r.entries)
	}
}

func TestACommandThatCannotBeRecordedIsNotRun(t *testing.T) {
	f, r := &fakeExec{}, &record{failOn: "start"}
	m := manager(f, r, []string{"node-a"}, nil)
	if _, err := m.Run(context.Background(), Request{Host: "node-a", Command: "id"}, "o"); err == nil {
		t.Fatal("an unrecorded command is exactly what the record exists to prevent")
	}
	if len(f.calls) != 0 {
		t.Fatal("nothing may run")
	}
}

func TestWithNoRecordConfiguredNothingRuns(t *testing.T) {
	f := &fakeExec{}
	m := &Manager{Exec: f, Allowed: func(context.Context) ([]string, error) { return []string{"a"}, nil }}
	if _, err := m.Run(context.Background(), Request{Host: "a", Command: "id"}, "o"); err == nil || len(f.calls) != 0 {
		t.Fatal("a manager without a record must refuse")
	}
}

func TestOnlyServersInTheAllowedSetAreReachable(t *testing.T) {
	f := &fakeExec{}
	m := manager(f, &record{}, []string{"node-a"}, nil)
	for _, host := range []string{"node-b", "evil.example.com", "NODE-A.other", "10.0.0.1", "localhost"} {
		if _, err := m.Run(context.Background(), Request{Host: host, Command: "id"}, "o"); err == nil {
			t.Errorf("%q is not an allowed server", host)
		}
	}
	if len(f.calls) != 0 {
		t.Fatalf("nothing may run on a server that is not allowed: %v", f.calls)
	}
	if _, err := m.Run(context.Background(), Request{Host: "NODE-A", Command: "id"}, "o"); err != nil {
		t.Fatalf("names compare without regard to case: %v", err)
	}
}

func TestWhenTheAllowedSetCannotBeReadNothingRuns(t *testing.T) {
	f := &fakeExec{}
	m := manager(f, &record{}, nil, errors.New("kubernetes unreachable"))
	_, err := m.Run(context.Background(), Request{Host: "node-a", Command: "id"}, "o")
	if err == nil || !strings.Contains(err.Error(), "nothing was run") || len(f.calls) != 0 {
		t.Fatalf("an unknown allowed set must mean none: %v", err)
	}
}

func TestHostNamesCannotCarryOptionsUsersOrPatterns(t *testing.T) {
	f := &fakeExec{}
	// Even if one of these were in the allowed set, it must not pass the name check:
	// a name beginning with "-" would be read by ssh as an option.
	hosts := []string{"-oProxyCommand=id", "root@node-a", "node-a;id", "node a", "node-a,node-b", "$(id)", "", "-l", "node/a"}
	m := manager(f, &record{}, hosts, nil)
	for _, host := range hosts {
		if _, err := m.Run(context.Background(), Request{Host: host, Command: "id"}, "o"); err == nil {
			t.Errorf("%q must be refused", host)
		}
	}
	if len(f.calls) != 0 {
		t.Fatal("nothing may run")
	}
}

func TestCommandLimits(t *testing.T) {
	f := &fakeExec{}
	m := manager(f, &record{}, []string{"a"}, nil)
	for _, command := range []string{"", "   ", strings.Repeat("x", maxCommandLength+1), "id\x00"} {
		if _, err := m.Run(context.Background(), Request{Host: "a", Command: command}, "o"); err == nil {
			t.Errorf("%q must be refused", command[:min(len(command), 10)])
		}
	}
	if _, err := m.Run(context.Background(), Request{Host: "a", Command: "id", TimeoutSeconds: 601}, "o"); err == nil {
		t.Error("a command may not run for more than ten minutes")
	}
}

func TestOneCommandAtATime(t *testing.T) {
	f := &fakeExec{gate: make(chan struct{})}
	m := manager(f, &record{}, []string{"a"}, nil)
	done := make(chan struct{})
	go func() { m.Run(context.Background(), Request{Host: "a", Command: "sleep 1"}, "o"); close(done) }()
	for i := 0; i < 100; i++ {
		f.mu.Lock()
		n := len(f.calls)
		f.mu.Unlock()
		if n == 1 {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	if _, err := m.Run(context.Background(), Request{Host: "a", Command: "id"}, "o"); !errors.Is(err, ErrBusy) {
		t.Fatalf("a second command must be refused while one runs: %v", err)
	}
	close(f.gate)
	<-done
}

func TestOutputIsCappedAndTheRecordKeepsOnlyItsSize(t *testing.T) {
	f, r := &fakeExec{output: strings.Repeat("A", maxOutput+500)}, &record{}
	m := manager(f, r, []string{"a"}, nil)
	result, _ := m.Run(context.Background(), Request{Host: "a", Command: "cat big"}, "o")
	if len(result.Output) != maxOutput || !result.Truncated {
		t.Fatalf("output must be capped: %d %v", len(result.Output), result.Truncated)
	}
	if r.entries[1].OutputSize != maxOutput+500 {
		t.Fatalf("the record notes how much there was: %+v", r.entries[1])
	}
	for _, e := range r.entries {
		if strings.Contains(e.Command, "AAAA") {
			t.Fatal("output must not be recorded, only its size")
		}
	}
}

func TestATimedOutCommandIsMarkedAndRecorded(t *testing.T) {
	f, r := &fakeExec{gate: make(chan struct{})}, &record{}
	m := manager(f, r, []string{"a"}, nil)
	result, err := m.Run(context.Background(), Request{Host: "a", Command: "sleep 99", TimeoutSeconds: 1}, "o")
	if err != nil || !result.TimedOut {
		t.Fatalf("expected a timeout: %+v %v", result, err)
	}
	if !r.entries[1].TimedOut {
		t.Fatalf("the timeout must be recorded: %+v", r.entries[1])
	}
}

func TestSSHExecUsesTheSafeOptionsAndAnEndOfOptionsMarker(t *testing.T) {
	dir := t.TempDir()
	script := "#!/bin/sh\necho \"$@\"\nexit 7\n"
	if err := os.WriteFile(filepath.Join(dir, "ssh"), []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", dir+string(os.PathListSeparator)+os.Getenv("PATH"))

	var out bytes.Buffer
	code, err := SSHExec{User: "ansible"}.Run(context.Background(), "node-a", "uptime; id", &out)
	if err != nil || code != 7 {
		t.Fatalf("the remote exit code passes through: %d %v", code, err)
	}
	line := out.String()
	for _, want := range []string{"BatchMode=yes", "ConnectTimeout=10", "-l ansible", "-- node-a uptime; id"} {
		if !strings.Contains(line, want) {
			t.Errorf("the ssh arguments are missing %q: %s", want, line)
		}
	}
	if strings.Index(line, "--") > strings.Index(line, "node-a") {
		t.Fatalf("options must end before the host, so a host can never be read as an option: %s", line)
	}
}

func TestTheAPIRefusesWhenOffAndNeedsALoginWhenOn(t *testing.T) {
	off := &API{Capabilities: Capabilities{Reason: OffReason}}
	mux := http.NewServeMux()
	off.Register(mux)
	server := httptest.NewServer(mux)
	defer server.Close()
	response, _ := http.Post(server.URL+"/api/v1/host-access/commands", "application/json", strings.NewReader(`{"host":"a","command":"id"}`))
	if response.StatusCode != http.StatusForbidden {
		t.Fatalf("an off override must refuse: %d", response.StatusCode)
	}
	body, _ := io.ReadAll(response.Body)
	if !strings.Contains(string(body), "-allow-host-ssh") {
		t.Fatalf("the refusal must say how to turn it on deliberately: %s", body)
	}

	on := &API{
		Manager:      manager(&fakeExec{output: "x"}, &record{}, []string{"a"}, nil),
		Capabilities: Capabilities{Enabled: true},
		RequireUser: func(next http.Handler) http.Handler {
			return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.Header.Get("Authorization") != "Bearer ok" {
					http.Error(w, "login", http.StatusUnauthorized)
					return
				}
				next.ServeHTTP(w, r)
			})
		},
	}
	mux2 := http.NewServeMux()
	on.Register(mux2)
	server2 := httptest.NewServer(mux2)
	defer server2.Close()
	if r, _ := http.Post(server2.URL+"/api/v1/host-access/commands", "application/json", strings.NewReader(`{"host":"a","command":"id"}`)); r.StatusCode != http.StatusUnauthorized {
		t.Fatalf("running a command needs a login: %d", r.StatusCode)
	}
	request, _ := http.NewRequest("POST", server2.URL+"/api/v1/host-access/commands", strings.NewReader(`{"host":"a","command":"id"}`))
	request.Header.Set("Authorization", "Bearer ok")
	if r, _ := http.DefaultClient.Do(request); r.StatusCode != http.StatusOK {
		t.Fatalf("a signed-in request should run: %d", r.StatusCode)
	}
	if r, _ := http.Get(server2.URL + "/api/v1/host-access/capabilities"); r.StatusCode != http.StatusOK {
		t.Fatal("capabilities describe the feature and need no login")
	}
}
