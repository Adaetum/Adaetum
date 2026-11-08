package cleanup

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
	"time"

	"github.com/Adaetum/Adaetum/agent/internal/inspect"
)

// crictl runs the runtime's command-line client. It is the only place the agent
// runs a command that can change anything, so the command and socket are
// literals: nothing but the arguments varies, and callers validate those. A test
// keeps the literals equal to the inspector's constants.
//
// crictl's own default timeout is 2 seconds, which is far too short to delete a
// large image (its layers must be unpacked-snapshot-deleted), so a long one is
// always passed.
var crictl = func(ctx context.Context, args ...string) ([]byte, error) {
	full := append([]string{"--timeout", "4m", "--runtime-endpoint", "unix:///run/k3s/containerd/containerd.sock"}, args...)
	return exec.CommandContext(ctx, "/var/lib/rancher/rke2/bin/crictl", full...).CombinedOutput()
}

const (
	removeTimeout = 5 * time.Minute
	checkTimeout  = 30 * time.Second
)

// RuntimeRemove deletes one image through the local containerd.
//
// The ID is validated against the shape of a containerd image ID before it
// reaches the command line, so it can never be read as an option, and it is
// passed as its own argument, never through a shell.
//
// A removal can outlast the call and still finish, or report an error after the
// work is done. So when the call fails, the image is looked up: if it is gone the
// goal was met and the removal counts as done, which keeps the audit trail true.
func RuntimeRemove(ctx context.Context, id string) error {
	if !inspect.ValidImageID(id) {
		return fmt.Errorf("refusing to remove %q: not an image ID", id)
	}
	removeCtx, cancel := context.WithTimeout(ctx, removeTimeout)
	defer cancel()
	output, err := crictl(removeCtx, "rmi", id)
	if err == nil {
		return nil
	}

	// A fresh context: the one above may be the reason the call failed.
	checkCtx, cancelCheck := context.WithTimeout(context.WithoutCancel(ctx), checkTimeout)
	defer cancelCheck()
	if lookup, lookupErr := crictl(checkCtx, "inspecti", id); lookupErr != nil && strings.Contains(strings.ToLower(string(lookup)), "not found") {
		return nil
	}
	return fmt.Errorf("crictl rmi: %w: %s", err, tail(string(output)))
}

// tail keeps the end of long command output, where the cause is, and drops the
// warning lines crictl prints before it.
func tail(output string) string {
	output = strings.TrimSpace(output)
	if lines := strings.Split(output, "\n"); len(lines) > 3 {
		output = strings.Join(lines[len(lines)-3:], " | ")
	}
	return output
}

// OpenAuditLog opens an append-only JSON-lines audit file and returns a function
// that writes one entry durably. Each entry is flushed to disk before the call
// returns, because the log must survive a crash in the middle of a removal.
func OpenAuditLog(path string) (audit func(Entry) error, closeLog func() error, err error) {
	if err := os.MkdirAll(filepath.Dir(path), 0o750); err != nil {
		return nil, nil, err
	}
	file, err := os.OpenFile(path, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o640)
	if err != nil {
		return nil, nil, err
	}
	write := func(entry Entry) error {
		line, err := json.Marshal(entry)
		if err != nil {
			return err
		}
		if _, err := file.Write(append(line, '\n')); err != nil {
			return err
		}
		return file.Sync()
	}
	return write, file.Close, nil
}

// Lock takes an exclusive, non-blocking lock so two applies can never run at the
// same time. The lock is released when unlock is called or the process exits.
func Lock(path string) (unlock func(), err error) {
	file, err := os.OpenFile(path, os.O_CREATE|os.O_RDWR, 0o600)
	if err != nil {
		return nil, err
	}
	if err := syscall.Flock(int(file.Fd()), syscall.LOCK_EX|syscall.LOCK_NB); err != nil {
		file.Close()
		return nil, fmt.Errorf("another image cleanup is already running")
	}
	return func() {
		_ = syscall.Flock(int(file.Fd()), syscall.LOCK_UN)
		file.Close()
	}, nil
}
