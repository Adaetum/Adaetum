package catalog

import (
	"context"
	"fmt"
	"net/url"
	"strings"
)

// artifactPackage is the part of an Artifact Hub package this package uses. Search
// results and the single-package document share these fields.
type artifactPackage struct {
	Name        string `json:"name"`
	Description string `json:"description"`
	Version     string `json:"version"`
	AppVersion  string `json:"app_version"`
	Stars       int    `json:"stars"`
	Deprecated  bool   `json:"deprecated"`
	Signed      bool   `json:"signed"`
	License     string `json:"license"`
	LogoImageID string `json:"logo_image_id"`
	Security    *struct {
		Critical int `json:"critical"`
		High     int `json:"high"`
	} `json:"security_report_summary"`
	Images []struct {
		Image string `json:"image"`
	} `json:"containers_images"`
	Repository struct {
		URL         string `json:"url"`
		Name        string `json:"name"`
		Official    bool   `json:"official"`
		Verified    bool   `json:"verified_publisher"`
		Org         string `json:"organization_name"`
		OrgDisplay  string `json:"organization_display_name"`
		User        string `json:"user_alias"`
		DisplayName string `json:"display_name"`
	} `json:"repository"`
}

func (p artifactPackage) publisher() string {
	for _, name := range []string{p.Repository.OrgDisplay, p.Repository.Org, p.Repository.User, p.Repository.DisplayName, p.Repository.Name} {
		if name != "" {
			return clean(name)
		}
	}
	return "unknown"
}

func (p artifactPackage) entry() Entry {
	return Entry{
		Kind:        KindChart,
		ID:          p.Repository.Name + "/" + p.Name,
		Name:        clean(p.Name),
		Description: clean(p.Description),
		Publisher:   p.publisher(),
		Version:     clean(p.Version),
		AppVersion:  clean(p.AppVersion),
		Verified:    p.Repository.Verified,
		Official:    p.Repository.Official,
		Signed:      p.Signed,
		Deprecated:  p.Deprecated,
		Stars:       p.Stars,
		Logo:        logoID(p.LogoImageID),
		Source:      "Artifact Hub",
		Page:        fmt.Sprintf("%s/packages/helm/%s/%s", artifactHubURL, escape(p.Repository.Name), escape(p.Name)),
	}
}

// searchPackages is the raw Artifact Hub chart search.
func (c *Client) searchPackages(ctx context.Context, query string) ([]artifactPackage, error) {
	var answer struct {
		Packages []artifactPackage `json:"packages"`
	}
	address := fmt.Sprintf("%s/api/v1/packages/search?kind=0&limit=%d&ts_query_web=%s", c.artifactHub(), perSource, url.QueryEscape(query))
	if _, err := c.getJSON(ctx, address, &answer); err != nil {
		return nil, err
	}
	return answer.Packages, nil
}

func (c *Client) searchCharts(ctx context.Context, query string) ([]Entry, error) {
	packages, err := c.searchPackages(ctx, query)
	if err != nil {
		return nil, err
	}
	var answer struct{ Packages []artifactPackage }
	answer.Packages = packages
	entries := make([]Entry, 0, len(answer.Packages))
	for _, p := range answer.Packages {
		// A result without the two names that identify it cannot be previewed.
		if p.Name == "" || p.Repository.Name == "" || !safeSegment(p.Name) || !safeSegment(p.Repository.Name) {
			continue
		}
		entries = append(entries, p.entry())
	}
	return entries, nil
}

func (c *Client) chart(ctx context.Context, repository, name string) (artifactPackage, error) {
	var p artifactPackage
	address := fmt.Sprintf("%s/api/v1/packages/helm/%s/%s", c.artifactHub(), escape(repository), escape(name))
	code, err := c.getJSON(ctx, address, &p)
	if err != nil {
		if code == 404 {
			return p, fmt.Errorf("Artifact Hub has no chart %s/%s", repository, name)
		}
		return p, fmt.Errorf("Artifact Hub didn't answer (%v)", err)
	}
	return p, nil
}

// dockerRepo is a Docker Hub search hit or repository document.
type dockerRepo struct {
	RepoName    string `json:"repo_name"` // search results
	Namespace   string `json:"namespace"` // repository documents
	Name        string `json:"name"`
	Description string `json:"short_description"`
	Short       string `json:"description"`
	Stars       int    `json:"star_count"`
	Pulls       int64  `json:"pull_count"`
	Official    bool   `json:"is_official"`
}

func (c *Client) searchImages(ctx context.Context, query string) ([]Entry, error) {
	var answer struct {
		Results []dockerRepo `json:"results"`
	}
	address := fmt.Sprintf("%s/v2/search/repositories/?page_size=%d&query=%s", c.dockerHub(), perSource, url.QueryEscape(query))
	if _, err := c.getJSON(ctx, address, &answer); err != nil {
		return nil, err
	}
	entries := make([]Entry, 0, len(answer.Results))
	for _, r := range answer.Results {
		namespace, name := "library", r.RepoName // Official Images come back without a namespace.
		if before, after, found := strings.Cut(r.RepoName, "/"); found {
			namespace, name = before, after
		}
		if !safeSegment(namespace) || !safeSegment(name) {
			continue
		}
		entries = append(entries, Entry{
			Kind:        KindImage,
			ID:          namespace + "/" + name,
			Name:        clean(name),
			Description: clean(r.Description),
			Publisher:   publisherOf(namespace),
			Official:    r.Official || namespace == "library",
			Stars:       r.Stars,
			Pulls:       r.Pulls,
			Source:      "Docker Hub",
			Page:        dockerPage(namespace, name),
		})
	}
	return entries, nil
}

func publisherOf(namespace string) string {
	if namespace == "library" {
		return "Docker Official Image"
	}
	return clean(namespace)
}

func dockerPage(namespace, name string) string {
	if namespace == "library" {
		return fmt.Sprintf("%s/_/%s", dockerHubURL, escape(name))
	}
	return fmt.Sprintf("%s/r/%s/%s", dockerHubURL, escape(namespace), escape(name))
}

func (c *Client) image(ctx context.Context, namespace, name string) (dockerRepo, error) {
	var r dockerRepo
	address := fmt.Sprintf("%s/v2/repositories/%s/%s/", c.dockerHub(), escape(namespace), escape(name))
	code, err := c.getJSON(ctx, address, &r)
	if err != nil {
		if code == 404 {
			return r, fmt.Errorf("Docker Hub has no image %s/%s", namespace, name)
		}
		return r, fmt.Errorf("Docker Hub didn't answer (%v)", err)
	}
	return r, nil
}

// imageDigest returns the content digest a tag points at, so the draft pins the exact
// image that was looked at rather than whatever the tag means tomorrow.
func (c *Client) imageDigest(ctx context.Context, namespace, name, tag string) (string, error) {
	var t struct {
		Digest string `json:"digest"`
	}
	address := fmt.Sprintf("%s/v2/repositories/%s/%s/tags/%s", c.dockerHub(), escape(namespace), escape(name), escape(tag))
	code, err := c.getJSON(ctx, address, &t)
	if err != nil {
		if code == 404 {
			return "", fmt.Errorf("Docker Hub has no tag %q for %s/%s", tag, namespace, name)
		}
		return "", fmt.Errorf("Docker Hub didn't answer (%v)", err)
	}
	if !digestPattern.MatchString(t.Digest) {
		return "", fmt.Errorf("Docker Hub gave no usable digest for tag %q", tag)
	}
	return t.Digest, nil
}
