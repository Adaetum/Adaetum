package inspect

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"regexp"
	"sort"
	"strings"
	"time"
)

// This file plans an image cleanup. It is read-only: it decides which images
// could be removed and why every other image must stay. Removing anything is
// done by a different package (internal/cleanup), so that inspection can never
// delete, however it is invoked.
//
// A plan is a preview, not an authorization. The apply step re-derives
// eligibility from live state before each removal.

const planLifetime = 10 * time.Minute

// Plan categories and exclusion reasons. They are part of the plan's JSON.
const (
	CategorySuperseded = "superseded" // an older version; a newer one of the same app is in use
	CategoryOrphan     = "orphan"     // no version of this app is in use anywhere
	// Only in aggressive plans: not used on this server, but something else may want it
	// (a pod on another server, a rollback, a workload scaled to zero).
	CategoryReferenced = "referenced"
	// Only in aggressive plans: an image with no name, so nothing can ask for it.
	CategoryUnnamed = "unnamed"

	ReasonInUseHere        = "in use by a container on this server"
	ReasonReferenced       = "referenced by the cluster (running, scaled to zero, scheduled, or kept for rollback)"
	ReasonSystem           = "system image"
	ReasonPinned           = "pinned by the system (preloaded for RKE2)"
	ReasonUnnamed          = "has no name, so it cannot be classified"
	ReasonOrphanNotAllowed = "no version of this app is in use; kept in case it is needed later"
)

// DefaultAllowedRegistries are registries whose images can be downloaded again
// without credentials or a private network. Images from anywhere else are kept:
// deleting them could make recovery depend on a registry that is down.
var DefaultAllowedRegistries = []string{"docker.io", "quay.io", "registry.k8s.io", "lscr.io"}

// systemRepositoryPrefixes mark images RKE2 and Kubernetes need to start. They
// are matched by name instead of read from RKE2's image lists, which keeps
// planning free of file reads. The rule is deliberately broad: everything under
// docker.io/rancher/ is kept.
var systemRepositoryPrefixes = []string{
	"docker.io/rancher/",
	"registry.k8s.io/pause",
	"k8s.gcr.io/pause",
}

// Plan levels.
//
// Careful keeps everything a recovery might need: images the cluster references
// (including on other servers and for rollback), apps with no version in use, and
// images from registries that may not be reachable. Aggressive removes every image
// no container on this server uses, accepting that anything wanted later is
// downloaded again. Both keep the two floors that are not about re-downloading:
// an image a container here uses, and system or pinned images.
const (
	LevelCareful    = "careful"
	LevelAggressive = "aggressive"
)

// PlanOptions tunes what the plan may include.
type PlanOptions struct {
	// Level is careful or aggressive. Empty means careful, the conservative
	// choice for any caller that does not say; the CLI, API, and schedule default
	// to aggressive explicitly.
	Level string `json:"level,omitempty"`
	// AllowedRegistries replaces DefaultAllowedRegistries when non-empty.
	AllowedRegistries []string
	// IncludeOrphans also plans images whose app has no version in use.
	IncludeOrphans bool
}

// PlanItem is an image the plan would remove.
type PlanItem struct {
	ID         string   `json:"id"`
	Names      []string `json:"names"`
	Repository string   `json:"repository"`
	Registry   string   `json:"registry"`
	Category   string   `json:"category"`
	// CompressedBytes is the size the runtime reports. The disk freed is larger.
	CompressedBytes uint64 `json:"compressedBytes"`
}

// PlanExclusion is an image the plan keeps, with the reason.
type PlanExclusion struct {
	ID              string `json:"id"`
	Name            string `json:"name"`
	Reason          string `json:"reason"`
	CompressedBytes uint64 `json:"compressedBytes"`
}

