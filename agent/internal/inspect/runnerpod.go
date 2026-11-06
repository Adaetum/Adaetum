package inspect

import (
	"context"
	"fmt"
	"regexp"
)

// The Ansible runner keeps its only record of what each run did in a file inside
// its container, and bakes the repository (playbooks and platform.yaml) into its
// image. To show either, the agent has to read inside the pod, which means
// `kubectl exec`. That is a more powerful verb than the `get` calls everything
// else here uses, so it is not offered generally: only the fixed reads below
// exist, each maps to one fixed command, and the only values that vary are
// validated.

const (
	runnerNamespace = "ansible"
	runnerCodeRoot  = "/opt/adaetum"
	runnerLogPath   = "/var/log/ansible-cron.log"
	// runnerLogTail bounds how much of the log is read. The log grows without
	// limit, and the newest runs are at the end.
	runnerLogTail = "400000"
)

var (
	runnerPodName = regexp.MustCompile(`^ansible-runner-[a-z0-9]+(-[a-z0-9]+)*$`)
	playbookFile  = regexp.MustCompile(`^[a-z0-9_-]+\.ya?ml$`)
)

// RunnerRead names one of the reads the agent may make inside the runner pod.
type RunnerRead int

const (
	// ReadRunLog is the tail of the runner's cron log.
	ReadRunLog RunnerRead = iota
	// ReadPlaybookList lists the playbooks baked into the runner image.
	ReadPlaybookList
	// ReadPlaybookHead is the first lines of one playbook (arg is its file name).
	ReadPlaybookHead
	// ReadPlatformProfile is platform.yaml as the runner sees it.
	ReadPlatformProfile
)

// RunnerNamespace and the helpers below let other packages build the same
// `kubectl get` queries without reaching into this package's constants.
func RunnerNamespace() string { return runnerNamespace }

// ReadRunnerPod performs one of the fixed reads inside the named runner pod.
func (i *Inspector) ReadRunnerPod(ctx context.Context, pod string, what RunnerRead, arg string) (string, error) {
	if !runnerPodName.MatchString(pod) {
		return "", fmt.Errorf("%q is not the runner pod", pod)
	}

	var command []string
	switch what {
	case ReadRunLog:
		command = []string{"tail", "-c", runnerLogTail, runnerLogPath}
	case ReadPlaybookList:
		command = []string{"ls", runnerCodeRoot + "/ansible/playbooks"}
	case ReadPlaybookHead:
		if !playbookFile.MatchString(arg) {
			return "", fmt.Errorf("%q is not a playbook file name", arg)
		}
		command = []string{"head", "-n", "40", runnerCodeRoot + "/ansible/playbooks/" + arg}
	case ReadPlatformProfile:
		command = []string{"cat", runnerCodeRoot + "/platform.yaml"}
	default:
		return "", fmt.Errorf("unknown runner read %d", what)
	}

	args := append([]string{"--kubeconfig", rke2Kubeconfig, "exec", "-n", runnerNamespace, pod, "--"}, command...)
	out, err := i.Run(ctx, rke2Kubectl, args...)
	if err != nil {
		return "", fmt.Errorf("reading %v in %s: %w", command[0], pod, err)
	}
	return out, nil
}

// KubectlJSON runs a read-only `kubectl get` and returns its JSON.
func (i *Inspector) KubectlJSON(ctx context.Context, resources string, extra ...string) ([]byte, error) {
	return i.kubectlGet(ctx, resources, extra...)
}
