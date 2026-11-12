package intake

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/Adaetum/Adaetum/agent/internal/runs"
)

type files map[string]string

func (f files) Read(path string) ([]byte, error) {
	if data, ok := f[path]; ok {
		return []byte(data), nil
	}
	return nil, errors.New("no such file")
}

type clk struct{}

func (clk) Now() time.Time                             { return time.Date(2026, 11, 12, 9, 0, 0, 0, time.UTC) }
func (clk) Sleep(context.Context, time.Duration) error { return nil }

const good = `# the first-boot runtime payload
BOOTSTRAP_BACKUP_PASSPHRASE="a very secret passphrase"
BOOTSTRAP_BACKUP_TO_R2=1
BOOTSTRAP_BACKUP_URL=https://bootstrap.example.cloud/backup
ARGOCD_GITHUB_TOKEN=ghp_abcdefghijklmnopqrstuvwxyz0123456789
GITEA_SEED_SOURCE_TOKEN='github_pat_abcdefghijklmnopqrstuvwxyz'
BOOTSTRAP_BUNDLE_URL=https://bootstrap.example.cloud/iso/firstboot-payload/ansible-runner-bundle.tar.gz
`

func run(t *testing.T, f files, inputs map[string]string) (runs.Snapshot, string) {
	t.Helper()
	var logged strings.Builder
	m, err := runs.NewManager(runs.Options{Journal: &runs.MemoryJournal{}, Clock: clk{}, OnRecord: func(r runs.Record) { logged.WriteString(r.Line() + "\n") }})
	if err != nil {
		t.Fatal(err)
	}
	defer m.Close()
	if err := m.Register(New(f)); err != nil {
		t.Fatalf("malformed: %v", err)
	}
	id, err := m.Start(context.Background(), runs.StartRequest{Procedure: Name, Inputs: inputs})
	if err != nil {
		t.Fatal(err)
	}
	_ = m.Wait(context.Background(), id)
	snap, _ := m.Get(id)
	return snap, logged.String()
}

func with(replace map[string]string) files {
	out := good
	for old, new := range replace {
		out = strings.Replace(out, old, new, 1)
	}
	return files{defaultPayload: out}
}

func TestAGoodPayloadPasses(t *testing.T) {
	snap, _ := run(t, files{defaultPayload: good}, nil)
	if snap.State != runs.StateSucceeded {
		t.Fatalf("got %s %q", snap.State, snap.Detail)
	}
}

func TestEachContractRuleIsEnforcedAndNamesTheKeyNotTheValue(t *testing.T) {
	cases := map[string]struct {
		change map[string]string
		want   string
	}{
		"no passphrase":      {map[string]string{`BOOTSTRAP_BACKUP_PASSPHRASE="a very secret passphrase"`: ""}, "BOOTSTRAP_BACKUP_PASSPHRASE"},
		"backup off":         {map[string]string{"BOOTSTRAP_BACKUP_TO_R2=1": "BOOTSTRAP_BACKUP_TO_R2=0"}, "BOOTSTRAP_BACKUP_TO_R2"},
		"backup url wrong":   {map[string]string{"/backup": "/restore"}, "BOOTSTRAP_BACKUP_URL"},
		"backup url query":   {map[string]string{"/backup": "/backup?token=abc"}, "BOOTSTRAP_BACKUP_URL"},
		"argocd token":       {map[string]string{"ghp_abcdefghijklmnopqrstuvwxyz0123456789": "not-a-token"}, "ARGOCD_GITHUB_TOKEN"},
		"seed token missing": {map[string]string{"GITEA_SEED_SOURCE_TOKEN='github_pat_abcdefghijklmnopqrstuvwxyz'": ""}, "GITEA_SEED_SOURCE_TOKEN"},
		"cache busting":      {map[string]string{"tar.gz": "tar.gz?v=3"}, "BOOTSTRAP_BUNDLE_URL"},
	}
	for name, c := range cases {
		snap, logged := run(t, with(c.change), nil)
		if snap.State != runs.StateFailed || snap.Failure != runs.FailBroken || snap.Code != runs.CodeConfig || !strings.Contains(snap.Detail, c.want) || snap.FailedStep != "payload-contract" {
			t.Errorf("%s: got %s/%s/%s at %s %q", name, snap.State, snap.Failure, snap.Code, snap.FailedStep, snap.Detail)
		}
		for _, secret := range []string{"a very secret passphrase", "abcdefghijklmnopqrstuvwxyz0123456789", "not-a-token", "token=abc"} {
			if strings.Contains(snap.Detail, secret) || strings.Contains(logged, secret) {
				t.Errorf("%s: a value leaked into the result or the log: %q", name, secret)
			}
		}
	}
}

