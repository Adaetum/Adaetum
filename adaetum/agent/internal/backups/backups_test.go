package backups

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"
)

var now = time.Date(2026, 11, 16, 12, 0, 0, 0, time.UTC)

const jobsJSON = `{"items":[
 {"metadata":{"name":"nightly"},"spec":{"task":"backup","cron":"0 2 * * *","retain":7,"groups":["adaetum-backup"]}},
 {"metadata":{"name":"hourly"},"spec":{"task":"snapshot","cron":"0 * * * *","retain":24,"groups":["default"]}}
]}`

func volume(name string, labels string, last string) string {
	status := `"kubernetesStatus":{"namespace":"apps","pvcName":"` + name + `-claim"}`
	if last != "" {
		status += `,"lastBackupAt":"` + last + `"`
	}
	return `{"metadata":{"name":"` + name + `","labels":{` + labels + `}},"spec":{"size":"1073741824"},"status":{` + status + `}}`
}

func getter(target, volumes string) Getter {
	return func(_ context.Context, resources string, _ ...string) ([]byte, error) {
		switch resources {
		case "backuptargets.longhorn.io":
			return []byte(target), nil
		case "recurringjobs.longhorn.io":
			return []byte(jobsJSON), nil
		case "volumes.longhorn.io":
			return []byte(`{"items":[` + volumes + `]}`), nil
		}
		return nil, errors.New("unexpected " + resources)
	}
}

const offTarget = `{"items":[{"metadata":{"name":"default"},"spec":{"backupTargetURL":""},"status":{"available":false,"conditions":[{"type":"Unavailable","status":"True","message":"backup target URL is empty"}]}}]}`
const goodTarget = `{"items":[{"metadata":{"name":"default"},"spec":{"backupTargetURL":"s3://bkt@auto/"},"status":{"available":true}}]}`
const badTarget = `{"items":[{"metadata":{"name":"default"},"spec":{"backupTargetURL":"s3://bkt@auto/"},"status":{"available":false,"conditions":[{"type":"Unavailable","status":"True","message":"AccessDenied"}]}}]}`

func TestNoTargetIsOff(t *testing.T) {
	state, err := Collect(context.Background(), getter(offTarget, volume("a", "", "")), now)
	if err != nil || state.Summary != "off" || state.Target.Configured || state.Target.Message == "" {
		t.Fatalf("%+v %v", state, err)
	}
}

func TestCoverageComesFromLabelsAndTheDefaultGroup(t *testing.T) {
	volumes := strings.Join([]string{
		volume("fresh", `"recurring-job-group.longhorn.io/adaetum-backup":"enabled"`, "2026-11-16T02:00:00Z"),
		volume("old", `"recurring-job.longhorn.io/nightly":"enabled"`, "2026-11-10T02:00:00Z"),
		volume("never", `"recurring-job-group.longhorn.io/adaetum-backup":"enabled"`, ""),
		volume("bare", ``, ""), // no labels: gets the default group (the snapshot job), but no backup job
		volume("off", `"recurring-job-group.longhorn.io/adaetum-backup":"disabled"`, ""),
	}, ",")
	state, err := Collect(context.Background(), getter(goodTarget, volumes), now)
	if err != nil {
		t.Fatal(err)
	}
	got := map[string]string{}
	for _, v := range state.Volumes {
		got[v.Name] = v.State
	}
	want := map[string]string{"fresh": "protected", "old": "stale", "never": "stale", "bare": "unprotected", "off": "unprotected"}
	for name, state := range want {
		if got[name] != state {
			t.Errorf("%s: got %q want %q", name, got[name], state)
		}
	}
	for _, v := range state.Volumes {
		if v.Name == "bare" && v.Snapshot != "hourly" {
			t.Errorf("a volume with no job labels must get the default group's snapshot job, got %q", v.Snapshot)
		}
	}
	if state.Summary != "attention" {
		t.Errorf("summary %q", state.Summary)
	}
}

func TestSummaries(t *testing.T) {
	protected := volume("p", `"recurring-job-group.longhorn.io/adaetum-backup":"enabled"`, "2026-11-16T02:00:00Z")
	for name, c := range map[string]struct{ target, volumes, want string }{
		"ok":          {goodTarget, protected, "ok"},
		"failing":     {badTarget, protected, "failing"},
		"unprotected": {goodTarget, protected + "," + volume("x", `"recurring-job.longhorn.io/other":"enabled"`, ""), "unprotected"},
	} {
		state, err := Collect(context.Background(), getter(c.target, c.volumes), now)
		if err != nil || state.Summary != c.want {
			t.Errorf("%s: got %q, %v", name, state.Summary, err)
		}
	}
	if _, err := Collect(context.Background(), func(context.Context, string, ...string) ([]byte, error) { return nil, errors.New("denied") }, now); err == nil {
		t.Error("an unreadable Longhorn must be an error")
	}
}
