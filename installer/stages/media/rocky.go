// Package media is the fourth setup stage: find, verify, or download the Rocky
// Linux installer ISO that the cluster's first server is built from, and make sure
// the checkout is ready for setup to continue.
//
// It ports first_run_installer_media and tasks/scripts/manage-rocky-installer-iso.sh.
// Boot ISOs (online installers) are not supported: they lack the local package
// repository Adaetum's kickstart expects.
package media

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
)

const (
	// Release is the Rocky Linux release setup offers.
	Release = "10.2"
	// DefaultMirror is Rocky's official download tree for release 10.
	DefaultMirror = "https://download.rockylinux.org/pub/rocky/10/isos"
)

var isoName = regexp.MustCompile(`^Rocky-10(\.[0-9]+)?-(x86_64|aarch64)-(minimal|dvd1|dvd)\.iso$`)

// ISO is a supported installer image found on disk.
type ISO struct {
	Path    string
	Version string // "Rocky 10.2"
	Arch    string
	Type    string // "minimal" or "dvd"
	Size    int64
}

// Label is how an ISO is described to the operator.
func (i ISO) Label() string {
	return fmt.Sprintf("%s — %s, %s, %s, %s", filepath.Base(i.Path), i.Version, i.Arch, i.Type, GiB(i.Size))
}

// GiB formats a byte count the way the old script did.
func GiB(n int64) string { return fmt.Sprintf("%.2f GiB", float64(n)/1073741824) }

// Supported reports whether a file name is a Rocky 10 Minimal or DVD ISO.
func Supported(base string) bool { return isoName.MatchString(base) }

// Library looks for installer media in the checkout and in the places people
// download to, and fetches it from Rocky's mirror.
type Library struct {
	ExtraDirs []string
	Root      string
	Home      string
	Mirror    string
	HTTP      *http.Client
}

func (l Library) mirror() string {
	if l.Mirror != "" {
		return strings.TrimRight(l.Mirror, "/")
	}
	return DefaultMirror
}

func (l Library) client() *http.Client {
	if l.HTTP != nil {
		return l.HTTP
	}
	// No overall timeout: a DVD download takes a long time. The caller's context
	// is what cancels it.
	return &http.Client{}
}

// Discover lists supported ISOs in the checkout root, ~/Downloads and ~/Desktop.
func (l Library) Discover() []ISO {
	dirs := append([]string{l.Root}, l.ExtraDirs...)
	if l.Home != "" {
		dirs = append(dirs, filepath.Join(l.Home, "Downloads"), filepath.Join(l.Home, "Desktop"))
	}
	seen := map[string]bool{}
	var found []ISO
	for _, dir := range dirs {
		entries, err := os.ReadDir(dir)
		if err != nil {
			continue
		}
		for _, e := range entries {
			m := isoName.FindStringSubmatch(e.Name())
			if m == nil || e.IsDir() {
				continue
			}
			path := filepath.Join(dir, e.Name())
			if seen[path] {
				continue
			}
			seen[path] = true
			info, err := e.Info()
			if err != nil || !info.Mode().IsRegular() {
				continue
			}
			version := "10" + m[1]
			if m[1] == "" {
				version = "10.0"
			}
			kind := m[3]
			if kind == "dvd1" {
				kind = "dvd"
			}
			found = append(found, ISO{Path: path, Version: "Rocky " + version, Arch: m[2], Type: kind, Size: info.Size()})
		}
	}
	sort.Slice(found, func(a, b int) bool { return found[a].Path < found[b].Path })
	return found
}

// SHA256File hashes a file.
func SHA256File(path string) (string, error) {
	f, err := os.Open(path)
	if err != nil {
		return "", err
	}
	defer f.Close()
	h := sha256.New()
	if _, err := io.Copy(h, f); err != nil {
		return "", err
	}
	return hex.EncodeToString(h.Sum(nil)), nil
}

var digest = regexp.MustCompile(`^[0-9a-f]{64}$`)

// published fetches Rocky's CHECKSUM file for an image and returns its SHA-256.
func (l Library) published(ctx context.Context, arch, base string) (string, error) {
	url := fmt.Sprintf("%s/%s/%s.CHECKSUM", l.mirror(), arch, base)
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return "", err
	}
	resp, err := l.client().Do(req)
	if err != nil {
		return "", fmt.Errorf("could not fetch Rocky's checksum: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("could not fetch Rocky's checksum: %s returned %s", url, resp.Status)
	}
	body, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if err != nil {
		return "", err
	}
	for _, line := range strings.Split(string(body), "\n") {
		f := strings.Fields(strings.TrimRight(line, "\r"))
		if len(f) >= 4 && f[0] == "SHA256" && f[2] == "=" {
			if d := strings.ToLower(f[3]); digest.MatchString(d) {
				return d, nil
			}
			break
		}
	}
	return "", errors.New("Rocky's checksum file did not contain a SHA-256 digest")
}

// Verify checks an ISO against Rocky's published SHA-256.
func (l Library) Verify(ctx context.Context, path string) error {
	base := filepath.Base(path)
	m := isoName.FindStringSubmatch(base)
	if m == nil {
		return fmt.Errorf("unsupported Rocky installer ISO: %s", path)
	}
	if _, err := os.Stat(path); err != nil {
		return fmt.Errorf("installer ISO does not exist: %s", path)
	}
	want, err := l.published(ctx, m[2], base)
	if err != nil {
		return err
	}
	got, err := SHA256File(path)
	if err != nil {
		return err
	}
	if got != want {
		return fmt.Errorf("installer ISO does not match Rocky's published SHA-256: %s", path)
	}
	return nil
}

