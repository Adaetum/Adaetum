package reset

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Adaetum/Adaetum/installer/engine"
	"github.com/Adaetum/Adaetum/installer/sys"
)

const envText = `TAILSCALE_DOMAIN=example.ts.net
TAILSCALE_OAUTH_CLIENT_ID=node-client
TAILSCALE_OPERATOR_OAUTH_CLIENT_ID=op-client
TAILSCALE_OPERATOR_OAUTH_CLIENT_SECRET=op-secret
CLOUDFLARE_API_TOKEN=cf-token
CLOUDFLARE_ACCOUNT_ID=acct
R2_BUCKET=iso
`

const (
	devicesList = `{"devices":[
		{"id":"1","hostname":"node-42187","name":"node-42187.example.ts.net","tags":["tag:rocky10","tag:server","tag:cluster"],"lastSeen":"2026-11-11T02:09:00Z","isHost":true},
		{"id":"2","hostname":"adaetum-ingress-0","tags":["tag:k8s"],"isHost":false}],"deleted":[]}`
	devicesGone = `{"devices":[],"deleted":["1","2"]}`
	clientList  = `{"clients":[{"id":"node-client","description":"Adaetum node enrollment"},{"id":"op-client","description":"Adaetum Kubernetes operator"}],"deleted":[]}`
	clientGone  = `{"clients":[],"deleted":["node-client","op-client"]}`
	logFound    = `{"found":{"node-42187":38},"deleted":{}}`
	logGone     = `{"found":{},"deleted":{"node-42187":38}}`
)

func ok(out string) sys.Result { return sys.Result{Stdout: out} }

func harness(t *testing.T, env string) (*Step, *sys.Fake) {
	t.Helper()
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, ".env"), []byte(env), 0o600); err != nil {
		t.Fatal(err)
	}
	const ts = "python3 ./tasks/scripts/bootstrap-tailscale.py"
	const cf = "python3 ./tasks/scripts/bootstrap-cloudflare.py"
	f := &sys.Fake{Rules: []sys.Rule{
		// Rules with a confirmation come first: the listing call is a prefix of them.
		{Prefix: ts + " --oauth-credentials-stdin --tailnet example.ts.net --cluster-tag tag:cluster --reset-devices --reset-confirm-ids 1,2", Result: ok(devicesGone)},
		{Prefix: ts + " --oauth-credentials-stdin --tailnet example.ts.net --cluster-tag tag:cluster --reset-devices --reset-confirm-ids 1", Result: ok(`{"devices":[],"deleted":["1"]}`)},
		{Prefix: ts + " --oauth-credentials-stdin --tailnet example.ts.net --cluster-tag tag:cluster --reset-devices", Result: ok(devicesList)},
		{Prefix: ts + " --user-token-stdin --tailnet example.ts.net --reset-oauth-clients --reset-client-ids node-client,op-client --reset-confirm-ids node-client,op-client", Result: ok(clientGone)},
		{Prefix: ts + " --user-token-stdin --tailnet example.ts.net --reset-oauth-clients", Result: ok(clientList)},
		{Prefix: cf + " --token-stdin --account-id acct --bucket iso --delete-host-logs node-42187 --delete-host-logs-confirm", Result: ok(logGone)},
		{Prefix: cf + " --token-stdin --account-id acct --bucket iso --delete-host-logs node-42187", Result: ok(logFound)},
	}}
	return New(f, dir, ""), f
}

func run(t *testing.T, s *Step, answers map[string]string, token string) error {
	t.Helper()
	secrets := map[string]string{}
	if token != "" {
		secrets[engine.SecretVar("reset-tailscale-token")] = token
	}
	return s.Apply(context.Background(), &engine.Env{}, engine.Headless{Answers: answers, Getenv: func(k string) string { return secrets[k] }})
}

func anyCall(f *sys.Fake, sub string) bool {
	for _, c := range f.Calls {
		if strings.Contains(c, sub) {
			return true
		}
	}
	return false
}

