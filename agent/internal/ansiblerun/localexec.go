package ansiblerun

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"syscall"
	"time"
)

// LocalExec runs ansible-playbook as a program on this machine.
//
// The program, directory and inventory come from how the agent was started (an
// operator's decision), never from a request. The playbook and limit come from
// Validate. The command is run directly, with no shell, so nothing in a value can be
// interpreted as syntax.
type LocalExec struct {
	// AnsiblePlaybook is the ansible-playbook program.
	AnsiblePlaybook string
	// Dir is the repository's ansible directory (it holds ansible.cfg and playbooks/).
	Dir string
	// Inventory overrides the inventory in ansible.cfg when set.
	Inventory string
}

// Run executes one playbook and streams its output.
func (e LocalExec) Run(ctx context.Context, spec ExecSpec, output io.Writer) (int, error) {
	args := []string{spec.Playbook, "--limit", spec.Limit, "-e", "@" + spec.ExtraVarsFile}
	if spec.Check {
		// --diff shows what a change would be, which is the point of rehearsing.
		args = append(args, "--check", "--diff")
	}
	if e.Inventory != "" {
		args = append(args, "-i", e.Inventory)
	}

	cmd := exec.CommandContext(ctx, e.AnsiblePlaybook, args...)
	cmd.Dir = e.Dir
	cmd.Env = append(os.Environ(),
		"ANSIBLE_CONFIG="+filepath.Join(e.Dir, "ansible.cfg"),
		"ANSIBLE_NOCOLOR=1",
		"ANSIBLE_FORCE_COLOR=0",
		"PYTHONUNBUFFERED=1",
	)
	cmd.Stdout, cmd.Stderr = output, output
	// Its own process group, so cancelling stops Ansible and everything it started
	// (ssh connections, forked workers), not only the top process.
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	cmd.Cancel = func() error { return syscall.Kill(-cmd.Process.Pid, syscall.SIGTERM) }
	cmd.WaitDelay = 15 * time.Second

	err := cmd.Run()
	var exitErr *exec.ExitError
	switch {
	case err == nil:
		return 0, nil
	case errors.As(err, &exitErr):
		return exitErr.ExitCode(), nil
	case ctx.Err() != nil:
		return -1, nil // stopped by cancel or timeout; the manager reports which
	default:
		return 0, fmt.Errorf("could not run %s: %w", e.AnsiblePlaybook, err)
	}
}
