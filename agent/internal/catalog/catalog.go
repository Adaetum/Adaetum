// Package catalog is the app explorer: it searches PUBLIC app sources (Helm charts on
// Artifact Hub, container images on Docker Hub) and turns a chosen result into a
// PREVIEW: what the source offers, who publishes it, what to know before using it,
// and a STARTING-POINT draft of files.
//
// WHAT THIS IS NOT. Nothing here installs anything. Installing an app in Adaetum is a
// change to the private recovery repository (pods/<namespace>/<app>.app.yaml plus its
// values or manifests) that Argo CD then deploys, so the honest "dry run" is the set
// of files a person can read, with a plain-English summary of what they would run.
//
// THE DRAFT IS NOT THE FINAL LAYOUT. Adaetum lays apps out its own way under pods/
// (a category folder, explicit settings, OpenBao secrets, routes, a README), so a
// found chart or image is converted to that layout before it is added. That
// conversion is an iterative job that needs a workspace and validation, so it is not
// done here; this package supplies the facts and a rough start.
//
// ONE BACKEND, TWO FRONT DOORS. The Explore page and the assistant's search tools both
// call these two HTTP routes, so a person and the assistant always see the same
// results and neither can reach a source the other cannot.
//
// WHAT IS TRUSTED. The agent only ever talks to the fixed hosts below (the two hubs, plus an icon set, Docker Hub and GitHub for logos); no address
// comes from a person or from the assistant. Everything a source returns is untrusted
// third-party text: descriptions are shortened and stripped of control characters,
// values that end up in generated YAML are validated and quoted, and the assistant is
// told to treat results as data, never as instructions.
package catalog

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"
)

// Kind says what a result is.
type Kind string

const (
	KindChart Kind = "chart" // a Helm chart (preferred: it is made to be configured)
	KindImage Kind = "image" // a container image (fallback: the agent must guess how to run it)
)

// The only places the agent will look (the icon set is for logos only). Adding one is a reviewed change here.
const (
	artifactHubURL = "https://artifacthub.io"
	dockerHubURL   = "https://hub.docker.com"
	// The community dashboard-icons set (the one Homepage uses), for app logos only.
	iconsURL = "https://cdn.jsdelivr.net/gh/homarr-labs/dashboard-icons/png"
)

const (
	perSource       = 8
	maxResponseSize = 2 << 20
	requestTimeout  = 12 * time.Second
	maxText         = 240
)

// Entry is one search result, reduced to what helps a person choose.
type Entry struct {
	Kind        Kind   `json:"kind"`
	ID          string `json:"id"` // chart: "<repository>/<chart>"; image: "<namespace>/<name>"
	Name        string `json:"name"`
	Description string `json:"description"`
	Publisher   string `json:"publisher"`
	Version     string `json:"version,omitempty"`
	AppVersion  string `json:"appVersion,omitempty"`
	// Verified is a chart repository whose publisher Artifact Hub has verified;
	// Official is a project's own listing (Artifact Hub) or a Docker Official Image.
	Verified   bool  `json:"verified"`
	Official   bool  `json:"official"`
	Signed     bool  `json:"signed,omitempty"`
	Deprecated bool  `json:"deprecated,omitempty"`
	Stars      int   `json:"stars"`
	Pulls      int64 `json:"pulls,omitempty"`
	// Logo is an Artifact Hub image id (never a URL). The page asks the agent for the
	// picture, so the browser never contacts a third party.
	Logo   string `json:"logo,omitempty"`
	Source string `json:"source"` // "Artifact Hub" or "Docker Hub"
	Page   string `json:"page"`   // where to read more
}

// Results is what a search returns. A source that did not answer is named in Notes
// instead of failing the whole search.
type Results struct {
	Query  string   `json:"query"`
	Charts []Entry  `json:"charts"`
	Images []Entry  `json:"images"`
	Notes  []string `json:"notes,omitempty"`
}

// Client searches the public sources. The base URLs are fields only so tests can
// point at a local server; production uses the constants above.
type Client struct {
	HTTP           *http.Client
	ArtifactHubURL string
	DockerHubURL   string
	IconsURL       string
	GitHubURL      string
	// AvatarHost is the only host a GitHub avatar may come from.
	AvatarHost string
	// DockerLogoHost is the only host a Docker Hub logo may come from.
	DockerLogoHost string
}

