package ansiblerun

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"os"
	"strings"
	"sync"
	"testing"
	"time"
)

type fakeExec struct {
	gate   chan struct{} // when set, Run waits for it
	run    func(ExecSpec, io.Writer) (int, error)
	mu     sync.Mutex
	specs  []ExecSpec
	varsIn []map[string]any
}

func (f *fakeExec) Run(ctx context.Context, spec ExecSpec, out io.Writer) (int, error) {
	f.mu.Lock()
	f.specs = append(f.specs, spec)
	data, _ := os.ReadFile(spec.ExtraVarsFile)
	var vars map[string]any
	_ = json.Unmarshal(data, &vars)
	f.varsIn = append(f.varsIn, vars)
	f.mu.Unlock()
	if f.gate != nil {
		select {
		case <-f.gate:
		case <-ctx.Done():
			return -1, nil
		}
	}
	if f.run != nil {
		return f.run(spec, out)
	}
	io.WriteString(out, "PLAY [x]\nok\n")
	return 0, nil
}

type auditLog struct {
	mu      sync.Mutex
	entries []AuditEntry
	failOn  string
}

func (a *auditLog) record(e AuditEntry) error {
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.failOn == e.Event {
		return errors.New("disk full")
	}
	a.entries = append(a.entries, e)
	return nil
}

func newManager(f *fakeExec, a *auditLog) *Manager {
	return NewManager(Env{Exec: f, Audit: a.record, Now: func() time.Time { return time.Date(2026, 11, 4, 12, 0, 0, 0, time.UTC) }})
}

var goodRequest = Request{Playbook: "host-agent", Limit: "node-a", Check: true, Vars: map[string]string{"host_agent_binary_src": "/x", "host_agent_join_token": "adj_topsecret1234"}}

func TestARunExecutesTheFixedCommandRecordsItAndKeepsItsOutput(t *testing.T) {
	f, a := &fakeExec{}, &auditLog{}
	m := newManager(f, a)
	id, err := m.Start(goodRequest, "operator")
	if err != nil {
		t.Fatal(err)
	}
	m.Wait()

	view, err := m.Get(id, 0)
	if err != nil || view.Status != StatusSucceeded || view.ExitCode == nil || *view.ExitCode != 0 {
		t.Fatalf("expected success: %+v %v", view, err)
	}
	if strings.Join(view.Lines, "|") != "PLAY [x]|ok" || view.NextLine != 2 {
		t.Fatalf("output was not kept: %+v", view)
	}
	spec := f.specs[0]
	if spec.Playbook != "playbooks/host-agent.yml" || spec.Limit != "node-a" || !spec.Check {
		t.Fatalf("the command must come from the catalog and the validated limit: %+v", spec)
	}
	if f.varsIn[0]["host_agent_join_token"] != "adj_topsecret1234" {
		t.Fatalf("the secret must reach Ansible through the variables file: %v", f.varsIn[0])
	}
	if len(a.entries) != 2 || a.entries[0].Event != "start" || a.entries[1].Event != "finish" || a.entries[1].Status != StatusSucceeded {
		t.Fatalf("a start and a finish must be recorded: %+v", a.entries)
	}
	if a.entries[0].By != "operator" {
		t.Fatalf("who asked must be recorded: %+v", a.entries[0])
	}
}

func TestSecretsAreNeverInTheAuditRecordOrTheRunOrItsOutput(t *testing.T) {
	f := &fakeExec{run: func(_ ExecSpec, out io.Writer) (int, error) {
		io.WriteString(out, "token is adj_topsecret1234 here\nsplit adj_tops")
		io.WriteString(out, "ecret1234 across writes\n")
		return 2, nil
	}}
	a := &auditLog{}
	m := newManager(f, a)
	id, _ := m.Start(goodRequest, "operator")
	m.Wait()

	view, _ := m.Get(id, 0)
	everything, _ := json.Marshal([]any{view, a.entries, m.List()})
	if strings.Contains(string(everything), "topsecret") {
		t.Fatalf("a secret leaked: %s", everything)
	}
	if !strings.Contains(strings.Join(view.Lines, "\n"), "***") {
		t.Fatalf("scrubbing should leave a marker: %v", view.Lines)
	}
}

func TestANonZeroExitIsAFailureWithItsCode(t *testing.T) {
	m := newManager(&fakeExec{run: func(ExecSpec, io.Writer) (int, error) { return 2, nil }}, &auditLog{})
	id, _ := m.Start(goodRequest, "o")
	m.Wait()
	view, _ := m.Get(id, 0)
	if view.Status != StatusFailed || *view.ExitCode != 2 {
		t.Fatalf("%+v", view)
	}
}

func TestAnExecutorThatCannotRunIsAnErrorNotAFailure(t *testing.T) {
	m := newManager(&fakeExec{run: func(ExecSpec, io.Writer) (int, error) { return 0, errors.New("ansible-playbook not found") }}, &auditLog{})
	id, _ := m.Start(goodRequest, "o")
	m.Wait()
	view, _ := m.Get(id, 0)
	if view.Status != StatusError || !strings.Contains(view.Error, "not found") || view.ExitCode != nil {
		t.Fatalf("%+v", view)
	}
}