func TestAHeadlessRunDeletesNothingUnlessToldTo(t *testing.T) {
	s, f := harness(t, envText)
	// No reset-what answer: the default choice is "Nothing, just look".
	if err := run(t, s, nil, "ts-token"); !errors.Is(err, engine.ErrDeclined) {
		t.Fatalf("err = %v, want the run to stop at the menu", err)
	}
	for _, forbidden := range []string{"--reset-confirm-ids", "--delete-host-logs-confirm", "--reset-oauth-clients"} {
		if anyCall(f, forbidden) {
			t.Errorf("a look-only run ran a delete: %s", forbidden)
		}
	}
	if !anyCall(f, "--reset-devices") || !anyCall(f, "--delete-host-logs node-42187") {
		t.Errorf("the reset did not list what it would remove: %v", f.Calls)
	}
}

func TestChoosingEverythingStillNeedsTheFinalYes(t *testing.T) {
	s, f := harness(t, envText)
	err := run(t, s, map[string]string{"reset-what": "everything"}, "ts-token") // no reset-confirm
	if !errors.Is(err, engine.ErrDeclined) {
		t.Fatalf("err = %v, want the final confirmation to decline by default", err)
	}
	if anyCall(f, "--reset-confirm-ids") || anyCall(f, "--delete-host-logs-confirm") {
		t.Error("deleted without the final yes")
	}
}

func TestConfirmingRemovesExactlyWhatWasListed(t *testing.T) {
	s, f := harness(t, envText)
	if err := run(t, s, map[string]string{"reset-what": "everything", "reset-confirm": "yes"}, "ts-token"); err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{
		"--reset-devices --reset-confirm-ids 1,2",
		"--delete-host-logs node-42187 --delete-host-logs-confirm",
		"--reset-oauth-clients --reset-client-ids node-client,op-client --reset-confirm-ids node-client,op-client",
	} {
		if !anyCall(f, want) {
			t.Errorf("missing call %q in %v", want, f.Calls)
		}
	}
	// Only the host's logs are cleaned, never the operator's proxy device.
	if anyCall(f, "adaetum-ingress-0") {
		t.Error("tried to clean logs for a proxy device")
	}
}

func TestDevicesAndLogsOnlyKeepsTheOAuthClientsAndNeverAsksForAToken(t *testing.T) {
	s, f := harness(t, envText)
	// No token in the environment: asking for one would fail the run.
	if err := run(t, s, map[string]string{"reset-what": "devices and logs", "reset-confirm": "yes"}, ""); err != nil {
		t.Fatal(err)
	}
	if anyCall(f, "--reset-oauth-clients") {
		t.Error("touched the OAuth clients when told to keep them")
	}
	if !anyCall(f, "--reset-devices --reset-confirm-ids 1,2") {
		t.Error("devices should have been removed")
	}
}

func TestPickingDevicesOneByOneRemovesOnlyThoseAndTheirHostLogs(t *testing.T) {
	s, f := harness(t, envText)
	answers := map[string]string{"reset-what": "choose", "reset-device-1": "yes", "reset-device-2": "no", "reset-clients": "no", "reset-confirm": "yes"}
	if err := run(t, s, answers, ""); err != nil {
		t.Fatal(err)
	}
	if !anyCall(f, "--reset-devices --reset-confirm-ids 1") || anyCall(f, "--reset-confirm-ids 1,2") {
		t.Errorf("wrong devices removed: %v", f.Calls)
	}
	if !anyCall(f, "--delete-host-logs node-42187 --delete-host-logs-confirm") {
		t.Error("the removed host's logs should have been cleaned")
	}
	if anyCall(f, "--reset-oauth-clients") {
		t.Error("removed OAuth clients that were declined")
	}
}

func TestOAuthClientsOnlyLeavesDevicesAlone(t *testing.T) {
	s, f := harness(t, envText)
	if err := run(t, s, map[string]string{"reset-what": "oauth", "reset-confirm": "yes"}, "ts-token"); err != nil {
		t.Fatal(err)
	}
	if anyCall(f, "--reset-devices --reset-confirm-ids") || anyCall(f, "--delete-host-logs-confirm") {
		t.Error("removed devices or logs when only the OAuth clients were chosen")
	}
	if !anyCall(f, "--reset-oauth-clients --reset-client-ids node-client,op-client --reset-confirm-ids") {
		t.Error("the OAuth clients were not removed")
	}
}

