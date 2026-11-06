package membership

import (
	"testing"
	"time"
)

var now = time.Date(2026, 11, 4, 12, 0, 0, 0, time.UTC)

func checkIn(host, name, version string, ago time.Duration) CheckIn {
	return CheckIn{HostID: host, Hostname: name, Version: version, At: now.Add(-ago)}
}

func statusOf(t *testing.T, a Assessment, server string) ServerStatus {
	t.Helper()
	for _, s := range a.Servers {
		if s.Server == server {
			return s
		}
	}
	t.Fatalf("no row for %s in %+v", server, a.Servers)
	return ServerStatus{}
}

func TestAServerWithNoCheckInIsMissing(t *testing.T) {
	a := Assess([]string{"node-a", "node-b"}, []CheckIn{checkIn("h1", "node-a", "0.1.0", time.Minute)}, now, Options{})
	if statusOf(t, a, "node-a").Status != StatusOK || statusOf(t, a, "node-b").Status != StatusMissing {
		t.Fatalf("unexpected: %+v", a.Servers)
	}
	if statusOf(t, a, "node-b").LastSeen != nil {
		t.Fatal("a missing server has never been seen")
	}
}

func TestAQuietHostIsStaleOnlyPastTheThreshold(t *testing.T) {
	servers := []string{"node-a"}
	if s := Assess(servers, []CheckIn{checkIn("h", "node-a", "1", 14*time.Minute)}, now, Options{}).Servers[0]; s.Status != StatusOK {
		t.Fatalf("two missed five-minute check-ins are tolerated: %+v", s)
	}
	if s := Assess(servers, []CheckIn{checkIn("h", "node-a", "1", 16*time.Minute)}, now, Options{}).Servers[0]; s.Status != StatusStale {
		t.Fatalf("expected stale: %+v", s)
	}
	if s := Assess(servers, []CheckIn{checkIn("h", "node-a", "1", 40*time.Minute)}, now, Options{StaleAfter: time.Hour}).Servers[0]; s.Status != StatusOK {
		t.Fatalf("StaleAfter must be configurable: %+v", s)
	}
}

func TestOutdatedNeedsALatestVersionToCompareAgainst(t *testing.T) {
	servers := []string{"node-a"}
	old := []CheckIn{checkIn("h", "node-a", "0.1.0", time.Minute)}
	if s := Assess(servers, old, now, Options{LatestVersion: "0.2.0"}).Servers[0]; s.Status != StatusOutdated {
		t.Fatalf("expected outdated: %+v", s)
	}
	if s := Assess(servers, old, now, Options{}).Servers[0]; s.Status != StatusOK {
		t.Fatalf("with no latest known version nothing is outdated: %+v", s)
	}
	if s := Assess(servers, []CheckIn{checkIn("h", "node-a", "0.2.0", time.Minute)}, now, Options{LatestVersion: "0.2.0"}).Servers[0]; s.Status != StatusOK {
		t.Fatalf("current version is ok: %+v", s)
	}
}

func TestStaleOutranksOutdatedBecauseALostHostIsTheBiggerProblem(t *testing.T) {
	s := Assess([]string{"a"}, []CheckIn{checkIn("h", "a", "0.1.0", time.Hour)}, now, Options{LatestVersion: "0.2.0"}).Servers[0]
	if s.Status != StatusStale {
		t.Fatalf("expected stale: %+v", s)
	}
}

func TestNamesMatchOnShortNameIgnoringCaseAndDomain(t *testing.T) {
	a := Assess([]string{"Node-A"}, []CheckIn{checkIn("h", "node-a.armadillo-peacock.ts.net", "1", time.Minute)}, now, Options{})
	if a.Servers[0].Status != StatusOK || len(a.Unmatched) != 0 {
		t.Fatalf("a fully qualified hostname must match the node's short name: %+v", a)
	}
}

func TestAReinstalledHostUsesItsNewestIDAndIsNotCountedTwice(t *testing.T) {
	a := Assess([]string{"node-a"}, []CheckIn{
		checkIn("old-id", "node-a", "0.1.0", 3*time.Hour),
		checkIn("new-id", "node-a", "0.1.0", time.Minute),
	}, now, Options{})
	if len(a.Servers) != 1 || a.Servers[0].HostID != "new-id" || a.Servers[0].Status != StatusOK || len(a.Unmatched) != 0 {
		t.Fatalf("expected one healthy row using the new ID: %+v", a)
	}
}

func TestAgentsOnHostsTheClusterDoesNotListAreReportedSeparately(t *testing.T) {
	a := Assess([]string{"node-a"}, []CheckIn{
		checkIn("h1", "node-a", "1", time.Minute),
		checkIn("h2", "new-box", "1", time.Minute),
	}, now, Options{})
	if len(a.Unmatched) != 1 || a.Unmatched[0].Hostname != "new-box" {
		t.Fatalf("expected new-box unmatched: %+v", a.Unmatched)
	}
}

func TestServersAreSortedAndDuplicatesCollapsed(t *testing.T) {
	a := Assess([]string{"b", "a", "B.lan"}, nil, now, Options{})
	if len(a.Servers) != 2 || a.Servers[0].Server != "a" || a.Servers[1].Server != "b" {
		t.Fatalf("unexpected: %+v", a.Servers)
	}
}

func TestVersionComparison(t *testing.T) {
	cases := []struct {
		a, b string
		less bool
	}{
		{"0.1.0", "0.2.0", true},
		{"0.2.0", "0.1.0", false},
		{"0.10.0", "0.9.0", false}, // numeric, not alphabetical
		{"1.0", "1.0.1", true},
		{"v1.2.3", "1.2.4", true},
		{"1.2.3", "1.2.3", false},
		{"dev", "1.0.0", false}, // unreadable never raises an alarm
		{"1.0.0", "", false},
		{"", "1.0.0", false},
	}
	for _, c := range cases {
		if got := versionLess(c.a, c.b); got != c.less {
			t.Errorf("versionLess(%q, %q) = %v, want %v", c.a, c.b, got, c.less)
		}
	}
}
