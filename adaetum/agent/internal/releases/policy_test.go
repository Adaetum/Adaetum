package releases

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
)

type policyRig struct {
	policy    *Policy
	proposals int
	audits    []string
}

func newFixture(t *testing.T, update bool) *policyRig {
	f := &policyRig{}
	f.policy = &Policy{
		Path:    filepath.Join(t.TempDir(), "update-settings.json"),
		Current: "0.1.0",
		Latest: func(_ context.Context, _, channel string) (Check, error) {
			return Check{UpdateAvailable: update, ManifestHash: "h", Release: Manifest{Version: "0.2.0"}}, nil
		},
		Propose: func(context.Context, Manifest) (Proposal, error) {
			f.proposals++
			return Proposal{URL: "https://git/pulls/1", Branch: "b"}, nil
		},
		Audit: func(e any) error { f.audits = append(f.audits, e.(map[string]any)["event"].(string)); return nil },
	}
	return f
}

func TestDefaultsAreManualStable(t *testing.T) {
	st := newFixture(t, true).policy.Status()
	if st.Channel != "stable" || st.Mode != ModeManual {
		t.Fatalf("got %+v", st.Settings)
	}
}

func TestManualNeverChecks(t *testing.T) {
	f := newFixture(t, true)
	f.policy.Latest = func(context.Context, string, string) (Check, error) {
		t.Fatal("checked in manual mode")
		return Check{}, nil
	}
	f.policy.Check(context.Background())
}

func TestNotifyReportsButDoesNotPropose(t *testing.T) {
	f := newFixture(t, true)
	if err := f.policy.Set(Settings{"beta", ModeNotify}, "me"); err != nil {
		t.Fatal(err)
	}
	f.policy.Check(context.Background())
	st := f.policy.Status()
	if !st.UpdateAvailable || st.Latest != "0.2.0" || f.proposals != 0 {
		t.Fatalf("status %+v proposals %d", st, f.proposals)
	}
}

func TestAutoProposesOncePerVersionAndSurvivesRestart(t *testing.T) {
	f := newFixture(t, true)
	if err := f.policy.Set(Settings{"stable", ModeAuto}, "me"); err != nil {
		t.Fatal(err)
	}
	f.policy.Check(context.Background())
	f.policy.Check(context.Background())
	if f.proposals != 1 || f.policy.Status().Proposal.URL == "" {
		t.Fatalf("proposals %d", f.proposals)
	}
	restarted := &Policy{Path: f.policy.Path, Current: "0.1.0", Latest: f.policy.Latest, Propose: f.policy.Propose, Audit: f.policy.Audit}
	restarted.Check(context.Background())
	if f.proposals != 1 {
		t.Fatalf("restart opened a duplicate: %d", f.proposals)
	}
}

func TestAutoWithoutGitOpsSaysSo(t *testing.T) {
	f := newFixture(t, true)
	f.policy.Propose = nil
	_ = f.policy.Set(Settings{"stable", ModeAuto}, "me")
	f.policy.Check(context.Background())
	if f.policy.Status().Error == "" {
		t.Fatal("expected an explanation")
	}
}

func TestNoUpdateNoProposal(t *testing.T) {
	f := newFixture(t, false)
	_ = f.policy.Set(Settings{"stable", ModeAuto}, "me")
	f.policy.Check(context.Background())
	if f.proposals != 0 {
		t.Fatal("proposed with nothing to update")
	}
}

func TestSetRejectsBadValuesAndNeedsAudit(t *testing.T) {
	f := newFixture(t, true)
	if f.policy.Set(Settings{"nightly", ModeAuto}, "me") == nil || f.policy.Set(Settings{"stable", "yolo"}, "me") == nil {
		t.Fatal("accepted invalid settings")
	}
	f.policy.Audit = func(any) error { return errors.New("disk full") }
	if f.policy.Set(Settings{"beta", ModeNotify}, "me") == nil || f.policy.Status().Channel != "stable" {
		t.Fatal("changed settings without a record")
	}
}

func TestSettingsRoutes(t *testing.T) {
	f := newFixture(t, true)
	mux := http.NewServeMux()
	API{Policy: f.policy}.Register(mux)
	put := func(body string) *httptest.ResponseRecorder {
		w := httptest.NewRecorder()
		mux.ServeHTTP(w, httptest.NewRequest("PUT", "/api/v1/releases/settings", strings.NewReader(body)))
		return w
	}
	if w := put(`{"channel":"beta","mode":"notify"}`); w.Code != 200 || !strings.Contains(w.Body.String(), `"beta"`) {
		t.Fatalf("%d %s", w.Code, w.Body)
	}
	if put(`{"channel":"beta","mode":"yolo"}`).Code != 400 || put(`{"channel":"beta","mode":"auto","extra":1}`).Code != 400 {
		t.Fatal("accepted a bad request")
	}
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, httptest.NewRequest("GET", "/api/v1/releases/settings", nil))
	if !strings.Contains(w.Body.String(), `"notify"`) {
		t.Fatal(w.Body.String())
	}
}