// Adopt copies an ISO into the checkout root. It never overwrites a same-named
// file there.
func (l Library) Adopt(src string) (string, error) {
	base := filepath.Base(src)
	if !Supported(base) {
		return "", fmt.Errorf("unsupported Rocky installer ISO: %s", src)
	}
	dest := filepath.Join(l.Root, base)
	if abs, err := filepath.Abs(src); err == nil && abs == dest {
		return dest, nil
	}
	if _, err := os.Stat(dest); err == nil {
		return "", fmt.Errorf("a same-named installer ISO already exists in the checkout: %s", dest)
	}
	in, err := os.Open(src)
	if err != nil {
		return "", err
	}
	defer in.Close()
	out, err := os.OpenFile(dest, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o644)
	if err != nil {
		return "", err
	}
	if _, err := io.Copy(out, in); err != nil {
		out.Close()
		os.Remove(dest)
		return "", err
	}
	return dest, out.Close()
}

// Name returns the file name of a release/arch/type, for example
// Rocky-10.2-x86_64-minimal.iso (the DVD image is called dvd1 upstream).
func Name(release, arch, kind string) string {
	if kind == "dvd" {
		kind = "dvd1"
	}
	return fmt.Sprintf("Rocky-%s-%s-%s.iso", release, arch, kind)
}

// Download fetches an installer into the checkout root and verifies it against
// Rocky's published SHA-256. An interrupted download is resumed from its
// .partial file; a finished or already-present image is reused after it verifies.
// A download that fails verification keeps its partial file so a rerun can resume.
func (l Library) Download(ctx context.Context, arch, kind, release string, progress func(done, total int64)) (string, error) {
	if arch != "x86_64" && arch != "aarch64" {
		return "", fmt.Errorf("unsupported Rocky installer architecture: %s", arch)
	}
	if kind != "minimal" && kind != "dvd" {
		return "", fmt.Errorf("unsupported Rocky installer image type: %s", kind)
	}
	base := Name(release, arch, kind)
	output := filepath.Join(l.Root, base)
	partial := output + ".partial"
	want, err := l.published(ctx, arch, base)
	if err != nil {
		return "", err
	}

	if _, err := os.Stat(output); err == nil {
		got, err := SHA256File(output)
		if err != nil {
			return "", err
		}
		if got != want {
			return "", fmt.Errorf("existing installer ISO does not match Rocky's published SHA-256: %s", output)
		}
		return output, nil
	}
	if _, err := os.Stat(partial); err == nil {
		if got, err := SHA256File(partial); err == nil && got == want {
			return output, os.Rename(partial, output)
		}
	}

	if err := l.fetch(ctx, fmt.Sprintf("%s/%s/%s", l.mirror(), arch, base), partial, progress); err != nil {
		return "", err
	}
	got, err := SHA256File(partial)
	if err != nil {
		return "", err
	}
	if got != want {
		return "", fmt.Errorf("the downloaded ISO did not match Rocky's published SHA-256; the partial file was kept so a rerun can resume it: %s", partial)
	}
	return output, os.Rename(partial, output)
}

type counter struct {
	w        io.Writer
	done     int64
	total    int64
	progress func(done, total int64)
}

func (c *counter) Write(p []byte) (int, error) {
	n, err := c.w.Write(p)
	c.done += int64(n)
	if c.progress != nil {
		c.progress(c.done, c.total)
	}
	return n, err
}

// fetch downloads url to path, resuming from the file's current size when the
// server supports ranges and starting over when it does not.
func (l Library) fetch(ctx context.Context, url, path string, progress func(done, total int64)) error {
	var have int64
	if info, err := os.Stat(path); err == nil {
		have = info.Size()
	}
	for attempt := 0; attempt < 2; attempt++ {
		req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
		if err != nil {
			return err
		}
		if have > 0 {
			req.Header.Set("Range", fmt.Sprintf("bytes=%d-", have))
		}
		resp, err := l.client().Do(req)
		if err != nil {
			return fmt.Errorf("download failed: %w", err)
		}

		flags := os.O_WRONLY | os.O_CREATE
		switch resp.StatusCode {
		case http.StatusPartialContent:
			flags |= os.O_APPEND
		case http.StatusOK:
			// The server ignored the range: start the file over.
			flags |= os.O_TRUNC
			have = 0
		case http.StatusRequestedRangeNotSatisfiable:
			resp.Body.Close()
			// The partial file is not a prefix of the image; discard it and retry once.
			os.Remove(path)
			have = 0
			continue
		default:
			resp.Body.Close()
			return fmt.Errorf("download failed: %s returned %s", url, resp.Status)
		}

		f, err := os.OpenFile(path, flags, 0o644)
		if err != nil {
			resp.Body.Close()
			return err
		}
		c := &counter{w: f, done: have, total: have + resp.ContentLength, progress: progress}
		if resp.ContentLength < 0 {
			c.total = 0
		}
		_, copyErr := io.Copy(c, resp.Body)
		resp.Body.Close()
		if err := f.Close(); copyErr == nil {
			copyErr = err
		}
		if copyErr != nil {
			return fmt.Errorf("download interrupted (run setup again to resume): %w", copyErr)
		}
		return nil
	}
	return errors.New("download failed: the server would not serve the image")
}
