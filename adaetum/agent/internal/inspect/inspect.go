// Package inspect collects a read-only snapshot of a host and of the RKE2
// installation on it.
//
// The Report type is the first contract between the host agent and its
// consumers (the future portal, other agents). Collection must never change the
// host: every command run here is a query, and every failure is recorded in the
// report instead of aborting, so a half-broken host still produces an answer.
package inspect

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"runtime"
	"sort"
	"strings"
	"sync"
	"time"
)

// rke2Binaries lists where RKE2 installs its binary: /usr/bin for the RPM
// (Rocky) install, /usr/local/bin for the tarball install script.
var rke2Binaries = []string{"/usr/bin/rke2", "/usr/local/bin/rke2"}

// SchemaVersion is bumped when Report changes in a way consumers must notice.
const SchemaVersion = 1

const (
	rke2Kubectl    = "/var/lib/rancher/rke2/bin/kubectl"
	rke2Kubeconfig = "/etc/rancher/rke2/rke2.yaml"
)

// Report is the JSON document served by the agent and printed by `inspect`.
type Report struct {
	SchemaVersion int       `json:"schemaVersion"`
	CollectedAt   time.Time `json:"collectedAt"`
	Host          Host      `json:"host"`
	RKE2          RKE2      `json:"rke2"`
	// Metrics is nil when nothing could be read. Like Workloads and Pods it is
	// an additive field, so SchemaVersion is unchanged.
	Metrics *Metrics `json:"metrics,omitempty"`
	// Errors lists collection problems. A non-empty list does not mean the
	// report is unusable; fields that could not be read are left empty.
	Errors []string `json:"errors,omitempty"`
}

type Host struct {
	Hostname  string `json:"hostname"`
	OSID      string `json:"osId"`
	OSVersion string `json:"osVersion"`
	OSName    string `json:"osName"`
	Kernel    string `json:"kernel"`
	Arch      string `json:"arch"`
	// FailedUnits are systemd units in the failed state.
	FailedUnits []string `json:"failedUnits"`
	// ContainerImages is about this machine's container runtime only; nil when
	// the host does not run RKE2 or the runtime could not be queried.
	ContainerImages *ContainerImages `json:"containerImages,omitempty"`
}

type RKE2 struct {
	Version string `json:"version,omitempty"`
	// Services maps systemd unit name to its `is-active` state.
	Services map[string]string `json:"services"`
	// Nodes is only filled when this host runs an RKE2 server that answers.
	Nodes []Node `json:"nodes,omitempty"`
	// Workloads and Pods are filled under the same condition as Nodes. They are
	// additive fields, so SchemaVersion is unchanged.
	Workloads []Workload `json:"workloads,omitempty"`
	Pods      []Pod      `json:"pods,omitempty"`
	// ArgoApps is also additive. It stays empty when Argo CD is not installed,
	// in which case the failed query is recorded in Report.Errors.
	ArgoApps []ArgoApp `json:"argoApps,omitempty"`
	// The remaining cluster views are additive too.
	Events  []Event  `json:"events,omitempty"`
	Volumes []Volume `json:"volumes,omitempty"`
	// StorageCapacity is Longhorn's disk accounting; nil when Longhorn cannot
	// be queried.
	StorageCapacity *StorageCapacity `json:"storageCapacity,omitempty"`
	Ingresses       []Ingress        `json:"ingresses,omitempty"`
	Certificates    []Certificate    `json:"certificates,omitempty"`
	// Autoscalers say how far each scaled workload may grow (additive field).
	Autoscalers []Autoscaler `json:"autoscalers,omitempty"`
	// Services, with the pods behind each, show how an app is reached (additive field).
	KubeServices []Service `json:"kubeServices,omitempty"`
}

type Node struct {
	Name  string `json:"name"`
	Ready bool   `json:"ready"`
	// Roles come from node-role.kubernetes.io/* labels.
	Roles      []string `json:"roles,omitempty"`
	Version    string   `json:"version,omitempty"`
	InternalIP string   `json:"internalIp,omitempty"`
	// Cordoned means the node is marked unschedulable.
	Cordoned bool `json:"cordoned,omitempty"`
	// Pressure lists active MemoryPressure, DiskPressure, or PIDPressure.
	Pressure []string `json:"pressure,omitempty"`
	// What Kubernetes already says about the machine, so nothing has to be measured
	// twice. Allocatable is what pods may use; Usage is the metrics API's current
	// reading and is absent (HasUsage false) without a metrics server.
	AllocatableCPUMillis   int64  `json:"allocatableCpuMillis,omitempty"`
	AllocatableMemoryBytes int64  `json:"allocatableMemoryBytes,omitempty"`
	HasUsage               bool   `json:"hasUsage,omitempty"`
	UsageCPUMillis         int64  `json:"usageCpuMillis,omitempty"`
	UsageMemoryBytes       int64  `json:"usageMemoryBytes,omitempty"`
	OSImage                string `json:"osImage,omitempty"`
	Kernel                 string `json:"kernel,omitempty"`
	ContainerRuntime       string `json:"containerRuntime,omitempty"`
	// ReadySince is when the Ready condition last changed, so a flapping or recently
	// restarted machine can be told from a steady one.
	ReadySince string `json:"readySince,omitempty"`
}

