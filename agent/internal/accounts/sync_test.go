package accounts

import (
	"context"
	"encoding/json"
	"errors"
	"testing"
)

func st(users, owners int, epoch string, version int64) State {
	return State{Schema: 1, Users: users, Owners: owners, Epoch: epoch, Version: version}
}

func TestDecide(t *testing.T) {
	cases := []struct {
		name      string
		local, up State
		want      Action
	}{
		{"nobody anywhere", st(0, 0, "", 0), st(0, 0, "", 0), Nothing},
		{"the first host seeds an empty cluster", st(2, 1, "E", 7), st(0, 0, "", 0), Push},
		{"but not people with no owner", st(2, 0, "E", 7), st(0, 0, "", 0), Warn},
		{"a new host takes the cluster's accounts", st(0, 0, "", 0), st(3, 1, "E", 9), Pull},
		{"same history, cluster newer", st(2, 1, "E", 7), st(3, 1, "E", 9), Pull},
		{"same history, same version", st(3, 1, "E", 9), st(3, 1, "E", 9), Nothing},
		{"cluster restored from an old backup: the cluster still wins", st(3, 1, "E", 9), st(2, 1, "E", 4), Replace},
		{"different histories, cluster looks newer", st(2, 1, "A", 1), st(2, 1, "B", 99), Replace},
		{"different histories, host looks newer: the cluster still wins", st(2, 1, "A", 99), st(2, 1, "B", 1), Replace},
		{"different account layouts", State{Schema: 2, Users: 1, Owners: 1, Epoch: "E"}, st(1, 1, "E", 1), Warn},
	}
	for _, c := range cases {
		if got := Decide(c.local, c.up).Action; got != c.want {
			t.Errorf("%s: %s, want %s", c.name, got, c.want)
		}
	}
}

type fakeLocal struct {
	state    State
	snapshot json.RawMessage
	applied  []json.RawMessage
	replaced []json.RawMessage
	verdict  ApplyResult
	err      error
}

func (f *fakeLocal) State(context.Context) (State, error) { return f.state, f.err }
func (f *fakeLocal) Snapshot(context.Context) (json.RawMessage, error) {
	if f.snapshot == nil {
		return nil, ErrNobody
	}
	return f.snapshot, nil
}
func (f *fakeLocal) Apply(_ context.Context, s json.RawMessage) (ApplyResult, error) {
	f.applied = append(f.applied, s)
	return f.verdict, f.err
}

func (f *fakeLocal) Replace(_ context.Context, s json.RawMessage) (ApplyResult, error) {
	f.replaced = append(f.replaced, s)
	return f.verdict, f.err
}

type fakeUp struct {
	state    State
	snapshot json.RawMessage
	seeded   []json.RawMessage
	verdict  ApplyResult
	err      error
}

func (f *fakeUp) State(context.Context) (State, error)              { return f.state, f.err }
func (f *fakeUp) Snapshot(context.Context) (json.RawMessage, error) { return f.snapshot, f.err }
func (f *fakeUp) Seed(_ context.Context, s json.RawMessage) (ApplyResult, error) {
	f.seeded = append(f.seeded, s)
	return f.verdict, f.err
}

func TestSyncOncePullsTheClustersAccounts(t *testing.T) {
	local := &fakeLocal{state: st(0, 0, "", 0), verdict: ApplyResult{Status: "applied", Version: 9}}
	up := &fakeUp{state: st(3, 1, "E", 9), snapshot: json.RawMessage(`{"v":9}`)}
	got, err := SyncOnce(context.Background(), local, up)
	if err != nil || got.Action != Pull || len(local.applied) != 1 || string(local.applied[0]) != `{"v":9}` {
		t.Fatalf("pull: %+v %v %v", got, err, local.applied)
	}
}

func TestSyncOnceReportsARefusedCopyAsAWarning(t *testing.T) {
	local := &fakeLocal{state: st(2, 1, "E", 7), verdict: ApplyResult{Status: "rejected", Reason: "not_newer", Detail: "older"}}
	up := &fakeUp{state: st(2, 1, "E", 9), snapshot: json.RawMessage(`{}`)}
	got, err := SyncOnce(context.Background(), local, up)
	if err != nil || got.Action != Warn {
		t.Fatalf("a refused copy must be a warning, not a failure: %+v %v", got, err)
	}
}

func TestSyncOnceSeedsAnEmptyClusterAndNeverTheReverse(t *testing.T) {
	local := &fakeLocal{state: st(2, 1, "E", 7), snapshot: json.RawMessage(`{"mine":true}`)}
	up := &fakeUp{state: st(0, 0, "", 0), verdict: ApplyResult{Status: "applied", Version: 7}}
	got, err := SyncOnce(context.Background(), local, up)
	if err != nil || got.Action != Push || len(up.seeded) != 1 {
		t.Fatalf("seed: %+v %v", got, err)
	}
	// A cluster that refuses (it got people in the meantime) is a warning, and this host
	// changes nothing.
	up = &fakeUp{state: st(0, 0, "", 0), verdict: ApplyResult{Status: "rejected", Reason: "primary_has_accounts"}}
	got, _ = SyncOnce(context.Background(), local, up)
	if got.Action != Warn || len(local.applied) != 0 {
		t.Fatalf("refused seed: %+v applied=%d", got, len(local.applied))
	}
}

func TestSyncOnceLetsTheClusterWinWhenHistoriesDisagree(t *testing.T) {
	local := &fakeLocal{state: st(2, 1, "A", 1), verdict: ApplyResult{Status: "applied", Version: 500}}
	up := &fakeUp{state: st(5, 1, "B", 500), snapshot: json.RawMessage(`{"v":500}`)}
	got, err := SyncOnce(context.Background(), local, up)
	if err != nil || got.Action != Replace || len(local.replaced) != 1 || len(local.applied) != 0 || len(up.seeded) != 0 {
		t.Fatalf("different histories: %+v %v", got, err)
	}
	// A host never offers its own list to a cluster that has people, however it compares.
	if len(up.seeded) != 0 {
		t.Fatal("the host pushed to a cluster that has accounts")
	}
}

func TestAnUnreachableClusterIsNormalAndIsLabelled(t *testing.T) {
	local := &fakeLocal{state: st(2, 1, "E", 7)}
	up := &fakeUp{err: errors.New("connection refused")}
	_, err := SyncOnce(context.Background(), local, up)
	if !errors.Is(err, ErrUpstream) {
		t.Fatalf("got %v", err)
	}
	if len(local.applied) != 0 {
		t.Fatal("nothing may change when the cluster cannot be reached")
	}
}

func TestADownLocalServiceIsNotMistakenForAnUnreachableCluster(t *testing.T) {
	local := &fakeLocal{err: errors.New("socket: no such file")}
	up := &fakeUp{state: st(3, 1, "E", 9)}
	_, err := SyncOnce(context.Background(), local, up)
	if err == nil || errors.Is(err, ErrUpstream) {
		t.Fatalf("got %v", err)
	}
}
