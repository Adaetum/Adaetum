package backups

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

const dailyLabel = "recurring-job-group.longhorn.io/adaetum-backup-daily"

func postVolume(api *VolumeAPI, name, body string) *httptest.ResponseRecorder {
	mux := http.NewServeMux()
	api.Register(mux)
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, httptest.NewRequest(http.MethodPost, "/api/v1/backups/volumes/"+name, strings.NewReader(body)))
	return rec
}

func library(labels map[string]string) cluster {
	return cluster{volumes: []volumeSpec{{name: "pvc-1", namespace: "media", claim: "library", created: "2026-11-01T00:00:00Z", labels: labels}}, noSync: true}
}

func TestChoosingASchedulePutsTheVolumeOnThatOneOnlyAfterRecordingIt(t *testing.T) {
	var order []string
	var got map[string]*string
	api := &VolumeAPI{
		Get:   library(nil).get,
		Audit: func(any) error { order = append(order, "record"); return nil },
		SetLabels: func(_ context.Context, _ string, labels map[string]*string) error {
			order = append(order, "label")
			got = labels
			return nil
		},
	}
	if rec := postVolume(api, "pvc-1", `{"mode":"schedule:daily"}`); rec.Code != 200 {
		t.Fatal(rec.Body.String())
	}
	if got[dailyLabel] == nil || *got[dailyLabel] != "enabled" || got[ChosenLabel] == nil || strings.Join(order, ",") != "record,label" {
		t.Errorf("order %v labels %v", order, got)
	}
	postVolume(api, "pvc-1", `{"mode":"off"}`)
	if v, ok := got[dailyLabel]; !ok || v != nil {
		t.Errorf("off should remove the schedule label: %v", got)
	}
}

func TestAScheduleThatIsNotActiveCannotBeChosen(t *testing.T) {
	changed := false
	api := &VolumeAPI{Get: library(nil).get, Audit: func(any) error { return nil }, SetLabels: func(context.Context, string, map[string]*string) error { changed = true; return nil }}
	if rec := postVolume(api, "pvc-1", `{"mode":"schedule:weekly"}`); rec.Code != 502 || changed {
		t.Errorf("%d changed=%v", rec.Code, changed)
	}
}

func TestBadRequestsAndMissingRecordsChangeNothing(t *testing.T) {
	changed := false
	set := func(context.Context, string, map[string]*string) error { changed = true; return nil }
	api := &VolumeAPI{Get: library(nil).get, Audit: func(any) error { return nil }, SetLabels: set}
	for _, c := range []struct{ name, body string }{{"pvc-1", `{"mode":"mirror"}`}, {"Bad_Name", `{"mode":"off"}`}, {"pvc-1", `nope`}, {"pvc-1", `{"mode":"schedule:Bad Name"}`}} {
		if rec := postVolume(api, c.name, c.body); rec.Code != 400 {
			t.Errorf("%v: %d", c, rec.Code)
		}
	}
	if rec := postVolume(&VolumeAPI{Get: library(nil).get, SetLabels: set}, "pvc-1", `{"mode":"off"}`); rec.Code != 503 || changed {
		t.Errorf("without a record nothing may change: %d", rec.Code)
	}
}

func TestOneModeAtATimeDropsTheScheduleOnceTheCopyExists(t *testing.T) {
	c := library(map[string]string{dailyLabel: "enabled"})
	c.noSync, c.synced = false, map[string]bool{"media/library": true}
	var cleared []string
	set := func(_ context.Context, name string, labels map[string]*string) error {
		if labels[dailyLabel] != nil {
			t.Errorf("only labels may be cleared: %v", labels)
		}
		cleared = append(cleared, name)
		return nil
	}
	if err := EnforceOneMode(context.Background(), c.get, set, func(any) error { return nil }); err != nil {
		t.Fatal(err)
	}
	if len(cleared) != 1 {
		t.Errorf("cleared %v", cleared)
	}
}

