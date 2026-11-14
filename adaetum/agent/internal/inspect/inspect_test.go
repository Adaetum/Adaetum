package inspect

import (
	"context"
	"errors"
	"reflect"
	"strings"
	"testing"
	"time"
)

const nodesJSON = `{"items":[
 {"metadata":{"name":"cp-1"},"status":{"conditions":[{"type":"Ready","status":"True"}]}},
 {"metadata":{"name":"cp-2"},"status":{"conditions":[{"type":"Ready","status":"False"}]}}]}`

func fakeInspector(run Runner) *Inspector {
	return &Inspector{
		Run: run,
		ReadFile: func(path string) ([]byte, error) {
			switch path {
			case "/proc/uptime":
				return []byte("3600.50 7000.00\n"), nil
			case "/proc/loadavg":
				return []byte("0.50 0.25 0.10 1/200 1234\n"), nil
			case "/proc/meminfo":
				return []byte(meminfoSample), nil
			}
			return []byte("ID=\"rocky\"\nVERSION_ID=\"9.4\"\nPRETTY_NAME=\"Rocky Linux 9.4 (Blue Onyx)\"\n"), nil
		},
		Hostname: func() (string, error) { return "node-a", nil },
		Arch:     "arm64",
		Now:      func() time.Time { return time.Unix(0, 0) },
	}
}

func TestCollectActiveServer(t *testing.T) {
	run := func(_ context.Context, name string, args ...string) (string, error) {
		switch {
		case name == "du":
			return "32000\t" + args[1] + "\n", nil
		case name == "uname" && args[0] == "-m":
			return "x86_64\n", nil
		case name == "uname":
			return "5.14.0\n", nil
		case name == "nproc":
			return "4\n", nil
		case name == "df":
			return dfSample, nil
		case name == "systemctl" && args[0] == "--failed":
			return "● broken.service loaded failed failed Broken thing\n", nil
		case name == "systemctl" && args[1] == "rke2-server":
			return "active\n", nil
		case name == "systemctl":
			return "inactive\n", errors.New("exit status 3")
		case name == rke2Binaries[0]:
			return "rke2 version v1.30.4+rke2r1 (abc)\ngo version go1.22\n", nil
		case name == rke2Kubectl && args[3] == "pods":
			return podsJSON, nil
		case name == rke2Kubectl && args[3] == "events":
			return eventsJSON, nil
		case name == rke2Kubectl && args[3] == "ingresses":
			return ingressesJSON, nil
		case name == rke2Kubectl && args[3] == "certificates.cert-manager.io":
			return certificatesJSON, nil
		case name == rke2Kubectl && args[3] == "persistentvolumeclaims":
			return pvcsJSON, nil
		case name == rke2Kubectl && args[3] == "volumes.longhorn.io":
			return longhornJSON, nil
		case name == rke2Crictl && args[2] == "images":
			return crictlImagesJSON, nil
		case name == rke2Crictl && args[2] == "ps":
			return crictlContainersJSON, nil
		case name == rke2Kubectl && args[3] == "--raw":
			return `{"node":{"fs":{"capacityBytes":10000},"runtime":{"imageFs":{"capacityBytes":10000,"usedBytes":2500}}}}`, nil
		case name == rke2Kubectl && args[3] == "nodes.longhorn.io":
			return longhornNodesJSON, nil
		case name == rke2Kubectl && args[3] == "settings.longhorn.io":
			return `{"value":"200"}`, nil
		case name == rke2Kubectl && args[3] == "applications.argoproj.io":
			return argoJSON, nil
		case name == rke2Kubectl && args[3] == "nodes":
			return nodesJSON, nil
		default:
			return workloadsJSON, nil
		}
	}

	report := fakeInspector(run).Collect(context.Background())

	if report.Host.OSID != "rocky" || report.Host.Hostname != "node-a" || report.Host.Kernel != "5.14.0" {
		t.Fatalf("unexpected host: %+v", report.Host)
	}
	if report.RKE2.Services["rke2-server"] != "active" || report.RKE2.Services["rke2-agent"] != "inactive" {
		t.Fatalf("unexpected services: %+v", report.RKE2.Services)
	}
	if report.RKE2.Version != "v1.30.4+rke2r1" {
		t.Fatalf("unexpected version %q", report.RKE2.Version)
	}
	if len(report.RKE2.Nodes) != 2 || !report.RKE2.Nodes[0].Ready || report.RKE2.Nodes[1].Ready {
		t.Fatalf("unexpected nodes: %+v", report.RKE2.Nodes)
	}
	if len(report.RKE2.Workloads) != 3 || len(report.RKE2.Pods) != 2 {
		t.Fatalf("unexpected workloads/pods: %+v", report.RKE2)
	}
	if m := report.Metrics; m == nil || m.CPUCount != 4 || m.Load1 != 0.5 || m.UptimeSeconds != 3600.5 || len(m.Disks) != 2 {
		t.Fatalf("unexpected metrics: %+v", report.Metrics)
	}
	if got := report.Host.FailedUnits; len(got) != 1 || got[0] != "broken.service" {
		t.Fatalf("unexpected failed units: %v", got)
	}
	if len(report.RKE2.Events) != 2 || len(report.RKE2.Ingresses) != 1 || len(report.RKE2.Certificates) != 2 || len(report.RKE2.Volumes) != 2 {
		t.Fatalf("unexpected cluster views: %+v", report.RKE2)
	}
	if capacity := report.RKE2.StorageCapacity; capacity == nil || len(capacity.Disks) != 2 || capacity.OverProvisionPercent != 200 {
		t.Fatalf("unexpected storage capacity: %+v", report.RKE2.StorageCapacity)
	}
	if images := report.Host.ContainerImages; images == nil || images.Total != 4 || images.Unused != 1 || images.UnusedBytes != 2000 || images.ContentBytes != 32000 {
		t.Fatalf("unexpected container images: %+v", report.Host.ContainerImages)
	}
	if disks := report.RKE2.StorageCapacity.Disks; disks[0].ImageBytes != 2500 || disks[1].ImageBytes != 0 {
		t.Fatalf("image usage belongs only to the disk on the image filesystem: %+v", disks)
	}
	if used := report.RKE2.Volumes[0].UsedBytes; used == nil || *used != 4294967296 {
		t.Fatalf("expected measured usage on the Longhorn volume: %+v", report.RKE2.Volumes[0])
	}
	if len(report.RKE2.ArgoApps) != 3 {
		t.Fatalf("unexpected argo apps: %+v", report.RKE2.ArgoApps)
	}
	if len(report.Errors) != 0 {
		t.Fatalf("unexpected errors: %v", report.Errors)
	}
}