// CleanupPlan is the preview of an image cleanup on one server.
type CleanupPlan struct {
	ID        string          `json:"id"`
	Host      string          `json:"host"`
	CreatedAt time.Time       `json:"createdAt"`
	ExpiresAt time.Time       `json:"expiresAt"`
	Options   PlanOptions     `json:"options"`
	Items     []PlanItem      `json:"items"`
	Excluded  []PlanExclusion `json:"excluded"`
	// Notes are things the operator should know about this plan, such as the
	// cluster view being unavailable.
	Notes []string `json:"notes,omitempty"`
}

// criImage is the subset of `crictl images` this package reads.
type criImage struct {
	ID          string     `json:"id"`
	RepoTags    []string   `json:"repoTags"`
	RepoDigests []string   `json:"repoDigests"`
	Size        flexUint64 `json:"size"`
	// Pinned images are preloaded (air-gap bundles) and exempt from garbage
	// collection. They are never planned, at either level.
	Pinned bool `json:"pinned"`
}

// planInputs is everything the planner needs, gathered by the caller, so the
// classification itself is a pure function that can be tested exhaustively.
type planInputs struct {
	images []criImage
	// containerRefs are image references held by containers on this server,
	// running or stopped.
	containerRefs []string
	// clusterRefs are image references found anywhere in the cluster: pods on
	// every server and the templates of workloads, including ones scaled to zero.
	clusterRefs []string
	// clusterText is the raw listing clusterRefs came from; see referenceSet.text.
	clusterText string
	host        string
	now         time.Time
	options     PlanOptions
}

// reference is an image reference taken apart. Registry and path form the
// repository; docker short names are expanded so "nginx:1.2" and
// "docker.io/library/nginx:1.2" compare equal.
type reference struct {
	repository string
	tag        string
	digest     string
}

var digestPattern = regexp.MustCompile(`sha256:[0-9a-f]{64}`)

func parseReference(raw string) reference {
	raw = strings.TrimSpace(raw)
	var ref reference
	if digest := digestPattern.FindString(raw); digest != "" {
		ref.digest = digest
	}
	// A bare image ID is a digest with no repository. Checked first, because its
	// colon would otherwise be read as a tag separator.
	if digestPattern.FindString(raw) == raw {
		return ref
	}
	name := raw
	if at := strings.Index(name, "@"); at >= 0 {
		name = name[:at]
	}
	// A tag is a colon after the last slash; a colon before it is a port.
	if colon := strings.LastIndex(name, ":"); colon > strings.LastIndex(name, "/") {
		ref.tag = name[colon+1:]
		name = name[:colon]
	}
	if name == "" || strings.HasPrefix(name, "sha256:") {
		// A bare image ID has no repository.
		return ref
	}
	first, rest, hasSlash := strings.Cut(name, "/")
	isRegistry := hasSlash && (strings.ContainsAny(first, ".:") || first == "localhost")
	switch {
	case isRegistry:
		ref.repository = strings.ToLower(first) + "/" + rest
	case hasSlash:
		ref.repository = "docker.io/" + name
	default:
		ref.repository = "docker.io/library/" + name
	}
	if ref.tag == "" && ref.digest == "" {
		ref.tag = "latest"
	}
	return ref
}

func (ref reference) registry() string {
	registry, _, _ := strings.Cut(ref.repository, "/")
	return registry
}

// identities lists the strings by which a reference can be matched: its digest,
// and its repository with tag.
func (ref reference) identities() []string {
	var out []string
	if ref.digest != "" {
		out = append(out, ref.digest)
	}
	if ref.repository != "" && ref.tag != "" {
		out = append(out, ref.repository+":"+ref.tag)
	}
	return out
}

// referenceSet holds identities and repositories seen in a list of references.
type referenceSet struct {
	identities   map[string]bool
	repositories map[string]bool
	// text is the raw listing the references came from, when there is one. Images
	// are also matched against it as plain text, which catches references that are
	// not in an "image" field: operators pass the images they will launch later in
	// environment variables, and arguments and annotations name images too.
	text string
}

