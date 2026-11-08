package catalog

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"regexp"
	"strings"
)

// Everything below that ends up inside generated YAML is either validated against
// one of these patterns or JSON-quoted (a JSON string is a valid YAML scalar), so
// third-party text can never add a key or break out of a value.
var (
	segmentPattern = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._-]{0,99}$`)
	namePattern    = regexp.MustCompile(`^[a-z0-9]([-a-z0-9]{0,38}[a-z0-9])?$`)
	versionPattern = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._+-]{0,63}$`)
	tagPattern     = regexp.MustCompile(`^[A-Za-z0-9_][A-Za-z0-9._-]{0,127}$`)
	digestPattern  = regexp.MustCompile(`^sha256:[a-f0-9]{64}$`)
)

func safeSegment(s string) bool { return segmentPattern.MatchString(s) }

// A namespace an app may not be placed in: the cluster's own, and the ones the
// platform's core pods use. The person still reviews the files before anything lands.
func reservedNamespace(ns string) bool {
	return ns == "default" || ns == "argocd" || ns == "gitea" || strings.HasPrefix(ns, "kube-")
}

// PreviewRequest names one result and, optionally, how to place it.
type PreviewRequest struct {
	Kind      Kind   `json:"kind"`
	ID        string `json:"id"`
	Name      string `json:"name,omitempty"`      // app name; default: the chart or image name
	Namespace string `json:"namespace,omitempty"` // default: the app name
	Tag       string `json:"tag,omitempty"`       // images only; default: latest
}

// Flag is one thing to know before using the app. Level is "good", "warn" or "bad".
type Flag struct {
	Level string `json:"level"`
	Text  string `json:"text"`
}

// File is one file the preview would add to the recovery repository.
type File struct {
	Path    string `json:"path"`
	Content string `json:"content"`
}

// Preview is the dry run: what the app is, what to know about it, and the files that
// would add it. Applied is always false; nothing here changes anything.
type Preview struct {
	Entry     Entry    `json:"entry"`
	Name      string   `json:"name"`
	Namespace string   `json:"namespace"`
	Summary   string   `json:"summary"`
	Flags     []Flag   `json:"flags"`
	Needs     []string `json:"needs"` // what the agent could not know and a person must check
	Files     []File   `json:"files"`
	Applied   bool     `json:"applied"`
}

// Preview drafts the files for one result. Facts (repository address, version,
// digest) are read from the source again here rather than trusted from the caller.
func (c *Client) Preview(ctx context.Context, request PreviewRequest) (Preview, error) {
	namespaceOwner, name, found := strings.Cut(request.ID, "/")
	if !found || !safeSegment(namespaceOwner) || !safeSegment(name) {
		return Preview{}, errors.New("give the result's id as it appeared in the search")
	}
	app := request.Name
	if app == "" {
		app = strings.ToLower(name)
	}
	if !namePattern.MatchString(app) {
		return Preview{}, errors.New("an app name uses lower-case letters, digits and dashes, and is at most 40 characters")
	}
	namespace := request.Namespace
	if namespace == "" {
		namespace = app
	}
	if !namePattern.MatchString(namespace) || reservedNamespace(namespace) {
		return Preview{}, fmt.Errorf("%q can't be used as the app's namespace; pick another", namespace)
	}
	switch request.Kind {
	case KindChart:
		return c.previewChart(ctx, namespaceOwner, name, app, namespace)
	case KindImage:
		tag := request.Tag
		if tag == "" {
			tag = "latest"
		}
		if !tagPattern.MatchString(tag) {
			return Preview{}, errors.New("that isn't a valid image tag")
		}
		return c.previewImage(ctx, namespaceOwner, name, tag, app, namespace)
	}
	return Preview{}, errors.New(`kind must be "chart" or "image"`)
}

func q(s string) string {
	b, _ := json.Marshal(s)
	return string(b)
}

const reviewNote = "Nothing has been installed. These files are only a starting point: Adaetum lays apps out its own way (a category folder, explicit settings, secrets from OpenBao, routes, a README), so the app still has to be converted to that layout before it goes into the recovery repository."

// helmSource converts an Artifact Hub repository address into what an Argo CD
// application needs: the repository, and the chart's name within it.
func helmSource(address, chart string) (repo, name string, err error) {
	u, err := url.Parse(address)
	if err != nil || u.User != nil || u.RawQuery != "" || u.Fragment != "" || u.Host == "" {
		return "", "", errors.New("the chart's repository address isn't one the agent will use")
	}
	switch u.Scheme {
	case "https":
		return address, chart, nil
	case "oci":
		// oci://ghcr.io/org/charts/app  ->  repoURL ghcr.io/org/charts, chart app
		path := strings.Trim(u.Path, "/")
		cut := strings.LastIndex(path, "/")
		if cut <= 0 {
			return "", "", errors.New("the chart's registry address isn't one the agent will use")
		}
		return u.Host + "/" + path[:cut], path[cut+1:], nil
	}
	return "", "", errors.New("the chart is hosted over plain http or an unknown protocol, so the agent won't use it")
}