const eventsJSON = `{"items":[
 {"metadata":{"namespace":"apps","creationTimestamp":"2026-01-01T00:00:00Z"},
  "involvedObject":{"kind":"Pod","name":"old"},"reason":"BackOff","message":"older","count":3,
  "lastTimestamp":"2026-01-02T00:00:00Z"},
 {"metadata":{"namespace":"apps","creationTimestamp":"2026-01-01T00:00:00Z"},
  "involvedObject":{"kind":"Pod","name":"new"},"reason":"FailedMount","message":"newer",
  "eventTime":"2026-01-03T00:00:00Z","series":{"count":7,"lastObservedTime":"2026-01-04T00:00:00Z"}}]}`

const ingressesJSON = `{"items":[
 {"metadata":{"name":"web","namespace":"apps"},
  "spec":{"ingressClassName":"nginx","rules":[{"host":"a.example.test"},{"host":"b.example.test"},{}],"tls":[{}]}}]}`

const certificatesJSON = `{"items":[
 {"metadata":{"name":"late","namespace":"apps"},"spec":{"dnsNames":["late.example.test"]},
  "status":{"notAfter":"2027-01-01T00:00:00Z","conditions":[{"type":"Ready","status":"True"}]}},
 {"metadata":{"name":"pending","namespace":"apps"},"spec":{},
  "status":{"conditions":[{"type":"Ready","status":"False","message":"waiting for issuer"}]}}]}`

const pvcsJSON = `{"items":[
 {"metadata":{"name":"data","namespace":"apps"},"spec":{"storageClassName":"longhorn","volumeName":"pvc-1"},
  "status":{"phase":"Bound","capacity":{"storage":"10Gi"}}},
 {"metadata":{"name":"scratch","namespace":"apps"},"spec":{"volumeName":"pvc-2"},"status":{"phase":"Pending"}}]}`

