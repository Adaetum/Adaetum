package localsecrets

import (
	"context"
	"encoding/json"
	"errors"
	"io/fs"
	"path"
	"sort"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/Adaetum/Adaetum/agent/internal/runs"
)

// memFS is a file system in memory that remembers what was done to it.
type memFS struct {
	mu     sync.Mutex
	files  map[string][]byte
	modes  map[string]fs.FileMode
	dirs   map[string]fs.FileMode
	writes []string
	// failWrite makes WriteAtomic fail for names containing this text.
	failWrite string
}

func newMemFS() *memFS {
	return &memFS{files: map[string][]byte{}, modes: map[string]fs.FileMode{}, dirs: map[string]fs.FileMode{}}
}

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

func (m *memFS) MkdirAll(p string, mode fs.FileMode) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.dirs[p] = mode
	return nil
}

func (m *memFS) Stat(p string) (fs.FileInfo, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if mode, ok := m.dirs[p]; ok {
		return info{path.Base(p), 0, mode | fs.ModeDir}, nil
	}
	if data, ok := m.files[p]; ok {
		return info{path.Base(p), int64(len(data)), m.modes[p]}, nil
	}
	return nil, &fs.PathError{Op: "stat", Path: p, Err: fs.ErrNotExist}
}

func (m *memFS) Read(p string) ([]byte, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if data, ok := m.files[p]; ok {
		return append([]byte(nil), data...), nil
	}
	return nil, &fs.PathError{Op: "open", Path: p, Err: fs.ErrNotExist}
}

func (m *memFS) WriteAtomic(p string, data []byte, mode fs.FileMode) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.failWrite != "" && strings.Contains(p, m.failWrite) {
		return errors.New("disk full")
	}
	m.files[p] = append([]byte(nil), data...)
	m.modes[p] = mode
	m.writes = append(m.writes, path.Base(p))
	return nil
}

func (m *memFS) written() []string {
	m.mu.Lock()
	defer m.mu.Unlock()
	return append([]string(nil), m.writes...)
}

// counter hands out predictable "random" bytes, different each call, so a test can tell
// two secrets apart.
func counter() Random {
	var n byte
	var mu sync.Mutex
	return func(size int) ([]byte, error) {
		mu.Lock()
		defer mu.Unlock()
		n++
		out := make([]byte, size)
		for i := range out {
			out[i] = n + byte(i)
		}
		return out, nil
	}
}

type clk struct{ now time.Time }

func (c *clk) Now() time.Time                                 { return c.now }
func (c *clk) Sleep(_ context.Context, _ time.Duration) error { return nil }

