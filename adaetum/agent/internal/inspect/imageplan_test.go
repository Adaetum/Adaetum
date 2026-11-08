package inspect

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"
)

// hexID builds a valid image ID from a single character, so fixtures stay
// readable while still matching the real ID shape.
func hexID(char string) string { return "sha256:" + strings.Repeat(char, 64) }

func image(id string, size uint64, names ...string) criImage {
	img := criImage{ID: hexID(id), Size: flexUint64(size)}
	for _, name := range names {
		if strings.Contains(name, "@") {
			img.RepoDigests = append(img.RepoDigests, name)
		} else {
			img.RepoTags = append(img.RepoTags, name)
		}
	}
	return img
}

var planNow = time.Date(2026, 11, 4, 12, 0, 0, 0, time.UTC)

func plan(options PlanOptions, local, cluster []string, images ...criImage) CleanupPlan {
	return buildImagePlan(planInputs{images: images, containerRefs: local, clusterRefs: cluster, host: "node-a", now: planNow, options: options})
}

func itemIDs(p CleanupPlan) []string {
	var ids []string
	for _, item := range p.Items {
		ids = append(ids, item.ID)
	}
	return ids
}

func exclusion(t *testing.T, p CleanupPlan, id string) PlanExclusion {
	t.Helper()
	for _, excluded := range p.Excluded {
		if excluded.ID == hexID(id) {
			return excluded
		}
	}
	t.Fatalf("image %s was expected to be excluded; plan items: %v", id, itemIDs(p))
	return PlanExclusion{}
}

func TestSupersededVersionOfAnAppInUseIsPlanned(t *testing.T) {
	p := plan(PlanOptions{}, nil, []string{"docker.io/acme/web:2"},
		image("a", 100, "docker.io/acme/web:1"), // older version: removable
		image("b", 900, "docker.io/acme/web:2"), // the live version: kept
	)
	if got := itemIDs(p); len(got) != 1 || got[0] != hexID("a") {
		t.Fatalf("only the older version should be planned, got %v", got)
	}
	if p.Items[0].Category != CategorySuperseded || p.Items[0].Registry != "docker.io" {
		t.Fatalf("unexpected item: %+v", p.Items[0])
	}
	if exclusion(t, p, "b").Reason != ReasonReferenced {
		t.Fatalf("the live version must be kept as referenced")
	}
}

func TestImageUsedByAContainerHereIsNeverPlanned(t *testing.T) {
	// Even a stopped container counts: it appears in the list like any other.
	p := plan(PlanOptions{IncludeOrphans: true}, []string{hexID("a")}, []string{"docker.io/acme/web:2"},
		image("a", 100, "docker.io/acme/web:1"))
	if len(p.Items) != 0 || exclusion(t, p, "a").Reason != ReasonInUseHere {
		t.Fatalf("an image held by a container here must be kept: %+v", p)
	}
}

func TestFailoverCacheIsKept(t *testing.T) {
	// Unused on this server but a pod elsewhere runs it: the failover cache.
	p := plan(PlanOptions{IncludeOrphans: true}, nil, []string{"docker.io/acme/api:7"},
		image("a", 100, "docker.io/acme/api:7"))
	if len(p.Items) != 0 || exclusion(t, p, "a").Reason != ReasonReferenced {
		t.Fatalf("an image another server needs must be kept: %+v", p)
	}
}

func TestClusterReferencesMatchByDigestShortNameAndDefaultTag(t *testing.T) {
	digest := strings.Repeat("c", 64)
	cases := map[string]struct {
		clusterRef string
		img        criImage
	}{
		"digest only":       {"docker.io/acme/x@sha256:" + digest, image("a", 1, "docker.io/acme/x@sha256:"+digest)},
		"image id":          {hexID("a"), image("a", 1, "docker.io/acme/x:old")},
		"short docker name": {"nginx:1.25", image("a", 1, "docker.io/library/nginx:1.25")},
		"user/repo short":   {"acme/web:3", image("a", 1, "docker.io/acme/web:3")},
		"implicit latest":   {"acme/web", image("a", 1, "docker.io/acme/web:latest")},
		"registry port":     {"reg.example.com:5000/acme/web:1", image("a", 1, "reg.example.com:5000/acme/web:1")},
	}
	for name, c := range cases {
		p := plan(PlanOptions{IncludeOrphans: true}, nil, []string{c.clusterRef}, c.img)
		if len(p.Items) != 0 {
			t.Fatalf("%s: a referenced image must never be planned: %+v", name, p.Items)
		}
	}
}