func TestSecretsReachTheToolsOnStandardInputOnly(t *testing.T) {
	s, f := harness(t, envText)
	if err := run(t, s, map[string]string{"reset-what": "everything", "reset-confirm": "yes"}, "ts-token"); err != nil {
		t.Fatal(err)
	}
	for _, c := range f.Calls {
		for _, secret := range []string{"op-secret", "cf-token", "ts-token"} {
			if strings.Contains(c, secret) {
				t.Errorf("a secret appeared in an argument list: %s", c)
			}
		}
	}
	joined := strings.Join(f.Inputs, "|")
	for _, want := range []string{"op-client\nop-secret\n", "cf-token", "ts-token"} {
		if !strings.Contains(joined, want) {
			t.Errorf("expected %q on standard input", want)
		}
	}
}

func TestWithoutATokenTheOAuthClientsAreKept(t *testing.T) {
	s, f := harness(t, envText)
	if err := run(t, s, map[string]string{"reset-what": "everything", "reset-confirm": "yes"}, ""); err != nil {
		t.Fatal(err)
	}
	if anyCall(f, "--reset-oauth-clients") {
		t.Error("touched OAuth clients without the access token that is needed to remove them")
	}
	if !anyCall(f, "--reset-devices --reset-confirm-ids 1,2") {
		t.Error("devices should still be removed without the token")
	}
}

func TestAnEnvWithoutTheOperatorClientIsBlocked(t *testing.T) {
	s, _ := harness(t, "TAILSCALE_DOMAIN=example.ts.net\n")
	finding, err := s.Check(context.Background(), &engine.Env{})
	if err != nil || finding.Status != engine.Blocked {
		t.Fatalf("finding = %+v, err = %v", finding, err)
	}
}

// ---- the SSH rule setup adds to the tailnet policy -------------------------------------

const envWithNodeSecret = envText + "TAILSCALE_OAUTH_CLIENT_SECRET=node-secret\n"

const sshTool = "python3 ./tasks/scripts/bootstrap-tailscale.py --oauth-credentials-stdin --tailnet example.ts.net --cluster-tag tag:cluster --reset-ssh-rule"

// sshHarness is harness plus the two answers the SSH-rule tool gives: the listing, and the removal.
func sshHarness(t *testing.T, env string, listing sys.Result) (*Step, *sys.Fake) {
	t.Helper()
	s, f := harness(t, env)
	f.Rules = append([]sys.Rule{
		{Prefix: sshTool + " --reset-confirm-ssh", Result: ok(`{"rules":[],"deleted":1}`)},
		{Prefix: sshTool, Result: listing},
	}, f.Rules...)
	return s, f
}

func indexOfCall(f *sys.Fake, sub string) int {
	for i, c := range f.Calls {
		if strings.Contains(c, sub) {
			return i
		}
	}
	return -1
}

func TestEverythingAlsoRemovesTheSSHRuleBeforeTheOAuthClientThatCanEditTheTailnetPolicy(t *testing.T) {
	s, f := sshHarness(t, envWithNodeSecret, ok(`{"rules":[{"action":"accept"}],"deleted":0}`))
	if err := run(t, s, map[string]string{"reset-what": "everything", "reset-confirm": "yes"}, "ts-token"); err != nil {
		t.Fatal(err)
	}
	rule := indexOfCall(f, "--reset-ssh-rule --reset-confirm-ssh")
	clients := indexOfCall(f, "--reset-oauth-clients --reset-client-ids node-client,op-client --reset-confirm-ids")
	if rule < 0 {
		t.Fatalf("the SSH rule was not removed: %v", f.Calls)
	}
	if clients >= 0 && rule > clients {
		t.Errorf("the SSH rule was removed after the node OAuth client that edits the policy was deleted")
	}
	// The node client's secret reaches the tool on standard input, never in the arguments.
	for i, c := range f.Calls {
		if strings.Contains(c, "node-secret") {
			t.Errorf("a secret appeared on the command line: %q", c)
		}
		if strings.Contains(c, "--reset-ssh-rule") && !strings.Contains(f.Inputs[i], "node-client\nnode-secret\n") {
			t.Errorf("the SSH rule tool did not get the node OAuth client on stdin: %q", f.Inputs[i])
		}
	}
}

