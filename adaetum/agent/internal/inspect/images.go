package inspect

import (
	"context"
	"encoding/json"
	"fmt"
	"strconv"
	"strings"
	"time"
)

const (
	rke2Crictl = "/var/lib/rancher/rke2/bin/crictl"
	// RKE2 runs its own containerd. The endpoint is passed explicitly because
	// sudo drops the environment variable crictl would otherwise read.
	rke2ContainerdSocket = "unix:///run/k3s/containerd/containerd.sock"

	// Exported so the cleanup package, which is the only code that removes images,
	// uses the same runtime binary and socket the inspector reads from.
	RKE2CrictlPath   = rke2Crictl
	ContainerdSocket = rke2ContainerdSocket

	// containerdContentDir holds the compressed copies of every image layer.
	// containerd keeps these next to the unpacked layers it runs containers
	// from, and the kubelet's image figure does not include them.
	containerdContentDir = "/var/lib/rancher/rke2/agent/containerd/io.containerd.content.v1.content"

	// measurementTTL spares the host a directory walk on every refresh: the
	// figure moves slowly, and the UI polls every 30 seconds.
	measurementTTL = 10 * time.Minute
)

// measurableDirectories is the complete set of directories the agent will size.
// Sizing is read-only, but restricting the paths keeps this package from
// becoming a way to probe arbitrary directories.
var measurableDirectories = map[string]bool{
	containerdContentDir: true,
}

// ContainerImages describes the container images stored on this host and how
// many are not used by any container. It is a fact about the machine the agent
// inspects, not about the whole cluster: only that machine's runtime can answer.
//
// Sizes are what the runtime reports, which is the compressed download size and
// understates the space images occupy once unpacked. They are useful as a
// proportion (what share of images is unused), not as an amount of disk space.
type ContainerImages struct {
	Total       int    `json:"total"`
	Unused      int    `json:"unused"`
	TotalBytes  uint64 `json:"totalBytes"`
	UnusedBytes uint64 `json:"unusedBytes"`
	// ContentBytes is the disk the compressed layer copies occupy. It is
	// measured, not reported by the runtime, and is zero when the measurement
	// failed. It is not part of the kubelet's image-store figure.
	ContentBytes uint64 `json:"contentBytes,omitempty"`
}

// flexUint64 reads a byte count that crictl prints as either a JSON number or
// a quoted string, depending on version.
type flexUint64 uint64

func (value *flexUint64) UnmarshalJSON(data []byte) error {
	text := string(data)
	if len(text) > 1 && text[0] == '"' {
		text = text[1 : len(text)-1]
	}
	if text == "" || text == "null" {
		*value = 0
		return nil
	}
	parsed, err := strconv.ParseUint(text, 10, 64)
	if err != nil {
		return fmt.Errorf("not a byte count: %s", string(data))
	}
	*value = flexUint64(parsed)
	return nil
}

// collectImages reads the host's images and containers. Both are read-only
// listings. It returns nil when the runtime cannot be queried, with the reason
// recorded by the caller, so a host without RKE2 simply has no image section.
func (i *Inspector) collectImages(ctx context.Context) (*ContainerImages, error) {
	imagesJSON, err := i.crictl(ctx, "images")
	if err != nil {
		return nil, err
	}
	containersJSON, err := i.crictl(ctx, "ps", "-a")
	if err != nil {
		return nil, err
	}
	return parseContainerImages([]byte(imagesJSON), []byte(containersJSON))
}

func (i *Inspector) crictl(ctx context.Context, subcommand string, extra ...string) (string, error) {
	args := append([]string{"--runtime-endpoint", rke2ContainerdSocket, subcommand}, extra...)
	args = append(args, "-o", "json")
	out, err := i.Run(ctx, rke2Crictl, args...)
	if err != nil {
		return "", fmt.Errorf("crictl %s: %w", subcommand, err)
	}
	return out, nil
}

// parseContainerImages counts images no container refers to. An image counts as
// used if a container (running or stopped) names it by ID, digest, or tag, which
// is the same rule `crictl rmi --prune` uses to decide what it may remove.
//
// Pod sandbox ("pause") images are not visible here, so they count as unused.
// They are well under a megabyte, so the effect on the totals is negligible.
func parseContainerImages(imagesJSON, containersJSON []byte) (*ContainerImages, error) {
	var images struct {
		Images []struct {
			ID          string     `json:"id"`
			RepoTags    []string   `json:"repoTags"`
			RepoDigests []string   `json:"repoDigests"`
			Size        flexUint64 `json:"size"`
		} `json:"images"`
	}
	if err := json.Unmarshal(imagesJSON, &images); err != nil {
		return nil, fmt.Errorf("images: %w", err)
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
	if err := json.Unmarshal(containersJSON, &containers); err != nil {
		return nil, fmt.Errorf("containers: %w", err)
	}

	inUse := map[string]bool{}
	for _, container := range containers.Containers {
		for _, reference := range []string{container.ImageRef, container.Image.Image, container.Image.UserSpecifiedImage} {
			if reference != "" {
				inUse[reference] = true
			}
		}
	}

	summary := &ContainerImages{Total: len(images.Images)}
	for _, image := range images.Images {
		summary.TotalBytes += uint64(image.Size)
		used := inUse[image.ID]
		for _, name := range append(append([]string{}, image.RepoDigests...), image.RepoTags...) {
			used = used || inUse[name]
		}
		if !used {
			summary.Unused++
			summary.UnusedBytes += uint64(image.Size)
		}
	}
	return summary, nil
}

// containerdContentBytes sizes the compressed layer store, reusing a recent
// measurement when there is one.
func (i *Inspector) containerdContentBytes(ctx context.Context) (uint64, error) {
	i.measureMutex.Lock()
	defer i.measureMutex.Unlock()

	if i.measuredAt != (time.Time{}) && i.Now().Sub(i.measuredAt) < measurementTTL {
		return i.measuredBytes, nil
	}
	bytes, err := i.directoryBytes(ctx, containerdContentDir)
	if err != nil {
		return 0, err
	}
	i.measuredBytes, i.measuredAt = bytes, i.Now()
	return bytes, nil
}

// directoryBytes returns the disk space a directory tree occupies. Only the
// directories in measurableDirectories are allowed.
func (i *Inspector) directoryBytes(ctx context.Context, path string) (uint64, error) {
	if !measurableDirectories[path] {
		return 0, fmt.Errorf("measuring %q is not permitted", path)
	}
	out, err := i.Run(ctx, "du", "-sB1", path)
	if err != nil {
		return 0, fmt.Errorf("du %s: %w", path, err)
	}
	fields := strings.Fields(out)
	if len(fields) == 0 {
		return 0, fmt.Errorf("du %s: empty output", path)
	}
	bytes, err := strconv.ParseUint(fields[0], 10, 64)
	if err != nil {
		return 0, fmt.Errorf("du %s: %w", path, err)
	}
	return bytes, nil
}
