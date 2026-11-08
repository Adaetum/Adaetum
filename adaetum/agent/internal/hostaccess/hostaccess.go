// Package hostaccess is the one place the agent may run a command directly on a
// server, bypassing Ansible. It is OFF unless an operator explicitly turns it on.
//
// THE SECURITY BOUNDARY (docs/security-boundary.md explains the whole thing):
//
//  1. The agent does not run commands on servers. Changes to a server are made by
//     Ansible playbooks that the agent LAUNCHES (internal/ansiblerun): reviewed,
//     catalogued, rehearsable, and recorded. That is the default everywhere.
//  2. In particular the cluster agent, which runs as a pod, has no host-level
//     access. It cannot run a command on a server except by launching Ansible.
//  3. A host agent acts only on its own machine, through narrow built-in actions
//     (for example removing unused images) that are each gated by root, a flag and
//     a token. It does not reach out to other servers.
//  4. This package is the deliberate exception: an OPTIONAL override that lets an
//     authorised person run a command on a server over SSH, skipping the playbook
//     review. It exists because sometimes you must reach a machine directly, and it
//     is built to be hard to enable by accident and impossible to use unseen:
//     off by default; needs an explicit flag, an access token, and a writable
//     audit file; never in -ssh development mode; limited to named servers; one
//     command at a time with a time limit; and every command is recorded BEFORE it
//     runs. A command that cannot be recorded is not run.
//
// The boundary is enforced, not just documented: .validator/validate-agent-
// boundary.py fails the build if any package other than the ones named there
// imports os/exec or an SSH library, so a new way to run commands cannot appear
// without a reviewed change to that list. This package is on it.
//
// NOT BUILT: interactive SSH sessions (a terminal in the page). This runs one
// command and returns its output.
package hostaccess

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"os/exec"
	"regexp"
	"strings"
	"sync"
	"time"
)

// Limits on what one command may do.
const (
	maxCommandLength = 4096
	defaultTimeout   = 60 * time.Second
	maxTimeout       = 10 * time.Minute
	// maxOutput bounds what is kept and returned; the rest is dropped.
	maxOutput = 64 * 1024
)

