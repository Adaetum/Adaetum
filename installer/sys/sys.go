// Package sys is the installer's only door to other programs (gh, git, python3).
// Keeping every process launch here means one place to review, and a fake to test
// against. The installer runs on the operator's own machine, a different trust
// domain from the cluster agent, so the agent's boundary validator does not apply
// to it; the rule here is the same in spirit: commands are fixed argument lists,
// never a shell string, and secrets are never passed as arguments.
package sys

import (
	"bytes"
	"context"
	"errors"
	"os"
	"os/exec"
	"strings"
)

// Result is what a finished command left behind.
type Result struct {
	Stdout   string
	Stderr   string
	ExitCode int
}

// OK reports whether the command exited with status 0.
func (r Result) OK() bool { return r.ExitCode == 0 }

// Runner runs one program. The error is only for a program that could not be
// started (missing, not executable); a program that ran and failed is a Result
// with a non-zero ExitCode, so callers can read its output to decide what to do.
type Runner interface {
	Run(ctx context.Context, dir, name string, args ...string) (Result, error)
	// RunInput is Run with text on the program's standard input. Secrets go this
	// way, never as arguments, which other users of the machine can read.
	RunInput(ctx context.Context, dir, stdin, name string, args ...string) (Result, error)
	// RunEnv adds environment variables (KEY=VALUE) for this one command. Like
	// stdin, this is how a secret reaches a program without appearing in its
	// argument list.
	RunEnv(ctx context.Context, dir string, env []string, stdin, name string, args ...string) (Result, error)
}

// Exec runs programs on this machine.
type Exec struct{}

func (e Exec) Run(ctx context.Context, dir, name string, args ...string) (Result, error) {
	return e.RunEnv(ctx, dir, nil, "", name, args...)
}

func (e Exec) RunInput(ctx context.Context, dir, stdin, name string, args ...string) (Result, error) {
	return e.RunEnv(ctx, dir, nil, stdin, name, args...)
}

func (Exec) RunEnv(ctx context.Context, dir string, env []string, stdin, name string, args ...string) (Result, error) {
	cmd := exec.CommandContext(ctx, name, args...)
	cmd.Dir = dir
	if len(env) > 0 {
		cmd.Env = append(os.Environ(), env...)
	}
	if stdin != "" {
		cmd.Stdin = strings.NewReader(stdin)
	}
	var stdout, stderr bytes.Buffer
	cmd.Stdout, cmd.Stderr = &stdout, &stderr
	err := cmd.Run()
	res := Result{Stdout: stdout.String(), Stderr: stderr.String()}
	var exit *exec.ExitError
	if errors.As(err, &exit) {
		res.ExitCode = exit.ExitCode()
		return res, nil
	}
	if err != nil {
		return res, err
	}
	return res, nil
}

// Has reports whether a program is on PATH.
func Has(name string) bool {
	_, err := exec.LookPath(name)
	return err == nil
}

// Streamer is a Runner that can hand output to a caller while the program is still
// running, for programs that print something the operator must act on before they
// finish (a one-time sign-in code, for example). Only Exec and the test fakes
// implement it; callers fall back to Run when a Runner does not.
type Streamer interface {
	RunStream(ctx context.Context, dir string, env []string, stdin, name string, args []string, onLine func(line string)) (Result, error)
}

func (Exec) RunStream(ctx context.Context, dir string, env []string, stdin, name string, args []string, onLine func(string)) (Result, error) {
	cmd := exec.CommandContext(ctx, name, args...)
	cmd.Dir = dir
	if len(env) > 0 {
		cmd.Env = append(os.Environ(), env...)
	}
	if stdin != "" {
		cmd.Stdin = strings.NewReader(stdin)
	}
	// Both streams go through one pipe so lines arrive in the order they were written.
	pr, pw, err := os.Pipe()
	if err != nil {
		return Result{}, err
	}
	cmd.Stdout, cmd.Stderr = pw, pw
	if err := cmd.Start(); err != nil {
		pr.Close()
		pw.Close()
		return Result{}, err
	}
	pw.Close()

	var all bytes.Buffer
	done := make(chan struct{})
	go func() {
		defer close(done)
		buf := make([]byte, 0, 4096)
		chunk := make([]byte, 4096)
		for {
			n, rerr := pr.Read(chunk)
			if n > 0 {
				all.Write(chunk[:n])
				buf = append(buf, chunk[:n]...)
				for {
					i := bytes.IndexByte(buf, '\n')
					if i < 0 {
						break
					}
					if onLine != nil {
						onLine(strings.TrimRight(string(buf[:i]), "\r"))
					}
					buf = buf[i+1:]
				}
			}
			if rerr != nil {
				if len(buf) > 0 && onLine != nil {
					onLine(strings.TrimRight(string(buf), "\r"))
				}
				return
			}
		}
	}()
	werr := cmd.Wait()
	<-done
	pr.Close()
	res := Result{Stdout: all.String()}
	var exit *exec.ExitError
	if errors.As(werr, &exit) {
		res.ExitCode = exit.ExitCode()
		return res, nil
	}
	return res, werr
}
