// Package repository is the first setup stage: make sure the operator has a
// standalone private recovery repository on GitHub, that this checkout's origin
// points at it with canonical Adaetum kept as read-only upstream, that its main
// branch carries the selected checkout plus the operator's platform.yaml, and
// that GitHub Actions can see the workflows.
//
// It ports first_run_ensure_github_login, first_run_ensure_github_actions,
// first_run_set_recovery_origin, first_run_track_recovery_branch and
// first_run_configure_recovery_repository from tasks/scripts/first-run-preflight.sh.
package repository

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/Adaetum/Adaetum/installer/engine"
	"github.com/Adaetum/Adaetum/installer/github"
	"github.com/Adaetum/Adaetum/installer/platform"
	"github.com/Adaetum/Adaetum/installer/sys"
)

const (
	stepID = "repository"
	// marker is a file every Adaetum recovery repository has. A populated
	// repository without it is somebody else's project, and is never reused.
	marker       = "platform.yaml"
	defaultName  = "Adaetum-cluster"
	maxSuffix    = 50
	loginScopes  = "repo,workflow,read:org"
	signInHint   = "gh auth login --hostname github.com --web --git-protocol https --scopes " + loginScopes
	refreshHint  = "gh auth refresh --hostname github.com --scopes " + loginScopes
	commitReason = "Refresh recovery main from selected checkout"
)

// Step is the repository stage.
type Step struct {
	Run sys.Runner
	Dir string // the checkout this installer was started from
	// Render regenerates the tracked outputs from platform.yaml. It is a field so
	// tests do not need the Python scripts; New sets the real one.
	Render func(ctx context.Context) error
	Sleep  func(time.Duration)
}

// New returns the stage for the checkout at dir.
func New(run sys.Runner, dir string) *Step {
	s := &Step{Run: run, Dir: dir, Sleep: time.Sleep}
	s.Render = platform.Scripts{Run: run, Dir: dir}.Render
	return s
}

func (s *Step) ID() string    { return stepID }
func (s *Step) Title() string { return "Private recovery repository" }

type repoMeta struct {
	FullName      string `json:"full_name"`
	Visibility    string `json:"visibility"`
	Fork          bool   `json:"fork"`
	Size          int    `json:"size"`
	DefaultBranch string `json:"default_branch"`
	Permissions   struct {
		Admin bool `json:"admin"`
	} `json:"permissions"`
}

// ---- command helpers -------------------------------------------------------

func (s *Step) git(ctx context.Context, args ...string) (sys.Result, error) {
	return s.Run.Run(ctx, s.Dir, "git", args...)
}

func (s *Step) gh(ctx context.Context, args ...string) (sys.Result, error) {
	return s.Run.Run(ctx, s.Dir, "gh", args...)
}

// gitOut runs a git command that has to succeed and returns its trimmed output.
// A failure carries git's own message, so the operator sees what git said.
func (s *Step) gitOut(ctx context.Context, what string, args ...string) (string, error) {
	return must(s.git(ctx, args...))(what)
}

func (s *Step) ghOut(ctx context.Context, what string, args ...string) (string, error) {
	return must(s.gh(ctx, args...))(what)
}

func must(res sys.Result, err error) func(what string) (string, error) {
	return func(what string) (string, error) {
		if err != nil {
			return "", fmt.Errorf("%s: %w", what, err)
		}
		if !res.OK() {
			return "", fmt.Errorf("%s: %s", what, strings.TrimSpace(res.Stderr+" "+res.Stdout))
		}
		return strings.TrimSpace(res.Stdout), nil
	}
}

func (s *Step) origin(ctx context.Context) string {
	res, err := s.git(ctx, "remote", "get-url", "origin")
	if err != nil || !res.OK() {
		return ""
	}
	return strings.TrimSpace(res.Stdout)
}

// ---- GitHub lookups --------------------------------------------------------

type lookupState int

const (
	exists lookupState = iota
	available
)

