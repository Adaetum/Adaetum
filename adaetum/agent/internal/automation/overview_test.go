package automation

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/Adaetum/Adaetum/agent/internal/inspect"
)

const deploymentJSON = `{"items":[
 {"metadata":{"name":"some-other"},"spec":{"replicas":1,"template":{"spec":{"containers":[{"image":"x"}]}}}},
 {"metadata":{"name":"ansible-runner"},"spec":{"replicas":1,"template":{"spec":{"containers":[{
   "image":"registry.example.ts.net/gitea-admin/ansible-runner:build-1","imagePullPolicy":"Always",
   "env":[{"name":"CRON_SCHEDULE","value":"*/10 * * * *"},{"name":"ANSIBLE_PLAYBOOK","value":"playbooks/day2.yml"},
          {"name":"ANSIBLE_INVENTORY","value":"node-inventory.yml"},
          {"name":"GITHUB_APP_PRIVATE_KEY_B64","valueFrom":{"secretKeyRef":{"name":"x","key":"y"}}}]}]}}},
  "status":{"readyReplicas":1}}]}`

const podsJSON = `{"items":[
 {"metadata":{"name":"ansible-runner-old-abc"},"spec":{"nodeName":"node-50449"},"status":{"phase":"Succeeded","containerStatuses":[{"restartCount":0}]}},
 {"metadata":{"name":"ansible-runner-749cf49b8c-6njbn"},"spec":{"nodeName":"node-56843"},"status":{"phase":"Running","startTime":"2026-11-03T09:29:00Z","containerStatuses":[{"restartCount":2}]}},
 {"metadata":{"name":"unrelated"},"spec":{"nodeName":"node-1"},"status":{"phase":"Running"}}]}`

const day2Head = `---
# The recurring runner owns bounded day-two maintenance. It deliberately does
# not import platform-bootstrap.yml: replaying bootstrap would regenerate local
# credentials.
- import_playbook: _break_glass.yml

- name: Reconcile routine host state
  hosts: all
`

const platformYAML = `apiVersion: adaetum.io/v1alpha1
kind: PlatformProfile
spec:
  hostMaintenance:
    enabled: true
    updates:
      policy: full
    reboots:
      startTime: 03:00
      endTime: 06:00
      timeZone: America/Chicago
  imageCleanup:
    enabled: true
    level: aggressive
    time: '04:00'
    maxImagesPerRun: 100
    onlyIfDiskOverPercent: 0
    checkOnly: false
  imageSharing:
    enabled: false
    registries: ["*"]
`

type fakeSource struct {
	log         string
	logErr      error
	deployments string
	pods        string
	reads       []string
}

func (f *fakeSource) KubectlJSON(_ context.Context, resources string, _ ...string) ([]byte, error) {
	if resources == "deployments" {
		return []byte(f.deployments), nil
	}
	return []byte(f.pods), nil
}

func (f *fakeSource) ReadRunnerPod(_ context.Context, pod string, what inspect.RunnerRead, arg string) (string, error) {
	f.reads = append(f.reads, pod)
	switch what {
	case inspect.ReadRunLog:
		return f.log, f.logErr
	case inspect.ReadPlaybookList:
		return "_break_glass.yml\nargocd.yml\nday2.yml\nREADME.md\n", nil
	case inspect.ReadPlaybookHead:
		if arg == "argocd.yml" {
			return "", errors.New("exec failed")
		}
		return day2Head, nil
	default:
		return platformYAML, nil
	}
}

func collect(f *fakeSource) Overview {
	collector := &Collector{Source: f, Now: func() time.Time { return time.Date(2026, 11, 4, 4, 0, 0, 0, time.UTC) }}
	return collector.Collect(context.Background())
}

func healthySource() *fakeSource {
	return &fakeSource{log: failingRun + successRun("2026-11-04T01:00:00Z", 2), deployments: deploymentJSON, pods: podsJSON}
}

func TestCollectGathersEverythingTheTabShows(t *testing.T) {
	overview := collect(healthySource())

	if len(overview.Errors) != 0 {
		t.Fatalf("unexpected errors: %v", overview.Errors)
	}
	runner := overview.Runner
	if !runner.Found || runner.Schedule != "*/10 * * * *" || runner.Playbook != "playbooks/day2.yml" || runner.Ready != 1 || runner.PullPolicy != "Always" {
		t.Fatalf("unexpected runner: %+v", runner)
	}
	if runner.Pod == nil || runner.Pod.Name != "ansible-runner-749cf49b8c-6njbn" || runner.Pod.Node != "node-56843" || runner.Pod.Restarts != 2 {
		t.Fatalf("the running pod must be chosen over an old one: %+v", runner.Pod)
	}
	if len(overview.Runs) != 2 || overview.Runs[0].Status != StatusSuccess || overview.Health.Total != 2 {
		t.Fatalf("unexpected runs: %+v", overview.Runs)
	}
}

