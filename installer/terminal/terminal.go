// Package terminal is a plain, line-based front end for the engine's questions, for
// commands that run in a shell but are not worth a window. It draws the same four kinds
// of screen as the window (text, secret, choice, confirm) with numbered options and
// y/N prompts, and it hides a secret as it is typed.
//
// It is deliberately small. Anything richer belongs in the window; this exists so a
// task like the Tailscale reset can ask "what do you want to remove?" instead of
// needing every answer on the command line.
package terminal

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"strconv"
	"strings"

	"golang.org/x/term"

	"github.com/Adaetum/Adaetum/installer/engine"
)

// UI asks on Out and reads answers from In. A secret is read without echo when In is
// a terminal; otherwise (tests, pipes) it is read as an ordinary line.
type UI struct {
	in  *bufio.Reader
	out io.Writer
	fd  int // file descriptor of In when it is a terminal, else -1
}

// New returns a UI on the process's standard input and output.
func New() *UI { return NewWith(os.Stdin, os.Stdout) }

// NewWith is New for chosen streams, which lets tests drive it with a script.
func NewWith(in io.Reader, out io.Writer) *UI {
	u := &UI{in: bufio.NewReader(in), out: out, fd: -1}
	if f, ok := in.(*os.File); ok && term.IsTerminal(int(f.Fd())) {
		u.fd = int(f.Fd())
	}
	return u
}

// IsInteractive reports whether the process has a terminal on both ends, which is when
// asking questions is possible at all.
func IsInteractive() bool {
	return term.IsTerminal(int(os.Stdin.Fd())) && term.IsTerminal(int(os.Stdout.Fd()))
}

func (u *UI) Report(e engine.Event) {
	if e.Message != "" {
		fmt.Fprintf(u.out, "  %s\n", e.Message)
	}
}

func (u *UI) line() (string, error) {
	s, err := u.in.ReadString('\n')
	if err != nil && (s == "" || !errors.Is(err, io.EOF)) {
		if errors.Is(err, io.EOF) {
			return "", engine.ErrNoAnswer
		}
		return "", err
	}
	return strings.TrimSpace(s), nil
}

func (u *UI) heading(s engine.Screen) {
	fmt.Fprintln(u.out)
	if s.Title != "" {
		fmt.Fprintln(u.out, s.Title)
	}
	if s.Intro != "" {
		fmt.Fprintln(u.out, s.Intro)
	}
	for i, step := range s.Steps {
		fmt.Fprintf(u.out, "  %d. %s\n", i+1, step.Text)
	}
}

func (u *UI) Ask(_ context.Context, s engine.Screen) (string, error) {
	u.heading(s)
	switch s.Kind {
	case engine.Secret:
		fmt.Fprintf(u.out, "%s: ", s.Prompt)
		if u.fd >= 0 {
			b, err := term.ReadPassword(u.fd)
			fmt.Fprintln(u.out)
			return strings.TrimSpace(string(b)), err
		}
		return u.line()
	case engine.Confirm:
		def := strings.EqualFold(s.Default, "yes")
		hint := "y/N"
		if def {
			hint = "Y/n"
		}
		if s.Prompt != "" {
			fmt.Fprintf(u.out, "%s ", s.Prompt)
		}
		fmt.Fprintf(u.out, "[%s]: ", hint)
		a, err := u.line()
		if err != nil {
			return "", err
		}
		switch strings.ToLower(a) {
		case "":
			if def {
				return "yes", nil
			}
			return "no", nil
		case "y", "yes":
			return "yes", nil
		}
		return "no", nil
	case engine.Choice:
		for i, o := range s.Options {
			mark := " "
			if o == s.Default {
				mark = "*"
			}
			fmt.Fprintf(u.out, " %s %d) %s\n", mark, i+1, o)
		}
		for {
			fmt.Fprintf(u.out, "%s [number]: ", s.Prompt)
			a, err := u.line()
			if err != nil {
				return "", err
			}
			if a == "" && s.Default != "" {
				return s.Default, nil
			}
			if n, convErr := strconv.Atoi(a); convErr == nil && n >= 1 && n <= len(s.Options) {
				return s.Options[n-1], nil
			}
			fmt.Fprintln(u.out, "  Enter one of the numbers above.")
		}
	default: // Text
		if s.Default != "" {
			fmt.Fprintf(u.out, "%s [%s]: ", s.Prompt, s.Default)
		} else {
			fmt.Fprintf(u.out, "%s: ", s.Prompt)
		}
		a, err := u.line()
		if err != nil {
			return "", err
		}
		if a == "" {
			return s.Default, nil
		}
		return a, nil
	}
}