// lookup tells an existing repository from a free name. Anything else (an outage,
// bad credentials) is an error: guessing "available" on a failed call could make
// setup try to create over somebody's repository.
func (s *Step) lookup(ctx context.Context, repo string) (repoMeta, lookupState, error) {
	res, err := s.gh(ctx, "api", "repos/"+repo)
	if err != nil {
		return repoMeta{}, 0, err
	}
	if res.OK() {
		var m repoMeta
		if err := json.Unmarshal([]byte(res.Stdout), &m); err != nil {
			return repoMeta{}, 0, fmt.Errorf("could not read GitHub's answer for %s: %w", repo, err)
		}
		return m, exists, nil
	}
	text := strings.ToLower(res.Stderr + res.Stdout)
	if strings.Contains(text, "http 404") || strings.Contains(text, "not found") {
		return repoMeta{}, available, nil
	}
	if strings.Contains(text, "invalid character '<'") {
		return repoMeta{}, 0, errors.New("GitHub returned an unavailable-service page instead of an API answer; check https://www.githubstatus.com and rerun")
	}
	return repoMeta{}, 0, fmt.Errorf("could not inspect %s: %s", repo, strings.TrimSpace(res.Stderr))
}

// usable reports whether an existing repository can be the recovery repository:
// private, administered by the operator, not a fork, and either empty or already
// an Adaetum repository. The returned string says why not.
func (s *Step) usable(ctx context.Context, repo string, m repoMeta) (bool, string) {
	if m.Visibility != "private" || !m.Permissions.Admin || m.Fork {
		return false, repo + " is not a standalone private repository you can administer."
	}
	if m.Size > 0 {
		res, err := s.gh(ctx, "api", "repos/"+repo+"/contents/"+marker)
		if err != nil || !res.OK() {
			return false, repo + " is populated but does not look like Adaetum."
		}
	}
	return true, ""
}

// reusableRepositories lists accessible candidates, then applies the same
// repository safety checks used immediately before connecting an origin.
func (s *Step) reusableRepositories(ctx context.Context, owner string) ([]string, error) {
	res, err := s.gh(ctx, "api", "--paginate", "user/repos?visibility=private&affiliation=owner,collaborator,organization_member&per_page=100", "--jq", `.[] | select(.private == true and .fork == false and .permissions.admin == true) | .full_name`)
	if err != nil {
		return nil, err
	}
	if !res.OK() {
		return nil, fmt.Errorf("could not list GitHub repositories: %s", strings.TrimSpace(res.Stderr))
	}
	var repos []string
	for _, name := range strings.Split(res.Stdout, "\n") {
		name = strings.TrimSpace(name)
		if name == "" {
			continue
		}
		if prefix, _, ok := strings.Cut(name, "/"); !ok || !strings.EqualFold(prefix, owner) {
			continue
		}
		m, state, err := s.lookup(ctx, name)
		if err != nil {
			return nil, err
		}
		if state == exists {
			if ok, _ := s.usable(ctx, name, m); ok {
				repos = append(repos, name)
			}
		}
	}
	return repos, nil
}

// repositoryOwners includes the personal account and visible organization
// memberships. GitHub enforces each organization's repository creation policy.
func (s *Step) repositoryOwners(ctx context.Context, login string) ([]string, error) {
	res, err := s.gh(ctx, "api", "--paginate", "user/orgs?per_page=100", "--jq", ".[].login")
	if err != nil {
		return nil, err
	}
	if !res.OK() {
		return nil, fmt.Errorf("could not list GitHub organizations: %s; check your GitHub CLI read:org permission", strings.TrimSpace(res.Stderr))
	}
	owners := []string{login}
	for _, org := range strings.Split(res.Stdout, "\n") {
		org = strings.TrimSpace(org)
		if org == "" {
			continue
		}
		duplicate := false
		for _, owner := range owners {
			if strings.EqualFold(owner, org) {
				duplicate = true
				break
			}
		}
		if !duplicate {
			owners = append(owners, org)
		}
	}
	return owners, nil
}

func chooseRepositoryOwner(ctx context.Context, ui engine.UI, owners []string) (string, error) {
	if len(owners) == 0 {
		return "", errors.New("no GitHub account is available")
	}
	if len(owners) == 1 {
		return owners[0], nil
	}
	return engine.Choose(ctx, ui, "repository-owner", "Where should your recovery repository live?", owners, "")
}

