package inspect

import "testing"

func TestQuantitiesAreReadTheWayKubernetesWritesThem(t *testing.T) {
	for quantity, want := range map[string]int64{"250m": 250, "2": 2000, "0.5": 500, "1500000n": 2, "3000u": 3, "1500m": 1500} {
		if got, ok := parseCPUMillis(quantity); !ok || got != want {
			t.Errorf("cpu %q = %d (%v), want %d", quantity, got, ok, want)
		}
	}
	for quantity, want := range map[string]int64{"256Mi": 256 << 20, "1Gi": 1 << 30, "128M": 128e6, "1048576": 1 << 20, "64Ki": 64 << 10} {
		if got, ok := parseBytes(quantity); !ok || got != want {
			t.Errorf("memory %q = %d (%v), want %d", quantity, got, ok, want)
		}
	}
	for _, bad := range []string{"", "lots", "-1m", "12Qi"} {
		if _, ok := parseCPUMillis(bad); ok {
			t.Errorf("cpu %q should not parse", bad)
		}
		if _, ok := parseBytes(bad); ok {
			t.Errorf("memory %q should not parse", bad)
		}
	}
}

func TestAPodWithAnyUnlimitedContainerHasNoKnownCeiling(t *testing.T) {
	pods, err := parsePods([]byte(`{"items":[
	 {"metadata":{"name":"both","namespace":"a"},"spec":{"containers":[
	   {"resources":{"limits":{"cpu":"500m","memory":"256Mi"}}},{"resources":{"limits":{"cpu":"250m","memory":"128Mi"}}}]},"status":{}},
	 {"metadata":{"name":"one-bare","namespace":"a"},"spec":{"containers":[
	   {"resources":{"limits":{"cpu":"500m","memory":"256Mi"}}},{"resources":{}}]},"status":{}}]}`))
	if err != nil {
		t.Fatal(err)
	}
	if pods[0].CPURequestMillis != 0 {
		t.Fatalf("no requests were set: %+v", pods[0])
	}
	if pods[0].CPULimitMillis != 750 || pods[0].MemoryLimitBytes != 384<<20 {
		t.Fatalf("limits should add up across containers: %+v", pods[0])
	}
	if pods[1].CPULimitMillis != 0 || pods[1].MemoryLimitBytes != 0 {
		t.Fatalf("an unlimited container means the ceiling is unknown, not the sum of the others: %+v", pods[1])
	}
}

func TestUsageAndAutoscalersAreReadFromTheirApis(t *testing.T) {
	usage, err := parsePodUsage([]byte(`{"items":[{"metadata":{"name":"web-1","namespace":"app"},
	 "containers":[{"usage":{"cpu":"3000000n","memory":"100Mi"}},{"usage":{"cpu":"2m","memory":"28Mi"}}]}]}`))
	if err != nil {
		t.Fatal(err)
	}
	if got := usage["app/web-1"]; got.cpuMillis != 5 || got.memoryBytes != 128<<20 {
		t.Fatalf("containers should be summed: %+v", got)
	}
	scalers, err := parseAutoscalers([]byte(`{"items":[{"metadata":{"name":"web","namespace":"app"},
	 "spec":{"minReplicas":2,"maxReplicas":6,"scaleTargetRef":{"kind":"Deployment","name":"web"}},"status":{"currentReplicas":3}}]}`))
	if err != nil || len(scalers) != 1 || scalers[0].Max != 6 || scalers[0].Min != 2 || scalers[0].Current != 3 || scalers[0].TargetName != "web" {
		t.Fatalf("%+v %v", scalers, err)
	}
}

func TestRequestsAreTotalledLikeLimits(t *testing.T) {
	pods, err := parsePods([]byte(`{"items":[{"metadata":{"name":"a","namespace":"x"},"spec":{"containers":[
	 {"resources":{"requests":{"cpu":"100m","memory":"64Mi"},"limits":{"cpu":"500m"}}},
	 {"resources":{"requests":{"cpu":"50m","memory":"32Mi"}}}]},"status":{}}]}`))
	if err != nil {
		t.Fatal(err)
	}
	if pods[0].CPURequestMillis != 150 || pods[0].MemoryRequestBytes != 96<<20 {
		t.Fatalf("requests should add up: %+v", pods[0])
	}
	if pods[0].CPULimitMillis != 0 {
		t.Fatalf("one container has no CPU limit, so the pod has none: %+v", pods[0])
	}
}

func TestAPodIsCountedForTheLongestMatchingWorkloadOnly(t *testing.T) {
	workloads := []Workload{{Namespace: "m", Name: "octo"}, {Namespace: "m", Name: "octo-shim"}, {Namespace: "x", Name: "octo"}}
	pods := []Pod{
		{Namespace: "m", Name: "octo-aaa", Phase: "Running", HasUsage: true, MemoryBytes: 100, CPUMillis: 10},
		{Namespace: "m", Name: "octo-bbb", Phase: "Running", HasUsage: true, MemoryBytes: 300, CPUMillis: 30},
		{Namespace: "m", Name: "octo-shim-ccc", Phase: "Running", HasUsage: true, MemoryBytes: 50, CPUMillis: 5},
		{Namespace: "m", Name: "octo-ddd", Phase: "Pending", HasUsage: true, MemoryBytes: 9999},
		{Namespace: "m", Name: "octo-eee", Phase: "Running"}, // no metrics yet
		{Namespace: "m", Name: "stranger-1", Phase: "Running", HasUsage: true, MemoryBytes: 1},
	}
	got := aggregateUsage(workloads, pods)
	if len(got) != 2 {
		t.Fatalf("%+v", got)
	}
	octo, shim := got[0], got[1]
	if octo.Name != "octo" || octo.Copies != 2 || octo.MemoryPeak != 300 || octo.MemoryAvg != 200 || octo.CPUPeak != 30 || octo.CPUAvg != 20 {
		t.Fatalf("octo: %+v", octo)
	}
	if shim.Name != "octo-shim" || shim.Copies != 1 || shim.MemoryPeak != 50 {
		t.Fatalf("the shim's pod must not be counted for octo: %+v", shim)
	}
}

func TestStorageClassesSayWhetherVolumesCanGrow(t *testing.T) {
	classes, err := parseStorageClasses([]byte(`{"items":[
	 {"metadata":{"name":"longhorn"},"allowVolumeExpansion":true},
	 {"metadata":{"name":"local-path"}}]}`))
	if err != nil || !classes["longhorn"] || classes["local-path"] {
		t.Fatalf("%v %v", classes, err)
	}
}