const longhornJSON = `{"items":[{"metadata":{"name":"pvc-1"},"spec":{"numberOfReplicas":2},"status":{"robustness":"degraded","actualSize":4294967296}}]}`

const longhornNodesJSON = `{"items":[{"metadata":{"name":"cp-1"},
 "spec":{"allowScheduling":true,"disks":{
  "d1":{"path":"/var/lib/longhorn/","allowScheduling":true,"storageReserved":1000},
  "d2":{"path":"/data","allowScheduling":false,"storageReserved":0}}},
 "status":{"diskStatus":{
  "d1":{"storageMaximum":10000,"storageAvailable":4000,"storageScheduled":6000,"conditions":[{"type":"Schedulable","status":"True"}]},
  "d2":{"storageMaximum":500,"storageAvailable":500,"storageScheduled":0,"conditions":[{"type":"Schedulable","status":"True"}]}}}}]}`

func TestParseEventsSortsNewestFirstAndHandlesBothShapes(t *testing.T) {
	events, err := parseEvents([]byte(eventsJSON))
	if err != nil {
		t.Fatal(err)
	}
	if events[0].Object != "Pod/new" || events[0].Count != 7 || events[0].LastSeen != "2026-01-04T00:00:00Z" {
		t.Fatalf("series event should win and sort first: %+v", events[0])
	}
	if events[1].Count != 3 || events[1].LastSeen != "2026-01-02T00:00:00Z" {
		t.Fatalf("unexpected legacy event: %+v", events[1])
	}
}

func TestParseEventsCapsLengthAndMessage(t *testing.T) {
	items := make([]string, 0, maxEvents+5)
	for index := 0; index < maxEvents+5; index++ {
		items = append(items, `{"metadata":{"namespace":"a"},"involvedObject":{"kind":"Pod","name":"p"},"message":"`+strings.Repeat("x", 400)+`","lastTimestamp":"2026-01-01T00:00:00Z"}`)
	}
	events, err := parseEvents([]byte(`{"items":[` + strings.Join(items, ",") + `]}`))
	if err != nil {
		t.Fatal(err)
	}
	if len(events) != maxEvents || len([]rune(events[0].Message)) != maxEventMessage+1 {
		t.Fatalf("got %d events, message length %d", len(events), len([]rune(events[0].Message)))
	}
}

func TestVolumesJoinLonghornHealthAndUsageByBackingVolume(t *testing.T) {
	volumes, backing, err := parseVolumes([]byte(pvcsJSON))
	if err != nil {
		t.Fatal(err)
	}
	measured, err := parseLonghornVolumes([]byte(longhornJSON))
	if err != nil {
		t.Fatal(err)
	}
	if backing[0] != "pvc-1" || measured[backing[0]].robustness != "degraded" || measured[backing[0]].actualBytes != 4294967296 || measured[backing[0]].replicas != 2 {
		t.Fatalf("unexpected join: %v %v", backing, measured)
	}
	if volumes[0].StorageClass != "longhorn" || volumes[0].Capacity != "10Gi" || volumes[0].CapacityBytes != 10<<30 {
		t.Fatalf("unexpected volume: %+v", volumes[0])
	}
	if _, found := measured[backing[1]]; found || volumes[1].StorageClass != "" || volumes[1].UsedBytes != nil {
		t.Fatalf("a claim without a Longhorn volume must stay unmeasured: %+v", volumes[1])
	}
}

func TestParseLonghornDisks(t *testing.T) {
	disks, err := parseLonghornDisks([]byte(longhornNodesJSON))
	if err != nil {
		t.Fatal(err)
	}
	if len(disks) != 2 {
		t.Fatalf("got %+v", disks)
	}
	main := disks[0]
	if main.Node != "cp-1" || main.Path != "/var/lib/longhorn/" || main.MaxBytes != 10000 || main.ReservedBytes != 1000 ||
		main.ScheduledBytes != 6000 || main.AvailableBytes != 4000 || !main.Schedulable {
		t.Fatalf("unexpected disk: %+v", main)
	}
	if disks[1].Schedulable {
		t.Fatalf("a disk with scheduling disabled must not be schedulable: %+v", disks[1])
	}
}