func TestTheDefaultRuleLabelsOnlyNewUnchosenVolumes(t *testing.T) {
	since := mustTime("2026-11-16T12:00:00Z")
	c := cluster{noSync: true, volumes: []volumeSpec{
		{name: "old", created: "2026-11-01T00:00:00Z"},
		{name: "new", created: "2026-11-17T00:00:00Z"},
		{name: "new-chosen", created: "2026-11-17T00:00:00Z", labels: map[string]string{ChosenLabel: "true"}},
	}}
	var labelled []string
	set := func(_ context.Context, name string, labels map[string]*string) error {
		labelled = append(labelled, name)
		if labels[dailyLabel] == nil {
			t.Errorf("%s: %v", name, labels)
		}
		return nil
	}
	audit := func(any) error { return nil }
	if _, err := Reconcile(context.Background(), c.get, set, audit, Default{Mode: "schedule:daily", Since: since}); err != nil {
		t.Fatal(err)
	}
	if len(labelled) != 1 || labelled[0] != "new" {
		t.Errorf("labelled %v", labelled)
	}
	labelled = nil
	_, _ = Reconcile(context.Background(), c.get, set, audit, Default{Mode: "off", Since: since})
	_, _ = Reconcile(context.Background(), c.get, set, audit, Default{Mode: "schedule:gone", Since: since})
	_, _ = Reconcile(context.Background(), c.get, set, func(any) error { return context.Canceled }, Default{Mode: "schedule:daily", Since: since})
	if len(labelled) != 0 {
		t.Errorf("nothing should change: %v", labelled)
	}
}

func TestSchedulesAreCheckedAndChangedByPullRequest(t *testing.T) {
	var got Changes
	api := &SchedulesAPI{Get: library(map[string]string{dailyLabel: "enabled"}).get, Audit: func(any) error { return nil }, Propose: func(_ context.Context, c Changes) (string, string, error) { got = c; return "u", "b", nil }}
	put := func(body string) int {
		mux := http.NewServeMux()
		api.Register(mux)
		rec := httptest.NewRecorder()
		mux.ServeHTTP(rec, httptest.NewRequest(http.MethodPut, "/api/v1/backups/schedules", strings.NewReader(body)))
		return rec.Code
	}
	if code := put(`{"schedules":[{"name":"daily","cron":"0 3 * * *","retain":7},{"name":"weekly","cron":"0 4 * * 0","retain":4}]}`); code != 200 || !strings.Contains(got.Update[schedulesFile], "backup-weekly") {
		t.Fatalf("%d %v", code, got)
	}
	for name, body := range map[string]string{
		"bad cron":        `{"schedules":[{"name":"daily","cron":"every day","retain":7}]}`,
		"bad name":        `{"schedules":[{"name":"Daily!","cron":"0 3 * * *","retain":7}]}`,
		"retain":          `{"schedules":[{"name":"daily","cron":"0 3 * * *","retain":0}]}`,
		"duplicate":       `{"schedules":[{"name":"a","cron":"0 3 * * *","retain":1},{"name":"a","cron":"0 3 * * *","retain":1}]}`,
		"in use, removed": `{"schedules":[{"name":"weekly","cron":"0 3 * * *","retain":1}]}`,
	} {
		got = Changes{}
		if code := put(body); code < 400 || got.Prefix != "" {
			t.Errorf("%s: %d", name, code)
		}
	}
}

func TestSizesAndTheSyncPodAreReported(t *testing.T) {
	c := library(nil)
	c.noSync, c.synced = false, map[string]bool{}
	state, err := Collect(context.Background(), c.get, mustTime("2026-11-17T00:00:00Z"))
	if err != nil {
		t.Fatal(err)
	}
	if state.Volumes[0].UsedBytes != 1048576 || state.Volumes[0].SizeBytes != 2147483648 || !state.SyncInstalled || state.Sync.Ready != 1 || len(state.Schedules) != 1 {
		t.Errorf("%+v", state)
	}
}