func (s *Step) findAvailable(ctx context.Context, owner string) (string, error) {
	for n := 1; n <= maxSuffix; n++ {
		candidate := owner + "/" + defaultName
		if n > 1 {
			candidate = fmt.Sprintf("%s/%s-%d", owner, defaultName, n)
		}
		_, state, err := s.lookup(ctx, candidate)
		if err != nil {
			return "", err
		}
		if state == available {
			return candidate, nil
		}
	}
	return "", fmt.Errorf("no available private repository name from %s through %s-%d", defaultName, defaultName, maxSuffix)
}

// findPreferred suggests the operator's existing recovery repository if there is
// one, and otherwise the first free name. A same-named unrelated repository is a
// collision, not something to reuse.
func (s *Step) findPreferred(ctx context.Context, owner string) (string, error) {
	candidate := owner + "/" + defaultName
	m, state, err := s.lookup(ctx, candidate)
	if err != nil {
		return "", err
	}
	if state == exists {
		if ok, _ := s.usable(ctx, candidate, m); ok {
			return candidate, nil
		}
	}
	return s.findAvailable(ctx, owner)
}

// ---- Check and Plan --------------------------------------------------------

func (s *Step) Check(ctx context.Context, _ *engine.Env) (engine.Finding, error) {
	if res, err := s.git(ctx, "--version"); err != nil || !res.OK() {
		return engine.Finding{Status: engine.Blocked, Detail: "git is required. Install it from https://git-scm.com/downloads."}, nil
	}
	if res, err := s.gh(ctx, "--version"); err != nil || !res.OK() {
		return engine.Finding{Status: engine.Blocked, Detail: "The GitHub CLI (gh) is required. Install it from https://cli.github.com/."}, nil
	}
	needed := func(why string) (engine.Finding, error) {
		return engine.Finding{Status: engine.Needed, Detail: why}, nil
	}

	origin := s.origin(ctx)
	if origin == "" || github.IsUpstream(origin) {
		return needed("origin is canonical Adaetum or missing; a private recovery repository is needed")
	}
	if res, err := s.gh(ctx, "auth", "token", "--hostname", "github.com"); err != nil || !res.OK() {
		return needed("not signed in to GitHub")
	}
	repo := github.Repo(origin)
	m, state, err := s.lookup(ctx, repo)
	if err != nil {
		// Not being able to look is something to report, not a reason to stop a dry
		// run before it has shown the other steps.
		return needed("could not check " + repo + " on GitHub: " + err.Error())
	}
	if state != exists {
		return needed(repo + " does not exist yet")
	}
	if ok, why := s.usable(ctx, repo, m); !ok {
		return needed(why)
	}
	if why := s.mainPublished(ctx); why != "" {
		return needed(why)
	}
	if n, _ := s.workflowCount(ctx, repo); n == 0 {
		return needed("GitHub has not registered workflows in " + repo)
	}
	return engine.Finding{Status: engine.Done, Detail: "origin is " + repo + ", main is published, and Actions sees its workflows"}, nil
}

// mainPublished returns "" when the selected checkout equals origin's main.
func (s *Step) mainPublished(ctx context.Context) string {
	branch, _ := s.gitOut(ctx, "branch", "branch", "--show-current")
	if branch == "" {
		return "the checkout is not on a named branch"
	}
	local, err := s.gitOut(ctx, "rev-parse", "rev-parse", "HEAD")
	remote := s.remoteHead(ctx)
	if err != nil || remote == "" || remote != local {
		return "origin's main does not match this checkout"
	}
	return ""
}

func (s *Step) Plan(ctx context.Context, env *engine.Env) ([]string, error) {
	finding, err := s.Check(ctx, env)
	if err != nil {
		return nil, err
	}
	return []string{
		"Sign in to GitHub if this machine is not already signed in (a browser step you do in a terminal).",
		"Create or reuse a private repository for your cluster, and keep canonical Adaetum as read-only upstream.",
		"Point origin at the private repository and publish the selected checkout to its main branch, preserving your platform.yaml.",
		"Check that GitHub Actions has registered the workflows.",
		"Right now: " + finding.Detail + ".",
	}, nil
}
