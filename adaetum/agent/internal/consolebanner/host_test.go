package consolebanner

import (
	"net"
	"os"
	"path/filepath"
	"reflect"
	"strconv"
	"testing"
)

func TestParseStatHandlesNamesWithSpacesAndParentheses(t *testing.T) {
	comm, tty, ok := parseStat("812 (tmux: server) S 1 812 812 1025 812 4194560 ...")
	if !ok || comm != "tmux: server" || tty != tty1 {
		t.Fatalf("%q %d %v", comm, tty, ok)
	}
	comm, tty, ok = parseStat("9 (a) b) S 1 9 9 0 -1 0")
	if !ok || comm != "a) b" || tty != 0 {
		t.Fatalf("%q %d %v", comm, tty, ok)
	}
	if _, _, ok := parseStat("garbage"); ok {
		t.Fatal("garbage parsed")
	}
}

func TestDefaultInterfaceNeedsAnUpRouteWithNoDestinationAndNoMask(t *testing.T) {
	route := "Iface\tDestination\tGateway\tFlags\tRefCnt\tUse\tMetric\tMask\n" +
		"ens192\t0002A8C0\t00000000\t0001\t0\t0\t100\t00FFFFFF\n" +
		"down0\t00000000\t0102A8C0\t0000\t0\t0\t50\t00000000\n" +
		"ens192\t00000000\t0102A8C0\t0003\t0\t0\t100\t00000000\n"
	if got := defaultInterface(route); got != "ens192" {
		t.Fatalf("got %q", got)
	}
	if defaultInterface("Iface\tDestination\n") != "" {
		t.Fatal("no routes must give no interface")
	}
}

func TestFirstIPv4SkipsIPv6(t *testing.T) {
	// An interface address is the host address with the network's mask, as net reports it.
	ip6, v6, _ := net.ParseCIDR("fe80::1/64")
	v6.IP = ip6
	ip4, v4, _ := net.ParseCIDR("192.168.2.15/24")
	v4.IP = ip4
	if got := firstIPv4([]net.Addr{v6, v4}); got != "192.168.2.15" {
		t.Fatalf("got %q", got)
	}
	if firstIPv4([]net.Addr{v6}) != "" {
		t.Fatal("an IPv6-only interface has no IPv4 address")
	}
}

func TestWriteBannerReplacesTheFileAtomicallyAndLeavesNoTemporaryFile(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "issue.d") // does not exist yet
	h := Host{IssuePath: filepath.Join(dir, "adaetum.issue")}
	if _, ok := h.ReadBanner(); ok {
		t.Fatal("a banner that was never written was read")
	}
	for _, text := range []string{"first\n", "second\n"} {
		if err := h.WriteBanner(text); err != nil {
			t.Fatal(err)
		}
	}
	if got, ok := h.ReadBanner(); !ok || got != "second\n" {
		t.Fatalf("got %q %v", got, ok)
	}
	entries, _ := os.ReadDir(dir)
	if len(entries) != 1 {
		t.Fatalf("a temporary file was left behind: %v", entries)
	}
	if info, _ := os.Stat(h.IssuePath); info.Mode().Perm() != 0o644 {
		t.Fatalf("mode %v", info.Mode().Perm())
	}
}

// proc makes a fake /proc with one process per entry: name and controlling terminal.
func proc(t *testing.T, procs map[int][2]string) string {
	t.Helper()
	root := t.TempDir()
	for pid, p := range procs {
		dir := filepath.Join(root, strconv.Itoa(pid))
		os.MkdirAll(dir, 0o755)
		os.WriteFile(filepath.Join(dir, "stat"), []byte(strconv.Itoa(pid)+" ("+p[0]+") S 1 1 1 "+p[1]+" 0 0"), 0o644)
	}
	os.MkdirAll(filepath.Join(root, "self"), 0o755) // not a pid: must be ignored
	return root
}

func TestConsoleIdleMeansOnlyALoginPromptIsOnTheFirstConsole(t *testing.T) {
	for name, tc := range map[string]struct {
		procs map[int][2]string
		idle  bool
	}{
		"an idle prompt":       {map[int][2]string{10: {"agetty", "1025"}, 11: {"sshd", "0"}}, true},
		"someone logged in":    {map[int][2]string{10: {"login", "1025"}, 12: {"bash", "1025"}}, false},
		"a shell and a prompt": {map[int][2]string{10: {"agetty", "1025"}, 12: {"bash", "1025"}}, false},
		"no prompt at all":     {map[int][2]string{11: {"sshd", "0"}}, false},
		"a prompt on tty2":     {map[int][2]string{10: {"agetty", "1026"}}, false},
	} {
		t.Run(name, func(t *testing.T) {
			h := Host{ProcDir: proc(t, tc.procs)}
			if got := h.ConsoleIdle(); got != tc.idle {
				t.Fatalf("idle = %v", got)
			}
		})
	}
}

func TestShowOnConsoleEndsOnlyTheIdlePromptAndClearsWhenAsked(t *testing.T) {
	console := filepath.Join(t.TempDir(), "tty1")
	os.WriteFile(console, nil, 0o644)
	var killed []int
	h := Host{ProcDir: proc(t, map[int][2]string{10: {"agetty", "1025"}, 11: {"sshd", "0"}}), Console: console,
		kill: func(pid int) error { killed = append(killed, pid); return nil }}

	if err := h.ShowOnConsole(false); err != nil || !reflect.DeepEqual(killed, []int{10}) {
		t.Fatalf("killed=%v err=%v", killed, err)
	}
	if data, _ := os.ReadFile(console); len(data) != 0 {
		t.Fatalf("the screen was cleared without being asked: %q", data)
	}
	if err := h.ShowOnConsole(true); err != nil {
		t.Fatal(err)
	}
	if data, _ := os.ReadFile(console); string(data) != clearScreen {
		t.Fatalf("console got %q", data)
	}

	busy := Host{ProcDir: proc(t, map[int][2]string{10: {"login", "1025"}}), kill: func(int) error { t.Fatal("signalled a login"); return nil }}
	if err := busy.ShowOnConsole(true); err == nil {
		t.Fatal("redrew a console that someone is using")
	}
}
