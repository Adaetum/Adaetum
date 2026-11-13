package consolebanner

import (
	"errors"
	"strings"
	"testing"
)

// fake is a machine whose state a test sets and whose effects it records.
type fake struct {
	lan, ts    string
	banner     string
	hasBanner  bool
	done, idle bool
	writeErr   error
	writes     int
	shows      []bool // one entry per redraw: whether it also cleared the screen
	showErr    error
}

func (f *fake) LANAddress() string         { return f.lan }
func (f *fake) TailscaleAddress() string   { return f.ts }
func (f *fake) ReadBanner() (string, bool) { return f.banner, f.hasBanner }
func (f *fake) WriteBanner(t string) error {
	if f.writeErr != nil {
		return f.writeErr
	}
	f.banner, f.hasBanner = t, true
	f.writes++
	return nil
}
func (f *fake) FirstBootDone() bool { return f.done }
func (f *fake) ConsoleIdle() bool   { return f.idle }
func (f *fake) ShowOnConsole(clear bool) error {
	f.shows = append(f.shows, clear)
	return f.showErr
}

func keeper(f *fake) *Keeper { return &Keeper{Machine: f, Port: "8443"} }

func TestRenderListsEveryAddressAndNeverAnythingSecret(t *testing.T) {
	got := Render("8443", "192.168.2.15", "100.64.0.9")
	for _, want := range []string{"Adaetum host agent", "https://192.168.2.15:8443", "https://100.64.0.9:8443"} {
		if !strings.Contains(got, want) {
			t.Errorf("missing %q in:\n%s", want, got)
		}
	}
	if !strings.Contains(Render("8443", "", ""), "No network address yet") {
		t.Error("a machine with no address must say so")
	}
	if strings.Contains(Render("8443", "192.168.2.15", ""), "Tailscale") {
		t.Error("the tailnet line must be left out before the node joins")
	}
}

func TestTheBannerFileIsWrittenEvenWhileFirstBootStillOwnsTheConsole(t *testing.T) {
	f := &fake{lan: "192.168.2.15", idle: true}
	keeper(f).Check()
	if f.writes != 1 || !strings.Contains(f.banner, "192.168.2.15") {
		t.Fatalf("writes=%d banner=%q", f.writes, f.banner)
	}
	if len(f.shows) != 0 {
		t.Fatal("the console was redrawn during first boot, which draws its own progress there")
	}
}

func TestAnIdlePromptIsRedrawnWhenTheAddressChangesAfterFirstBoot(t *testing.T) {
	f := &fake{lan: "192.168.2.15", done: true, idle: true}
	k := keeper(f)
	k.Check() // first sight: file written, prompt redrawn, screen not cleared
	if len(f.shows) != 1 || f.shows[0] {
		t.Fatalf("shows=%v", f.shows)
	}
	k.Check() // nothing changed
	if len(f.shows) != 1 || f.writes != 1 {
		t.Fatalf("an unchanged banner caused work: writes=%d shows=%v", f.writes, f.shows)
	}
	f.lan = "192.168.2.99" // DHCP moved it
	k.Check()
	if len(f.shows) != 2 || f.shows[1] || f.writes != 2 {
		t.Fatalf("writes=%d shows=%v", f.writes, f.shows)
	}
}

func TestTheScreenIsClearedOnceWhenFirstBootFinishes(t *testing.T) {
	f := &fake{lan: "192.168.2.15", idle: true}
	k := keeper(f)
	k.Check() // unfinished: file only
	f.done = true
	k.Check() // the transition: clear and show, although the text did not change
	if len(f.shows) != 1 || !f.shows[0] {
		t.Fatalf("shows=%v", f.shows)
	}
	k.Check()
	k.Check()
	if len(f.shows) != 1 {
		t.Fatalf("it kept redrawing: %v", f.shows)
	}
}

func TestAnAgentStartedAfterFirstBootDoesNotWipeAScreenNobodyIsWaitingOn(t *testing.T) {
	f := &fake{lan: "192.168.2.15", done: true, idle: true, banner: Render("8443", "192.168.2.15", ""), hasBanner: true}
	keeper(f).Check()
	if len(f.shows) != 0 || f.writes != 0 {
		t.Fatalf("writes=%d shows=%v", f.writes, f.shows)
	}
}

func TestAPersonAtTheConsoleIsNeverInterrupted(t *testing.T) {
	f := &fake{lan: "192.168.2.15", done: true, idle: false}
	keeper(f).Check()
	if len(f.shows) != 0 {
		t.Fatalf("the prompt was redrawn under someone: %v", f.shows)
	}
	if f.writes != 1 {
		t.Fatal("the file must still be written so their next prompt shows it")
	}
}

func TestFailuresAreLoggedAndNeverStopTheLoop(t *testing.T) {
	var lines []string
	f := &fake{lan: "192.168.2.15", done: true, idle: true, writeErr: errors.New("read-only filesystem")}
	k := &Keeper{Machine: f, Port: "8443", Log: func(format string, args ...any) { lines = append(lines, format) }}
	k.Check()
	f.writeErr, f.showErr = nil, errors.New("not at a prompt")
	f.lan = "192.168.2.16"
	k.Check()
	if len(lines) != 2 {
		t.Fatalf("log lines: %v", lines)
	}
}
