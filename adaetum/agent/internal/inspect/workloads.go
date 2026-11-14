package inspect

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
)

// Workload is a Deployment, StatefulSet, or DaemonSet and how many of its
// desired replicas are ready.
type Workload struct {
	Kind      string `json:"kind"`
	Namespace string `json:"namespace"`
	Name      string `json:"name"`
	Desired   int    `json:"desired"`
	Ready     int    `json:"ready"`
	// Accepted names the sizing shortfalls a person has chosen to live with, from the
	// workload's own adaetum.io/accepted annotation (see acceptedKeys). The score still counts
	// them; the page shows them in gray instead of as problems.
	Accepted []string `json:"accepted,omitempty"`
}

// acceptedAnnotation holds a comma-separated list of acceptedKeys. It lives on the workload
// (the Deployment, StatefulSet or DaemonSet), so for a chart that is not ours it is added with
// a kustomize patch in pods/, next to the thing it excuses.
const acceptedAnnotation = "adaetum.io/accepted"

// acceptedKeys is every shortfall that can be accepted. The page's sizing (web/src/lib/sizing.ts)
// decides what each one covers; an unknown word is dropped here so a typo accepts nothing.
var acceptedKeys = map[string]bool{
	"memory-limit":     true, // no memory limit
	"memory-request":   true, // no memory request
	"cpu-request":      true, // no CPU request
	"memory-ratio":     true, // memory limit far above the request
	"over-provisioned": true, // reserves much more than it uses
}

func parseAccepted(annotations map[string]string) []string {
	var keys []string
	seen := map[string]bool{}
	for _, word := range strings.Split(annotations[acceptedAnnotation], ",") {
		word = strings.TrimSpace(word)
		if acceptedKeys[word] && !seen[word] {
			seen[word] = true
			keys = append(keys, word)
		}
	}
	return keys
}

// Pod is the subset of pod state needed to tell at a glance what is unhealthy.
type Pod struct {
	Namespace       string `json:"namespace"`
	Name            string `json:"name"`
	Node            string `json:"node"`
	Phase           string `json:"phase"`
	ReadyContainers int    `json:"readyContainers"`
	TotalContainers int    `json:"totalContainers"`
	Restarts        int    `json:"restarts"`
	// Reason is the first waiting reason (for example CrashLoopBackOff) or the
	// pod-level reason (for example Evicted). It is empty for healthy pods.
	Reason string `json:"reason,omitempty"`
	// HasUsage says the metrics server reported this pod; CPUMillis (thousandths of a
	// core) and MemoryBytes are what it is using right now.
	HasUsage    bool  `json:"hasUsage,omitempty"`
	CPUMillis   int64 `json:"cpuMillis,omitempty"`
	MemoryBytes int64 `json:"memoryBytes,omitempty"`
	// The limits are totals over the pod's containers. Zero means at least one
	// container has no limit, so the pod's ceiling is unknown, not zero.
	CPULimitMillis   int64 `json:"cpuLimitMillis,omitempty"`
	MemoryLimitBytes int64 `json:"memoryLimitBytes,omitempty"`
	// Requests are what the scheduler reserves for the pod, totalled like the limits:
	// zero means at least one container requests none.
	CPURequestMillis   int64 `json:"cpuRequestMillis,omitempty"`
	MemoryRequestBytes int64 `json:"memoryRequestBytes,omitempty"`
	// Calls are the services ("namespace/name") this pod's own settings mention. A hint
	// found by reading environment values and arguments, not a record of traffic.
	Calls []string `json:"calls,omitempty"`

	// labels and settings are only needed to link services, so they never leave the agent.
	labels   map[string]string
	settings []string
}

type objectMeta struct {
	Name        string            `json:"name"`
	Namespace   string            `json:"namespace"`
	Annotations map[string]string `json:"annotations"`
}

// parseWorkloads reads `kubectl get deployments,statefulsets,daemonsets -o json`.
// Items of any other kind are skipped.
func parseWorkloads(data []byte) ([]Workload, error) {
	var list struct {
		Items []struct {
			Kind     string     `json:"kind"`
			Metadata objectMeta `json:"metadata"`
			Spec     struct {
				// A nil replica count means the Kubernetes default of 1.
				Replicas *int `json:"replicas"`
			} `json:"spec"`
			Status struct {
				ReadyReplicas          int `json:"readyReplicas"`
				DesiredNumberScheduled int `json:"desiredNumberScheduled"`
				NumberReady            int `json:"numberReady"`
			} `json:"status"`
		} `json:"items"`
	}
	if err := json.Unmarshal(data, &list); err != nil {
		return nil, err
	}

	workloads := []Workload{}
	for _, item := range list.Items {
		workload := Workload{Kind: item.Kind, Namespace: item.Metadata.Namespace, Name: item.Metadata.Name, Accepted: parseAccepted(item.Metadata.Annotations)}
		switch item.Kind {
		case "Deployment", "StatefulSet":
			workload.Desired = 1
			if item.Spec.Replicas != nil {
				workload.Desired = *item.Spec.Replicas
			}
			workload.Ready = item.Status.ReadyReplicas
		case "DaemonSet":
			workload.Desired = item.Status.DesiredNumberScheduled
			workload.Ready = item.Status.NumberReady
		default:
			continue
		}
		workloads = append(workloads, workload)
	}
	return workloads, nil
}

