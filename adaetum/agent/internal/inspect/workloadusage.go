package inspect

import (
	"context"
	"errors"
	"sort"
)

// WorkloadUsage is what one workload's running copies use at this moment, per copy:
// the busiest copy and the average. It is what usage history records.
type WorkloadUsage struct {
	Namespace  string
	Name       string
	Copies     int
	MemoryPeak int64 // bytes
	MemoryAvg  int64
	CPUPeak    int64 // millicores
	CPUAvg     int64
}

// WorkloadUsage takes one reading of every workload's usage. It is three read-only
// kubectl queries (workloads, pods, the metrics API). Without a metrics server it
// fails rather than returning zeros, so a gap in history is a gap, not a flat line.
func (i *Inspector) WorkloadUsage(ctx context.Context) ([]WorkloadUsage, error) {
	var rke2 RKE2
	var sink errorSink
	fail := func(what string, err error) { sink.add(what, err) }
	runConcurrently(2,
		query(i, ctx, fail, "workloads", &rke2.Workloads, parseWorkloads, "deployments,statefulsets,daemonsets", "-A"),
		query(i, ctx, fail, "pods", &rke2.Pods, parsePods, "pods", "-A"),
	)
	if len(sink.errors) > 0 {
		return nil, errors.New(sink.sorted()[0])
	}
	i.collectUsage(ctx, &rke2, fail)
	if len(sink.errors) > 0 {
		return nil, errors.New(sink.sorted()[0])
	}
	return aggregateUsage(rke2.Workloads, rke2.Pods), nil
}

// aggregateUsage assigns each running, measured pod to the workload whose name is the
// longest prefix of the pod's name ("media-stack-octo-ytdlp-shim-x" belongs to
// "media-stack-octo-ytdlp-shim", not to "media-stack-octo"), then totals per workload.
func aggregateUsage(workloads []Workload, pods []Pod) []WorkloadUsage {
	byNamespace := map[string][]string{}
	for _, workload := range workloads {
		byNamespace[workload.Namespace] = append(byNamespace[workload.Namespace], workload.Name)
	}
	type total struct {
		usage             WorkloadUsage
		memorySum, cpuSum int64
	}
	totals := map[string]*total{}
	for _, pod := range pods {
		if pod.Phase != "Running" || !pod.HasUsage {
			continue
		}
		owner := ""
		for _, name := range byNamespace[pod.Namespace] {
			if len(pod.Name) > len(name)+1 && pod.Name[:len(name)+1] == name+"-" && len(name) > len(owner) {
				owner = name
			}
		}
		if owner == "" {
			continue
		}
		key := pod.Namespace + "/" + owner
		t := totals[key]
		if t == nil {
			t = &total{usage: WorkloadUsage{Namespace: pod.Namespace, Name: owner}}
			totals[key] = t
		}
		t.usage.Copies++
		t.memorySum += pod.MemoryBytes
		t.cpuSum += pod.CPUMillis
		if pod.MemoryBytes > t.usage.MemoryPeak {
			t.usage.MemoryPeak = pod.MemoryBytes
		}
		if pod.CPUMillis > t.usage.CPUPeak {
			t.usage.CPUPeak = pod.CPUMillis
		}
	}
	out := make([]WorkloadUsage, 0, len(totals))
	for _, t := range totals {
		t.usage.MemoryAvg = t.memorySum / int64(t.usage.Copies)
		t.usage.CPUAvg = t.cpuSum / int64(t.usage.Copies)
		out = append(out, t.usage)
	}
	sort.Slice(out, func(a, b int) bool {
		if out[a].Namespace != out[b].Namespace {
			return out[a].Namespace < out[b].Namespace
		}
		return out[a].Name < out[b].Name
	})
	return out
}

// VolumeUsage is how full one volume is at this moment.
type VolumeUsage struct {
	Namespace     string
	Name          string
	CapacityBytes int64
	UsedBytes     int64
}

// VolumeUsage takes one reading of every measurable volume (Longhorn-backed ones; the
// rest have no used figure and are skipped).
func (i *Inspector) VolumeUsage(ctx context.Context) ([]VolumeUsage, error) {
	var sink errorSink
	volumes := i.collectVolumes(ctx, func(what string, err error) { sink.add(what, err) })
	if len(volumes) == 0 && len(sink.errors) > 0 {
		return nil, errors.New(sink.sorted()[0])
	}
	var out []VolumeUsage
	for _, volume := range volumes {
		if volume.UsedBytes == nil || volume.CapacityBytes == 0 {
			continue
		}
		out = append(out, VolumeUsage{Namespace: volume.Namespace, Name: volume.Name, CapacityBytes: int64(volume.CapacityBytes), UsedBytes: int64(*volume.UsedBytes)})
	}
	return out, nil
}
