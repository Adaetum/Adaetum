package consolebanner

import (
	"errors"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
)

const (
	// IssueFile is the banner. agetty prints every *.issue file in this directory above
	// the login prompt, so a file of our own never fights the distribution's /etc/issue.
	IssueFile = "/etc/issue.d/adaetum.issue"
	// FirstBootDoneFile is written by the first-boot flow when it finishes.
	FirstBootDoneFile = "/var/lib/tailscale-firstboot.done"

	// tty1 is device major 4, minor 1; /proc/<pid>/stat reports it as major*256+minor.
	tty1 = 4*256 + 1
	// clearScreen homes the cursor, wipes the screen and clears the scrollback.
	clearScreen = "\x1b[H\x1b[2J\x1b[3J"
)

// Host is the real machine. Every path is a field so a test can point it at a temp directory.
type Host struct {
	IssuePath, DoneFile, RouteFile, ProcDir, Console string

	// kill sends SIGTERM to a process. Replaceable so a test never signals a real one.
	kill func(pid int) error
}

// NewHost returns the real machine.
func NewHost() Host {
	return Host{
		IssuePath: IssueFile, DoneFile: FirstBootDoneFile,
		RouteFile: "/proc/net/route", ProcDir: "/proc", Console: "/dev/tty1",
	}
}

func (h Host) LANAddress() string {
	data, err := os.ReadFile(h.RouteFile)
	if err != nil {
		return ""
	}
	name := defaultInterface(string(data))
	if name == "" {
		return ""
	}
	return interfaceIPv4(name)
}

func (h Host) TailscaleAddress() string { return interfaceIPv4("tailscale0") }

func (h Host) ReadBanner() (string, bool) {
	data, err := os.ReadFile(h.IssuePath)
	if err != nil {
		return "", false
	}
	return string(data), true
}

// WriteBanner writes to a temporary file in the same directory and renames it over the
// banner, so the login prompt can never read a half-written file. The temporary name does
// not end in ".issue", so agetty never picks it up.
func (h Host) WriteBanner(text string) error {
	dir := filepath.Dir(h.IssuePath)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	tmp, err := os.CreateTemp(dir, ".adaetum-banner-*")
	if err != nil {
		return err
	}
	name := tmp.Name()
	fail := func(err error) error {
		tmp.Close()
		os.Remove(name)
		return err
	}
	if err := tmp.Chmod(0o644); err != nil {
		return fail(err)
	}
	if _, err := tmp.WriteString(text); err != nil {
		return fail(err)
	}
	if err := tmp.Close(); err != nil {
		os.Remove(name)
		return err
	}
	if err := os.Rename(name, h.IssuePath); err != nil {
		os.Remove(name)
		return err
	}
	return nil
}

func (h Host) FirstBootDone() bool {
	_, err := os.Stat(h.DoneFile)
	return err == nil
}

// ConsoleIdle is true when the only thing on the first console is a login prompt waiting
// for a username. Once someone types one, agetty becomes login and then a shell, so any
// other process on that console means a person is there.
func (h Host) ConsoleIdle() bool {
	prompts, others := h.consoleProcesses()
	return len(prompts) > 0 && others == 0
}

// ShowOnConsole clears the screen if asked, then ends the idle login prompt. systemd
// starts a new one at once (getty keeps itself running), and a new prompt reads the banner
// file again. It does this with a signal, not a command.
func (h Host) ShowOnConsole(clear bool) error {
	prompts, others := h.consoleProcesses()
	if len(prompts) == 0 || others > 0 {
		return errors.New("the console is not at an idle login prompt")
	}
	if clear {
		// A failure to clear is cosmetic; the prompt is still redrawn.
		if f, err := os.OpenFile(h.Console, os.O_WRONLY|syscall.O_NOCTTY, 0); err == nil {
			f.WriteString(clearScreen)
			f.Close()
		}
	}
	kill := h.kill
	if kill == nil {
		kill = func(pid int) error { return syscall.Kill(pid, syscall.SIGTERM) }
	}
	var first error
	for _, pid := range prompts {
		if err := kill(pid); err != nil && first == nil {
			first = fmt.Errorf("ending login prompt %d: %w", pid, err)
		}
	}
	return first
}

// consoleProcesses lists the login prompts (agetty) on the first console and counts every
// other process there.
func (h Host) consoleProcesses() (prompts []int, others int) {
	entries, err := os.ReadDir(h.ProcDir)
	if err != nil {
		return nil, 0
	}
	for _, e := range entries {
		pid, err := strconv.Atoi(e.Name())
		if err != nil {
			continue
		}
		data, err := os.ReadFile(filepath.Join(h.ProcDir, e.Name(), "stat"))
		if err != nil {
			continue // the process ended while we looked
		}
		comm, tty, ok := parseStat(string(data))
		if !ok || tty != tty1 {
			continue
		}
		if comm == "agetty" {
			prompts = append(prompts, pid)
		} else {
			others++
		}
	}
	return prompts, others
}

// parseStat reads the process name and controlling terminal from /proc/<pid>/stat:
// "pid (comm) state ppid pgrp session tty_nr ...". The name is in parentheses and may
// itself contain spaces and parentheses, so the last ")" ends it.
func parseStat(line string) (comm string, tty int, ok bool) {
	open, end := strings.Index(line, "("), strings.LastIndex(line, ")")
	if open < 0 || end < open {
		return "", 0, false
	}
	fields := strings.Fields(line[end+1:])
	// After the name: state ppid pgrp session tty_nr.
	if len(fields) < 5 {
		return "", 0, false
	}
	tty, err := strconv.Atoi(fields[4])
	if err != nil {
		return "", 0, false
	}
	return line[open+1 : end], tty, true
}

// defaultInterface returns the interface that has the IPv4 default route, from the text of
// /proc/net/route: "Iface Destination Gateway Flags RefCnt Use Metric Mask ...". A default
// route has destination 0, mask 0 and the UP flag (0x1).
func defaultInterface(route string) string {
	for i, line := range strings.Split(route, "\n") {
		f := strings.Fields(line)
		if i == 0 || len(f) < 8 || f[1] != "00000000" || f[7] != "00000000" {
			continue
		}
		if flags, err := strconv.ParseUint(f[3], 16, 32); err == nil && flags&1 != 0 {
			return f[0]
		}
	}
	return ""
}

func interfaceIPv4(name string) string {
	iface, err := net.InterfaceByName(name)
	if err != nil {
		return ""
	}
	addrs, err := iface.Addrs()
	if err != nil {
		return ""
	}
	return firstIPv4(addrs)
}

func firstIPv4(addrs []net.Addr) string {
	for _, a := range addrs {
		if ipnet, ok := a.(*net.IPNet); ok {
			if ip := ipnet.IP.To4(); ip != nil {
				return ip.String()
			}
		}
	}
	return ""
}
