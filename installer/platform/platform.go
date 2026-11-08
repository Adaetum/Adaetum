// Package platform drives the repository's platform.yaml scripts: read the public
// profile, propose a new one, and render the tracked outputs from it. The scripts
// stay in tasks/scripts until they are ported; this is the one place that knows
// how to call them, and it never goes through the Taskfile.
package platform

import (
	"context"
	"errors"
	"fmt"
	"os"
	"runtime"
	"strings"

	"github.com/Adaetum/Adaetum/installer/sys"
)

// Profile is the public part of platform.yaml, as `configure-platform-profile.py
// --show` reports it.
type Profile map[string]string

// Placeholder reports whether the profile still holds the template's stand-in
// domain or tailnet, meaning the operator has not configured it yet.
func (p Profile) Placeholder() bool {
	return strings.HasSuffix(p["domain"], ".invalid") || p["overlay_domain"] == "example-tailnet.ts.net" ||
		p["domain"] == "" || p["overlay_domain"] == ""
}

// Scripts runs the platform scripts for the checkout at Dir.
type Scripts struct {
	Run sys.Runner
	Dir string
}

func python() string {
	if runtime.GOOS == "windows" {
		return "python"
	}
	return "python3"
}

func (s Scripts) py(ctx context.Context, args ...string) (sys.Result, error) {
	return s.Run.Run(ctx, s.Dir, python(), args...)
}

// Show reads the current profile.
func (s Scripts) Show(ctx context.Context) (Profile, error) {
	res, err := s.py(ctx, "./tasks/scripts/configure-platform-profile.py", "--show")
	if err != nil {
		return nil, fmt.Errorf("python3 is required to read platform.yaml: %w", err)
	}
	if !res.OK() {
		return nil, fmt.Errorf("could not read platform.yaml: %s", strings.TrimSpace(res.Stderr+res.Stdout))
	}
	p := Profile{}
	for _, line := range strings.Split(res.Stdout, "\n") {
		if k, v, ok := strings.Cut(strings.TrimRight(line, "\r"), "="); ok {
			p[k] = v
		}
	}
	return p, nil
}

// Proposal is the public configuration an operator reviews before it is written.
type Proposal struct {
	Domain          string
	LocalDomain     string
	OverlayDomain   string
	OverlayTag      string
	RepositoryOwner string
	RepositoryName  string
	BootstrapURL    string
	R2Bucket        string
}

// Standard fills everything that Adaetum chooses for the operator, from the two
// values they do choose: the public domain and the tailnet domain.
func Standard(domain, overlayDomain string) Proposal {
	local := domain
	// The private namespace replaces the public suffix; appending ".local" would
	// turn example.test into the unintended example.test.local.
	if i := strings.LastIndex(domain, "."); i >= 0 {
		local = domain[:i]
	}
	return Proposal{
		Domain: domain, LocalDomain: local + ".local", OverlayDomain: overlayDomain, OverlayTag: "tag:cluster",
		RepositoryOwner: "gitea-admin", RepositoryName: "cluster",
		BootstrapURL: "https://bootstrap." + domain, R2Bucket: "iso",
	}
}

// Propose writes the proposed profile to output without touching platform.yaml.
func (s Scripts) Propose(ctx context.Context, output string, p Proposal) error {
	res, err := s.py(ctx, "./tasks/scripts/configure-platform-profile.py", "--profile", "./platform.yaml", "--output", output,
		"--domain", p.Domain, "--local-domain", p.LocalDomain, "--overlay-domain", p.OverlayDomain,
		"--overlay-cluster-tag", p.OverlayTag, "--repository-owner", p.RepositoryOwner,
		"--repository-name", p.RepositoryName, "--repository-branch", "main",
		"--bootstrap-base-url", p.BootstrapURL, "--r2-bucket", p.R2Bucket)
	if err != nil {
		return err
	}
	if !res.OK() {
		return fmt.Errorf("the profile was not accepted: %s", strings.TrimSpace(res.Stderr+res.Stdout))
	}
	return nil
}

// Render validates platform.yaml and regenerates every tracked output from it, the
// same two steps `task platform:render` runs.
func (s Scripts) Render(ctx context.Context) error {
	res, err := s.py(ctx, "./tasks/scripts/validate-platform-profile.py", "--profile", "./platform.yaml")
	if err != nil {
		return err
	}
	if !res.OK() {
		return fmt.Errorf("platform.yaml is not valid: %s", strings.TrimSpace(res.Stderr+res.Stdout))
	}
	res, err = s.py(ctx, "./tasks/scripts/render-platform-profile.py", "--profile", "./platform.yaml",
		"--output-env", "./dist/platform.env", "--config-file", "./pods/cluster-config/cluster-config.env", "--render-pods")
	if err != nil {
		return err
	}
	if !res.OK() {
		return fmt.Errorf("could not render platform.yaml: %s", strings.TrimSpace(res.Stderr+res.Stdout))
	}
	return nil
}

// ErrNotResumable says the worktree holds changes that are not exactly a rendered
// profile waiting to be committed.
var ErrNotResumable = errors.New("the tracked worktree holds changes that are not a rendered profile")