func TestAnyNameOfAnImageBeingInUseKeepsIt(t *testing.T) {
	// One image with two tags; the cluster uses only one of them.
	p := plan(PlanOptions{IncludeOrphans: true}, nil, []string{"docker.io/acme/web:stable"},
		image("a", 100, "docker.io/acme/web:1.0", "docker.io/acme/web:stable"))
	if len(p.Items) != 0 {
		t.Fatalf("an image with any in-use name must be kept: %+v", p.Items)
	}
}

func TestSystemImagesAreAlwaysKept(t *testing.T) {
	p := plan(PlanOptions{IncludeOrphans: true}, nil, []string{"docker.io/acme/web:2"},
		image("a", 100, "docker.io/rancher/hardened-etcd:v3.5.0-old"),
		image("b", 100, "docker.io/rancher/mirrored-pause:3.6"),
		image("c", 100, "registry.k8s.io/pause:3.9"),
	)
	if len(p.Items) != 0 {
		t.Fatalf("system images must never be planned: %+v", p.Items)
	}
	for _, id := range []string{"a", "b", "c"} {
		if exclusion(t, p, id).Reason != ReasonSystem {
			t.Fatalf("image %s should be a system image", id)
		}
	}
}

func TestRegistriesOffTheAllowedListAreKept(t *testing.T) {
	cluster := []string{"ghcr.io/org/tool:2", "registry.armadillo.ts.net/team/app:2", "docker.io/acme/web:2"}
	p := plan(PlanOptions{}, nil, cluster,
		image("a", 100, "ghcr.io/org/tool:1"),
		image("b", 100, "registry.armadillo.ts.net/team/app:1"),
		image("c", 100, "docker.io/acme/web:1"),
	)
	if got := itemIDs(p); len(got) != 1 || got[0] != hexID("c") {
		t.Fatalf("only the image from an allowed registry may be planned, got %v", got)
	}
	if reason := exclusion(t, p, "a").Reason; !strings.Contains(reason, "ghcr.io") || !strings.Contains(reason, "not on the allowed list") {
		t.Fatalf("the reason should name the registry: %q", reason)
	}
}

func TestAllowedRegistriesCanBeOverridden(t *testing.T) {
	p := plan(PlanOptions{AllowedRegistries: []string{"GHCR.io"}}, nil, []string{"ghcr.io/org/tool:2", "docker.io/acme/web:2"},
		image("a", 100, "ghcr.io/org/tool:1"),
		image("b", 100, "docker.io/acme/web:1"),
	)
	if got := itemIDs(p); len(got) != 1 || got[0] != hexID("a") {
		t.Fatalf("an explicit list replaces the defaults (case-insensitively), got %v", got)
	}
}

func TestOrphansAreKeptUnlessRequested(t *testing.T) {
	// No version of "game" is referenced anywhere: it may be scaled to zero.
	images := []criImage{image("a", 100, "docker.io/acme/game:1")}
	cluster := []string{"docker.io/acme/other:1"}

	if p := plan(PlanOptions{}, nil, cluster, images...); len(p.Items) != 0 || exclusion(t, p, "a").Reason != ReasonOrphanNotAllowed {
		t.Fatalf("an orphan must be kept by default: %+v", p)
	}
	p := plan(PlanOptions{IncludeOrphans: true}, nil, cluster, images...)
	if len(p.Items) != 1 || p.Items[0].Category != CategoryOrphan {
		t.Fatalf("an orphan is planned only when asked: %+v", p.Items)
	}
}

func TestUnnamedImagesCannotBeClassified(t *testing.T) {
	p := plan(PlanOptions{IncludeOrphans: true}, nil, []string{"docker.io/acme/web:2"}, image("a", 100))
	if len(p.Items) != 0 || exclusion(t, p, "a").Reason != ReasonUnnamed {
		t.Fatalf("an image with no name must be kept: %+v", p)
	}
}

