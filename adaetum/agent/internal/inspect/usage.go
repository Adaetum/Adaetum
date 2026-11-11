package inspect

import (
	"context"
	"encoding/json"
	"strconv"
	"strings"
)

// Autoscaler is a HorizontalPodAutoscaler: how many copies of a workload may run.
// With the pods' limits it gives the most an app could ever use, not just what it has
// been given right now.
type Autoscaler struct {
	Namespace string `json:"namespace"`
	Name      string `json:"name"`
	// Target is the workload it scales (kind and name).
	TargetKind string `json:"targetKind"`
	TargetName string `json:"targetName"`
	Min        int    `json:"min"`
	Max        int    `json:"max"`
	Current    int    `json:"current"`
}

// parseAutoscalers reads `kubectl get horizontalpodautoscalers -A -o json`.
func parseAutoscalers(data []byte) ([]Autoscaler, error) {
	var list struct {
		Items []struct {
			Metadata objectMeta `json:"metadata"`
			Spec     struct {
				MinReplicas    *int `json:"minReplicas"`
				MaxReplicas    int  `json:"maxReplicas"`
				ScaleTargetRef struct {
					Kind string `json:"kind"`
					Name string `json:"name"`
				} `json:"scaleTargetRef"`
			} `json:"spec"`
			Status struct {
				CurrentReplicas int `json:"currentReplicas"`
			} `json:"status"`
		} `json:"items"`
	}
	if err := json.Unmarshal(data, &list); err != nil {
		return nil, err
	}
	scalers := make([]Autoscaler, 0, len(list.Items))
	for _, item := range list.Items {
		scaler := Autoscaler{
			Namespace: item.Metadata.Namespace, Name: item.Metadata.Name,
			TargetKind: item.Spec.ScaleTargetRef.Kind, TargetName: item.Spec.ScaleTargetRef.Name,
			Min: 1, Max: item.Spec.MaxReplicas, Current: item.Status.CurrentReplicas,
		}
		if item.Spec.MinReplicas != nil {
			scaler.Min = *item.Spec.MinReplicas
		}
		scalers = append(scalers, scaler)
	}
	return scalers, nil
}

// podUsage is one pod's current use, from the metrics API.
type podUsage struct {
	cpuMillis   int64
	memoryBytes int64
}

// parsePodUsage reads the metrics API's pod list (`kubectl get --raw
// /apis/metrics.k8s.io/v1beta1/pods`), keyed by "namespace/name". Containers are summed.
func parsePodUsage(data []byte) (map[string]podUsage, error) {
	var list struct {
		Items []struct {
			Metadata   objectMeta `json:"metadata"`
			Containers []struct {
				Usage struct {
					CPU    string `json:"cpu"`
					Memory string `json:"memory"`
				} `json:"usage"`
			} `json:"containers"`
		} `json:"items"`
	}
	if err := json.Unmarshal(data, &list); err != nil {
		return nil, err
	}
	usage := make(map[string]podUsage, len(list.Items))
	for _, item := range list.Items {
		var total podUsage
		for _, container := range item.Containers {
			cpu, _ := parseCPUMillis(container.Usage.CPU)
			memory, _ := parseBytes(container.Usage.Memory)
			total.cpuMillis += cpu
			total.memoryBytes += memory
		}
		usage[item.Metadata.Namespace+"/"+item.Metadata.Name] = total
	}
	return usage, nil
}

// collectUsage merges current use into the pods. Without a metrics server the pods
// simply carry no usage, and the failure is reported.
func (i *Inspector) collectUsage(ctx context.Context, rke2 *RKE2, fail func(string, error)) {
	data, err := i.kubectlRaw(ctx, "/apis/metrics.k8s.io/v1beta1/pods")
	if err != nil {
		fail("pod usage", err)
		return
	}
	usage, err := parsePodUsage(data)
	if err != nil {
		fail("pod usage", err)
		return
	}
	for index := range rke2.Pods {
		pod := &rke2.Pods[index]
		if found, ok := usage[pod.Namespace+"/"+pod.Name]; ok {
			pod.HasUsage, pod.CPUMillis, pod.MemoryBytes = true, found.cpuMillis, found.memoryBytes
		}
	}
}

