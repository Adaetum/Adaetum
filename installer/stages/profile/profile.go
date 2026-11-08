// Package profile is the third setup stage: turn the operator's two public
// choices (a domain and a tailnet) into platform.yaml, render the files derived
// from it, validate the result with the repository's commit hooks, and publish it
// to the private recovery repository's main branch as one commit.
//
// It ports first_run_profile, first_run_profile_render_is_resumable and
// first_run_review_profile from tasks/scripts/first-run-preflight.sh.
package profile

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/Adaetum/Adaetum/installer/engine"
	"github.com/Adaetum/Adaetum/installer/platform"
	"github.com/Adaetum/Adaetum/installer/sys"
)

const (
	stepID       = "profile"
	commitMsg    = "Configure Adaetum platform profile"
	domainKey    = "domain"
	overlayKey   = "overlay_domain"
	hookConfig   = ".pre-commit-config.yaml"
	profileFile  = "platform.yaml"
	remoteBranch = "refs/heads/main"
)

// Step is the profile stage.
type Step struct {
	Run sys.Runner
	Dir string

	Platform platform.Scripts
	// Propose and Render are fields so tests can run without the Python scripts.
	// New sets the real ones.
	Propose func(ctx context.Context, output string, p platform.Proposal) error
	Render  func(ctx context.Context) error
}

// New returns the stage for the checkout at dir.
func New(run sys.Runner, dir string) *Step {
	p := platform.Scripts{Run: run, Dir: dir}
	return &Step{Run: run, Dir: dir, Platform: p, Propose: p.Propose, Render: p.Render}
}

func (s *Step) ID() string    { return stepID }
func (s *Step) Title() string { return "Public platform profile" }

func (s *Step) git(ctx context.Context, args ...string) (sys.Result, error) {
	return s.Run.Run(ctx, s.Dir, "git", args...)
}

func (s *Step) gitOut(ctx context.Context, what string, args ...string) (string, error) {
	res, err := s.git(ctx, args...)
	if err != nil {
		return "", fmt.Errorf("%s: %w", what, err)
	}
	if !res.OK() {
		return "", fmt.Errorf("%s: %s", what, strings.TrimSpace(res.Stderr+" "+res.Stdout))
	}
	return strings.TrimSpace(res.Stdout), nil
}

func (s *Step) clean(ctx context.Context) bool {
	res, err := s.git(ctx, "diff", "--quiet", "HEAD")
	return err == nil && res.OK()
}

func (s *Step) remoteHead(ctx context.Context) string {
	res, err := s.git(ctx, "ls-remote", "--heads", "origin", "main")
	if err != nil || !res.OK() {
		return ""
	}
	if f := strings.Fields(res.Stdout); len(f) > 0 {
		return f[0]
	}
	return ""
}

func (s *Step) Check(ctx context.Context, env *engine.Env) (engine.Finding, error) {
	if res, err := s.Run.Run(ctx, s.Dir, "python3", "--version"); err != nil || !res.OK() {
		return engine.Finding{Status: engine.Blocked, Detail: "python3 is required to read and render platform.yaml. Install it from https://www.python.org/downloads/."}, nil
	}
	current, err := s.Platform.Show(ctx)
	if err != nil {
		return engine.Finding{Status: engine.Blocked, Detail: err.Error()}, nil
	}
	if current.Placeholder() {
		return engine.Finding{Status: engine.Needed, Detail: "platform.yaml still holds the template domain and tailnet"}, nil
	}
	if (env.Value(domainKey) != "" && env.Value(domainKey) != current[domainKey]) || (env.Value(overlayKey) != "" && env.Value(overlayKey) != current[overlayKey]) {
		return engine.Finding{Status: engine.Needed, Detail: "provider selections differ from platform.yaml"}, nil
	}
	if !s.clean(ctx) {
		return engine.Finding{Status: engine.Needed, Detail: "the profile has changes that are not committed"}, nil
	}
	head, _ := s.gitOut(ctx, "read HEAD", "rev-parse", "HEAD")
	if remote := s.remoteHead(ctx); remote == "" || remote != head {
		return engine.Finding{Status: engine.Needed, Detail: "the configured profile is not published to origin's main yet"}, nil
	}
	return engine.Finding{Status: engine.Done, Detail: "platform.yaml is configured for " + current["domain"] + " and published"}, nil
}

func (s *Step) Plan(ctx context.Context, env *engine.Env) ([]string, error) {
	finding, err := s.Check(ctx, env)
	if err != nil {
		return nil, err
	}
	return []string{
		"Take your public domain and Tailscale tailnet (chosen with your providers, or asked here).",
		"Fill in Adaetum's standard defaults for local DNS, node tagging, the first Gitea repository, bootstrap delivery and R2 storage.",
		"Write platform.yaml, render the files derived from it, and run the repository's commit checks.",
		"Commit that as one change and publish it to your private repository's main.",
		"Right now: " + finding.Detail + ".",
	}, nil
}

// choose returns a shared value, else an existing real profile value, else asks.
func choose(ctx context.Context, env *engine.Env, ui engine.UI, key, screenID, prompt string, existing platform.Profile) (string, error) {
	if v := env.Value(key); v != "" {
		return v, nil
	}
	def := existing[key]
	if existing.Placeholder() {
		def = ""
	}
	v, err := engine.AskText(ctx, ui, screenID, prompt, def)
	if err != nil {
		return "", err
	}
	v = strings.ToLower(strings.TrimSpace(v))
	if v == "" || strings.ContainsAny(v, " /\t") || !strings.Contains(v, ".") {
		return "", fmt.Errorf("%q is not a domain name (for example example.com)", v)
	}
	env.SetValue(key, v)
	return v, nil
}

