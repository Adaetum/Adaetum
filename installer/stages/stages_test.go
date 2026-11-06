package stages

import (
	"context"
	"testing"

	"github.com/Adaetum/Adaetum/installer/engine"
	"github.com/Adaetum/Adaetum/installer/sys"
)

// A runner that finds no programs: every stage that checks the machine reports
// Blocked or Needed without touching anything.
func nothingInstalled() sys.Runner {
	return &sys.Fake{Rules: []sys.Rule{{Prefix: "", Result: sys.Result{ExitCode: 127}}}}
}

func TestStageIDsAreUnique(t *testing.T) {
	seen := map[string]bool{}
	for _, s := range ISO("/x", nothingInstalled()) {
		if seen[s.ID()] {
			t.Fatalf("duplicate stage id %q", s.ID())
		}
		seen[s.ID()] = true
	}
}

func TestADryRunWalksEveryStageEvenWhenTheFirstIsBlocked(t *testing.T) {
	steps := ISO("/x", nothingInstalled())
	res, err := engine.Run(context.Background(), steps, &engine.Env{DryRun: true}, engine.Headless{})
	if err != nil || len(res) != len(steps) {
		t.Fatalf("err=%v walked %d of %d", err, len(res), len(steps))
	}
	if res[0].Finding.Status != engine.Blocked {
		t.Fatalf("first stage is %s, want blocked", res[0].Finding.Status)
	}
}
