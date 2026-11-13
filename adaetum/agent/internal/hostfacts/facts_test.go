package hostfacts

import (
	"context"
	"errors"
	"testing"

	"github.com/Adaetum/Adaetum/agent/internal/inspect"
)

const v4WithDefault = `Iface	Destination	Gateway 	Flags	RefCnt	Use	Metric	Mask		MTU	Window	IRTT
eth0	00000000	0102A8C0	0003	0	0	100	00000000	0	0	0
eth0	0002A8C0	00000000	0001	0	0	100	00FFFFFF	0	0	0
`

const v4NoDefault = `Iface	Destination	Gateway 	Flags	RefCnt	Use	Metric	Mask		MTU	Window	IRTT
eth0	0002A8C0	00000000	0001	0	0	100	00FFFFFF	0	0	0
`

const v6Default = `fe800000000000000000000000000000 40 00000000000000000000000000000000 00 00000000000000000000000000000000 00000100 00000001 00000000 00000001 eth0
00000000000000000000000000000000 00 00000000000000000000000000000000 00 fe800000000000000000000000000001 00000064 00000001 00000000 00000003 eth0
`

// The kernel lists an unreachable default on the loopback device even with no real route.
const v6RejectOnly = `00000000000000000000000000000000 00 00000000000000000000000000000000 00 00000000000000000000000000000000 ffffffff 00000001 00000000 00200200 lo
`

func facts(files map[string]string, run func(name string, args ...string) (string, error)) Facts {
	return Facts{Inspector: &inspect.Inspector{
		ReadFile: func(path string) ([]byte, error) {
			if data, ok := files[path]; ok {
				return []byte(data), nil
			}
			return nil, errors.New("no such file")
		},
		Run: func(_ context.Context, name string, args ...string) (string, error) { return run(name, args...) },
	}}
}

func TestADefaultRouteIsFoundForIPv4OrIPv6(t *testing.T) {
	cases := []struct {
		name  string
		files map[string]string
		want  bool
	}{
		{"ipv4 default", map[string]string{"/proc/net/route": v4WithDefault}, true},
		{"only a local network", map[string]string{"/proc/net/route": v4NoDefault}, false},
		{"ipv6 only", map[string]string{"/proc/net/route": v4NoDefault, "/proc/net/ipv6_route": v6Default}, true},
		{"ipv6 reject entry is not a route out", map[string]string{"/proc/net/route": v4NoDefault, "/proc/net/ipv6_route": v6RejectOnly}, false},
		{"ipv6 file missing", map[string]string{"/proc/net/route": v4NoDefault}, false},
	}
	for _, c := range cases {
		got, err := facts(c.files, nil).HasDefaultRoute(context.Background())
		if err != nil || got != c.want {
			t.Errorf("%s: got %v, %v; want %v", c.name, got, err, c.want)
		}
	}
	if _, err := facts(nil, nil).HasDefaultRoute(context.Background()); err == nil {
		t.Error("an unreadable routing table should be an error, not 'no route'")
	}
}

func TestTheClockIsSynchronizedKnownOrUnknown(t *testing.T) {
	ask := func(out string, err error) (bool, bool) {
		return facts(nil, func(name string, args ...string) (string, error) {
			if name != "timedatectl" || len(args) != 4 || args[0] != "show" {
				t.Errorf("unexpected command %s %v", name, args)
			}
			return out, err
		}).ClockSynchronized(context.Background())
	}
	if synced, known := ask("yes\n", nil); !synced || !known {
		t.Error("yes should be synchronized")
	}
	if synced, known := ask("no\n", nil); synced || !known {
		t.Error("no should be known and not synchronized")
	}
	if _, known := ask("", errors.New("not found")); known {
		t.Error("a failing command means the state is unknown")
	}
	if _, known := ask("maybe", nil); known {
		t.Error("unrecognised output means the state is unknown")
	}
}

func TestFreeBytesReadsTheAvailableColumnAndRefusesUnsafePaths(t *testing.T) {
	f := facts(nil, func(name string, args ...string) (string, error) {
		if name != "df" || len(args) != 3 || args[2] != "/var" {
			t.Errorf("unexpected command %s %v", name, args)
		}
		return "Filesystem 1-blocks Used Available Capacity Mounted on\n/dev/mapper/rl-root 53660876800 8000000000 45660876800 15% /\n", nil
	})
	got, err := f.FreeBytes(context.Background(), "/var")
	if err != nil || got != 45660876800 {
		t.Fatalf("got %d, %v", got, err)
	}
	for _, bad := range []string{"", "var", "-h", "/var; rm", "/var lib", "--help", "/a$(b)"} {
		if _, err := f.FreeBytes(context.Background(), bad); err == nil {
			t.Errorf("path %q should be refused", bad)
		}
	}
	for _, bad := range []string{"", "Filesystem only", "a\nb c", "a\nb c d notanumber"} {
		if _, err := parseDF(bad); err == nil {
			t.Errorf("df output %q should be refused", bad)
		}
	}
}