// parsePods reads `kubectl get pods -A -o json`.
func parsePods(data []byte) ([]Pod, error) {
	var list struct {
		Items []struct {
			Metadata struct {
				objectMeta
				Labels map[string]string `json:"labels"`
			} `json:"metadata"`
			Spec struct {
				NodeName   string          `json:"nodeName"`
				Containers []containerSpec `json:"containers"`
			} `json:"spec"`
			Status struct {
				Phase             string `json:"phase"`
				Reason            string `json:"reason"`
				ContainerStatuses []struct {
					Ready        bool `json:"ready"`
					RestartCount int  `json:"restartCount"`
					State        struct {
						Waiting *struct {
							Reason string `json:"reason"`
						} `json:"waiting"`
					} `json:"state"`
				} `json:"containerStatuses"`
			} `json:"status"`
		} `json:"items"`
	}
	if err := json.Unmarshal(data, &list); err != nil {
		return nil, err
	}

	pods := make([]Pod, 0, len(list.Items))
	for _, item := range list.Items {
		pod := Pod{
			Namespace: item.Metadata.Namespace,
			Name:      item.Metadata.Name,
			Node:      item.Spec.NodeName,
			Phase:     item.Status.Phase,
			Reason:    item.Status.Reason,
			labels:    item.Metadata.Labels,
		}
		for _, container := range item.Spec.Containers {
			for _, variable := range container.Env {
				pod.settings = append(pod.settings, variable.Value)
			}
			pod.settings = append(pod.settings, container.Command...)
			pod.settings = append(pod.settings, container.Args...)
		}
		pod.CPULimitMillis, pod.MemoryLimitBytes = podTotals(item.Spec.Containers, func(c containerSpec) resourceQuantities { return c.Resources.Limits })
		pod.CPURequestMillis, pod.MemoryRequestBytes = podTotals(item.Spec.Containers, func(c containerSpec) resourceQuantities { return c.Resources.Requests })
		for _, container := range item.Status.ContainerStatuses {
			pod.TotalContainers++
			pod.Restarts += container.RestartCount
			if container.Ready {
				pod.ReadyContainers++
			}
			if pod.Reason == "" && container.State.Waiting != nil {
				pod.Reason = container.State.Waiting.Reason
			}
		}
		pods = append(pods, pod)
	}
	return pods, nil
}

// containerSpec is the part of a container definition the report reads.
type containerSpec struct {
	Env []struct {
		Value string `json:"value"`
	} `json:"env"`
	Command   []string `json:"command"`
	Args      []string `json:"args"`
	Resources struct {
		Limits   resourceQuantities `json:"limits"`
		Requests resourceQuantities `json:"requests"`
	} `json:"resources"`
}

type resourceQuantities struct {
	CPU    string `json:"cpu"`
	Memory string `json:"memory"`
}

// podTotals adds up one kind of quantity (limits or requests) over the containers.
// One container without it leaves that resource unset for the pod, which is reported
// as 0 (unknown) rather than a misleading sum of the others.
func podTotals(containers []containerSpec, pick func(containerSpec) resourceQuantities) (cpuMillis, memoryBytes int64) {
	cpuKnown, memoryKnown := len(containers) > 0, len(containers) > 0
	for _, container := range containers {
		quantities := pick(container)
		if millis, ok := parseCPUMillis(quantities.CPU); ok && millis > 0 {
			cpuMillis += millis
		} else {
			cpuKnown = false
		}
		if bytes, ok := parseBytes(quantities.Memory); ok && bytes > 0 {
			memoryBytes += bytes
		} else {
			memoryKnown = false
		}
	}
	if !cpuKnown {
		cpuMillis = 0
	}
	if !memoryKnown {
		memoryBytes = 0
	}
	return cpuMillis, memoryBytes
}

// kubectlGet runs a read-only `kubectl get` against the local RKE2 server.
func (i *Inspector) kubectlGet(ctx context.Context, resources string, extra ...string) ([]byte, error) {
	if i.Get != nil {
		return i.Get(ctx, resources, extra...)
	}
	args := append([]string{"--kubeconfig", rke2Kubeconfig, "get", resources}, extra...)
	args = append(args, "-o", "json")
	out, err := i.Run(ctx, rke2Kubectl, args...)
	if err != nil {
		return nil, fmt.Errorf("kubectl get %s: %w", resources, err)
	}
	return []byte(out), nil
}

// kubectlRaw reads an API server path directly, such as a kubelet's stats via
// the node proxy. It is still a read: `get --raw` issues a GET.
func (i *Inspector) kubectlRaw(ctx context.Context, path string) ([]byte, error) {
	if i.Raw != nil {
		return i.Raw(ctx, path)
	}
	out, err := i.Run(ctx, rke2Kubectl, "--kubeconfig", rke2Kubeconfig, "get", "--raw", path)
	if err != nil {
		return nil, fmt.Errorf("kubectl get --raw %s: %w", path, err)
	}
	return []byte(out), nil
}
