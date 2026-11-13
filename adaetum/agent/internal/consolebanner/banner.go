// Package consolebanner keeps the console login banner current: the lines above the login
// prompt that say where the agent can be reached. It replaces the shell script and systemd
// timer the kickstart used to install, so the agent that serves those addresses is also what
// announces them and the two cannot drift.
//
// The login program (agetty) reads its banner files only when a prompt starts, so merely
// rewriting a file never updates a prompt that is already on screen. That is the bug this
// fixes: after a change, an idle prompt is redrawn so it reads the file again.
//
// Three rules keep it from being a nuisance at the keyboard:
//   - It only ever redraws a prompt nobody is using. A person who is logged in, or typing
//     a username, is never interrupted.
//   - It leaves the console alone until first boot has finished, because first boot
//     draws its own progress there.
//   - It clears the screen once, at the moment first boot finishes, so the operator lands
//     on the address and not on a page of install output.
//
// The banner never contains a secret: the console is not a private place.
//
// It touches the machine only through the Machine interface, so every decision here is
// tested against a fake. The real one is Host (host.go), which reads files and sends one
// signal and runs no commands, which is what the agent's security boundary requires
// (docs/security-boundary.md).
package consolebanner

import (
	"context"
	"fmt"
	"strings"
	"time"
)

// Machine is everything the banner needs from the host.
type Machine interface {
	// LANAddress is the machine's IPv4 address on the interface that has the default
	// route, or "" when it has none yet.
	LANAddress() string
	// TailscaleAddress is the machine's IPv4 address on the tailnet, or "" before it joins.
	TailscaleAddress() string
	// ReadBanner returns the banner file as it is now; ok is false when it does not exist.
	ReadBanner() (text string, ok bool)
	// WriteBanner replaces the banner file. It must never leave a half-written file.
	WriteBanner(text string) error
	// FirstBootDone says whether first boot has finished.
	FirstBootDone() bool
	// ConsoleIdle says whether the first virtual console is sitting at a login prompt
	// with nobody on it.
	ConsoleIdle() bool
	// ShowOnConsole redraws that login prompt so it reads the banner file again. With
	// clear it first wipes the screen.
	ShowOnConsole(clear bool) error
}

// Render is the banner text. port is the port the agent serves HTTPS on. The addresses
// change (DHCP, the tailnet joining later), so it is rendered again on every check.
func Render(port, lan, tailscale string) string {
	var b strings.Builder
	b.WriteString("Adaetum host agent\n")
	if lan != "" {
		fmt.Fprintf(&b, "  On this network:  https://%s:%s\n", lan, port)
	}
	if tailscale != "" {
		fmt.Fprintf(&b, "  Over Tailscale:   https://%s:%s\n", tailscale, port)
	}
	if lan == "" && tailscale == "" {
		b.WriteString("  No network address yet; this updates every 30 seconds.\n")
	}
	b.WriteString("\n")
	return b.String()
}

// Keeper keeps the banner file and the console prompt current.
type Keeper struct {
	Machine Machine
	// Port is the port the agent serves on, as it should appear in the banner.
	Port string
	// Every is how often Run checks. Default 30 seconds.
	Every time.Duration
	// Log, if set, is told about anything worth a line in the journal.
	Log func(format string, args ...any)

	sawUnfinished bool // first boot was seen unfinished, so its finish is a transition
}

// Check looks at the machine once and does what is needed.
func (k *Keeper) Check() {
	m := k.Machine
	done := m.FirstBootDone()
	// "Just finished" is a transition we watch happen. An agent that starts after first
	// boot was already done has missed it, and does not clear a screen nobody is waiting on.
	finishedNow := false
	if !done {
		k.sawUnfinished = true
	} else if k.sawUnfinished {
		finishedNow, k.sawUnfinished = true, false
	}

	want := Render(k.Port, m.LANAddress(), m.TailscaleAddress())
	have, _ := m.ReadBanner()
	changed := want != have
	if changed {
		if err := m.WriteBanner(want); err != nil {
			k.logf("console banner: could not write it: %v", err)
			return
		}
	}
	if !done || !(changed || finishedNow) {
		return
	}
	if !m.ConsoleIdle() {
		return // someone is using the console; the next login prompt reads the file anyway
	}
	if err := m.ShowOnConsole(finishedNow); err != nil {
		k.logf("console banner: could not redraw the login prompt: %v", err)
	}
}

// Run checks now and then every k.Every until ctx ends.
func (k *Keeper) Run(ctx context.Context) {
	every := k.Every
	if every <= 0 {
		every = 30 * time.Second
	}
	k.Check()
	ticker := time.NewTicker(every)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			k.Check()
		}
	}
}

func (k *Keeper) logf(format string, args ...any) {
	if k.Log != nil {
		k.Log(format, args...)
	}
}