func TestRulesApplyInOrderOfStrongestReasonToKeep(t *testing.T) {
	// In use AND a system image AND from a disallowed registry: the reason shown
	// is the first, strongest one.
	p := plan(PlanOptions{}, []string{hexID("a")}, nil, image("a", 1, "ghcr.io/rancher/x:1"))
	if exclusion(t, p, "a").Reason != ReasonInUseHere {
		t.Fatalf("in-use must outrank every other reason: %+v", p.Excluded)
	}
}

func TestPlanIsDeterministicAndIdentifiesItsContents(t *testing.T) {
	a, b := image("a", 1, "docker.io/acme/web:1"), image("b", 1, "docker.io/acme/web:0")
	cluster := []string{"docker.io/acme/web:2"}
	first := plan(PlanOptions{}, nil, cluster, a, b)
	second := plan(PlanOptions{}, nil, cluster, b, a)
	if first.ID != second.ID || strings.Join(itemIDs(first), ",") != strings.Join(itemIDs(second), ",") {
		t.Fatalf("input order must not change the plan: %v vs %v", itemIDs(first), itemIDs(second))
	}
	if third := plan(PlanOptions{}, nil, cluster, a); third.ID == first.ID {
		t.Fatal("a plan with different contents must have a different ID")
	}
	if !first.ExpiresAt.Equal(planNow.Add(planLifetime)) {
		t.Fatalf("unexpected expiry %v", first.ExpiresAt)
	}
}

func TestParseReference(t *testing.T) {
	digest := strings.Repeat("d", 64)
	cases := []struct {
		raw        string
		repository string
		tag        string
		hasDigest  bool
	}{
		{"nginx", "docker.io/library/nginx", "latest", false},
		{"nginx:1.25", "docker.io/library/nginx", "1.25", false},
		{"acme/web:3", "docker.io/acme/web", "3", false},
		{"GHCR.io/Org/tool:v1", "ghcr.io/Org/tool", "v1", false},
		{"reg.example.com:5000/acme/web:1", "reg.example.com:5000/acme/web", "1", false},
		{"localhost/web", "localhost/web", "latest", false},
		{"docker.io/acme/web:2@sha256:" + digest, "docker.io/acme/web", "2", true},
		{"docker.io/acme/web@sha256:" + digest, "docker.io/acme/web", "", true},
		{hexID("a"), "", "", true},
	}
	for _, c := range cases {
		got := parseReference(c.raw)
		if got.repository != c.repository || got.tag != c.tag || (got.digest != "") != c.hasDigest {
			t.Fatalf("parseReference(%q) = %+v, want repo %q tag %q digest %v", c.raw, got, c.repository, c.tag, c.hasDigest)
		}
	}
}

// fakeClusterWithResources is fakeCluster plus a list of custom resource types
// the cluster defines and the listing returned for them.
func fakeClusterWithResources(clusterJSON, definedResources, customJSON string, customErr error) *Inspector {
	inspector := fakeCluster(clusterJSON, nil, func() string { return noContainers })
	base := inspector.Run
	inspector.Run = func(ctx context.Context, name string, args ...string) (string, error) {
		if name == rke2Kubectl && args[2] == "api-resources" {
			return definedResources, nil
		}
		if name == rke2Kubectl && args[2] == "get" && strings.Contains(args[3], "virtualmachines") {
			return customJSON, customErr
		}
		return base(ctx, name, args...)
	}
	return inspector
}

// fakeCluster answers the commands the planner issues.
func fakeCluster(clusterJSON string, clusterErr error, containers func() string) *Inspector {
	return &Inspector{
		Hostname: func() (string, error) { return "node-a", nil },
		Now:      func() time.Time { return planNow },
		Run: func(_ context.Context, name string, args ...string) (string, error) {
			switch {
			case name == rke2Crictl && args[2] == "images":
				return `{"images":[
				  {"id":"` + hexID("a") + `","repoTags":["docker.io/acme/web:1"],"repoDigests":[],"size":"100"},
				  {"id":"` + hexID("b") + `","repoTags":["docker.io/acme/web:2"],"repoDigests":[],"size":"900"}]}`, nil
			case name == rke2Crictl && args[2] == "ps":
				return containers(), nil
			case name == rke2Kubectl:
				return clusterJSON, clusterErr
			}
			return "", errors.New("unexpected command " + name)
		},
	}
}

