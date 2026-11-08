package inspect

import (
	"context"
	"fmt"
	"os/exec"
	"strings"
	"time"
)

// NewRemoteInspector inspects another host over SSH without installing anything
// on it. It exists for developing against machines that must not run the agent
// yet (for example production appliances).
//
// It keeps the same guarantees as local inspection: only allowlisted commands
// run, and the only files readable are the entries in readableFiles. SSH uses BatchMode, so it
// relies on the operator's existing key and never prompts.
func NewRemoteInspector(target string) (*Inspector, error) {
	// A leading dash would let the target be parsed as an ssh option.
	if target == "" || strings.HasPrefix(target, "-") {
		return nil, fmt.Errorf("invalid ssh target %q", target)
	}

	return &Inspector{
		Run: func(ctx context.Context, name string, args ...string) (string, error) {
			if !allowedCommands[name] {
				return "", fmt.Errorf("command %q is not permitted for inspection", name)
			}
			command := shellJoin(append([]string{name}, args...))
			// kubectl needs root for its root-readable kubeconfig; crictl for the
			// container runtime's socket; du for containerd's root-owned data.
			if name == rke2Kubectl || name == rke2Crictl || name == "du" {
				command = "sudo -n " + command
			}
			return sshOutput(ctx, target, command)
		},
		ReadFile: func(path string) ([]byte, error) {
			if !readableFiles[path] {
				return nil, fmt.Errorf("reading %q is not permitted for inspection", path)
			}
			ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
			defer cancel()
			out, err := sshOutput(ctx, target, shellJoin([]string{"cat", path}))
			return []byte(out), err
		},
		Hostname: func() (string, error) {
			ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
			defer cancel()
			out, err := sshOutput(ctx, target, "hostname")
			return strings.TrimSpace(out), err
		},
		// Left empty so Collect asks the remote host with `uname -m`.
		Arch: "",
		Now:  time.Now,
	}, nil
}

// readableFiles is the complete set of files inspection may read from a remote
// host. Local inspection reads the same files.
var readableFiles = map[string]bool{
	"/etc/os-release": true,
	"/proc/uptime":    true,
	"/proc/loadavg":   true,
	"/proc/meminfo":   true,
}

func sshOutput(ctx context.Context, target, command string) (string, error) {
	out, err := exec.CommandContext(ctx, "ssh",
		"-o", "BatchMode=yes",
		"-o", "ConnectTimeout=10",
		// Share one connection across the many short commands of a collection.
		// Only the local ssh master lingers (60s); nothing is left on the target.
		// %C is a short hash, which keeps the socket path under the OS limit.
		"-o", "ControlMaster=auto",
		"-o", "ControlPath=/tmp/adaetum-ssh-%C",
		"-o", "ControlPersist=60",
		target, command,
	).Output()
	return string(out), err
}

// shellJoin quotes each word for the remote shell. Arguments are constants in
// this package, but quoting keeps that true if a caller ever changes.
func shellJoin(words []string) string {
	quoted := make([]string, len(words))
	for index, word := range words {
		quoted[index] = "'" + strings.ReplaceAll(word, "'", `'\''`) + "'"
	}
	return strings.Join(quoted, " ")
}

// normalizeArch maps `uname -m` output to the GOARCH names used locally so the
// report is consistent whether the host is inspected locally or remotely.
func normalizeArch(machine string) string {
	switch machine = strings.TrimSpace(machine); machine {
	case "x86_64":
		return "amd64"
	case "aarch64":
		return "arm64"
	default:
		return machine
	}
}
