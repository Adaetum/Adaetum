package inspect

import (
	"context"
	"fmt"
	"strconv"
	"strings"
)

// Metrics is a point-in-time resource snapshot. It is deliberately not a time
// series: rates (CPU percent, network throughput) need two samples and belong
// with the future local database, not in a stateless inspect call.
type Metrics struct {
	UptimeSeconds float64 `json:"uptimeSeconds"`
	CPUCount      int     `json:"cpuCount"`
	Load1         float64 `json:"load1"`
	Load5         float64 `json:"load5"`
	Load15        float64 `json:"load15"`
	// Memory values are bytes. MemAvailable is the kernel's estimate of memory
	// usable without swapping, which is the figure that matters, not MemFree.
	MemTotal     uint64 `json:"memTotalBytes"`
	MemAvailable uint64 `json:"memAvailableBytes"`
	SwapTotal    uint64 `json:"swapTotalBytes"`
	SwapFree     uint64 `json:"swapFreeBytes"`
	Disks        []Disk `json:"disks,omitempty"`
}

// Disk is one mounted filesystem. Sizes are bytes.
type Disk struct {
	Mount      string `json:"mount"`
	Filesystem string `json:"filesystem"`
	Total      uint64 `json:"totalBytes"`
	Used       uint64 `json:"usedBytes"`
	Available  uint64 `json:"availableBytes"`
}

// Pseudo and container filesystems are excluded: they are not capacity the
// operator manages and a Kubernetes node mounts hundreds of them.
const dfExcludes = "tmpfs devtmpfs overlay squashfs efivarfs"

// collectMetrics fills what it can and reports each failure separately, so a
// host without /proc (such as a developer laptop) still yields a report.
func (i *Inspector) collectMetrics(ctx context.Context, fail func(string, error)) *Metrics {
	metrics := &Metrics{}
	collected := false

	if data, err := i.ReadFile("/proc/uptime"); err != nil {
		fail("uptime", err)
	} else if uptime, err := parseUptime(string(data)); err != nil {
		fail("uptime", err)
	} else {
		metrics.UptimeSeconds, collected = uptime, true
	}

	if data, err := i.ReadFile("/proc/loadavg"); err != nil {
		fail("loadavg", err)
	} else if load, err := parseLoadavg(string(data)); err != nil {
		fail("loadavg", err)
	} else {
		metrics.Load1, metrics.Load5, metrics.Load15, collected = load[0], load[1], load[2], true
	}

	if out, err := i.Run(ctx, "nproc"); err != nil {
		fail("cpu count", err)
	} else if count, err := strconv.Atoi(strings.TrimSpace(out)); err != nil {
		fail("cpu count", err)
	} else {
		metrics.CPUCount, collected = count, true
	}

	if data, err := i.ReadFile("/proc/meminfo"); err != nil {
		fail("meminfo", err)
	} else {
		meminfo := parseMeminfo(string(data))
		metrics.MemTotal = meminfo["MemTotal"]
		metrics.MemAvailable = meminfo["MemAvailable"]
		metrics.SwapTotal = meminfo["SwapTotal"]
		metrics.SwapFree = meminfo["SwapFree"]
		collected = true
	}

	// df exits non-zero when any one mount is unreadable but still prints the
	// rest, so keep the output and only fail when there is none.
	args := []string{"-P", "-B1"}
	for _, fsType := range strings.Fields(dfExcludes) {
		args = append(args, "-x", fsType)
	}
	if out, err := i.Run(ctx, "df", args...); strings.TrimSpace(out) == "" {
		if err == nil {
			err = fmt.Errorf("no output")
		}
		fail("disks", err)
	} else {
		metrics.Disks, collected = parseDF(out), true
	}

	if !collected {
		return nil
	}
	return metrics
}

func parseUptime(content string) (float64, error) {
	fields := strings.Fields(content)
	if len(fields) < 1 {
		return 0, fmt.Errorf("empty /proc/uptime")
	}
	return strconv.ParseFloat(fields[0], 64)
}

func parseLoadavg(content string) ([3]float64, error) {
	var load [3]float64
	fields := strings.Fields(content)
	if len(fields) < 3 {
		return load, fmt.Errorf("unexpected /proc/loadavg %q", strings.TrimSpace(content))
	}
	for index := range load {
		value, err := strconv.ParseFloat(fields[index], 64)
		if err != nil {
			return load, err
		}
		load[index] = value
	}
	return load, nil
}

// parseMeminfo converts /proc/meminfo "Key:   123 kB" lines to bytes.
func parseMeminfo(content string) map[string]uint64 {
	values := map[string]uint64{}
	for _, line := range strings.Split(content, "\n") {
		key, rest, ok := strings.Cut(line, ":")
		if !ok {
			continue
		}
		fields := strings.Fields(rest)
		if len(fields) == 0 {
			continue
		}
		number, err := strconv.ParseUint(fields[0], 10, 64)
		if err != nil {
			continue
		}
		if len(fields) > 1 && fields[1] == "kB" {
			number *= 1024
		}
		values[key] = number
	}
	return values
}

// parseDF reads `df -P -B1` output: Filesystem, 1-blocks, Used, Available,
// Capacity, Mounted on. The mount point is last and may contain spaces.
func parseDF(output string) []Disk {
	disks := []Disk{}
	lines := strings.Split(strings.TrimSpace(output), "\n")
	for _, line := range lines[min(1, len(lines)):] {
		fields := strings.Fields(line)
		if len(fields) < 6 {
			continue
		}
		total, errTotal := strconv.ParseUint(fields[1], 10, 64)
		used, errUsed := strconv.ParseUint(fields[2], 10, 64)
		available, errAvailable := strconv.ParseUint(fields[3], 10, 64)
		if errTotal != nil || errUsed != nil || errAvailable != nil {
			continue
		}
		disks = append(disks, Disk{
			Filesystem: fields[0],
			Total:      total,
			Used:       used,
			Available:  available,
			Mount:      strings.Join(fields[5:], " "),
		})
	}
	return disks
}
