package inspect

import (
	"context"
	"strings"
	"testing"
)

// recorder is an Inspector whose commands are recorded, not run.
func recorder(output string) (*Inspector, *[][]string) {
	var calls [][]string
	return &Inspector{Run: func(_ context.Context, name string, args ...string) (string, error) {
		calls = append(calls, append([]string{name}, args...))
		return output, nil
	}}, &calls
}

func TestEachRunnerReadIsOneFixedCommandInsideTheRunnerPod(t *testing.T) {
	cases := []struct {
		name    string
		what    RunnerRead
		arg     string
		command string
	}{
		{"log", ReadRunLog, "", "tail -c 400000 /var/log/ansible-cron.log"},
		{"list", ReadPlaybookList, "", "ls /opt/adaetum/ansible/playbooks"},
		{"head", ReadPlaybookHead, "day2.yml", "head -n 40 /opt/adaetum/ansible/playbooks/day2.yml"},
		{"profile", ReadPlatformProfile, "", "cat /opt/adaetum/platform.yaml"},
	}
	for _, c := range cases {
		inspector, calls := recorder("ok")
		if _, err := inspector.ReadRunnerPod(context.Background(), "ansible-runner-749cf49b8c-6njbn", c.what, c.arg); err != nil {
			t.Fatalf("%s: %v", c.name, err)
		}
		call := (*calls)[0]
		if call[0] != rke2Kubectl {
			t.Fatalf("%s: must run the allowlisted kubectl, got %q", c.name, call[0])
		}
		joined := strings.Join(call[1:], " ")
		want := "--kubeconfig " + rke2Kubeconfig + " exec -n ansible ansible-runner-749cf49b8c-6njbn -- " + c.command
		if joined != want {
			t.Fatalf("%s:\n got %s\nwant %s", c.name, joined, want)
		}
	}
}

func TestOnlyTheRunnerPodCanBeReadFrom(t *testing.T) {
	for _, pod := range []string{"", "kube-apiserver-node-a", "ansible-runner", "ansible-runner-x; id", "ansible-runner-x --all", "../ansible-runner-x", "ANSIBLE-RUNNER-X"} {
		inspector, calls := recorder("")
		if _, err := inspector.ReadRunnerPod(context.Background(), pod, ReadRunLog, ""); err == nil {
			t.Errorf("pod %q must be refused", pod)
		}
		if len(*calls) != 0 {
			t.Errorf("pod %q: nothing may run for a refused pod", pod)
		}
	}
}

func TestPlaybookNamesCannotEscapeThePlaybookDirectory(t *testing.T) {
	for _, name := range []string{"", "../platform.yaml", "../../etc/shadow", "a/b.yml", "day2.yml; id", "day2", "DAY2.yml", "day2.yml extra", ".yml", "-n.yml"} {
		inspector, calls := recorder("")
		_, err := inspector.ReadRunnerPod(context.Background(), "ansible-runner-x", ReadPlaybookHead, name)
		if name == "-n.yml" {
			// A leading dash matches the name pattern but is a path argument after the
			// directory prefix, so it can never be read as an option.
			if err != nil {
				t.Errorf("%q is a harmless file name: %v", name, err)
			}
			if joined := strings.Join((*calls)[0], " "); !strings.HasSuffix(joined, "/playbooks/-n.yml") {
				t.Errorf("a leading dash must stay inside the path: %s", joined)
			}
			continue
		}
		if err == nil || len(*calls) != 0 {
			t.Errorf("playbook name %q must be refused without running anything", name)
		}
	}
}

func TestAnUnknownReadIsRefused(t *testing.T) {
	inspector, calls := recorder("")
	if _, err := inspector.ReadRunnerPod(context.Background(), "ansible-runner-x", RunnerRead(99), ""); err == nil || len(*calls) != 0 {
		t.Fatal("there is no general way to run a command in the pod")
	}
}
