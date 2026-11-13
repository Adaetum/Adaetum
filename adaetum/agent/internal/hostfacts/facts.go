// Package hostfacts reads a few facts about the machine the agent runs on, for the
// procedures that decide whether it is ready for something: whether it has a route
// out, whether its clock is synchronized, how much disk is free.
//
// It is an adapter in the run engine's sense: the only place these procedures touch the
// real machine. Everything here is READ-ONLY, and it adds no way to run commands of its
// own. It reads files and runs the fixed, read-only commands the inspector already
// allows (internal/inspect), so it is built on the inspector's seams and is tested with
// fake files and fake command output.
package hostfacts

import (
	"context"
	"errors"
	"fmt"
	"regexp"
	"strconv"
	"strings"

	"github.com/Adaetum/Adaetum/agent/internal/inspect"
)

// Facts reads the machine through an Inspector.
type Facts struct {
	Inspector *inspect.Inspector
}

// HasDefaultRoute reports whether the machine has a route to the outside world, IPv4 or
// IPv6. It is the check first boot makes before it tries to reach anything.
func (f Facts) HasDefaultRoute(context.Context) (bool, error) {
	v4, err := f.Inspector.ReadFile("/proc/net/route")
	if err != nil {
		return false, fmt.Errorf("reading the routing table: %w", err)
	}
	if hasIPv4Default(v4) {
		return true, nil
	}
	// A machine with only IPv6 is still online. The file may not exist (IPv6 off), which
	// just means there is no IPv6 route.
	if v6, err := f.Inspector.ReadFile("/proc/net/ipv6_route"); err == nil && hasIPv6Default(v6) {
		return true, nil
	}
	return false, nil
}

// hasIPv4Default reads /proc/net/route. Columns: Iface Destination Gateway Flags RefCnt
// Use Metric Mask ... A default route has destination 0, mask 0, and the UP flag (0x1).
func hasIPv4Default(data []byte) bool {
	for i, line := range strings.Split(string(data), "\n") {
		fields := strings.Fields(line)
		if i == 0 || len(fields) < 8 {
			continue // header or a short line
		}
		flags, err := strconv.ParseUint(fields[3], 16, 32)
		if err != nil {
			continue
		}
		if fields[1] == "00000000" && fields[7] == "00000000" && flags&0x1 != 0 {
			return true
		}
	}
	return false
}

// hasIPv6Default reads /proc/net/ipv6_route. Columns: dest destPrefix src srcPrefix
// nexthop metric refcnt use flags iface. A default route has an all-zero destination and
// a zero prefix. The kernel also lists "reject" default entries on the loopback device
// (flag 0x200); those are not a way out.
func hasIPv6Default(data []byte) bool {
	const zero = "00000000000000000000000000000000"
	for _, line := range strings.Split(string(data), "\n") {
		fields := strings.Fields(line)
		if len(fields) < 10 {
			continue
		}
		flags, err := strconv.ParseUint(fields[8], 16, 32)
		if err != nil {
			continue
		}
		if fields[0] == zero && fields[1] == "00" && flags&0x1 != 0 && flags&0x200 == 0 && fields[9] != "lo" {
			return true
		}
	}
	return false
}

// ClockSynchronized reports whether the system clock is synchronized with a time
// source. known is false when the machine could not say (no timedatectl, an error), so a
// caller can tell "not yet" from "cannot tell".
func (f Facts) ClockSynchronized(ctx context.Context) (synced, known bool) {
	out, err := f.Inspector.Run(ctx, "timedatectl", "show", "-p", "NTPSynchronized", "--value")
	if err != nil && strings.TrimSpace(out) == "" {
		return false, false
	}
	switch strings.ToLower(strings.TrimSpace(out)) {
	case "yes", "true", "1":
		return true, true
	case "no", "false", "0":
		return false, true
	}
	return false, false
}

// A path handed to df: absolute, and only ordinary path characters, so it can never be
// read as an option.
var safePath = regexp.MustCompile(`^/[A-Za-z0-9._/-]{0,200}$`)

// FreeBytes returns the space available to ordinary programs on the filesystem that
// holds path.
func (f Facts) FreeBytes(ctx context.Context, path string) (uint64, error) {
	if !safePath.MatchString(path) {
		return 0, errors.New("the path must be absolute and use only letters, digits and . _ / -")
	}
	out, err := f.Inspector.Run(ctx, "df", "-P", "-B1", path)
	if err != nil && strings.TrimSpace(out) == "" {
		return 0, fmt.Errorf("measuring free space on %s: %w", path, err)
	}
	return parseDF(out)
}

// parseDF reads POSIX df output: a header, then
// "Filesystem 1-blocks Used Available Capacity Mounted-on".
func parseDF(output string) (uint64, error) {
	lines := strings.Split(strings.TrimSpace(output), "\n")
	if len(lines) < 2 {
		return 0, errors.New("df printed no filesystem")
	}
	fields := strings.Fields(lines[len(lines)-1])
	if len(fields) < 4 {
		return 0, errors.New("df output was not understood")
	}
	available, err := strconv.ParseUint(fields[3], 10, 64)
	if err != nil {
		return 0, fmt.Errorf("df reported %q for the free space", fields[3])
	}
	return available, nil
}