func run(t *testing.T, files Files, random Random, inputs map[string]string) (runs.Snapshot, []string) {
	t.Helper()
	var mu sync.Mutex
	var logged []string
	var journaled []runs.Record
	journal := &runs.MemoryJournal{}
	m, err := runs.NewManager(runs.Options{
		Journal: journal, Clock: &clk{now: time.Date(2026, 11, 12, 9, 0, 0, 0, time.UTC)},
		OnRecord: func(rec runs.Record) {
			mu.Lock()
			logged = append(logged, rec.Line())
			mu.Unlock()
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	defer m.Close()
	if err := m.Register(New(files, random)); err != nil {
		t.Fatalf("the procedure is malformed: %v", err)
	}
	id, err := m.Start(context.Background(), runs.StartRequest{Procedure: Name, Inputs: inputs, StartedBy: "test"})
	if err != nil {
		t.Fatal(err)
	}
	if err := m.Wait(context.Background(), id); err != nil {
		t.Fatal(err)
	}
	snap, _ := m.Get(id)
	records, _ := journal.Load(context.Background())
	journaled = records
	var everything []string
	for _, rec := range journaled {
		encoded, _ := json.Marshal(rec)
		everything = append(everything, string(encoded))
	}
	mu.Lock()
	everything = append(everything, logged...)
	mu.Unlock()
	return snap, everything
}

const dir = "/var/lib/bootstrap-secrets"

func TestAFreshMachineGetsEverySecretWithTheRightShapeAndPermissions(t *testing.T) {
	files := newMemFS()
	snap, _ := run(t, files, counter(), nil)
	if snap.State != runs.StateSucceeded {
		t.Fatalf("got %s: %s %s", snap.State, snap.Code, snap.Detail)
	}
	if files.dirs[dir] != 0o700 {
		t.Fatalf("the directory mode is %v", files.dirs[dir])
	}
	if len(files.files) != len(generated)+len(literals) {
		t.Fatalf("%d files written, want %d", len(files.files), len(generated)+len(literals))
	}
	for name, mode := range files.modes {
		if mode != 0o600 {
			t.Errorf("%s has mode %v, want 0600", name, mode)
		}
	}
	// 48 random bytes encode to 64 base64 characters; 24 to 32; 32 to 44.
	for name, want := range map[string]int{"rke2_token": 64, "rancher_admin_password": 32, "authentik_bootstrap_token": 44} {
		if got := len(files.files[dir+"/"+name]); got != want {
			t.Errorf("%s is %d characters, want %d", name, got, want)
		}
	}
	ntfy := string(files.files[dir+"/ntfy_apprise_password"])
	if !urlSafe32.MatchString(ntfy) {
		t.Errorf("the ntfy publisher password must be 32 URL-safe characters, got %q", ntfy)
	}
	if string(files.files[dir+"/authentik_admin_username"]) != "akadmin" || string(files.files[dir+"/adaetum_owner_email"]) != "break-glass@adaetum.local" {
		t.Error("the fixed values are wrong")
	}
	seen := map[string]string{}
	for name, data := range files.files {
		if other, dup := seen[string(data)]; dup && !strings.HasPrefix(name, dir+"/authentik_admin_username") {
			t.Errorf("%s and %s have the same value", name, other)
		}
		seen[string(data)] = name
	}
}

func TestRunningAgainChangesNothing(t *testing.T) {
	files := newMemFS()
	run(t, files, counter(), nil)
	before := map[string]string{}
	for name, data := range files.files {
		before[name] = string(data)
	}
	writes := len(files.written())

	snap, _ := run(t, files, counter(), nil)
	if snap.State != runs.StateSucceeded {
		t.Fatalf("got %s", snap.State)
	}
	if len(files.written()) != writes {
		t.Fatalf("a rerun wrote %d more files", len(files.written())-writes)
	}
	for name, data := range files.files {
		if before[name] != string(data) {
			t.Errorf("%s changed on a rerun", name)
		}
	}
	for _, step := range snap.Steps {
		if step.Code != runs.CodeAlready {
			t.Errorf("%s should already be in place, got %s", step.Name, step.Code)
		}
	}
}

func TestOnlyWhatIsMissingIsWrittenAndWhatExistsIsNeverTouched(t *testing.T) {
	files := newMemFS()
	files.dirs[dir] = 0o700
	files.files[dir+"/rke2_token"] = []byte("existing-token")
	files.modes[dir+"/rke2_token"] = 0o600
	snap, _ := run(t, files, counter(), nil)
	if snap.State != runs.StateSucceeded {
		t.Fatalf("got %s", snap.State)
	}
	if string(files.files[dir+"/rke2_token"]) != "existing-token" {
		t.Fatal("an existing secret was replaced")
	}
	for _, name := range files.written() {
		if name == "rke2_token" {
			t.Fatal("an existing secret was rewritten")
		}
	}
}

func TestAnEmptySecretStopsForAPersonAndNothingIsWritten(t *testing.T) {
	files := newMemFS()
	files.dirs[dir] = 0o700
	files.files[dir+"/gitea_admin_password"] = nil
	files.modes[dir+"/gitea_admin_password"] = 0o600
	snap, _ := run(t, files, counter(), nil)
	if snap.State != runs.StateNeedsHuman || snap.Code != runs.CodeConfig || !strings.Contains(snap.Detail, "gitea_admin_password") || !strings.Contains(snap.Detail, InputRegenerateEmpty) {
		t.Fatalf("got %s/%s %q", snap.State, snap.Code, snap.Detail)
	}
	if len(files.written()) != 0 {
		t.Fatalf("something was written before a person decided: %v", files.written())
	}
}

func TestRegenerateEmptyReplacesOnlyTheEmptyFile(t *testing.T) {
	files := newMemFS()
	files.dirs[dir] = 0o700
	files.files[dir+"/gitea_admin_password"] = nil
	files.modes[dir+"/gitea_admin_password"] = 0o600
	files.files[dir+"/rke2_token"] = []byte("keep-me")
	files.modes[dir+"/rke2_token"] = 0o600
	snap, _ := run(t, files, counter(), map[string]string{InputRegenerateEmpty: "true"})
	if snap.State != runs.StateSucceeded {
		t.Fatalf("got %s: %s", snap.State, snap.Detail)
	}
	if len(files.files[dir+"/gitea_admin_password"]) != 32 {
		t.Fatal("the empty secret was not regenerated")
	}
	if string(files.files[dir+"/rke2_token"]) != "keep-me" {
		t.Fatal("a good secret was replaced")
	}
}

func TestAnEmptyFixedValueIsWrittenOverLikeTheOldScriptDid(t *testing.T) {
	files := newMemFS()
	files.dirs[dir] = 0o700
	files.files[dir+"/authentik_admin_username"] = nil
	files.modes[dir+"/authentik_admin_username"] = 0o600
	snap, _ := run(t, files, counter(), nil)
	if snap.State != runs.StateSucceeded || string(files.files[dir+"/authentik_admin_username"]) != "akadmin" {
		t.Fatalf("got %s %q", snap.State, files.files[dir+"/authentik_admin_username"])
	}
}

func TestAnInvalidNtfyPasswordStopsForAPerson(t *testing.T) {
	files := newMemFS()
	files.dirs[dir] = 0o700
	files.files[dir+"/ntfy_apprise_password"] = []byte("has spaces and is too short")
	files.modes[dir+"/ntfy_apprise_password"] = 0o600
	snap, _ := run(t, files, counter(), nil)
	if snap.State != runs.StateNeedsHuman || !strings.Contains(snap.Detail, "ntfy_apprise_password") {
		t.Fatalf("got %s %q", snap.State, snap.Detail)
	}
}

func TestADirectoryThatIsTooOpenIsTightened(t *testing.T) {
	files := newMemFS()
	files.dirs[dir] = 0o755
	snap, _ := run(t, files, counter(), nil)
	if snap.State != runs.StateSucceeded || files.dirs[dir] != 0o700 {
		t.Fatalf("got %s with directory mode %v", snap.State, files.dirs[dir])
	}
}

func TestAFileWhereTheDirectoryShouldBeStopsForAPerson(t *testing.T) {
	files := newMemFS()
	files.files[dir] = []byte("x")
	files.modes[dir] = 0o600
	// The fake reports a file here, not a directory.
	snap, _ := run(t, files, counter(), nil)
	if snap.State == runs.StateSucceeded {
		t.Fatalf("a regular file must not be accepted as the secret directory")
	}
}

func TestUnsafeDirectoriesAreRefused(t *testing.T) {
	for _, bad := range []string{"relative/path", "/var/../etc", "/var/lib/with space", "/", "/a;rm"} {
		files := newMemFS()
		snap, _ := run(t, files, counter(), map[string]string{InputDir: bad})
		if snap.State != runs.StateFailed || snap.Code != runs.CodeConfig {
			t.Errorf("%q: got %s/%s", bad, snap.State, snap.Code)
		}
		if len(files.written()) != 0 {
			t.Errorf("%q: something was written", bad)
		}
	}
}

func TestASecretValueNeverReachesTheJournalOrTheLog(t *testing.T) {
	files := newMemFS()
	_, everything := run(t, files, counter(), nil)
	joined := strings.Join(everything, "\n")
	for name, data := range files.files {
		value := string(data)
		if value == "akadmin" || strings.HasSuffix(name, "adaetum_owner_email") {
			continue // fixed, non-secret values
		}
		if strings.Contains(joined, value) {
			t.Fatalf("the value of %s reached a record or log line", name)
		}
	}
	if !strings.Contains(joined, "step=generated") {
		t.Fatal("the log should still say what happened")
	}
}

func TestAFailedWriteStopsTheRunAndARetryFinishesWithoutRepeatingWork(t *testing.T) {
	files := newMemFS()
	files.failWrite = "gitea_jwt_secret"
	snap, _ := run(t, files, counter(), nil)
	if snap.State != runs.StateFailed || snap.Code != runs.CodeConfig || !strings.Contains(snap.Detail, "gitea_jwt_secret") {
		t.Fatalf("got %s/%s %q", snap.State, snap.Code, snap.Detail)
	}
	firstRun := files.written()

	files.failWrite = "" // the disk has room again
	snap, _ = run(t, files, counter(), nil)
	if snap.State != runs.StateSucceeded {
		t.Fatalf("after the fix: %s %s", snap.State, snap.Detail)
	}
	rewritten := map[string]bool{}
	for _, name := range files.written()[len(firstRun):] {
		rewritten[name] = true
	}
	for _, name := range firstRun {
		if rewritten[name] {
			t.Errorf("%s was written again although it already existed", name)
		}
	}
}

func TestAWrongSizedRandomSourceIsRefused(t *testing.T) {
	files := newMemFS()
	snap, _ := run(t, files, func(n int) ([]byte, error) { return make([]byte, n-1), nil }, nil)
	if snap.State != runs.StateFailed {
		t.Fatalf("got %s", snap.State)
	}
	names := make([]string, 0)
	for name := range files.files {
		names = append(names, name)
	}
	sort.Strings(names)
	if len(names) != 0 {
		t.Fatalf("a short random value was written: %v", names)
	}
}
