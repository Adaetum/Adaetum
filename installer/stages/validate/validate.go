// Package validate is the first bootstrap stage: before anything is rendered or
// published, confirm that every input the rest of setup needs is present and
// sensible, and gather the derived values (bootstrap URL, bucket, tag, installer
// ISO) that later stages read. It ports "Validate captured setup inputs" from
// tasks/scripts/run-opinionated-setup.sh and changes nothing.
package validate

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"
	"regexp"
	"runtime"
	"strings"

	"github.com/Adaetum/Adaetum/installer/engine"
	"github.com/Adaetum/Adaetum/installer/platform"
	"github.com/Adaetum/Adaetum/installer/stages/media"
	"github.com/Adaetum/Adaetum/installer/sys"
)

const stepID = "validate"

var (
	httpURL = regexp.MustCompile(`^https?://[^\s]+$`)
	domain  = regexp.MustCompile(`^[A-Za-z0-9.-]+\.[A-Za-z]{2,}$`)
)

// Step is the validation stage.
type Step struct {
	Run  sys.Runner
	Root string
	// Profile reads the values derived from the reviewed platform profile. It is a
	// field so tests do not need the Python scripts.
	Profile func(ctx context.Context) (platform.SetupValues, error)
	// Validate checks platform.yaml. Same reason.
	Validate func(ctx context.Context) error
}

// New returns the stage for the checkout at root.
func New(run sys.Runner, root string) *Step {
	p := platform.Scripts{Run: run, Dir: root}
	return &Step{Run: run, Root: root, Profile: p.SetupValues, Validate: p.Validate}
}

func (s *Step) ID() string    { return stepID }
func (s *Step) Title() string { return "Validate captured inputs" }

func python() string {
	if runtime.GOOS == "windows" {
		return "python"
	}
	return "python3"
}

// Check cannot see the secrets earlier stages collected (they live in memory for
// the run), so a read-only look reports the stage as needed.
func (s *Step) Check(ctx context.Context, _ *engine.Env) (engine.Finding, error) {
	if res, err := s.Run.Run(ctx, s.Root, python(), "--version"); err != nil || !res.OK() {
		return engine.Finding{Status: engine.Blocked, Detail: "python3 is required to read the platform profile."}, nil
	}
	return engine.Finding{Status: engine.Needed, Detail: "runs once the provider credentials are collected"}, nil
}

func (s *Step) Plan(ctx context.Context, env *engine.Env) ([]string, error) {
	f, err := s.Check(ctx, env)
	if err != nil {
		return nil, err
	}
	return []string{
		"Check that the reviewed platform profile is valid and read its domain, tailnet, bootstrap URL and storage bucket.",
		"Check that the Cloudflare, GitHub and Tailscale credentials gathered earlier are all present.",
		"Check that an installer ISO is in the checkout.",
		"Right now: " + f.Detail + ".",
	}, nil
}

func compact(s string) string {
	return strings.TrimSpace(strings.ReplaceAll(strings.ReplaceAll(s, "\r", ""), "\n", ""))
}

// gitCapable reports whether a GitHub token can push over HTTPS. Rendered GitOps
// state is pushed with it, so a read-only or app token would fail late.
func gitCapable(token string) bool {
	for _, p := range []string{"github_pat_", "ghp_", "gho_", "ghu_", "ghs_"} {
		if strings.HasPrefix(token, p) {
			return true
		}
	}
	return false
}

func (s *Step) Apply(ctx context.Context, env *engine.Env, ui engine.UI) error {
	say := func(msg string) { ui.Report(engine.Event{StepID: stepID, Message: msg}) }

	if err := s.Validate(ctx); err != nil {
		return err
	}
	vals, err := s.Profile(ctx)
	if err != nil {
		return err
	}
	say("Platform profile validated.")

	missing := func(what, from string) error {
		return fmt.Errorf("%s is missing; it comes from the %s stage, so run setup from the start", what, from)
	}
	if compact(env.Secret("cloudflare_token")) == "" {
		return missing("the Cloudflare token", "providers")
	}
	if vals.TailscaleDomain == "" {
		return errors.New("the platform profile has no Tailscale tailnet")
	}
	hasOAuth := env.Secret("tailscale_oauth_client_id") != "" && env.Secret("tailscale_oauth_client_secret") != ""
	if env.Secret("tailscale_token") == "" && !hasOAuth {
		return missing("Tailscale OAuth credentials or an access token", "providers")
	}

	// The GitHub credential is the session gh already holds; it was validated in
	// the repository stage. It is read here, in memory, never written to disk.
	res, err := s.Run.Run(ctx, s.Root, "gh", "auth", "token", "--hostname", "github.com")
	if err != nil || !res.OK() {
		return errors.New("GitHub sign-in is missing; run setup from the start")
	}
	gh := compact(res.Stdout)
	if !gitCapable(gh) {
		return errors.New("the GitHub credential is not a git-capable token, so rendered GitOps state could not be pushed")
	}
	env.SetSecret("github_token", gh)

	ks := vals.KSBaseURL
	if ks == "" {
		d := strings.ToLower(strings.TrimRight(compact(vals.ClusterDomain), "/"))
		switch {
		case httpURL.MatchString(d):
			ks = d
		case domain.MatchString(strings.TrimPrefix(strings.TrimPrefix(d, "https://"), "http://")):
			ks = "https://bootstrap." + strings.TrimPrefix(strings.TrimPrefix(d, "https://"), "http://")
		default:
			return fmt.Errorf("invalid cluster domain %q; use a domain like example.services", d)
		}
	}

	iso := env.Value("iso_path")
	if iso == "" {
		found := media.Library{Root: s.Root}.Discover()
		for _, i := range found {
			if filepath.Dir(i.Path) == s.Root {
				iso = i.Path
				break
			}
		}
	}
	if iso == "" {
		return errors.New("setup needs a Rocky installer ISO in the checkout; run the installer media stage first")
	}

	env.SetValue("cluster_domain", vals.ClusterDomain)
	env.SetValue("tailscale_domain", vals.TailscaleDomain)
	env.SetValue("cluster_tag", vals.ClusterTag)
	env.SetValue("ks_base_url", ks)
	env.SetValue("r2_bucket", vals.R2Bucket)
	env.SetValue("iso_path", iso)
	say(fmt.Sprintf("Using cluster domain %s, tailnet %s and installer %s.", vals.ClusterDomain, vals.TailscaleDomain, filepath.Base(iso)))
	return nil
}
