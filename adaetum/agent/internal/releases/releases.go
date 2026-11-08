// Package releases discovers Adaetum's published, immutable agent artifacts.
// Discovery is read-only; choosing or applying an update is a separate action.
package releases

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"
)

const repository = "Adaetum/Adaetum"

var releaseTag = regexp.MustCompile(`^adaetum-v(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)(-beta\.(0|[1-9][0-9]*))?$`)
var commitPattern = regexp.MustCompile(`^[0-9a-f]{40}$`)
var hashPattern = regexp.MustCompile(`^[0-9a-f]{64}$`)

type Binary struct {
	URL    string `json:"url"`
	SHA256 string `json:"sha256"`
}
type Manifest struct {
	Version      string            `json:"version"`
	SourceCommit string            `json:"source_commit"`
	Images       map[string]string `json:"images"`
	Binaries     map[string]Binary `json:"binaries"`
}
type Check struct {
	Current         string   `json:"current"`
	UpdateAvailable bool     `json:"updateAvailable"`
	ManifestHash    string   `json:"manifestHash"`
	Release         Manifest `json:"release"`
}

type Client struct {
	HTTP                  *http.Client
	APIBase, DownloadBase string
}

// NewClient follows only GitHub's release-asset hosts, so a redirect cannot
// turn a release check into a request to an internal cluster service.
func NewClient() Client {
	return Client{APIBase: "https://api.github.com", DownloadBase: "https://github.com", HTTP: &http.Client{Timeout: 20 * time.Second, CheckRedirect: func(req *http.Request, via []*http.Request) error {
		if len(via) >= 5 || req.URL.Scheme != "https" {
			return errors.New("unsafe release redirect")
		}
		switch strings.ToLower(req.URL.Hostname()) {
		case "github.com", "objects.githubusercontent.com", "release-assets.githubusercontent.com":
			return nil
		default:
			return errors.New("untrusted release redirect")
		}
	}}}
}

func version(value string) ([5]uint64, bool) {
	var out [5]uint64
	m := releaseTag.FindStringSubmatch("adaetum-v" + value)
	if m == nil {
		return out, false
	}
	for i := 0; i < 3; i++ {
		n, err := strconv.ParseUint(m[i+1], 10, 32)
		if err != nil {
			return out, false
		}
		out[i] = n
	}
	if m[4] == "" {
		out[3] = 1
	} else {
		n, err := strconv.ParseUint(m[5], 10, 64)
		if err != nil {
			return out, false
		}
		out[4] = n
	}
	return out, true
}
func greater(a, b [5]uint64) bool {
	for i := range a {
		if a[i] != b[i] {
			return a[i] > b[i]
		}
	}
	return false
}
func (c Client) getJSON(ctx context.Context, address string, result any) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, address, nil)
	if err != nil {
		return err
	}
	req.Header.Set("Accept", "application/vnd.github+json")
	req.Header.Set("User-Agent", "adaetum-agent-release-check")
	resp, err := c.HTTP.Do(req)
	if err != nil {
		return fmt.Errorf("release lookup: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("release lookup returned HTTP %d", resp.StatusCode)
	}
	if err := json.NewDecoder(io.LimitReader(resp.Body, 2<<20)).Decode(result); err != nil {
		return fmt.Errorf("decode release data: %w", err)
	}
	return nil
}

// Latest finds the greatest stable agent release, then validates every address and
// checksum before returning it. It never follows a URL from an untrusted response.
func (c Client) Latest(ctx context.Context, current, channel string) (Check, error) {
	if channel != "stable" && channel != "beta" {
		return Check{}, errors.New("unknown release channel")
	}
	var entries []struct {
		Tag        string `json:"tag_name"`
		Draft      bool   `json:"draft"`
		Prerelease bool   `json:"prerelease"`
		Assets     []struct {
			Name string `json:"name"`
		} `json:"assets"`
	}
	if err := c.getJSON(ctx, strings.TrimRight(c.APIBase, "/")+"/repos/"+repository+"/releases?per_page=100", &entries); err != nil {
		return Check{}, err
	}
	type candidate struct {
		tag, ver string
		n        [5]uint64
	}
	var choices []candidate
	for _, entry := range entries {
		if entry.Draft {
			continue
		}
		m := releaseTag.FindStringSubmatch(entry.Tag)
		if m == nil || (channel == "beta") != (m[4] != "") || entry.Prerelease != (m[4] != "") {
			continue
		}
		found := false
		for _, asset := range entry.Assets {
			if asset.Name == "release.json" {
				found = true
				break
			}
		}
		if !found {
			continue
		}
		v := strings.TrimPrefix(entry.Tag, "adaetum-v")
		n, ok := version(v)
		if ok {
			choices = append(choices, candidate{entry.Tag, v, n})
		}
	}
	if len(choices) == 0 {
		return Check{}, errors.New("no Adaetum agent release for the selected channel with a manifest was found")
	}
	sort.Slice(choices, func(i, j int) bool { return greater(choices[i].n, choices[j].n) })
	selected := choices[0]
	address := strings.TrimRight(c.DownloadBase, "/") + "/" + repository + "/releases/download/" + url.PathEscape(selected.tag) + "/release.json"
	var manifest Manifest
	if err := c.getJSON(ctx, address, &manifest); err != nil {
		return Check{}, err
	}
	if err := validate(manifest, selected.ver, selected.tag); err != nil {
		return Check{}, err
	}
	previous, ok := version(strings.TrimPrefix(current, "v"))
	encoded, err := json.Marshal(manifest)
	if err != nil {
		return Check{}, err
	}
	hash := sha256.Sum256(encoded)
	return Check{Current: current, UpdateAvailable: ok && greater(selected.n, previous), ManifestHash: hex.EncodeToString(hash[:]), Release: manifest}, nil
}
func validate(m Manifest, expected, tag string) error {
	if m.Version != expected || !commitPattern.MatchString(m.SourceCommit) {
		return errors.New("release manifest has an invalid version or source commit")
	}
	for _, component := range []string{"agent", "auth", "embed"} {
		prefix := "ghcr.io/adaetum/adaetum-" + component + "@sha256:"
		image := m.Images[component]
		if !strings.HasPrefix(image, prefix) || !hashPattern.MatchString(strings.TrimPrefix(image, prefix)) {
			return fmt.Errorf("release manifest has an invalid %s image", component)
		}
	}
	for _, component := range []string{"agent", "auth"} {
		for _, arch := range []string{"amd64", "arm64"} {
			name := "adaetum-" + component + "-linux-" + arch
			binary, ok := m.Binaries[name]
			expectedURL := "https://github.com/" + repository + "/releases/download/" + tag + "/" + name
			if !ok || binary.URL != expectedURL || !hashPattern.MatchString(binary.SHA256) {
				return fmt.Errorf("release manifest has an invalid %s artifact", name)
			}
		}
	}
	return nil
}