// Runner executes a command and returns its standard output. Output is
// returned even on error because `systemctl is-active` reports state on stdout
// while exiting non-zero for anything but "active".
type Runner func(ctx context.Context, name string, args ...string) (string, error)

// Inspector holds the host seams so collection can be tested without a host.
type Inspector struct {
	Run      Runner
	ReadFile func(path string) ([]byte, error)
	Hostname func() (string, error)
	Arch     string
	Now      func() time.Time

	// The last directory measurement, so repeated reports do not re-walk it.
	measureMutex  sync.Mutex
	measuredBytes uint64
	measuredAt    time.Time
}

// NewInspector returns an Inspector wired to the real host.
func NewInspector() *Inspector {
	return &Inspector{
		Run:      runAllowed,
		ReadFile: os.ReadFile,
		Hostname: os.Hostname,
		Arch:     runtime.GOARCH,
		Now:      time.Now,
	}
}

// allowedCommands is the complete set of programs inspection may execute. The
// real runner refuses anything else so that no future caller can turn this
// read-only package into a general command executor.
var allowedCommands = map[string]bool{
	"uname":     true,
	"systemctl": true,
	// timedatectl is read with "show -p NTPSynchronized" only: whether the clock is
	// synchronized, which first boot waits for because TLS depends on it.
	"timedatectl": true,
	"df":          true,
	"du":          true,
	"nproc":       true,
	rke2Kubectl:   true,
	rke2Crictl:    true,
}

func init() {
	for _, path := range rke2Binaries {
		allowedCommands[path] = true
	}
}

func runAllowed(ctx context.Context, name string, args ...string) (string, error) {
	if !allowedCommands[name] {
		return "", fmt.Errorf("command %q is not permitted for inspection", name)
	}
	// Stdout only: kubectl prints warnings to stderr, which would corrupt JSON.
	out, err := exec.CommandContext(ctx, name, args...).Output()
	return string(out), err
}

// Collect gathers the report. It never returns an error: failures are recorded
// in Report.Errors.
func (i *Inspector) Collect(ctx context.Context) Report {
	report := Report{
		SchemaVersion: SchemaVersion,
		CollectedAt:   i.Now().UTC(),
		Host:          Host{Arch: i.Arch},
		RKE2:          RKE2{Services: map[string]string{}},
	}
	sink := &errorSink{}
	fail := sink.add

	if name, err := i.Hostname(); err == nil {
		report.Host.Hostname = name
	} else {
		fail("hostname", err)
	}

	if data, err := i.ReadFile("/etc/os-release"); err == nil {
		fields := parseOSRelease(string(data))
		report.Host.OSID = fields["ID"]
		report.Host.OSVersion = fields["VERSION_ID"]
		report.Host.OSName = fields["PRETTY_NAME"]
	} else {
		fail("os-release", err)
	}

	if out, err := i.Run(ctx, "uname", "-r"); err == nil {
		report.Host.Kernel = strings.TrimSpace(out)
	} else {
		fail("kernel", err)
	}

	// Remote inspectors leave Arch empty because the local GOARCH would
	// describe the wrong machine.
	if report.Host.Arch == "" {
		if out, err := i.Run(ctx, "uname", "-m"); err == nil {
			report.Host.Arch = normalizeArch(out)
		} else {
			fail("arch", err)
		}
	}

	report.Metrics = i.collectMetrics(ctx, fail)

	// The exit status is deliberately ignored: inactive units exit non-zero but
	// still print their state, which is the answer we want.
	for _, unit := range []string{"rke2-server", "rke2-agent"} {
		out, _ := i.Run(ctx, "systemctl", "is-active", unit)
		state := strings.TrimSpace(out)
		if state == "" {
			state = "unknown"
		}
		report.RKE2.Services[unit] = state
	}

	if out, err := i.Run(ctx, "systemctl", "--failed", "--no-legend", "--plain"); err != nil {
		fail("failed units", err)
	} else {
		report.Host.FailedUnits = parseFailedUnits(out)
	}

	// Only hosts that run RKE2 have a container runtime to ask; elsewhere the
	// section is simply absent rather than an error.
	if report.RKE2.Services["rke2-server"] == "active" || report.RKE2.Services["rke2-agent"] == "active" {
		if images, err := i.collectImages(ctx); err != nil {
			fail("container images", err)
		} else {
			// A failed measurement leaves the figure at zero; the rest of the
			// image summary is still worth reporting.
			if bytes, err := i.containerdContentBytes(ctx); err != nil {
				fail("container image copies", err)
			} else {
				images.ContentBytes = bytes
			}
			report.Host.ContainerImages = images
		}
	}

	for _, binary := range rke2Binaries {
		if out, err := i.Run(ctx, binary, "--version"); err == nil {
			report.RKE2.Version = parseRKE2Version(out)
			break
		}
	}

	if report.RKE2.Services["rke2-server"] == "active" {
		i.collectCluster(ctx, &report.RKE2, fail)
	}

	report.Errors = sink.sorted()
	return report
}