func TestParseSettingPercent(t *testing.T) {
	if got, err := parseSettingPercent([]byte(`{"value":"200"}`)); err != nil || got != 200 {
		t.Fatalf("got %d, %v", got, err)
	}
	if _, err := parseSettingPercent([]byte(`{"value":"lots"}`)); err == nil {
		t.Fatal("expected an error for a non-numeric setting")
	}
}

func TestParseQuantity(t *testing.T) {
	cases := map[string]struct {
		bytes uint64
		ok    bool
	}{
		"10Gi":  {10 << 30, true},
		"500M":  {500_000_000, true},
		"1.5Gi": {3 << 29, true},
		"2048":  {2048, true},
		"100m":  {0, false}, // a milli-unit is not a byte size
		"lots":  {0, false},
		"":      {0, false},
	}
	for in, want := range cases {
		got, ok := parseQuantity(in)
		if got != want.bytes || ok != want.ok {
			t.Fatalf("parseQuantity(%q) = %d, %v; want %d, %v", in, got, ok, want.bytes, want.ok)
		}
	}
}

func TestParseIngressesSkipsRulesWithoutHost(t *testing.T) {
	ingresses, err := parseIngresses([]byte(ingressesJSON))
	if err != nil {
		t.Fatal(err)
	}
	if got := ingresses[0]; got.Class != "nginx" || !got.TLS || len(got.Hosts) != 2 {
		t.Fatalf("unexpected ingress: %+v", got)
	}
}

func TestParseCertificatesPutsUnissuedFirst(t *testing.T) {
	certificates, err := parseCertificates([]byte(certificatesJSON))
	if err != nil {
		t.Fatal(err)
	}
	if certificates[0].Name != "pending" || certificates[0].Ready || certificates[0].Message != "waiting for issuer" {
		t.Fatalf("unissued certificate should sort first with its reason: %+v", certificates[0])
	}
	if certificates[1].NotAfter == "" || !certificates[1].Ready {
		t.Fatalf("unexpected certificate: %+v", certificates[1])
	}
}

func TestParseNodesReportsRolesPressureAndCordon(t *testing.T) {
	nodes, err := parseNodes([]byte(`{"items":[{
	  "metadata":{"name":"cp-1","labels":{"node-role.kubernetes.io/control-plane":"true","node-role.kubernetes.io/etcd":"true","kubernetes.io/os":"linux"}},
	  "spec":{"unschedulable":true},
	  "status":{"nodeInfo":{"kubeletVersion":"v1.35.6"},
	   "addresses":[{"type":"Hostname","address":"cp-1"},{"type":"InternalIP","address":"10.0.0.5"}],
	   "conditions":[{"type":"Ready","status":"True"},{"type":"DiskPressure","status":"True"},{"type":"MemoryPressure","status":"False"}]}}]}`))
	if err != nil {
		t.Fatal(err)
	}
	node := nodes[0]
	if strings.Join(node.Roles, ",") != "control-plane,etcd" || node.InternalIP != "10.0.0.5" || !node.Cordoned ||
		node.Version != "v1.35.6" || len(node.Pressure) != 1 || node.Pressure[0] != "DiskPressure" {
		t.Fatalf("unexpected node: %+v", node)
	}
}

func TestParseFailedUnitsHandlesBulletsAndEmptyOutput(t *testing.T) {
	if got := parseFailedUnits("● a.service loaded failed failed A\nb.service loaded failed failed B\n"); len(got) != 2 || got[0] != "a.service" {
		t.Fatalf("got %v", got)
	}
	if got := parseFailedUnits(""); len(got) != 0 {
		t.Fatalf("got %v", got)
	}
}

const argoJSON = `{"items":[
 {"metadata":{"name":"cert-manager","namespace":"argocd"},"spec":{"project":"platform"},
  "status":{"sync":{"status":"Synced","revision":"0123456789abcdef0123456789abcdef01234567"},"health":{"status":"Healthy"},
   "operationState":{"phase":"Succeeded","message":"successfully synced"}}},
 {"metadata":{"name":"broken","namespace":"argocd"},"spec":{"project":"apps"},
  "status":{"sync":{"status":"Unknown"},"health":{"status":"Unknown"},
   "conditions":[{"type":"ComparisonError","message":"manifest generation failed"}]}},
 {"metadata":{"name":"stuck","namespace":"argocd"},"spec":{"project":"apps"},
  "status":{"sync":{"status":"OutOfSync","revision":"abc"},"health":{"status":"Degraded"},
   "operationState":{"phase":"Failed","message":"one or more objects failed to apply"}}}]}`