func TestLookingOnlyNeverTouchesTheSSHRule(t *testing.T) {
	s, f := sshHarness(t, envWithNodeSecret, ok(`{"rules":[{"action":"accept"}],"deleted":0}`))
	if err := run(t, s, nil, ""); !errors.Is(err, engine.ErrDeclined) {
		t.Fatalf("err = %v", err)
	}
	if anyCall(f, "--reset-confirm-ssh") {
		t.Error("a look-only run removed the SSH rule")
	}
}

func TestDevicesAndLogsOnlyKeepsTheSSHRule(t *testing.T) {
	s, f := sshHarness(t, envWithNodeSecret, ok(`{"rules":[{"action":"accept"}],"deleted":0}`))
	if err := run(t, s, map[string]string{"reset-what": "devices and logs", "reset-confirm": "yes"}, ""); err != nil {
		t.Fatal(err)
	}
	if anyCall(f, "--reset-confirm-ssh") {
		t.Error("removed the cluster-wide SSH rule when only devices and logs were chosen")
	}
}

func TestWithoutTheNodeClientSecretTheSSHRuleIsNotLookedFor(t *testing.T) {
	s, f := sshHarness(t, envText, ok(`{"rules":[{"action":"accept"}],"deleted":0}`))
	if err := run(t, s, map[string]string{"reset-what": "everything", "reset-confirm": "yes"}, "ts-token"); err != nil {
		t.Fatal(err)
	}
	if anyCall(f, "--reset-ssh-rule") {
		t.Error("looked for the SSH rule without the credentials that can read the policy")
	}
}

func TestAFailedSSHRuleLookupIsReportedAndDoesNotBlockTheRest(t *testing.T) {
	s, f := sshHarness(t, envWithNodeSecret, sys.Result{ExitCode: 1, Stderr: "policy_file scope missing"})
	if err := run(t, s, map[string]string{"reset-what": "everything", "reset-confirm": "yes"}, "ts-token"); err != nil {
		t.Fatalf("a failed rule lookup stopped the reset: %v", err)
	}
	if anyCall(f, "--reset-confirm-ssh") {
		t.Error("tried to remove a rule it could not see")
	}
	if !anyCall(f, "--reset-devices --reset-confirm-ids 1,2") {
		t.Error("devices were not removed after the rule lookup failed")
	}
}

func TestQuickRemovesDevicesAndLogsOnlyAndNeedsNoMenuOrToken(t *testing.T) {
	s, f := harness(t, envText)
	s.Quick = true
	// No reset-what answer and no token: quick must not ask either.
	if err := run(t, s, map[string]string{"reset-confirm": "yes"}, ""); err != nil {
		t.Fatal(err)
	}
	if !anyCall(f, "--reset-devices --reset-confirm-ids 1,2") || !anyCall(f, "--delete-host-logs-confirm") {
		t.Error("quick should remove the devices and their logs")
	}
	for _, kept := range []string{"--reset-oauth-clients", "--reset-ssh-rule"} {
		if anyCall(f, kept) {
			t.Errorf("quick touched %s, which it must keep", kept)
		}
	}
}

func TestQuickStillNeedsTheFinalYes(t *testing.T) {
	s, f := harness(t, envText)
	s.Quick = true
	if err := run(t, s, nil, ""); !errors.Is(err, engine.ErrDeclined) {
		t.Fatalf("a quick run with no confirmation should be declined, got %v", err)
	}
	if anyCall(f, "--reset-confirm-ids") || anyCall(f, "--delete-host-logs-confirm") {
		t.Error("quick deleted something without the final yes")
	}
}