func newReferenceSet(raws []string) referenceSet {
	set := referenceSet{identities: map[string]bool{}, repositories: map[string]bool{}}
	for _, raw := range raws {
		if raw == "" {
			continue
		}
		set.identities[raw] = true // an image ID passes through unchanged
		ref := parseReference(raw)
		for _, identity := range ref.identities() {
			set.identities[identity] = true
		}
		if ref.repository != "" {
			set.repositories[ref.repository] = true
		}
	}
	return set
}

// uses reports whether any name of the image appears in the set. A match on any
// one name counts, which errs toward keeping the image.
func (set referenceSet) uses(image criImage) bool {
	if set.identities[image.ID] {
		return true
	}
	for _, name := range append(append([]string{}, image.RepoDigests...), image.RepoTags...) {
		for _, identity := range parseReference(name).identities() {
			if set.identities[identity] {
				return true
			}
		}
	}
	return set.text != "" && set.mentions(image)
}

// mentions reports whether the raw text contains the image's digest or its
// repository-and-tag, in the full form or the shortened Docker form. Substring
// matching can only err toward keeping an image (a tag "web:1" also matches
// "web:10"), which is the safe direction.
func (set referenceSet) mentions(image criImage) bool {
	for _, name := range append(append([]string{}, image.RepoDigests...), image.RepoTags...) {
		ref := parseReference(name)
		if ref.digest != "" && strings.Contains(set.text, strings.TrimPrefix(ref.digest, "sha256:")) {
			return true
		}
		if ref.repository == "" || ref.tag == "" {
			continue
		}
		full := ref.repository + ":" + ref.tag
		short := strings.TrimPrefix(strings.TrimPrefix(full, "docker.io/"), "library/")
		for _, form := range []string{full, short} {
			if strings.Contains(set.text, form) {
				return true
			}
		}
	}
	return false
}

func imageRepositories(image criImage) []string {
	seen := map[string]bool{}
	var out []string
	for _, name := range append(append([]string{}, image.RepoTags...), image.RepoDigests...) {
		if repository := parseReference(name).repository; repository != "" && !seen[repository] {
			seen[repository] = true
			out = append(out, repository)
		}
	}
	sort.Strings(out)
	return out
}

func isSystemRepository(repository string) bool {
	for _, prefix := range systemRepositoryPrefixes {
		if strings.HasPrefix(repository, prefix) {
			return true
		}
	}
	return false
}