func TestPlaybooksAreListedWithDescriptionsAndInternalOnesHidden(t *testing.T) {
	overview := collect(healthySource())
	files := []string{}
	for _, playbook := range overview.Playbooks {
		files = append(files, playbook.File)
	}
	if strings.Join(files, ",") != "argocd.yml,day2.yml" {
		t.Fatalf("underscore playbooks and non-playbooks must be hidden: %v", files)
	}
	day2 := overview.Playbooks[1]
	if !strings.HasPrefix(day2.Description, "The recurring runner owns bounded day-two maintenance.") || !strings.Contains(day2.Description, "regenerate local credentials") {
		t.Fatalf("the header comment is the description: %q", day2.Description)
	}
	if len(day2.Plays) != 2 || day2.Plays[0] != "runs _break_glass" || day2.Plays[1] != "Reconcile routine host state" {
		t.Fatalf("unexpected plays: %v", day2.Plays)
	}
	if overview.Playbooks[0].Description != "" {
		t.Fatal("a playbook whose header could not be read is still listed, without a description")
	}
}

func TestDeclaredPoliciesAreReadFromThePlatformProfile(t *testing.T) {
	declared := collect(healthySource()).Declared
	if declared == nil || declared.ImageCleanup == nil || declared.ImageSharing == nil || declared.HostMaintenance == nil {
		t.Fatalf("all three policies are declared: %+v", declared)
	}
	if c := declared.ImageCleanup; !c.Enabled || c.Level != "aggressive" || c.Time != "04:00" || c.MaxImagesPerRun != 100 {
		t.Fatalf("unexpected cleanup policy: %+v", c)
	}
	if declared.ImageSharing.Enabled || len(declared.ImageSharing.Registries) != 1 {
		t.Fatalf("unexpected sharing policy: %+v", declared.ImageSharing)
	}
	if m := declared.HostMaintenance; !m.Enabled || m.UpdatePolicy != "full" || m.RebootWindow != "03:00 to 06:00 America/Chicago" {
		t.Fatalf("unexpected maintenance policy: %+v", m)
	}
}

func TestAProfileWithoutNewPoliciesDeclaresNone(t *testing.T) {
	declared, err := ParseDeclared("spec:\n  hostMaintenance:\n    enabled: true\n    updates:\n      policy: full\n")
	if err != nil || declared.ImageCleanup != nil || declared.ImageSharing != nil {
		t.Fatalf("an undeclared policy must be nil, not a disabled default: %+v %v", declared, err)
	}
}

func TestEnvironmentValuesFromSecretsAreNeverRead(t *testing.T) {
	// The fixture has a secret-backed variable; only the three plain ones may
	// reach the Runner.
	runner := collect(healthySource()).Runner
	encoded := runner.Image + runner.Schedule + runner.Playbook + runner.Inventory
	if strings.Contains(encoded, "secretKeyRef") || strings.Contains(encoded, "GITHUB_APP") {
		t.Fatalf("a secret reference leaked into the runner state: %+v", runner)
	}
}

func TestAMissingRunnerIsReportedNotFatal(t *testing.T) {
	overview := collect(&fakeSource{deployments: `{"items":[]}`, pods: `{"items":[]}`})
	if overview.Runner.Found || len(overview.Runs) != 0 || len(overview.Errors) != 0 {
		t.Fatalf("no runner is a state to show, not an error: %+v", overview)
	}
}

func TestNothingIsReadInsideAPodThatIsNotRunning(t *testing.T) {
	f := healthySource()
	f.pods = `{"items":[{"metadata":{"name":"ansible-runner-x"},"spec":{"nodeName":"n"},"status":{"phase":"Pending"}}]}`
	overview := collect(f)
	if len(f.reads) != 0 {
		t.Fatalf("a pod that is not running cannot be exec'd into: %v", f.reads)
	}
	if overview.Runner.Pod == nil || overview.Runner.Pod.Phase != "Pending" {
		t.Fatalf("the pod state is still reported: %+v", overview.Runner.Pod)
	}
}

func TestEachFailingPartIsReportedAndTheRestStillShows(t *testing.T) {
	f := healthySource()
	f.logErr = errors.New("tail: cannot open")
	overview := collect(f)
	if len(overview.Errors) != 1 || !strings.Contains(overview.Errors[0], "run history") {
		t.Fatalf("expected exactly the run history to fail: %v", overview.Errors)
	}
	if len(overview.Playbooks) == 0 || overview.Declared == nil {
		t.Fatal("a failing log read must not hide the playbooks or policies")
	}
}

func TestAnInvalidProfileIsAnErrorNotACrash(t *testing.T) {
	if _, err := ParseDeclared("spec: [unterminated"); err == nil {
		t.Fatal("expected an error")
	}
}