func (c *Client) previewChart(ctx context.Context, repository, chartName, app, namespace string) (Preview, error) {
	p, err := c.chart(ctx, repository, chartName)
	if err != nil {
		return Preview{}, err
	}
	if !versionPattern.MatchString(p.Version) {
		return Preview{}, errors.New("Artifact Hub gave no usable version for this chart")
	}
	repo, chart, err := helmSource(p.Repository.URL, p.Name)
	if err != nil {
		return Preview{}, err
	}
	if !safeSegment(chart) {
		return Preview{}, errors.New("the chart's name isn't one the agent will use")
	}
	entry := p.entry()
	// The id the caller used wins, so the preview matches the result they clicked.
	entry.ID = repository + "/" + chartName

	// Same shape as pods/gitea/gitea.app.yaml: the ApplicationSet in pods/argocd
	// turns this into an Argo CD application, with values kept in the repository.
	valuesPath := fmt.Sprintf("pods/%s/%s-values.yaml", namespace, app)
	appFile := fmt.Sprintf(`name: %s
project: default
source_type: helm
source_repo_url: %s
source_chart: %s
source_target_revision: %s
source_helm_value_files:
  - $values/%s
destination_namespace: %s
`, q(app), q(repo), q(chart), q(p.Version), valuesPath, q(namespace))
	valuesFile := fmt.Sprintf(`# Settings for %s. Empty means the chart's own defaults.
# See the chart's page for what can be set: %s
{}
`, chart, entry.Page)

	flags := publisherFlags(entry)
	if p.Security != nil && (p.Security.Critical > 0 || p.Security.High > 0) {
		flags = append(flags, Flag{"bad", fmt.Sprintf("Artifact Hub's scan found %d critical and %d high-severity security issues in its images.", p.Security.Critical, p.Security.High)})
	} else if p.Security != nil {
		flags = append(flags, Flag{"good", "Artifact Hub's scan found no critical or high-severity issues."})
	} else {
		flags = append(flags, Flag{"warn", "Artifact Hub has no security scan for this chart."})
	}
	if entry.Signed {
		flags = append(flags, Flag{"good", "The chart is signed by its publisher."})
	} else {
		flags = append(flags, Flag{"warn", "The chart isn't signed, so its authenticity can't be checked."})
	}
	if p.License != "" {
		flags = append(flags, Flag{"good", "License: " + clean(p.License) + "."})
	}
	if len(p.Images) > 0 {
		var shown []string
		for i, image := range p.Images {
			if i == 5 {
				shown = append(shown, fmt.Sprintf("and %d more", len(p.Images)-5))
				break
			}
			shown = append(shown, clean(image.Image))
		}
		flags = append(flags, Flag{"warn", "It would run these container images: " + strings.Join(shown, ", ") + "."})
	}

	return Preview{
		Entry:     entry,
		Name:      app,
		Namespace: namespace,
		Summary: fmt.Sprintf("“%s” is published as a Helm chart (version %s), a bundle with everything the app needs. This draft points Argo CD straight at the publisher's chart as the app “%s” in the namespace “%s”. %s",
			entry.Name, p.Version, app, namespace, reviewNote),
		Flags: flags,
		Needs: []string{
			"Check the chart's page for settings that expose the app outside the cluster (an ingress or a public load balancer). Leave them off unless you want that.",
			"The app has not been tried on this cluster, and an OCI-hosted chart may need its registry added to Argo CD first.",
			"The chart's default settings aren't Adaetum's: storage class and a lean volume size (chart defaults are often generous), resource limits, monitoring labels and secrets all need setting the way the rest of pods/ does.",
		},
		Files: []File{
			{Path: fmt.Sprintf("pods/%s/%s.app.yaml", namespace, app), Content: appFile},
			{Path: valuesPath, Content: valuesFile},
		},
	}, nil
}

func publisherFlags(entry Entry) []Flag {
	switch {
	case entry.Official:
		return []Flag{{"good", "Published by the project itself (marked official)."}}
	case entry.Verified:
		return []Flag{{"good", fmt.Sprintf("Published by %s, whose identity Artifact Hub has verified. That shows who they are, not that the chart is safe.", entry.Publisher)}}
	}
	return []Flag{{"warn", fmt.Sprintf("Published by %s, who isn't verified. Anyone can publish here, so check who they are.", entry.Publisher)}}
}