// buildImagePlan classifies every image. The first matching rule decides, and
// the rules are ordered so the strongest reason to keep an image wins.
func buildImagePlan(in planInputs) CleanupPlan {
	allowed := map[string]bool{}
	registries := in.options.AllowedRegistries
	if len(registries) == 0 {
		registries = DefaultAllowedRegistries
	}
	for _, registry := range registries {
		allowed[strings.ToLower(registry)] = true
	}

	local := newReferenceSet(in.containerRefs)
	cluster := newReferenceSet(in.clusterRefs)
	cluster.text = in.clusterText

	plan := CleanupPlan{
		Host:      in.host,
		CreatedAt: in.now.UTC(),
		ExpiresAt: in.now.UTC().Add(planLifetime),
		Options:   in.options,
		Items:     []PlanItem{},
		Excluded:  []PlanExclusion{},
	}

	for _, image := range in.images {
		size := uint64(image.Size)
		repositories := imageRepositories(image)
		label := image.ID
		if len(repositories) > 0 {
			label = repositories[0]
		}
		keep := func(reason string) {
			plan.Excluded = append(plan.Excluded, PlanExclusion{ID: image.ID, Name: label, Reason: reason, CompressedBytes: size})
		}

		if in.options.Level == LevelAggressive {
			switch {
			case local.uses(image):
				keep(ReasonInUseHere)
			case image.Pinned:
				keep(ReasonPinned)
			case anySystem(repositories):
				keep(ReasonSystem)
			default:
				item := PlanItem{ID: image.ID, Names: append(append([]string{}, image.RepoTags...), image.RepoDigests...), CompressedBytes: size}
				switch {
				case len(repositories) == 0:
					item.Category, item.Repository = CategoryUnnamed, "(unnamed image)"
				case cluster.uses(image):
					item.Category, item.Repository, item.Registry = CategoryReferenced, repositories[0], strings.SplitN(repositories[0], "/", 2)[0]
				default:
					item.Category, item.Repository, item.Registry = CategoryOrphan, repositories[0], strings.SplitN(repositories[0], "/", 2)[0]
					for _, candidate := range repositories {
						if cluster.repositories[candidate] || local.repositories[candidate] {
							item.Category = CategorySuperseded
						}
					}
				}
				plan.Items = append(plan.Items, item)
			}
			continue
		}

		switch {
		case local.uses(image):
			keep(ReasonInUseHere)
		case image.Pinned:
			keep(ReasonPinned)
		case cluster.uses(image):
			keep(ReasonReferenced)
		case len(repositories) == 0:
			keep(ReasonUnnamed)
		case anySystem(repositories):
			keep(ReasonSystem)
		default:
			repository := repositories[0]
			registry := strings.SplitN(repository, "/", 2)[0]
			if !allowed[registry] {
				keep(fmt.Sprintf("from %s, which is not on the allowed list (it may need credentials or a private network)", registry))
				continue
			}
			// "Superseded" means a version of the same app is in use somewhere,
			// so this one is an older leftover. With none in use, the app may be
			// scaled to zero or about to return, so it is kept unless allowed.
			category := CategoryOrphan
			for _, candidate := range repositories {
				if cluster.repositories[candidate] || local.repositories[candidate] {
					category = CategorySuperseded
				}
			}
			if category == CategoryOrphan && !in.options.IncludeOrphans {
				keep(ReasonOrphanNotAllowed)
				continue
			}
			names := append(append([]string{}, image.RepoTags...), image.RepoDigests...)
			plan.Items = append(plan.Items, PlanItem{
				ID: image.ID, Names: names, Repository: repository, Registry: registry,
				Category: category, CompressedBytes: size,
			})
		}
	}

	sort.Slice(plan.Items, func(a, b int) bool {
		if plan.Items[a].Repository != plan.Items[b].Repository {
			return plan.Items[a].Repository < plan.Items[b].Repository
		}
		return plan.Items[a].ID < plan.Items[b].ID
	})
	sort.Slice(plan.Excluded, func(a, b int) bool { return plan.Excluded[a].ID < plan.Excluded[b].ID })

	identity := sha256.New()
	fmt.Fprint(identity, plan.Host, plan.CreatedAt.Format(time.RFC3339Nano))
	for _, item := range plan.Items {
		fmt.Fprint(identity, item.ID)
	}
	plan.ID = hex.EncodeToString(identity.Sum(nil))[:16]
	return plan
}

func anySystem(repositories []string) bool {
	for _, repository := range repositories {
		if isSystemRepository(repository) {
			return true
		}
	}
	return false
}

// TotalCompressedBytes is the size the runtime reports for the planned items.
func (plan CleanupPlan) TotalCompressedBytes() uint64 {
	var total uint64
	for _, item := range plan.Items {
		total += item.CompressedBytes
	}
	return total
}