const noContainers = `{"containers":[]}`

func TestPlanRefusesWhenTheClusterViewIsUnavailable(t *testing.T) {
	// If the API server cannot be read, planning would be guessing which images
	// other servers need. It must fail, not plan.
	inspector := fakeCluster("", errors.New("connection refused"), func() string { return noContainers })
	if _, err := inspector.PlanImageCleanup(context.Background(), PlanOptions{}); err == nil || !strings.Contains(err.Error(), "refusing to plan") {
		t.Fatalf("expected a refusal, got %v", err)
	}
}

func TestPlanRefusesAnEmptyClusterListing(t *testing.T) {
	inspector := fakeCluster(`{"items":[]}`, nil, func() string { return noContainers })
	if _, err := inspector.PlanImageCleanup(context.Background(), PlanOptions{}); err == nil {
		t.Fatal("a listing with no images means the view is broken, not that nothing is used")
	}
}

func TestClusterWalkFindsInitContainersTemplatesAndImageIDs(t *testing.T) {
	// The live version is referenced only from a cron job's template and an init
	// container's status; both must be found.
	cluster := `{"items":[
	 {"kind":"CronJob","spec":{"jobTemplate":{"spec":{"template":{"spec":{"containers":[{"name":"c","image":"docker.io/acme/web:2"}]}}}}}},
	 {"kind":"Pod","status":{"initContainerStatuses":[{"imageID":"docker.io/acme/other@sha256:` + strings.Repeat("e", 64) + `"}]}}]}`
	inspector := fakeCluster(cluster, nil, func() string { return noContainers })

	p, err := inspector.PlanImageCleanup(context.Background(), PlanOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if got := itemIDs(p); len(got) != 1 || got[0] != hexID("a") {
		t.Fatalf("the old version should be planned and the cron job's version kept, got %v", got)
	}
}

func TestContainerUseCheckerRelistsContainersEveryCall(t *testing.T) {
	// The first listing has no container; by the second, one has started on the
	// old image. A stale snapshot would call it safe.
	calls := 0
	inspector := fakeCluster(`{}`, nil, func() string {
		calls++
		if calls >= 2 {
			return `{"containers":[{"imageRef":"","image":{"image":"` + hexID("a") + `"}}]}`
		}
		return noContainers
	})

	check, err := inspector.ContainerUseChecker(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if used, err := check(context.Background(), hexID("a")); err != nil || used {
		t.Fatalf("first check: used=%v err=%v", used, err)
	}
	if used, err := check(context.Background(), hexID("a")); err != nil || !used {
		t.Fatalf("a container that started in between must be noticed: used=%v err=%v", used, err)
	}
	if _, err := check(context.Background(), hexID("z")); err == nil {
		t.Fatal("an image that is not on this server must be an error")
	}
}

func TestValidImageID(t *testing.T) {
	good := hexID("a")
	for _, bad := range []string{"", "sha256:abc", good + "x", "../" + good, "--all", "sha256:" + strings.Repeat("G", 64), good + " --force"} {
		if ValidImageID(bad) {
			t.Fatalf("%q must not be accepted as an image ID", bad)
		}
	}
	if !ValidImageID(good) {
		t.Fatal("a real ID must be accepted")
	}
}

func TestRollbackHistoryKeepsPreviousVersions(t *testing.T) {
	// "web:1" is not running anywhere, but an old ReplicaSet (the Deployment's
	// rollback history) still names it. A rollback needs it, so it must be kept.
	cluster := `{"items":[
	 {"kind":"Deployment","spec":{"template":{"spec":{"containers":[{"image":"docker.io/acme/web:2"}]}}}},
	 {"kind":"ReplicaSet","spec":{"replicas":0,"template":{"spec":{"containers":[{"image":"docker.io/acme/web:1"}]}}}}]}`
	inspector := fakeCluster(cluster, nil, func() string { return noContainers })

	p, err := inspector.PlanImageCleanup(context.Background(), PlanOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if len(p.Items) != 0 {
		t.Fatalf("an image named by rollback history must not be planned: %v", itemIDs(p))
	}
	if exclusion(t, p, "a").Reason != ReasonReferenced {
		t.Fatalf("it should be kept as referenced: %+v", p.Excluded)
	}
}

func TestPlannerReadsRevisionHistoryResources(t *testing.T) {
	// Guards the list itself: dropping replicasets or controllerrevisions from
	// the query would silently re-open the rollback gap.
	for _, kind := range []string{"pods", "deployments", "replicasets", "statefulsets", "daemonsets", "controllerrevisions", "cronjobs", "jobs"} {
		if !strings.Contains(","+clusterImageResources+",", ","+kind+",") {
			t.Fatalf("the cluster view must include %s", kind)
		}
	}
}

func TestAnImageNamedOnlyInAnEnvironmentVariableIsKept(t *testing.T) {
	// Operators (CDI, KubeVirt, Longhorn) pass the images they will launch later
	// in environment variables. They are in no "image" field, but they are needed.
	p := plan(PlanOptions{IncludeOrphans: true}, nil, []string{"docker.io/acme/operator:1"}, image("a", 100, "quay.io/kubevirt/cdi-importer:v1.66.0"))
	if len(p.Items) != 1 {
		t.Fatalf("without the raw text this image looks like an orphan: %+v", p.Items)
	}

	text := `{"env":[{"name":"IMPORTER_IMAGE","value":"quay.io/kubevirt/cdi-importer:v1.66.0"}]}`
	p = buildImagePlan(planInputs{
		images: []criImage{image("a", 100, "quay.io/kubevirt/cdi-importer:v1.66.0")}, clusterRefs: []string{"docker.io/acme/operator:1"},
		clusterText: text, host: "node-a", now: planNow, options: PlanOptions{IncludeOrphans: true},
	})
	if len(p.Items) != 0 || exclusion(t, p, "a").Reason != ReasonReferenced {
		t.Fatalf("an image an operator will launch must be kept: %+v", p)
	}
}

func TestRawTextMatchesDigestsAndShortDockerNames(t *testing.T) {
	digest := strings.Repeat("c", 64)
	cases := map[string]struct {
		text string
		img  criImage
	}{
		"digest in an annotation": {`{"annotations":{"x":"build@sha256:` + digest + `"}}`, image("a", 1, "docker.io/acme/x@sha256:"+digest)},
		"short docker name":       {`{"args":["--image=node:20"]}`, image("a", 1, "docker.io/library/node:20")},
		"user/repo short":         {`{"value":"acme/web:3"}`, image("a", 1, "docker.io/acme/web:3")},
	}
	for name, c := range cases {
		p := buildImagePlan(planInputs{images: []criImage{c.img}, clusterRefs: []string{"docker.io/other/app:1"}, clusterText: c.text,
			host: "node-a", now: planNow, options: PlanOptions{IncludeOrphans: true}})
		if len(p.Items) != 0 {
			t.Errorf("%s: an image mentioned in the raw text must be kept", name)
		}
	}
}

func TestTextMatchingOnlyEverErrsTowardKeeping(t *testing.T) {
	// "web:1" is a substring of "web:10", so it is kept. That over-protects, and
	// over-protecting is the safe direction.
	p := buildImagePlan(planInputs{images: []criImage{image("a", 1, "docker.io/acme/web:1")}, clusterRefs: []string{"docker.io/other/app:1"},
		clusterText: `{"image":"docker.io/acme/web:10"}`, host: "node-a", now: planNow, options: PlanOptions{IncludeOrphans: true}})
	if len(p.Items) != 0 {
		t.Fatal("a possible match must keep the image")
	}
}

func TestVirtualMachineDisksAreReadWhenTheCRDsExist(t *testing.T) {
	vms := `{"items":[{"kind":"VirtualMachine","spec":{"template":{"spec":{"volumes":[{"containerDisk":{"image":"quay.io/containerdisks/ubuntu:24.04"}}]}}}}]}`
	workloads := `{"items":[{"kind":"Deployment","spec":{"template":{"spec":{"containers":[{"image":"docker.io/acme/web:2"}]}}}}]}`

	// The server holds the disk image, and nothing but the virtual machine uses it.
	withDisk := func(definedResources string) *Inspector {
		inspector := fakeClusterWithResources(workloads, definedResources, vms, nil)
		base := inspector.Run
		inspector.Run = func(ctx context.Context, name string, args ...string) (string, error) {
			if name == rke2Crictl && args[2] == "images" {
				return `{"images":[{"id":"` + hexID("c") + `","repoTags":["quay.io/containerdisks/ubuntu:24.04"],"repoDigests":[],"size":"900"}]}`, nil
			}
			return base(ctx, name, args...)
		}
		return inspector
	}
	plannedDisk := func(p CleanupPlan) bool {
		for _, item := range p.Items {
			if strings.Contains(item.Repository, "containerdisks") {
				return true
			}
		}
		return false
	}

	// Control: when the virtual machine resources are not read, the disk image
	// looks like an orphan. This is what makes the next assertion meaningful.
	control, err := withDisk("pods\n").PlanImageCleanup(context.Background(), PlanOptions{IncludeOrphans: true})
	if err != nil {
		t.Fatal(err)
	}
	if !plannedDisk(control) {
		t.Fatal("control failed: the disk image should be planned when virtual machines are not read")
	}

	p, err := withDisk("pods\nvirtualmachines.kubevirt.io\n").PlanImageCleanup(context.Background(), PlanOptions{IncludeOrphans: true})
	if err != nil {
		t.Fatal(err)
	}
	if plannedDisk(p) {
		t.Fatalf("a disk image used by a virtual machine must be kept: %+v", p.Items)
	}
	if exclusion(t, p, "c").Reason != ReasonReferenced {
		t.Fatalf("it should be kept as referenced: %+v", p.Excluded)
	}
}

func TestNoVirtualMachineResourcesAreQueriedWhenTheCRDsAreAbsent(t *testing.T) {
	queried := false
	inspector := fakeCluster(`{"items":[{"spec":{"containers":[{"image":"docker.io/acme/web:2"}]}}]}`, nil, func() string { return noContainers })
	base := inspector.Run
	inspector.Run = func(ctx context.Context, name string, args ...string) (string, error) {
		if name == rke2Kubectl && args[2] == "get" && strings.Contains(args[3], "virtualmachines") {
			queried = true
		}
		return base(ctx, name, args...)
	}
	if _, err := inspector.PlanImageCleanup(context.Background(), PlanOptions{}); err != nil {
		t.Fatal(err)
	}
	if queried {
		t.Fatal("a resource type the cluster does not define must not be queried (it would fail the plan)")
	}
}

func TestPlanRefusesWhenAnExistingCustomResourceCannotBeRead(t *testing.T) {
	workloads := `{"items":[{"kind":"Deployment","spec":{"template":{"spec":{"containers":[{"image":"docker.io/acme/web:2"}]}}}}]}`
	inspector := fakeClusterWithResources(workloads, "virtualmachines.kubevirt.io\n", "", errors.New("forbidden"))
	if _, err := inspector.PlanImageCleanup(context.Background(), PlanOptions{}); err == nil || !strings.Contains(err.Error(), "refusing to plan") {
		t.Fatalf("an unreadable virtual machine listing must refuse the plan, got %v", err)
	}
}

// --- aggressive level -------------------------------------------------------

var aggressive = PlanOptions{Level: LevelAggressive}

func TestAggressiveRemovesEverythingNoContainerHereUses(t *testing.T) {
	cluster := []string{"docker.io/acme/web:2", "docker.io/acme/api:7"}
	p := plan(aggressive, []string{hexID("a")}, cluster,
		image("a", 100, "docker.io/acme/web:2"),     // in use on this server: kept
		image("b", 100, "docker.io/acme/web:1"),     // older version
		image("c", 100, "docker.io/acme/api:7"),     // failover cache: another server runs it
		image("d", 100, "ghcr.io/org/private:1"),    // a registry careful would not touch
		image("e", 100, "docker.io/acme/retired:3"), // no version running anywhere
		image("f", 100), // no name
	)
	want := map[string]string{"b": CategorySuperseded, "c": CategoryReferenced, "d": CategoryOrphan, "e": CategoryOrphan, "f": CategoryUnnamed}
	got := map[string]string{}
	for _, item := range p.Items {
		got[strings.TrimPrefix(item.ID, "sha256:")[:1]] = item.Category
	}
	if len(got) != len(want) {
		t.Fatalf("aggressive must plan everything not in use here: got %v, want %v", got, want)
	}
	for id, category := range want {
		if got[id] != category {
			t.Fatalf("image %s: category %q, want %q", id, got[id], category)
		}
	}
	if exclusion(t, p, "a").Reason != ReasonInUseHere {
		t.Fatal("the image a container here uses must be kept")
	}
}

func TestAggressiveStillKeepsTheTwoFloors(t *testing.T) {
	// Floors that are not about re-downloading: an image a container here uses,
	// and system or pinned images (a server needs those to start at all).
	pinned := image("b", 100, "docker.io/acme/preloaded:1")
	pinned.Pinned = true
	p := plan(aggressive, []string{hexID("a")}, []string{"docker.io/other/app:1"},
		image("a", 100, "docker.io/acme/web:1"),
		pinned,
		image("c", 100, "docker.io/rancher/hardened-etcd:v3"),
		image("d", 100, "registry.k8s.io/pause:3.9"),
	)
	if len(p.Items) != 0 {
		t.Fatalf("aggressive must never plan these: %+v", p.Items)
	}
	reasons := map[string]string{"a": ReasonInUseHere, "b": ReasonPinned, "c": ReasonSystem, "d": ReasonSystem}
	for id, reason := range reasons {
		if exclusion(t, p, id).Reason != reason {
			t.Fatalf("image %s should be kept as %q, got %q", id, reason, exclusion(t, p, id).Reason)
		}
	}
}

func TestPinnedImagesAreKeptAtEveryLevel(t *testing.T) {
	pinned := image("a", 100, "docker.io/acme/preloaded:1")
	pinned.Pinned = true
	for _, options := range []PlanOptions{{}, {Level: LevelCareful, IncludeOrphans: true}, aggressive} {
		p := plan(options, nil, []string{"docker.io/other/app:1"}, pinned)
		if len(p.Items) != 0 || exclusion(t, p, "a").Reason != ReasonPinned {
			t.Fatalf("a pinned image must be kept (level %q): %+v", options.Level, p)
		}
	}
}

func TestAnEmptyLevelIsCarefulNotAggressive(t *testing.T) {
	// A caller that forgets to say must get the conservative behavior.
	p := plan(PlanOptions{}, nil, []string{"docker.io/acme/web:2"}, image("a", 100, "docker.io/acme/web:2"))
	if len(p.Items) != 0 || exclusion(t, p, "a").Reason != ReasonReferenced {
		t.Fatalf("an unspecified level must behave as careful: %+v", p)
	}
}

func TestAggressiveStillWorksWhenTheClusterCannotBeRead(t *testing.T) {
	// The API server being down is exactly when a disk may be full. Aggressive
	// needs no cluster view to be safe, so it must still plan, and say what it lost.
	inspector := fakeCluster("", errors.New("connection refused"), func() string {
		return `{"containers":[{"imageRef":"","image":{"image":"` + hexID("b") + `"}}]}`
	})
	p, err := inspector.PlanImageCleanup(context.Background(), aggressive)
	if err != nil {
		t.Fatalf("an aggressive plan must not need the cluster view: %v", err)
	}
	if len(p.Notes) == 0 || !strings.Contains(p.Notes[0], "could not be read") {
		t.Fatalf("the plan must say the cluster was unavailable: %+v", p.Notes)
	}
	if got := itemIDs(p); len(got) != 1 || got[0] != hexID("a") {
		t.Fatalf("the image a container uses must still be kept: %v", got)
	}

	// Careful keeps refusing: without the cluster view it cannot protect anything.
	if _, err := inspector.PlanImageCleanup(context.Background(), PlanOptions{Level: LevelCareful}); err == nil || !strings.Contains(err.Error(), "refusing to plan") {
		t.Fatalf("a careful plan must still refuse, got %v", err)
	}
}