func TestAllTheProblemsAreReportedTogether(t *testing.T) {
	snap, _ := run(t, files{defaultPayload: "BOOTSTRAP_BACKUP_TO_R2=0\n"}, nil)
	for _, want := range []string{"PASSPHRASE", "BOOTSTRAP_BACKUP_TO_R2", "BOOTSTRAP_BACKUP_URL", "ARGOCD_GITHUB_TOKEN", "GITEA_SEED_SOURCE_TOKEN"} {
		if !strings.Contains(snap.Detail, want) {
			t.Errorf("the detail should mention %s: %q", want, snap.Detail)
		}
	}
}

func TestFormatProblemsAreCaughtFirstAndNameOnlyTheLine(t *testing.T) {
	for name, body := range map[string]string{
		"missing":       "",
		"empty":         "   \n\n",
		"no equals":     "this line is a secret and has no equals sign\n",
		"bad key":       "not a key=value\n",
		"export":        "export FOO=bar\n",
		"leading digit": "1ABC=value\n",
	} {
		f := files{}
		if name != "missing" {
			f[defaultPayload] = body
		}
		snap, logged := run(t, f, nil)
		if snap.State != runs.StateFailed || snap.FailedStep != "payload-format" {
			t.Errorf("%s: got %s at %s %q", name, snap.State, snap.FailedStep, snap.Detail)
		}
		if strings.Contains(snap.Detail, "secret") || strings.Contains(logged, "has no equals sign") {
			t.Errorf("%s: the offending line leaked: %q", name, snap.Detail)
		}
	}
}

func TestTheFirstDuplicateKeyWinsLikeTheOldScript(t *testing.T) {
	f := files{defaultPayload: good + "BOOTSTRAP_BACKUP_TO_R2=0\n"}
	if snap, _ := run(t, f, nil); snap.State != runs.StateSucceeded {
		t.Fatalf("the first BOOTSTRAP_BACKUP_TO_R2 is 1, so this passes: %s %q", snap.State, snap.Detail)
	}
}

func TestQuotesAndWindowsLineEndingsAreAccepted(t *testing.T) {
	crlf := strings.ReplaceAll(good, "\n", "\r\n")
	if snap, _ := run(t, files{defaultPayload: crlf}, nil); snap.State != runs.StateSucceeded {
		t.Fatalf("got %s %q", snap.State, snap.Detail)
	}
}

func TestAnotherPayloadFileCanBeNamedButOnlyAnOrdinaryPath(t *testing.T) {
	if snap, _ := run(t, files{"/srv/payload.env": good}, map[string]string{InputPayload: "/srv/payload.env"}); snap.State != runs.StateSucceeded {
		t.Fatalf("got %s %q", snap.State, snap.Detail)
	}
	for _, bad := range []string{"relative.env", "/etc/../shadow", "/etc/a b", "/"} {
		snap, _ := run(t, files{}, map[string]string{InputPayload: bad})
		if snap.State != runs.StateFailed || snap.Code != runs.CodeConfig {
			t.Errorf("%q: got %s/%s", bad, snap.State, snap.Code)
		}
	}
}

func TestTheProcedureChangesNothing(t *testing.T) {
	m, _ := runs.NewManager(runs.Options{Journal: &runs.MemoryJournal{}, Clock: clk{}})
	defer m.Close()
	_ = m.Register(New(files{defaultPayload: good}))
	for _, p := range m.Procedures() {
		for _, step := range p.Steps {
			if step.Kind != "convergent" {
				t.Errorf("%s is %s", step.Name, step.Kind)
			}
		}
	}
	id, _ := m.Start(context.Background(), runs.StartRequest{Procedure: Name})
	_ = m.Wait(context.Background(), id)
	for _, rec := range m.Events(id) {
		if rec.Kind == runs.KindStepApply || rec.Kind == runs.KindStepApplied {
			t.Fatalf("intake must never apply anything: %+v", rec)
		}
	}
}