// PlanImageCleanup gathers live state and builds a plan. If any part of the
// picture is unavailable, in particular the cluster-wide view, it returns an
// error and no plan: guessing which images the cluster needs is how an image
// gets deleted that a restart depends on.
func (i *Inspector) PlanImageCleanup(ctx context.Context, options PlanOptions) (CleanupPlan, error) {
	host, err := i.Hostname()
	if err != nil {
		return CleanupPlan{}, fmt.Errorf("hostname: %w", err)
	}
	images, containerRefs, err := i.collectImageState(ctx)
	if err != nil {
		return CleanupPlan{}, err
	}
	var notes []string
	clusterRefs, clusterText, err := i.collectClusterImageRefs(ctx)
	if err != nil {
		// A careful plan protects what the cluster references, so without the
		// cluster view it cannot be trusted and there is no plan. An aggressive
		// plan only ever keeps what a container here uses and system images, which
		// need no cluster view, so freeing a full disk must still work when the API
		// server is down. It just cannot say why an image is wanted.
		if options.Level != LevelAggressive {
			return CleanupPlan{}, fmt.Errorf("cluster view unavailable, refusing to plan: %w", err)
		}
		clusterRefs, clusterText = nil, ""
		notes = append(notes, "The cluster could not be read, so images are not labeled by what the cluster uses. Images a container on this server uses are still kept.")
	}
	plan := buildImagePlan(planInputs{
		images:        images,
		containerRefs: containerRefs,
		clusterRefs:   clusterRefs,
		clusterText:   clusterText,
		host:          host,
		now:           i.Now(),
		options:       options,
	})
	plan.Notes = notes
	return plan, nil
}

// collectImageState reads this server's images and the references its
// containers hold.
func (i *Inspector) collectImageState(ctx context.Context) ([]criImage, []string, error) {
	images, err := i.listImages(ctx)
	if err != nil {
		return nil, nil, err
	}
	refs, err := i.containerImageRefs(ctx)
	if err != nil {
		return nil, nil, err
	}
	return images, refs, nil
}

func (i *Inspector) listImages(ctx context.Context) ([]criImage, error) {
	out, err := i.crictl(ctx, "images")
	if err != nil {
		return nil, err
	}
	var images struct {
		Images []criImage `json:"images"`
	}
	if err := json.Unmarshal([]byte(out), &images); err != nil {
		return nil, fmt.Errorf("images: %w", err)
	}
	return images.Images, nil
}

// containerImageRefs lists the image references of all containers on this
// server, running or stopped.
func (i *Inspector) containerImageRefs(ctx context.Context) ([]string, error) {
	out, err := i.crictl(ctx, "ps", "-a")
	if err != nil {
		return nil, err
	}
	var containers struct {
		Containers []struct {
			ImageRef string `json:"imageRef"`
			Image    struct {
				Image              string `json:"image"`
				UserSpecifiedImage string `json:"userSpecifiedImage"`
			} `json:"image"`
		} `json:"containers"`
	}
	if err := json.Unmarshal([]byte(out), &containers); err != nil {
		return nil, fmt.Errorf("containers: %w", err)
	}
	var refs []string
	for _, container := range containers.Containers {
		refs = append(refs, container.ImageRef, container.Image.Image, container.Image.UserSpecifiedImage)
	}
	return refs, nil
}

// clusterImageResources is every kind whose spec can name an image a workload
// may run or return to. Besides pods and workload templates, that includes the
// revision history Kubernetes keeps for rollback: old ReplicaSets for
// Deployments, and ControllerRevisions for StatefulSets and DaemonSets. Those
// hold the previous versions, and an image a rollback needs is not one to
// delete.
const clusterImageResources = "pods,deployments,replicasets,statefulsets,daemonsets,controllerrevisions,cronjobs,jobs"