func TestParseArgoApps(t *testing.T) {
	apps, err := parseArgoApps([]byte(argoJSON))
	if err != nil {
		t.Fatal(err)
	}
	if healthy := apps[0]; healthy.Revision != "0123456" || healthy.Message != "" || healthy.Operation != "Succeeded" {
		t.Fatalf("a healthy app should have a short revision and no message: %+v", healthy)
	}
	if apps[1].Message != "manifest generation failed" {
		t.Fatalf("a condition should become the message: %+v", apps[1])
	}
	if stuck := apps[2]; stuck.Message != "one or more objects failed to apply" || stuck.Revision != "abc" {
		t.Fatalf("a failed operation should explain itself: %+v", stuck)
	}
}

func TestShortRevisionOnlyShortensGitSHAs(t *testing.T) {
	cases := map[string]string{
		"0123456789abcdef0123456789abcdef01234567": "0123456",
		"2026.5.1":    "2026.5.1",
		"v1.2.3-beta": "v1.2.3-beta",
		"":            "",
	}
	for in, want := range cases {
		if got := shortRevision(in); got != want {
			t.Fatalf("shortRevision(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestParseArgoAppsDefaultsMissingStatusToUnknown(t *testing.T) {
	apps, err := parseArgoApps([]byte(`{"items":[{"metadata":{"name":"new"},"status":{}}]}`))
	if err != nil {
		t.Fatal(err)
	}
	if apps[0].Sync != "Unknown" || apps[0].Health != "Unknown" {
		t.Fatalf("got %+v", apps[0])
	}
}

const meminfoSample = `MemTotal:       16000000 kB
MemFree:          500000 kB
MemAvailable:    8000000 kB
SwapTotal:       2000000 kB
SwapFree:        1500000 kB
`

const dfSample = `Filesystem     1-blocks        Used   Available Capacity Mounted on
/dev/sda2    100000000000 40000000000 60000000000      40% /
/dev/sdb1      5000000000   100000000  4900000000       2% /mnt/my data
`

func TestParseMeminfoConvertsKilobytes(t *testing.T) {
	values := parseMeminfo(meminfoSample)
	if values["MemTotal"] != 16000000*1024 || values["SwapFree"] != 1500000*1024 {
		t.Fatalf("unexpected values: %+v", values)
	}
}

func TestParseDFKeepsMountsWithSpaces(t *testing.T) {
	disks := parseDF(dfSample)
	if len(disks) != 2 || disks[1].Mount != "/mnt/my data" || disks[0].Used != 40000000000 {
		t.Fatalf("unexpected disks: %+v", disks)
	}
}

func TestParseLoadavgRejectsShortInput(t *testing.T) {
	if _, err := parseLoadavg("0.1"); err == nil {
		t.Fatal("expected an error")
	}
}

const workloadsJSON = `{"items":[
 {"kind":"Deployment","metadata":{"name":"web","namespace":"apps"},"spec":{"replicas":3},"status":{"readyReplicas":2}},
 {"kind":"StatefulSet","metadata":{"name":"db","namespace":"apps"},"spec":{},"status":{"readyReplicas":1}},
 {"kind":"DaemonSet","metadata":{"name":"agent","namespace":"kube-system"},"status":{"desiredNumberScheduled":2,"numberReady":2}},
 {"kind":"Job","metadata":{"name":"ignored","namespace":"apps"}}]}`

const podsJSON = `{"items":[
 {"metadata":{"name":"web-1","namespace":"apps"},"spec":{"nodeName":"cp-1"},
  "status":{"phase":"Running","containerStatuses":[
   {"ready":true,"restartCount":0,"state":{"running":{}}},
   {"ready":false,"restartCount":4,"state":{"waiting":{"reason":"CrashLoopBackOff"}}}]}},
 {"metadata":{"name":"old","namespace":"apps"},"spec":{},"status":{"phase":"Failed","reason":"Evicted"}}]}`

func TestParseWorkloads(t *testing.T) {
	workloads, err := parseWorkloads([]byte(workloadsJSON))
	if err != nil {
		t.Fatal(err)
	}
	want := []Workload{
		{Kind: "Deployment", Namespace: "apps", Name: "web", Desired: 3, Ready: 2},
		{Kind: "StatefulSet", Namespace: "apps", Name: "db", Desired: 1, Ready: 1}, // nil replicas defaults to 1
		{Kind: "DaemonSet", Namespace: "kube-system", Name: "agent", Desired: 2, Ready: 2},
	}
	if len(workloads) != len(want) {
		t.Fatalf("got %+v", workloads)
	}
	for index := range want {
		if !reflect.DeepEqual(workloads[index], want[index]) {
			t.Fatalf("workload %d: got %+v, want %+v", index, workloads[index], want[index])
		}
	}
}

func TestParsePods(t *testing.T) {
	pods, err := parsePods([]byte(podsJSON))
	if err != nil {
		t.Fatal(err)
	}
	web := pods[0]
	if web.ReadyContainers != 1 || web.TotalContainers != 2 || web.Restarts != 4 || web.Reason != "CrashLoopBackOff" || web.Node != "cp-1" {
		t.Fatalf("unexpected pod: %+v", web)
	}
	if pods[1].Phase != "Failed" || pods[1].Reason != "Evicted" {
		t.Fatalf("unexpected pod: %+v", pods[1])
	}
}

func TestCollectWithoutRKE2RecordsNoNodes(t *testing.T) {
	run := func(_ context.Context, name string, _ ...string) (string, error) {
		if name == "systemctl" {
			return "", errors.New("unit not found")
		}
		return "", errors.New("not installed")
	}

	report := fakeInspector(run).Collect(context.Background())

	if report.RKE2.Services["rke2-server"] != "unknown" {
		t.Fatalf("expected unknown state, got %+v", report.RKE2.Services)
	}
	if report.Host.ContainerImages != nil {
		t.Fatalf("a host without RKE2 must not report container images: %+v", report.Host.ContainerImages)
	}
	if len(report.RKE2.Nodes) != 0 || report.RKE2.Version != "" {
		t.Fatalf("expected empty rke2 details: %+v", report.RKE2)
	}
}

func TestShellJoinQuotesArguments(t *testing.T) {
	got := shellJoin([]string{"echo", "it's", "a b"})
	want := `'echo' 'it'\''s' 'a b'`
	if got != want {
		t.Fatalf("got %s, want %s", got, want)
	}
}

func TestNormalizeArch(t *testing.T) {
	for in, want := range map[string]string{"x86_64\n": "amd64", "aarch64": "arm64", "riscv64": "riscv64"} {
		if got := normalizeArch(in); got != want {
			t.Fatalf("normalizeArch(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestRemoteInspectorRejectsOptionLikeTarget(t *testing.T) {
	if _, err := NewRemoteInspector("-oProxyCommand=x"); err == nil {
		t.Fatal("expected an error for a target that looks like an ssh option")
	}
}

func TestSameFilesystem(t *testing.T) {
	cases := []struct {
		a, b uint64
		want bool
	}{
		{253157765120, 253157765120, true},
		{253157765120, 253157700000, true}, // same device read at different moments
		{253157765120, 500000000000, false},
		{0, 100, false}, // unknown never matches
		{100, 0, false},
	}
	for _, c := range cases {
		if got := sameFilesystem(c.a, c.b); got != c.want {
			t.Fatalf("sameFilesystem(%d, %d) = %v, want %v", c.a, c.b, got, c.want)
		}
	}
}

func TestAttachImageUsageRejectsUnsafeNodeNames(t *testing.T) {
	var asked []string
	inspector := &Inspector{Run: func(_ context.Context, name string, args ...string) (string, error) {
		asked = append(asked, strings.Join(args, " "))
		return `{}`, nil
	}}
	var failures []string
	disks := []StorageDisk{{Node: "../../secrets", MaxBytes: 10}, {Node: "Node_One", MaxBytes: 10}}

	inspector.attachImageUsage(context.Background(), disks, func(what string, err error) { failures = append(failures, err.Error()) })

	if len(asked) != 0 {
		t.Fatalf("an invalid node name must never reach the API path: %v", asked)
	}
	if len(failures) != 2 {
		t.Fatalf("each invalid name should be reported: %v", failures)
	}
}

// Four images: A is used by digest, C by image ID (size as a number), D by tag,
// and B by nothing, so only B is unused.
const crictlImagesJSON = `{"images":[
 {"id":"sha256:aaa","repoTags":[],"repoDigests":["reg/a@sha256:aaa"],"size":"1000"},
 {"id":"sha256:bbb","repoTags":["reg/b:1"],"repoDigests":["reg/b@sha256:bbb"],"size":"2000"},
 {"id":"sha256:ccc","repoTags":[],"repoDigests":[],"size":500},
 {"id":"sha256:ddd","repoTags":["reg/d:latest"],"repoDigests":[],"size":"300"}]}`

const crictlContainersJSON = `{"containers":[
 {"imageRef":"reg/a@sha256:aaa","image":{"image":"reg/a@sha256:aaa"}},
 {"imageRef":"","image":{"image":"sha256:ccc"}},
 {"imageRef":"","image":{"image":"","userSpecifiedImage":"reg/d:latest"}}]}`

func TestParseContainerImagesCountsOnlyImagesNoContainerUses(t *testing.T) {
	images, err := parseContainerImages([]byte(crictlImagesJSON), []byte(crictlContainersJSON))
	if err != nil {
		t.Fatal(err)
	}
	if images.Total != 4 || images.Unused != 1 || images.TotalBytes != 3800 || images.UnusedBytes != 2000 {
		t.Fatalf("unexpected summary: %+v", images)
	}
}

func TestParseContainerImagesRejectsUnreadableSizes(t *testing.T) {
	_, err := parseContainerImages([]byte(`{"images":[{"id":"x","size":"lots"}]}`), []byte(`{"containers":[]}`))
	if err == nil {
		t.Fatal("expected an error for a size that is not a byte count")
	}
}

func TestContainerdContentBytesIsCachedForTheTTL(t *testing.T) {
	calls := 0
	clock := time.Unix(1000, 0)
	inspector := &Inspector{
		Now: func() time.Time { return clock },
		Run: func(_ context.Context, name string, args ...string) (string, error) {
			calls++
			return "777\t" + args[1] + "\n", nil
		},
	}

	for attempt := 0; attempt < 3; attempt++ {
		got, err := inspector.containerdContentBytes(context.Background())
		if err != nil || got != 777 {
			t.Fatalf("got %d, %v", got, err)
		}
	}
	if calls != 1 {
		t.Fatalf("repeated reports inside the TTL must reuse the measurement, ran du %d times", calls)
	}

	clock = clock.Add(measurementTTL + time.Second)
	if _, err := inspector.containerdContentBytes(context.Background()); err != nil || calls != 2 {
		t.Fatalf("an expired measurement must be redone: calls=%d err=%v", calls, err)
	}
}

func TestContainerdContentBytesDoesNotCacheFailures(t *testing.T) {
	calls := 0
	inspector := &Inspector{
		Now: time.Now,
		Run: func(context.Context, string, ...string) (string, error) {
			calls++
			return "", errors.New("permission denied")
		},
	}
	for attempt := 0; attempt < 2; attempt++ {
		if _, err := inspector.containerdContentBytes(context.Background()); err == nil {
			t.Fatal("expected an error")
		}
	}
	if calls != 2 {
		t.Fatalf("a failed measurement must be retried, not cached: du ran %d times", calls)
	}
}

func TestDirectoryBytesRefusesUnlistedPathsAndGarbage(t *testing.T) {
	ran := false
	inspector := &Inspector{Run: func(context.Context, string, ...string) (string, error) {
		ran = true
		return "not-a-number\t/x\n", nil
	}}

	if _, err := inspector.directoryBytes(context.Background(), "/etc"); err == nil || ran {
		t.Fatalf("an unlisted path must be refused before anything runs (ran=%v, err=%v)", ran, err)
	}
	if _, err := inspector.directoryBytes(context.Background(), containerdContentDir); err == nil {
		t.Fatal("unparseable du output must be an error, not zero")
	}
}

// What Kubernetes already says about a machine is used as it is, never measured again.
func TestParseNodesCarriesWhatKubernetesAlreadyKnows(t *testing.T) {
	nodes, err := parseNodes([]byte(`{"items":[{
	  "metadata":{"name":"w-1"},
	  "status":{"allocatable":{"cpu":"3920m","memory":"15Gi"},
	   "nodeInfo":{"kubeletVersion":"v1.35.6","osImage":"Rocky Linux 10.2","kernelVersion":"6.12.0","containerRuntimeVersion":"containerd://2.0"},
	   "conditions":[{"type":"Ready","status":"True","lastTransitionTime":"2026-11-10T01:02:03Z"}]}}]}`))
	if err != nil {
		t.Fatal(err)
	}
	node := nodes[0]
	if node.AllocatableCPUMillis != 3920 || node.AllocatableMemoryBytes != 15<<30 || node.OSImage != "Rocky Linux 10.2" ||
		node.Kernel != "6.12.0" || node.ContainerRuntime != "containerd://2.0" || node.ReadySince != "2026-11-10T01:02:03Z" {
		t.Fatalf("unexpected node: %+v", node)
	}
}

func TestParseNodeUsageIsKeyedByNodeName(t *testing.T) {
	usage, err := parseNodeUsage([]byte(`{"items":[{"metadata":{"name":"w-1"},"usage":{"cpu":"250m","memory":"2Gi"}}]}`))
	if err != nil {
		t.Fatal(err)
	}
	if got := usage["w-1"]; got.cpuMillis != 250 || got.memoryBytes != 2<<30 {
		t.Fatalf("unexpected usage: %+v", got)
	}
}

func TestOnlyReadOnlyProgramsAreAllowedForInspection(t *testing.T) {
	for _, name := range []string{"uname", "systemctl", "df", "du", "nproc", "timedatectl"} {
		if !allowedCommands[name] {
			t.Errorf("%s should be allowed", name)
		}
	}
	for _, name := range []string{"sh", "bash", "rm", "curl", "chronyc", "tailscale"} {
		if allowedCommands[name] {
			t.Errorf("%s must not be allowed: inspection is read-only and fixed", name)
		}
	}
}

func TestWorkloadsCarryOnlyKnownAcceptedShortfalls(t *testing.T) {
	data := `{"items":[{"kind":"Deployment","metadata":{"name":"a","namespace":"x","annotations":{"adaetum.io/accepted":" memory-limit, typo ,cpu-request,memory-limit"}},"spec":{},"status":{}},
	{"kind":"Deployment","metadata":{"name":"b","namespace":"x"},"spec":{},"status":{}}]}`
	workloads, err := parseWorkloads([]byte(data))
	if err != nil {
		t.Fatal(err)
	}
	if got := strings.Join(workloads[0].Accepted, ","); got != "memory-limit,cpu-request" {
		t.Fatalf("known words only, once each, in order: %q", got)
	}
	if workloads[1].Accepted != nil {
		t.Fatalf("no annotation accepts nothing: %v", workloads[1].Accepted)
	}
}

func TestThirdPartyWorkloadsAreAcceptedAndOursAreNot(t *testing.T) {
	data := `{"items":[
	{"kind":"DaemonSet","metadata":{"name":"longhorn-manager","namespace":"longhorn-system"},"spec":{},"status":{}},
	{"kind":"Deployment","metadata":{"name":"rancher","namespace":"cattle-system"},"spec":{},"status":{}},
	{"kind":"Deployment","metadata":{"name":"rke2-coredns-rke2-coredns","namespace":"kube-system"},"spec":{},"status":{}},
	{"kind":"DaemonSet","metadata":{"name":"kube-vip-ds","namespace":"kube-system"},"spec":{},"status":{}},
	{"kind":"Deployment","metadata":{"name":"grafana","namespace":"observability"},"spec":{},"status":{}}]}`
	workloads, err := parseWorkloads([]byte(data))
	if err != nil {
		t.Fatal(err)
	}
	want := []bool{true, true, true, false, false}
	for index, workload := range workloads {
		if (len(workload.Accepted) > 0) != want[index] {
			t.Errorf("%s/%s accepted=%v, want accepted=%v", workload.Namespace, workload.Name, workload.Accepted, want[index])
		}
	}
}