func TestOnlyOneRunAtATime(t *testing.T) {
	f := &fakeExec{gate: make(chan struct{})}
	m := newManager(f, &auditLog{})
	if _, err := m.Start(goodRequest, "o"); err != nil {
		t.Fatal(err)
	}
	if _, err := m.Start(goodRequest, "o"); !errors.Is(err, ErrBusy) {
		t.Fatalf("a second launch must be refused while one runs: %v", err)
	}
	close(f.gate)
	m.Wait()
	if _, err := m.Start(goodRequest, "o"); err != nil {
		t.Fatalf("a new run may start once the last finished: %v", err)
	}
	m.Wait()
}

func TestCancellingStopsTheRun(t *testing.T) {
	f := &fakeExec{gate: make(chan struct{})}
	a := &auditLog{}
	m := newManager(f, a)
	id, _ := m.Start(goodRequest, "o")
	if !m.Cancel() {
		t.Fatal("there is a run to cancel")
	}
	m.Wait()
	view, _ := m.Get(id, 0)
	if view.Status != StatusCancelled {
		t.Fatalf("expected cancelled: %+v", view)
	}
	if m.Cancel() {
		t.Fatal("nothing is running now")
	}
	if a.entries[len(a.entries)-1].Status != StatusCancelled {
		t.Fatal("the cancellation must be recorded")
	}
}

func TestARunThatCannotBeRecordedIsRefusedAndNothingRuns(t *testing.T) {
	f, a := &fakeExec{}, &auditLog{failOn: "start"}
	m := newManager(f, a)
	if _, err := m.Start(goodRequest, "o"); err == nil {
		t.Fatal("the agent must not run Ansible as an administrator without being able to record it")
	}
	m.Wait()
	if len(f.specs) != 0 || len(m.List()) != 0 {
		t.Fatal("nothing may have run or been remembered")
	}
	if _, err := m.Start(goodRequest, "o"); err == nil {
		// still failing: and the manager must not be wedged "busy"
	} else if errors.Is(err, ErrBusy) {
		t.Fatal("a refused launch must not leave the manager busy")
	}
}

func TestAnInvalidRequestNeverReachesTheExecutorOrTheRecord(t *testing.T) {
	f, a := &fakeExec{}, &auditLog{}
	m := newManager(f, a)
	if _, err := m.Start(Request{Playbook: "host-agent", Limit: "all"}, "o"); err == nil {
		t.Fatal("expected a validation error")
	}
	if len(f.specs) != 0 || len(a.entries) != 0 {
		t.Fatal("a refused request must leave no trace but the error")
	}
}

func TestTheVariablesFileIsPrivateAndRemovedAfterwards(t *testing.T) {
	var path string
	var mode os.FileMode
	f := &fakeExec{run: func(spec ExecSpec, _ io.Writer) (int, error) {
		path = spec.ExtraVarsFile
		info, err := os.Stat(path)
		if err != nil {
			t.Error(err)
			return 1, nil
		}
		mode = info.Mode().Perm()
		return 0, nil
	}}
	m := newManager(f, &auditLog{})
	m.Start(goodRequest, "o")
	m.Wait()
	if mode&0o077 != 0 {
		t.Fatalf("the file holding secrets must be private: %v", mode)
	}
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Fatal("the variables file must be removed when the run ends")
	}
}

func TestOutputIsServedIncrementallyAndBoundedWithStableLineNumbers(t *testing.T) {
	buffer := newOutputBuffer(nil)
	for i := 0; i < maxLines+10; i++ {
		buffer.Write([]byte("line\n"))
	}
	lines, first, next, dropped := buffer.From(0)
	if !dropped || first != 10 || next != maxLines+10 || len(lines) != maxLines {
		t.Fatalf("old lines are dropped but numbering continues: first=%d next=%d n=%d dropped=%v", first, next, len(lines), dropped)
	}
	more, first2, next2, _ := buffer.From(next)
	if len(more) != 0 || first2 != next || next2 != next {
		t.Fatalf("polling at the end returns nothing new: %v %d %d", more, first2, next2)
	}
	buffer.Write([]byte("tail without newline"))
	buffer.Flush()
	fresh, _, _, _ := buffer.From(next)
	if len(fresh) != 1 || fresh[0] != "tail without newline" {
		t.Fatalf("a last line without a newline must not be lost: %v", fresh)
	}
}

func TestOnlyTheLatestRunsAreKept(t *testing.T) {
	m := newManager(&fakeExec{}, &auditLog{})
	var first string
	for i := 0; i < keepRuns+3; i++ {
		id, err := m.Start(goodRequest, "o")
		if err != nil {
			t.Fatal(err)
		}
		m.Wait()
		if i == 0 {
			first = id
		}
	}
	if len(m.List()) != keepRuns {
		t.Fatalf("expected %d runs kept, got %d", keepRuns, len(m.List()))
	}
	if _, err := m.Get(first, 0); !errors.Is(err, ErrNotFound) {
		t.Fatal("the oldest run should have been forgotten")
	}
}