// resumableCheck proves that what is in the worktree is exactly a reviewed
// profile, its generated outputs, and nothing else. A failed commit hook leaves
// that state behind; resuming it is safe, but absorbing an unrelated edit into the
// recovery commit is not. It is the original check, run by Python because the
// renderer's list of owned outputs lives there.
const resumableCheck = `
import importlib.util, subprocess, sys
from pathlib import Path
root = Path(sys.argv[1]).resolve()
spec = importlib.util.spec_from_file_location("render_platform_profile", root / "tasks/scripts/render-platform-profile.py")
if spec is None or spec.loader is None:
    raise SystemExit(1)
mod = importlib.util.module_from_spec(spec)
spec.loader.exec_module(mod)
renderer = mod.load_pods_renderer()
profile = mod.load_profile(root / "platform.yaml")
mod.validate_profile(profile)
config = mod.config_from_profile(profile)
expected = "".join(f"{k}={config[k]}\n" for k in renderer.ENV_KEYS)
if (root / "pods/cluster-config/cluster-config.env").read_text(encoding="utf-8") != expected:
    raise SystemExit(1)
if renderer.render_templates(config, check=True) or renderer.render_app_configs(config, check=True):
    raise SystemExit(1)
allowed = {"platform.yaml", "pods/cluster-config/cluster-config.env"}
allowed.update(t for _x, t in renderer.TEMPLATE_TARGETS)
allowed.update(t for t, _n, _k in renderer.APP_CONFIG_TARGETS)
def names(*args):
    return set(subprocess.run(["git", *args], cwd=root, check=True, capture_output=True, text=True).stdout.splitlines())
staged = names("diff", "--cached", "--name-only", "HEAD", "--")
unstaged = names("diff", "--name-only", "--")
if not staged or staged - allowed or unstaged & allowed:
    raise SystemExit(1)
`

// Resumable reports whether the worktree is an interrupted profile publication.
func (s Scripts) Resumable(ctx context.Context) bool {
	res, err := s.py(ctx, "-c", resumableCheck, s.Dir)
	return err == nil && res.OK()
}

// outputsScript prints every file the renderer owns, so the installer never keeps
// its own copy of that list. (The old bash list had drifted: it omitted seven
// rendered files, which were therefore never committed or pushed.)
const outputsScript = `
import importlib.util, sys
from pathlib import Path
root = Path(sys.argv[1]).resolve()
spec = importlib.util.spec_from_file_location("render_platform_profile", root / "tasks/scripts/render-platform-profile.py")
mod = importlib.util.module_from_spec(spec)
spec.loader.exec_module(mod)
renderer = mod.load_pods_renderer()
out = {"platform.yaml", "pods/cluster-config/cluster-config.env"}
out.update(t for _x, t in renderer.TEMPLATE_TARGETS)
out.update(t for t, _n, _k in renderer.APP_CONFIG_TARGETS)
print("\n".join(sorted(out)))
`

// Outputs lists the tracked files that rendering platform.yaml owns.
func (s Scripts) Outputs(ctx context.Context) ([]string, error) {
	res, err := s.py(ctx, "-c", outputsScript, s.Dir)
	if err != nil {
		return nil, err
	}
	if !res.OK() {
		return nil, fmt.Errorf("could not list the rendered files: %s", strings.TrimSpace(res.Stderr))
	}
	var out []string
	for _, line := range strings.Split(res.Stdout, "\n") {
		if line = strings.TrimSpace(line); line != "" {
			out = append(out, line)
		}
	}
	if len(out) == 0 {
		return nil, errors.New("the renderer reported no output files")
	}
	return out, nil
}

// SetupValues are the values setup derives from the reviewed profile.
type SetupValues struct {
	ClusterDomain   string
	TailscaleDomain string
	ClusterTag      string
	KSBaseURL       string
	R2Bucket        string
}

// Validate checks platform.yaml quietly.
func (s Scripts) Validate(ctx context.Context) error {
	res, err := s.py(ctx, "./tasks/scripts/validate-platform-profile.py", "--quiet", "--profile", "./platform.yaml")
	if err != nil {
		return err
	}
	if !res.OK() {
		return fmt.Errorf("platform.yaml is not valid: %s", strings.TrimSpace(res.Stderr+res.Stdout))
	}
	return nil
}

// SetupValues reads the setup-facing values the renderer derives from the profile.
func (s Scripts) SetupValues(ctx context.Context) (SetupValues, error) {
	tmp, err := os.CreateTemp("", "adaetum-setup-env-*")
	if err != nil {
		return SetupValues{}, err
	}
	path := tmp.Name()
	tmp.Close()
	defer os.Remove(path)

	res, err := s.py(ctx, "./tasks/scripts/render-platform-profile.py", "--profile", "./platform.yaml", "--output-setup-env", path)
	if err != nil {
		return SetupValues{}, err
	}
	if !res.OK() {
		return SetupValues{}, fmt.Errorf("could not read the platform profile: %s", strings.TrimSpace(res.Stderr+res.Stdout))
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return SetupValues{}, err
	}
	kv := map[string]string{}
	for _, line := range strings.Split(string(data), "\n") {
		if k, v, ok := strings.Cut(strings.TrimRight(line, "\r"), "="); ok {
			kv[k] = strings.TrimSpace(v)
		}
	}
	return SetupValues{
		ClusterDomain: kv["CLUSTER_DOMAIN"], TailscaleDomain: kv["TAILSCALE_DOMAIN"], ClusterTag: kv["TAILSCALE_CLUSTER_TAG"],
		KSBaseURL: kv["KS_BASE_URL"], R2Bucket: kv["R2_BUCKET"],
	}, nil
}