func (s *Step) Apply(ctx context.Context, env *engine.Env, ui engine.UI) error {
	say := func(msg string) { ui.Report(engine.Event{StepID: stepID, Message: msg}) }

	existing, err := s.Platform.Show(ctx)
	if err != nil {
		return err
	}
	domain, err := choose(ctx, env, ui, domainKey, "domain", "Public cluster domain (a domain you manage in Cloudflare)", existing)
	if err != nil {
		return err
	}
	overlay, err := choose(ctx, env, ui, overlayKey, "overlay-domain", "Tailscale tailnet domain (for example tail1234.ts.net)", existing)
	if err != nil {
		return err
	}
	proposal := platform.Standard(domain, overlay)
	say(fmt.Sprintf("Domain %s, tailnet %s.", domain, overlay))
	tmp, err := os.CreateTemp("", "adaetum-platform-*.yaml")
	if err != nil {
		return err
	}
	proposalPath := tmp.Name()
	tmp.Close()
	defer os.Remove(proposalPath)
	if err := s.Propose(ctx, proposalPath, proposal); err != nil {
		return err
	}

	// Rendering is an atomic publication boundary: starting from a clean tracked
	// worktree means everything staged below was produced by the reviewed profile
	// and its renderer, never by an operator's unrelated edit.
	resume := false
	if !s.clean(ctx) {
		current, readErr := os.ReadFile(filepath.Join(s.Dir, profileFile))
		proposed, propErr := os.ReadFile(proposalPath)
		if readErr == nil && propErr == nil && string(current) == string(proposed) && s.Platform.Resumable(ctx) {
			resume = true
			say("Resuming the previously rendered profile after an interrupted commit check.")
		} else {
			return errors.New("the tracked worktree changed before the profile was rendered; commit or stash unrelated changes, then rerun")
		}
	}
	if !resume {
		if err := os.Rename(proposalPath, filepath.Join(s.Dir, profileFile)); err != nil {
			// Rename fails across filesystems; copy instead.
			data, readErr := os.ReadFile(proposalPath)
			if readErr != nil {
				return readErr
			}
			if err := os.WriteFile(filepath.Join(s.Dir, profileFile), data, 0o644); err != nil {
				return err
			}
		}
		// The proposal came from a private temp file (0600); platform.yaml is an
		// ordinary tracked file that other tools read.
		if err := os.Chmod(filepath.Join(s.Dir, profileFile), 0o644); err != nil {
			return err
		}
		say("Rendering the files derived from platform.yaml...")
		if err := s.Render(ctx); err != nil {
			return err
		}
	}

	if s.clean(ctx) {
		say("The profile already matches the repository; nothing to commit.")
		return nil
	}
	return s.commitAndPublish(ctx, resume, say)
}

// hookRunner finds prek or pre-commit; the commit checks are not optional.
func (s *Step) hookRunner(ctx context.Context) (string, error) {
	for _, name := range []string{"prek", "pre-commit"} {
		if res, err := s.Run.Run(ctx, s.Dir, name, "--version"); err == nil && res.OK() {
			return name, nil
		}
	}
	return "", errors.New("neither prek nor pre-commit is installed, and the profile commit has to be validated")
}

func (s *Step) commitAndPublish(ctx context.Context, resume bool, say func(string)) error {
	var unstagedBefore string
	if !resume {
		if _, err := s.gitOut(ctx, "stage the profile", "add", "-u"); err != nil {
			return err
		}
	} else {
		// Preserve unrelated unstaged work while proving the hooks did not rewrite it.
		before, err := s.gitOut(ctx, "snapshot unstaged work", "diff", "--binary", "--")
		if err != nil {
			return err
		}
		unstagedBefore = before
	}

	runner, err := s.hookRunner(ctx)
	if err != nil {
		return err
	}
	say("Running the repository's commit checks...")
	res, err := s.Run.Run(ctx, s.Dir, runner, "run", "--config", hookConfig, "--all-files")
	if err != nil {
		return err
	}
	if !res.OK() {
		return fmt.Errorf("the profile commit checks failed; fix the reported problem, then rerun:\n%s", strings.TrimSpace(res.Stdout+res.Stderr))
	}
	say("Profile commit checks passed.")

	if resume {
		after, err := s.gitOut(ctx, "snapshot unstaged work", "diff", "--binary", "--")
		if err != nil {
			return err
		}
		if after != unstagedBefore {
			return errors.New("the profile checks changed unrelated unstaged files; review the changes, then rerun")
		}
	} else if res, err := s.git(ctx, "diff", "--quiet"); err != nil || !res.OK() {
		return errors.New("the profile checks changed tracked files after staging; review the changes, then rerun")
	}

	// The whole staged configuration was validated above; skip the hook on commit
	// so it does not repeat the same result.
	if _, err := s.gitOut(ctx, "commit the profile", "commit", "--no-verify", "-m", commitMsg); err != nil {
		return err
	}
	say("Publishing the profile to your private repository...")
	if _, err := s.gitOut(ctx, "publish the profile", "push", "origin", "HEAD:"+remoteBranch); err != nil {
		return err
	}
	local, err := s.gitOut(ctx, "read HEAD", "rev-parse", "HEAD")
	if err != nil {
		return err
	}
	if remote := s.remoteHead(ctx); remote == "" || remote != local {
		return errors.New("profile push verification failed: origin/main does not match the local HEAD")
	}
	return nil
}
