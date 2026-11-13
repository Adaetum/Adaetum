package firstboot

import (
	"context"
	"errors"
	"io/fs"
	"path"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/Adaetum/Adaetum/agent/internal/runs"
)

// machine is a fake host and file system in one: everything first boot touches.
type machine struct {
	mu      sync.Mutex
	route   bool
	files   map[string][]byte
	dirs    map[string]fs.FileMode
	written []string
}

func newMachine() *machine {
	return &machine{route: true, files: map[string][]byte{}, dirs: map[string]fs.FileMode{}}
}

func (m *machine) HasDefaultRoute(context.Context) (bool, error)     { return m.route, nil }
func (m *machine) ClockSynchronized(context.Context) (bool, bool)    { return true, true }
func (m *machine) FreeBytes(context.Context, string) (uint64, error) { return 100 << 30, nil }

type info struct {
	name string
	size int64
	mode fs.FileMode
}

func (i info) Name() string       { return i.name }
func (i info) Size() int64        { return i.size }
func (i info) Mode() fs.FileMode  { return i.mode }
func (i info) ModTime() time.Time { return time.Time{} }
func (i info) IsDir() bool        { return i.mode.IsDir() }
func (i info) Sys() any           { return nil }

func (m *machine) MkdirAll(p string, mode fs.FileMode) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.dirs[p] = mode
	return nil
}
func (m *machine) Stat(p string) (fs.FileInfo, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if mode, ok := m.dirs[p]; ok {
		return info{path.Base(p), 0, mode | fs.ModeDir}, nil
	}
	if data, ok := m.files[p]; ok {
		return info{path.Base(p), int64(len(data)), 0o600}, nil
	}
	return nil, &fs.PathError{Op: "stat", Path: p, Err: fs.ErrNotExist}
}
func (m *machine) Read(p string) ([]byte, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if data, ok := m.files[p]; ok {
		return append([]byte(nil), data...), nil
	}
	return nil, errors.New("no such file")
}
func (m *machine) WriteAtomic(p string, data []byte, _ fs.FileMode) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.files[p] = append([]byte(nil), data...)
	m.written = append(m.written, path.Base(p))
	return nil
}

const payload = `BOOTSTRAP_BACKUP_PASSPHRASE=pass
BOOTSTRAP_BACKUP_TO_R2=1
BOOTSTRAP_BACKUP_URL=https://bootstrap.example.cloud/backup
ARGOCD_GITHUB_TOKEN=ghp_abcdefghijklmnopqrstuvwxyz0123456789
GITEA_SEED_SOURCE_TOKEN=ghp_abcdefghijklmnopqrstuvwxyz9876543210
`

func ready() *machine {
	m := newMachine()
	m.files["/etc/bootstrap-runtime.env"] = []byte(payload)
	return m
}

func zeros(n int) ([]byte, error) { return make([]byte, n), nil }

// clk moves forward when something sleeps, so a wait that should stall actually reaches
// its stall window instead of polling forever.
type clk struct {
	mu  sync.Mutex
	now time.Time
}

func (c *clk) Now() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.now
}
func (c *clk) Sleep(ctx context.Context, d time.Duration) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	c.mu.Lock()
	c.now = c.now.Add(d)
	c.mu.Unlock()
	return nil
}

func newManager(t *testing.T, m *machine, register ...*runs.Procedure) *runs.Manager {
	t.Helper()
	mgr, err := runs.NewManager(runs.Options{
		Journal: &runs.MemoryJournal{}, Clock: &clk{now: time.Date(2026, 11, 12, 9, 0, 0, 0, time.UTC)},
		Policy: runs.Policy{PollEvery: time.Second, StallAfter: 10 * time.Second, Ceiling: time.Hour},
	})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(mgr.Close)
	for _, p := range register {
		if err := mgr.Register(p); err != nil {
			t.Fatalf("%s is malformed: %v", p.Name, err)
		}
	}
	return mgr
}

func runIt(t *testing.T, mgr *runs.Manager, name string) runs.Snapshot {
	t.Helper()
	id, err := mgr.Start(context.Background(), runs.StartRequest{Procedure: name, StartedBy: "test"})
	if err != nil {
		t.Fatal(err)
	}
	_ = mgr.Wait(context.Background(), id)
	snap, _ := mgr.Get(id)
	return snap
}

