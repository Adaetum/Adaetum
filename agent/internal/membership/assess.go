package membership

import (
	"sort"
	"strconv"
	"strings"
	"time"
)

// Status says whether a cluster server is running a healthy, current host agent.
type Status string

const (
	// StatusOK: the server has checked in recently with the current version.
	StatusOK Status = "ok"
	// StatusMissing: the cluster knows the server but no agent has ever checked in
	// for it. The agent is probably not installed.
	StatusMissing Status = "missing"
	// StatusStale: the agent checked in before but not recently. It may be stopped,
	// the host may be down, or the host may be cut off from the cluster. This does
	// not say which; the host agent's own endpoint is how to find out.
	StatusStale Status = "stale"
	// StatusOutdated: checking in, but running an older version than the newest
	// known. The agent is kept current by Ansible; this shows where that lags.
	StatusOutdated Status = "outdated"
)

// Options tunes Assess. The zero value is usable.
type Options struct {
	// StaleAfter is how long without a check-in before a host is stale. Hosts
	// check in every five minutes, so the default of fifteen tolerates two missed.
	StaleAfter time.Duration
	// LatestVersion is the newest agent version known to the cluster. Empty disables
	// the outdated check, because "outdated" needs something to be older than.
	LatestVersion string
}

const defaultStaleAfter = 15 * time.Minute

// ServerStatus is one row of the cluster view.
type ServerStatus struct {
	Server  string `json:"server"`
	Status  Status `json:"status"`
	HostID  string `json:"hostId,omitempty"`
	Version string `json:"version,omitempty"`
	// LastSeen is nil when the server has never reported (StatusMissing). A pointer,
	// not a zero time, so the JSON omits it instead of sending year 1.
	LastSeen *time.Time `json:"lastSeen,omitempty"`
}

// Assessment is the result of comparing the cluster's servers with the check-ins.
type Assessment struct {
	// Servers has one entry per cluster server, sorted by name.
	Servers []ServerStatus `json:"servers"`
	// Unmatched are agents that checked in from a host the cluster does not list,
	// for example a machine about to join, or a node that was removed.
	Unmatched []CheckIn `json:"unmatched"`
}

// Assess compares the servers Kubernetes lists with the check-ins received. It is
// pure, so the dashboard rule "which servers are not running the agent?" can be
// tested without a cluster.
//
// Servers and hosts are matched on short hostname, ignoring case and any domain
// suffix, because Kubernetes names nodes by the short name while a host may report
// a fully qualified one. If several host IDs check in under one name (a machine
// reinstalled, so it has a new database and a new ID) the newest check-in wins and
// the older ID is ignored rather than reported as a second server.
func Assess(servers []string, checkIns []CheckIn, now time.Time, options Options) Assessment {
	staleAfter := options.StaleAfter
	if staleAfter <= 0 {
		staleAfter = defaultStaleAfter
	}

	newest := map[string]CheckIn{}
	for _, checkIn := range checkIns {
		key := shortName(checkIn.Hostname)
		if current, found := newest[key]; !found || checkIn.At.After(current.At) {
			newest[key] = checkIn
		}
	}

	var result Assessment
	matched := map[string]bool{}
	for _, server := range servers {
		key := shortName(server)
		if matched[key] {
			continue // the same node listed twice
		}
		matched[key] = true

		checkIn, found := newest[key]
		status := ServerStatus{Server: server, Status: StatusMissing}
		if found {
			seen := checkIn.At
			status.HostID, status.Version, status.LastSeen = checkIn.HostID, checkIn.Version, &seen
			switch {
			case now.Sub(checkIn.At) > staleAfter:
				status.Status = StatusStale
			case options.LatestVersion != "" && versionLess(checkIn.Version, options.LatestVersion):
				status.Status = StatusOutdated
			default:
				status.Status = StatusOK
			}
		}
		result.Servers = append(result.Servers, status)
	}
	sort.Slice(result.Servers, func(a, b int) bool { return result.Servers[a].Server < result.Servers[b].Server })

	for key, checkIn := range newest {
		if !matched[key] {
			result.Unmatched = append(result.Unmatched, checkIn)
		}
	}
	sort.Slice(result.Unmatched, func(a, b int) bool { return result.Unmatched[a].Hostname < result.Unmatched[b].Hostname })
	return result
}

func shortName(name string) string {
	short, _, _ := strings.Cut(strings.ToLower(strings.TrimSpace(name)), ".")
	return short
}

// versionLess reports whether a is an older dotted-number version than b
// ("0.1.0" < "0.2.0"). A version that cannot be read is never considered older,
// so an unexpected value does not raise a false alarm.
func versionLess(a, b string) bool {
	left, leftOK := parseVersion(a)
	right, rightOK := parseVersion(b)
	if !leftOK || !rightOK {
		return false
	}
	for index := 0; index < len(left) || index < len(right); index++ {
		var l, r int
		if index < len(left) {
			l = left[index]
		}
		if index < len(right) {
			r = right[index]
		}
		if l != r {
			return l < r
		}
	}
	return false
}

func parseVersion(version string) ([]int, bool) {
	version = strings.TrimPrefix(strings.TrimSpace(version), "v")
	if version == "" {
		return nil, false
	}
	var parts []int
	for _, field := range strings.Split(version, ".") {
		number, err := strconv.Atoi(field)
		if err != nil || number < 0 {
			return nil, false
		}
		parts = append(parts, number)
	}
	return parts, true
}