func (c *Client) previewImage(ctx context.Context, namespaceOwner, name, tag, app, namespace string) (Preview, error) {
	repo, err := c.image(ctx, namespaceOwner, name)
	if err != nil {
		return Preview{}, err
	}
	digest, err := c.imageDigest(ctx, namespaceOwner, name, tag)
	if err != nil {
		return Preview{}, err
	}
	official := namespaceOwner == "library"
	entry := Entry{
		Kind: KindImage, ID: namespaceOwner + "/" + name, Name: clean(name),
		Description: clean(firstNonEmpty(repo.Description, repo.Short)),
		Publisher:   publisherOf(namespaceOwner), Official: official,
		Stars: repo.Stars, Pulls: repo.Pulls, Source: "Docker Hub", Page: dockerPage(namespaceOwner, name),
	}
	// Docker Hub's own naming: Official Images live under "library", written bare.
	reference := "docker.io/" + namespaceOwner + "/" + name + "@" + digest

	// STUB (development): the agent cannot know how an arbitrary image wants to be
	// run, so this is a minimal starter (one copy, one guessed port, no storage) and
	// says so under "needs". A chart is preferred for exactly that reason.
	root := fmt.Sprintf("pods/%s/%s", namespace, app)
	appFile := fmt.Sprintf(`name: %s
project: default
source_type: git
source_target_revision: HEAD
source_path: %s
destination_namespace: %s
`, q(app), root, q(namespace))
	kustomization := "apiVersion: kustomize.config.k8s.io/v1beta1\nkind: Kustomization\nresources:\n  - deployment.yaml\n  - service.yaml\n"
	deployment := fmt.Sprintf(`apiVersion: apps/v1
kind: Deployment
metadata:
  name: %s
spec:
  replicas: 1
  selector:
    matchLabels:
      app: %s
  template:
    metadata:
      labels:
        app: %s
    spec:
      containers:
        - name: app
          # Pinned by digest to what tag %s pointed at when this was drafted.
          image: %s
          ports:
            - containerPort: 8080 # GUESS: set the port this app really listens on.
          resources:
            requests: { cpu: 50m, memory: 128Mi }
            limits: { memory: 512Mi }
`, q(app), q(app), q(app), tag, q(reference))
	service := fmt.Sprintf(`apiVersion: v1
kind: Service
metadata:
  name: %s
spec:
  selector:
    app: %s
  ports:
    - port: 80
      targetPort: 8080 # keep in step with the container port above
`, q(app), q(app))

	flags := []Flag{}
	if official {
		flags = append(flags, Flag{"good", "A Docker Official Image, curated and scanned by Docker."})
	} else {
		flags = append(flags, Flag{"warn", fmt.Sprintf("Published by the Docker Hub user “%s”. Anyone can publish here, and Docker Hub doesn't tell us whether they are verified.", entry.Publisher)})
	}
	flags = append(flags, Flag{"good", fmt.Sprintf("Pinned to an exact version of the image (%s), so it can't change underneath you.", digest[:19]+"…")})
	if repo.Pulls > 0 {
		flags = append(flags, Flag{"good", fmt.Sprintf("Downloaded %d times with %d stars.", repo.Pulls, repo.Stars)})
	}
	flags = append(flags, Flag{"warn", "No security scan result is available here."})

	return Preview{
		Entry:     entry,
		Name:      app,
		Namespace: namespace,
		Summary: fmt.Sprintf("No Helm chart was used, so this is a plain container starter for “%s” (tag %s) as the app “%s” in the namespace “%s”: one copy, one network port, no saved data. %s",
			entry.Name, tag, app, namespace, reviewNote),
		Flags: flags,
		Needs: []string{
			"Which port the app listens on. The draft guesses 8080; check the image's Docker Hub page.",
			"Where the app keeps its data. No storage is set up, so anything it saves is lost when it restarts.",
			"Whether it needs settings or secrets (passwords, keys). None are included.",
			"It is not reachable from outside the cluster. That's deliberate.",
		},
		Files: []File{
			{Path: fmt.Sprintf("pods/%s/%s.app.yaml", namespace, app), Content: appFile},
			{Path: root + "/kustomization.yaml", Content: kustomization},
			{Path: root + "/deployment.yaml", Content: deployment},
			{Path: root + "/service.yaml", Content: service},
		},
	}, nil
}

func firstNonEmpty(values ...string) string {
	for _, v := range values {
		if v != "" {
			return v
		}
	}
	return ""
}