// parseOSRelease reads KEY=value lines, stripping optional quotes.
func parseOSRelease(content string) map[string]string {
	fields := map[string]string{}
	for _, line := range strings.Split(content, "\n") {
		key, value, ok := strings.Cut(strings.TrimSpace(line), "=")
		if !ok || strings.HasPrefix(key, "#") {
			continue
		}
		fields[key] = strings.Trim(value, `"'`)
	}
	return fields
}

// parseRKE2Version returns the version token from `rke2 version v1.xx.x+rke2r1 (...)`.
func parseRKE2Version(output string) string {
	first, _, _ := strings.Cut(strings.TrimSpace(output), "\n")
	parts := strings.Fields(first)
	if len(parts) >= 3 && parts[0] == "rke2" && parts[1] == "version" {
		return parts[2]
	}
	return ""
}

// parseNodes reads `kubectl get nodes -o json`.
func parseNodes(data []byte) ([]Node, error) {
	var list struct {
		Items []struct {
			Metadata struct {
				Name   string            `json:"name"`
				Labels map[string]string `json:"labels"`
			} `json:"metadata"`
			Spec struct {
				Unschedulable bool `json:"unschedulable"`
			} `json:"spec"`
			Status struct {
				Conditions []struct {
					Type               string `json:"type"`
					Status             string `json:"status"`
					LastTransitionTime string `json:"lastTransitionTime"`
				} `json:"conditions"`
				Allocatable struct {
					CPU    string `json:"cpu"`
					Memory string `json:"memory"`
				} `json:"allocatable"`
				Addresses []struct {
					Type    string `json:"type"`
					Address string `json:"address"`
				} `json:"addresses"`
				NodeInfo struct {
					KubeletVersion          string `json:"kubeletVersion"`
					OSImage                 string `json:"osImage"`
					KernelVersion           string `json:"kernelVersion"`
					ContainerRuntimeVersion string `json:"containerRuntimeVersion"`
				} `json:"nodeInfo"`
			} `json:"status"`
		} `json:"items"`
	}
	if err := json.Unmarshal(data, &list); err != nil {
		return nil, err
	}

	const rolePrefix = "node-role.kubernetes.io/"
	nodes := make([]Node, 0, len(list.Items))
	for _, item := range list.Items {
		node := Node{
			Name:     item.Metadata.Name,
			Version:  item.Status.NodeInfo.KubeletVersion,
			Cordoned: item.Spec.Unschedulable,

			OSImage:          item.Status.NodeInfo.OSImage,
			Kernel:           item.Status.NodeInfo.KernelVersion,
			ContainerRuntime: item.Status.NodeInfo.ContainerRuntimeVersion,
		}
		node.AllocatableCPUMillis, _ = parseCPUMillis(item.Status.Allocatable.CPU)
		node.AllocatableMemoryBytes, _ = parseBytes(item.Status.Allocatable.Memory)
		for label := range item.Metadata.Labels {
			if role, ok := strings.CutPrefix(label, rolePrefix); ok && role != "" {
				node.Roles = append(node.Roles, role)
			}
		}
		sort.Strings(node.Roles)
		for _, address := range item.Status.Addresses {
			if address.Type == "InternalIP" {
				node.InternalIP = address.Address
			}
		}
		for _, condition := range item.Status.Conditions {
			switch condition.Type {
			case "Ready":
				node.Ready = condition.Status == "True"
				node.ReadySince = condition.LastTransitionTime
			case "MemoryPressure", "DiskPressure", "PIDPressure":
				if condition.Status == "True" {
					node.Pressure = append(node.Pressure, condition.Type)
				}
			}
		}
		nodes = append(nodes, node)
	}
	return nodes, nil
}
