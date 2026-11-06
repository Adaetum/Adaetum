package ansiblerun

import (
	"context"
	"errors"
	"io"
)

// KubernetesJob is the planned Executor for the cluster agent running as a pod: it
// would create a Kubernetes Job from the existing Ansible runner's pod template
// (which already carries Ansible, the repository, and the SSH identity), run the
// playbook there, and stream the pod's log back as the output. The manifest would
// be checked with `kubectl --dry-run=server` first.
//
// STUB: returns an error. Until it exists the launcher needs ansible-playbook on
// the machine the agent runs on (LocalExec).
type KubernetesJob struct{}

// Run is not implemented.
func (KubernetesJob) Run(context.Context, ExecSpec, io.Writer) (int, error) {
	return 0, errors.New("running playbooks as Kubernetes Jobs is not built yet")
}