// parseCPUMillis reads a Kubernetes CPU quantity ("250m", "2", "0.5", "1500000n") as
// thousandths of a core.
func parseCPUMillis(quantity string) (int64, bool) {
	quantity = strings.TrimSpace(quantity)
	if quantity == "" {
		return 0, false
	}
	scale := map[string]float64{"n": 1e-6, "u": 1e-3, "m": 1}
	unit := quantity[len(quantity)-1:]
	factor, number := 1000.0, quantity
	if s, ok := scale[unit]; ok {
		factor, number = s, quantity[:len(quantity)-1]
	}
	value, err := strconv.ParseFloat(number, 64)
	if err != nil || value < 0 {
		return 0, false
	}
	return int64(value*factor + 0.5), true
}

// parseBytes reads a Kubernetes memory quantity ("256Mi", "1Gi", "128M", "1048576").
func parseBytes(quantity string) (int64, bool) {
	quantity = strings.TrimSpace(quantity)
	if quantity == "" {
		return 0, false
	}
	units := []struct {
		suffix string
		factor float64
	}{
		{"Ki", 1 << 10}, {"Mi", 1 << 20}, {"Gi", 1 << 30}, {"Ti", 1 << 40},
		{"k", 1e3}, {"K", 1e3}, {"M", 1e6}, {"G", 1e9}, {"T", 1e12},
	}
	number, factor := quantity, 1.0
	for _, unit := range units {
		if strings.HasSuffix(quantity, unit.suffix) {
			number, factor = strings.TrimSuffix(quantity, unit.suffix), unit.factor
			break
		}
	}
	value, err := strconv.ParseFloat(number, 64)
	if err != nil || value < 0 {
		return 0, false
	}
	return int64(value*factor + 0.5), true
}

// parseNodeUsage reads the metrics API's node list (`kubectl get --raw
// /apis/metrics.k8s.io/v1beta1/nodes`), keyed by node name.
func parseNodeUsage(data []byte) (map[string]podUsage, error) {
	var list struct {
		Items []struct {
			Metadata objectMeta `json:"metadata"`
			Usage    struct {
				CPU    string `json:"cpu"`
				Memory string `json:"memory"`
			} `json:"usage"`
		} `json:"items"`
	}
	if err := json.Unmarshal(data, &list); err != nil {
		return nil, err
	}
	usage := make(map[string]podUsage, len(list.Items))
	for _, item := range list.Items {
		cpu, _ := parseCPUMillis(item.Usage.CPU)
		memory, _ := parseBytes(item.Usage.Memory)
		usage[item.Metadata.Name] = podUsage{cpuMillis: cpu, memoryBytes: memory}
	}
	return usage, nil
}

// collectNodeUsage merges each machine's current CPU and memory use, as Kubernetes
// already measures it, into the nodes. Without a metrics server the nodes simply carry
// no usage, and the failure is reported.
func (i *Inspector) collectNodeUsage(ctx context.Context, rke2 *RKE2, fail func(string, error)) {
	data, err := i.kubectlRaw(ctx, "/apis/metrics.k8s.io/v1beta1/nodes")
	if err != nil {
		fail("node usage", err)
		return
	}
	usage, err := parseNodeUsage(data)
	if err != nil {
		fail("node usage", err)
		return
	}
	for index := range rke2.Nodes {
		node := &rke2.Nodes[index]
		if found, ok := usage[node.Name]; ok {
			node.HasUsage, node.UsageCPUMillis, node.UsageMemoryBytes = true, found.cpuMillis, found.memoryBytes
		}
	}
}
