package artifacts

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/Adaetum/Adaetum/installer/sys"
)

// r2 uploads to the cluster's Cloudflare R2 bucket through rclone. Credentials
// reach rclone as environment variables for the one command, never as arguments.
type r2 struct {
	run                           sys.Runner
	dir                           string
	endpoint, bucket, key, secret string
}

// backupSetup always writes a new, extensionless recovery object. The bootstrap
// Worker's artifact route cannot serve extensionless objects; recovery is through
// the operator's Cloudflare account, not the installer shared token.
func (r r2) backupSetup(ctx context.Context, local, key string) error {
	want, err := os.ReadFile(local)
	if err != nil {
		return fmt.Errorf("read setup recovery data: %w", err)
	}
	res, err := r.run.RunEnv(ctx, r.dir, r.env(), "", "rclone", "copyto", local, r.remote(key))
	if err != nil || !res.OK() {
		return fmt.Errorf("upload setup recovery data failed")
	}
	res, err = r.run.RunEnv(ctx, r.dir, r.env(), "", "rclone", "cat", r.remote(key))
	if err != nil || !res.OK() || !bytes.Equal(want, []byte(res.Stdout)) {
		return fmt.Errorf("setup recovery upload could not be verified")
	}
	return nil
}

func (r r2) env() []string {
	return []string{
		"RCLONE_CONFIG_CLUSTER_R2_TYPE=s3", "RCLONE_CONFIG_CLUSTER_R2_PROVIDER=Cloudflare",
		"RCLONE_CONFIG_CLUSTER_R2_ACCESS_KEY_ID=" + r.key, "RCLONE_CONFIG_CLUSTER_R2_SECRET_ACCESS_KEY=" + r.secret,
		"RCLONE_CONFIG_CLUSTER_R2_ENDPOINT=" + r.endpoint, "RCLONE_CONFIG_CLUSTER_R2_NO_CHECK_BUCKET=true",
	}
}

func (r r2) remote(key string) string { return "cluster_r2:" + r.bucket + "/" + key }

// remoteSize returns the size of an object, or false when it is not there.
func (r r2) remoteSize(ctx context.Context, key string) (int64, bool) {
	res, err := r.run.RunEnv(ctx, r.dir, r.env(), "", "rclone", "lsjson", r.remote(key), "--files-only")
	if err != nil || !res.OK() {
		return 0, false
	}
	var items []struct {
		Size *int64 `json:"Size"`
	}
	if json.Unmarshal([]byte(res.Stdout), &items) != nil || len(items) == 0 || items[0].Size == nil {
		return 0, false
	}
	return *items[0].Size, true
}

// upload copies a file unless the bucket already holds one of the same size,
// which is how a rerun avoids re-sending a multi-gigabyte installer.
func (r r2) upload(ctx context.Context, local, key string) (string, error) {
	info, err := os.Stat(local)
	if err != nil {
		return "", err
	}
	if size, ok := r.remoteSize(ctx, key); ok && size == info.Size() {
		return "skipped", nil
	}
	res, err := r.run.RunEnv(ctx, r.dir, r.env(), "", "rclone", "copyto", local, r.remote(key))
	if err != nil {
		return "", err
	}
	if !res.OK() {
		return "", fmt.Errorf("upload of %s failed: %s", filepath.Base(local), strings.TrimSpace(res.Stderr))
	}
	return "uploaded", nil
}

// defaultKey is where an installer with no rendered GOLDEN_ISO_KEY is stored.
func defaultKey(file string) string {
	switch {
	case strings.HasPrefix(file, "Rocky-10") && strings.HasSuffix(file, ".iso"):
		return "rocky10/" + file
	case strings.HasPrefix(file, "Rocky-9") && strings.HasSuffix(file, ".iso"):
		return "rocky9/" + file
	case (strings.HasPrefix(file, "ubuntu-24.04") || strings.HasPrefix(file, "Ubuntu-24.04")) && strings.HasSuffix(file, ".iso"):
		return "ubuntu2404/" + file
	}
	return file
}

// goldenKeys reads the "# GOLDEN_ISO_KEY=" lines the kickstart templates declare:
// where each installer image belongs in the bucket.
func goldenKeys(root string) []string {
	files, _ := filepath.Glob(filepath.Join(root, "dist", "ks-templates", "*.ks"))
	seen := map[string]bool{}
	for _, f := range files {
		data, err := os.ReadFile(f)
		if err != nil {
			continue
		}
		for _, line := range strings.Split(string(data), "\n") {
			if v, ok := strings.CutPrefix(strings.TrimRight(line, "\r"), "# GOLDEN_ISO_KEY="); ok && strings.TrimSpace(v) != "" {
				seen[strings.TrimSpace(v)] = true
			}
		}
	}
	keys := make([]string, 0, len(seen))
	for k := range seen {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}

func rootISOs(root string) []string {
	files, _ := filepath.Glob(filepath.Join(root, "*.iso"))
	sort.Strings(files)
	return files
}