func TestFirstBootRunsEveryChunkInOrderOnAHealthyMachine(t *testing.T) {
	m := ready()
	whole, err := New(m, m, zeros)
	if err != nil {
		t.Fatal(err)
	}
	snap := runIt(t, newManager(t, m, whole), Name)
	if snap.State != runs.StateSucceeded {
		t.Fatalf("got %s at %s: %s", snap.State, snap.FailedStep, snap.Detail)
	}
	var names []string
	for _, step := range snap.Steps {
		names = append(names, step.Name)
		if step.State != runs.StepDone {
			t.Errorf("%s is %s", step.Name, step.State)
		}
	}
	want := "host-readiness/network,host-readiness/clock,host-readiness/disk,intake/payload-format,intake/payload-contract,local-secrets/directory,local-secrets/generated,local-secrets/literals"
	if got := strings.Join(names, ","); got != want {
		t.Fatalf("steps\n got %s\nwant %s", got, want)
	}
	if len(m.written) != 20 {
		t.Fatalf("%d secrets written, want 20", len(m.written))
	}
}

func TestFirstBootAgainDoesNothingBecauseEveryChunkChecksFirst(t *testing.T) {
	m := ready()
	whole, _ := New(m, m, zeros)
	mgr := newManager(t, m, whole)
	runIt(t, mgr, Name)
	before := len(m.written)
	if snap := runIt(t, mgr, Name); snap.State != runs.StateSucceeded {
		t.Fatalf("got %s", snap.State)
	}
	if len(m.written) != before {
		t.Fatalf("a second first-boot wrote %d more secrets", len(m.written)-before)
	}
}

func TestAnEarlyFailureStopsTheLaterChunksAndCreatesNothing(t *testing.T) {
	m := ready()
	m.files["/etc/bootstrap-runtime.env"] = []byte("BOOTSTRAP_BACKUP_TO_R2=0\n") // intake will reject this
	whole, _ := New(m, m, zeros)
	snap := runIt(t, newManager(t, m, whole), Name)
	if snap.State != runs.StateFailed || snap.FailedStep != "intake/payload-contract" {
		t.Fatalf("got %s at %s", snap.State, snap.FailedStep)
	}
	if len(m.written) != 0 || len(m.dirs) != 0 {
		t.Fatalf("local-secrets ran after intake failed: wrote %v, dirs %v", m.written, m.dirs)
	}
}

func TestAMachineWithNoRouteStopsBeforeAnythingElse(t *testing.T) {
	m := ready()
	m.route = false
	whole, _ := New(m, m, zeros)
	snap := runIt(t, newManager(t, m, whole), Name)
	if snap.State != runs.StateFailed || snap.FailedStep != "host-readiness/network" || snap.Failure != runs.FailStalled {
		t.Fatalf("got %s/%s at %s", snap.State, snap.Failure, snap.FailedStep)
	}
}

func TestEachChunkRunsAloneOnTheSameMachine(t *testing.T) {
	// This is what "testable independently" means: every chunk is a procedure of its
	// own, with no dependence on the chunks before it having run in the same process.
	for _, chunk := range Chunks(ready(), ready(), zeros) {
		m := ready()
		fresh := Chunks(m, m, zeros)
		var one *runs.Procedure
		for _, candidate := range fresh {
			if candidate.Name == chunk.Name {
				one = candidate
			}
		}
		snap := runIt(t, newManager(t, m, one), one.Name)
		if snap.State != runs.StateSucceeded {
			t.Errorf("%s alone: %s at %s: %s", one.Name, snap.State, snap.FailedStep, snap.Detail)
		}
	}
}

func TestTheChunksAndTheWholeCanBeRegisteredTogether(t *testing.T) {
	m := ready()
	whole, err := New(m, m, zeros)
	if err != nil {
		t.Fatal(err)
	}
	mgr := newManager(t, m, append(Chunks(m, m, zeros), whole)...)
	if len(mgr.Procedures()) != 4 {
		t.Fatalf("%d procedures", len(mgr.Procedures()))
	}
}