// A server name: letters, digits, dots, dashes and underscores. No patterns, no
// user@ prefix, no options: only a plain name that is then looked up in the allowed set.
var hostName = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._-]*$`)

// Request is a command to run on one server.
type Request struct {
	Host    string `json:"host"`
	Command string `json:"command"`
	// TimeoutSeconds is optional; the default is 60 and the most is 600.
	TimeoutSeconds int `json:"timeoutSeconds"`
}

// Result is what the command did.
type Result struct {
	ExitCode   int    `json:"exitCode"`
	Output     string `json:"output"`
	Truncated  bool   `json:"truncated"`
	DurationMs int64  `json:"durationMs"`
	// TimedOut is true when the command was stopped for running too long.
	TimedOut bool `json:"timedOut"`
}

// AuditEntry is one line of the record.
type AuditEntry struct {
	Time       time.Time `json:"time"`
	Event      string    `json:"event"` // "start" or "finish"
	Host       string    `json:"host"`
	Command    string    `json:"command"`
	By         string    `json:"by"`
	ExitCode   *int      `json:"exitCode,omitempty"`
	OutputSize int       `json:"outputBytes,omitempty"`
	TimedOut   bool      `json:"timedOut,omitempty"`
	Error      string    `json:"error,omitempty"`
}

// Executor runs a command on a server and writes its output.
type Executor interface {
	Run(ctx context.Context, host, command string, output io.Writer) (exitCode int, err error)
}

// Manager applies the policy around an Executor.
type Manager struct {
	Exec Executor
	// Audit records an entry. A failure on "start" refuses the command.
	Audit func(AuditEntry) error
	// Allowed returns the servers a command may be run on. It is asked for every
	// command, and an error means "none": when in doubt, refuse.
	Allowed func(ctx context.Context) ([]string, error)
	Now     func() time.Time

	busy sync.Mutex
}

// ErrBusy means another command is running.
var ErrBusy = errors.New("another command is already running on a server; wait for it to finish")

// Run checks a request against the policy, records it, runs it, and records the result.
func (m *Manager) Run(ctx context.Context, request Request, by string) (Result, error) {
	host := strings.TrimSpace(request.Host)
	command := strings.TrimSpace(request.Command)
	switch {
	case !hostName.MatchString(host):
		return Result{}, errors.New("name one server, exactly as the cluster lists it")
	case command == "":
		return Result{}, errors.New("there is no command to run")
	case len(command) > maxCommandLength || strings.ContainsRune(command, 0):
		return Result{}, fmt.Errorf("a command must be text of at most %d characters", maxCommandLength)
	}
	timeout := defaultTimeout
	if request.TimeoutSeconds > 0 {
		timeout = time.Duration(request.TimeoutSeconds) * time.Second
	}
	if timeout > maxTimeout {
		return Result{}, fmt.Errorf("a command may run for at most %d minutes", int(maxTimeout.Minutes()))
	}

	allowed, err := m.Allowed(ctx)
	if err != nil {
		return Result{}, fmt.Errorf("cannot tell which servers are allowed, so nothing was run: %w", err)
	}
	permitted := false
	for _, name := range allowed {
		if strings.EqualFold(name, host) {
			permitted = true
		}
	}
	if !permitted {
		return Result{}, fmt.Errorf("%q is not one of the servers commands are allowed on", host)
	}

	if !m.busy.TryLock() {
		return Result{}, ErrBusy
	}
	defer m.busy.Unlock()

	if err := m.record(AuditEntry{Event: "start", Host: host, Command: command, By: by}); err != nil {
		return Result{}, fmt.Errorf("the command was not run because it could not be recorded: %w", err)
	}

	runCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	output := &cappedBuffer{limit: maxOutput}
	started := m.now()
	exit, runErr := m.Exec.Run(runCtx, host, command, output)

	result := Result{ExitCode: exit, Output: output.String(), Truncated: output.truncated, DurationMs: m.now().Sub(started).Milliseconds(), TimedOut: runCtx.Err() == context.DeadlineExceeded}
	finish := AuditEntry{Event: "finish", Host: host, Command: command, By: by, ExitCode: &exit, OutputSize: output.total, TimedOut: result.TimedOut}
	if runErr != nil {
		finish.Error, finish.ExitCode = runErr.Error(), nil
	}
	_ = m.record(finish) // best effort: the start was already recorded
	if runErr != nil {
		return result, fmt.Errorf("could not run the command: %w", runErr)
	}
	return result, nil
}

func (m *Manager) record(entry AuditEntry) error {
	entry.Time = m.now().UTC()
	if m.Audit == nil {
		return errors.New("no record is configured")
	}
	return m.Audit(entry)
}

func (m *Manager) now() time.Time {
	if m.Now != nil {
		return m.Now()
	}
	return time.Now()
}

// cappedBuffer keeps the first `limit` bytes and counts the rest.
type cappedBuffer struct {
	limit     int
	buffer    bytes.Buffer
	total     int
	truncated bool
}

func (c *cappedBuffer) Write(data []byte) (int, error) {
	c.total += len(data)
	if room := c.limit - c.buffer.Len(); room > 0 {
		if len(data) > room {
			c.buffer.Write(data[:room])
			c.truncated = true
		} else {
			c.buffer.Write(data)
		}
	} else if len(data) > 0 {
		c.truncated = true
	}
	return len(data), nil
}

func (c *cappedBuffer) String() string { return c.buffer.String() }

// SSHExec runs commands with the system's ssh program, found on the PATH. The
// program is fixed in code (not configurable and never from a request); the login
// user comes from how the agent was started; the host passed in has already been
// checked against the allowed set.
type SSHExec struct {
	User string
}

// Run executes the command on the host and returns ssh's exit code, which is the
// remote command's (255 means ssh itself could not connect).
func (e SSHExec) Run(ctx context.Context, host, command string, output io.Writer) (int, error) {
	args := []string{
		"-o", "BatchMode=yes", // never prompt: there is nobody to answer
		"-o", "ConnectTimeout=10",
		"-o", "ServerAliveInterval=15",
		"-l", e.User,
		"--", host, command,
	}
	// The program is the literal "ssh". Only its arguments vary, and they are passed
	// as separate strings with no shell on this side.
	cmd := exec.CommandContext(ctx, "ssh", args...)
	cmd.Stdout, cmd.Stderr = output, output
	err := cmd.Run()
	var exitErr *exec.ExitError
	switch {
	case err == nil:
		return 0, nil
	case errors.As(err, &exitErr):
		return exitErr.ExitCode(), nil
	case ctx.Err() != nil:
		return -1, nil
	default:
		return 0, err
	}
}