// collectClusterImageRefs finds every image reference in the cluster by walking
// those resources for "image" and "imageID" fields. Walking generically is
// deliberate: it also catches init containers, cron job templates, revision
// history, and workloads scaled to zero without naming each shape, and matching
// more than strictly necessary only keeps more images.
//
// Images referenced only by custom resources (for example virtual machine disk
// images) are not visible here. That is why the default plan covers only
// superseded versions, where a sibling in use is the evidence the app is live.
func (i *Inspector) collectClusterImageRefs(ctx context.Context) (refs []string, text string, err error) {
	data, err := i.kubectlGet(ctx, clusterImageResources, "-A")
	if err != nil {
		return nil, "", err
	}
	text = string(data)
	var document any
	if err := json.Unmarshal(data, &document); err != nil {
		return nil, "", err
	}
	collectImageFields(document, &refs)
	if len(refs) == 0 {
		// A real cluster always references some image. An empty answer means the
		// view is broken, not that nothing is in use.
		return nil, "", fmt.Errorf("the cluster listing contained no images")
	}

	// Custom resources that name images (virtual machines and their disks) are
	// read too, when their definitions exist. Failing to read one that exists
	// refuses the plan: an unseen reference is how an image gets deleted that
	// something needed.
	optional, err := i.presentImageBearingResources(ctx)
	if err != nil {
		return nil, "", err
	}
	if len(optional) > 0 {
		extra, err := i.kubectlGet(ctx, strings.Join(optional, ","), "-A")
		if err != nil {
			return nil, "", err
		}
		text += string(extra)
		var extraDocument any
		if err := json.Unmarshal(extra, &extraDocument); err != nil {
			return nil, "", err
		}
		collectImageFields(extraDocument, &refs)
	}
	return refs, text, nil
}

// imageBearingCustomResources are known custom resources whose specs name
// container images. They are read only if the cluster defines them.
var imageBearingCustomResources = []string{
	"virtualmachines.kubevirt.io",
	"virtualmachineinstances.kubevirt.io",
	"virtualmachinepools.pool.kubevirt.io",
	"datavolumes.cdi.kubevirt.io",
	"datasources.cdi.kubevirt.io",
	"dataimportcrons.cdi.kubevirt.io",
}

// presentImageBearingResources returns the known custom resources this cluster
// defines, using the API's own list of resource types.
func (i *Inspector) presentImageBearingResources(ctx context.Context) ([]string, error) {
	out, err := i.Run(ctx, rke2Kubectl, "--kubeconfig", rke2Kubeconfig, "api-resources", "--verbs=list", "-o", "name")
	if err != nil {
		return nil, fmt.Errorf("kubectl api-resources: %w", err)
	}
	defined := map[string]bool{}
	for _, line := range strings.Split(out, "\n") {
		defined[strings.TrimSpace(line)] = true
	}
	var present []string
	for _, kind := range imageBearingCustomResources {
		if defined[kind] {
			present = append(present, kind)
		}
	}
	return present, nil
}

func collectImageFields(node any, refs *[]string) {
	switch value := node.(type) {
	case map[string]any:
		for key, child := range value {
			if text, ok := child.(string); ok && (key == "image" || key == "imageID") {
				*refs = append(*refs, text)
				continue
			}
			collectImageFields(child, refs)
		}
	case []any:
		for _, child := range value {
			collectImageFields(child, refs)
		}
	}
}

// ContainerUseChecker returns a function that reports whether any container on
// this server currently holds the given image. The image names are read once;
// the containers are listed again on every call, because that is what changes.
func (i *Inspector) ContainerUseChecker(ctx context.Context) (func(context.Context, string) (bool, error), error) {
	// Only the image names are read here. Containers are listed on every check,
	// never at setup, so no check can rely on a list that predates it.
	images, err := i.listImages(ctx)
	if err != nil {
		return nil, err
	}
	byID := map[string]criImage{}
	for _, image := range images {
		byID[image.ID] = image
	}
	return func(ctx context.Context, id string) (bool, error) {
		image, known := byID[id]
		if !known {
			return false, fmt.Errorf("image %s is not on this server", id)
		}
		refs, err := i.containerImageRefs(ctx)
		if err != nil {
			return false, err
		}
		return newReferenceSet(refs).uses(image), nil
	}, nil
}

// idPattern is what containerd image IDs look like. The apply step validates
// against it before an ID reaches a command line.
var idPattern = regexp.MustCompile(`^sha256:[0-9a-f]{64}$`)

// ValidImageID reports whether id has the shape of a containerd image ID.
func ValidImageID(id string) bool { return idPattern.MatchString(id) }