func (c *Client) icons() string {
	if c.IconsURL != "" {
		return c.IconsURL
	}
	return iconsURL
}

func (c *Client) artifactHub() string {
	if c.ArtifactHubURL != "" {
		return c.ArtifactHubURL
	}
	return artifactHubURL
}

func (c *Client) dockerHub() string {
	if c.DockerHubURL != "" {
		return c.DockerHubURL
	}
	return dockerHubURL
}

func (c *Client) client() *http.Client {
	if c.HTTP != nil {
		return c.HTTP
	}
	return &http.Client{
		Timeout: requestTimeout,
		// A source may not send the agent somewhere else.
		CheckRedirect: func(req *http.Request, via []*http.Request) error {
			if len(via) >= 3 || req.URL.Host != via[0].URL.Host {
				return errors.New("redirect refused")
			}
			return nil
		},
	}
}

// getJSON fetches one document from a source, capped in size.
func (c *Client) getJSON(ctx context.Context, address string, into any) (int, error) {
	return c.getJSONHeaders(ctx, address, nil, into)
}

// getJSONWith is getJSON with extra request headers, returning only the error.
func (c *Client) getJSONWith(ctx context.Context, address string, headers map[string]string, into any) error {
	_, err := c.getJSONHeaders(ctx, address, headers, into)
	return err
}

func (c *Client) getJSONHeaders(ctx context.Context, address string, headers map[string]string, into any) (int, error) {
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, address, nil)
	if err != nil {
		return 0, err
	}
	request.Header.Set("Accept", "application/json")
	for key, value := range headers {
		request.Header.Set(key, value)
	}
	request.Header.Set("User-Agent", "adaetum-agent-catalog")
	response, err := c.client().Do(request)
	if err != nil {
		return 0, err
	}
	defer response.Body.Close()
	data, err := io.ReadAll(io.LimitReader(response.Body, maxResponseSize))
	if err != nil {
		return response.StatusCode, err
	}
	if response.StatusCode/100 != 2 {
		return response.StatusCode, fmt.Errorf("answered %d", response.StatusCode)
	}
	if err := json.Unmarshal(data, into); err != nil {
		return response.StatusCode, errors.New("sent something unreadable")
	}
	return response.StatusCode, nil
}

// Search looks in every source at once. A query is plain words, 2 to 80 characters.
func (c *Client) Search(ctx context.Context, query string) (Results, error) {
	query = strings.Join(strings.Fields(query), " ")
	if len(query) < 2 || len(query) > 80 || hasControl(query) {
		return Results{}, errors.New("type 2 to 80 characters to search for")
	}
	results := Results{Query: query, Charts: []Entry{}, Images: []Entry{}}
	var wg sync.WaitGroup
	var mu sync.Mutex
	run := func(name string, search func() ([]Entry, error), into *[]Entry) {
		defer wg.Done()
		entries, err := search()
		mu.Lock()
		defer mu.Unlock()
		if err != nil {
			results.Notes = append(results.Notes, fmt.Sprintf("%s didn't answer (%v), so its results are missing.", name, err))
			return
		}
		*into = entries
	}
	wg.Add(2)
	go run("Artifact Hub", func() ([]Entry, error) { return c.searchCharts(ctx, query) }, &results.Charts)
	go run("Docker Hub", func() ([]Entry, error) { return c.searchImages(ctx, query) }, &results.Images)
	wg.Wait()
	return results, nil
}

// clean shortens third-party text and removes anything that is not plain text.
func clean(text string) string {
	var b strings.Builder
	for _, r := range text {
		switch {
		case r == '\n' || r == '\t':
			b.WriteRune(' ')
		case r < 0x20 || r == 0x7f:
		default:
			b.WriteRune(r)
		}
	}
	out := strings.Join(strings.Fields(b.String()), " ")
	if runes := []rune(out); len(runes) > maxText {
		out = string(runes[:maxText]) + "…"
	}
	return out
}

func hasControl(text string) bool {
	for _, r := range text {
		if r < 0x20 || r == 0x7f {
			return true
		}
	}
	return false
}

func escape(segment string) string { return url.PathEscape(segment) }
