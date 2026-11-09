package repository

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"

	"github.com/Adaetum/Adaetum/installer/engine"
	"github.com/Adaetum/Adaetum/installer/github"
	"github.com/Adaetum/Adaetum/installer/sys"
)

func (s *Step) Apply(ctx context.Context, env *engine.Env, ui engine.UI) error {
	say := func(msg string) { ui.Report(engine.Event{StepID: stepID, Message: msg}) }
	reconnect := false
	for {
		if reconnect {
			if err := s.githubLogin(ctx, ui, say, false); err != nil {
				return err
			}
			reconnect = false
		}
		login, err := s.signIn(ctx, ui, say)
		if err != nil {
			return err
		}
		ui.Report(engine.Event{StepID: stepID, Kind: "github-account", Message: login})
		if err := s.prepareSourceSnapshot(ctx, say); err != nil {
			return err
		}
		repo, err := s.configure(ctx, ui, say, login)
		if errors.Is(err, errSwitchAccount) {
			if os.Getenv("GH_TOKEN") != "" || os.Getenv("GITHUB_TOKEN") != "" {
				return errors.New("GitHub authentication comes from an environment variable; remove it before switching accounts")
			}
			res, logoutErr := s.gh(ctx, "auth", "logout", "--hostname", "github.com", "--user", login)
			if logoutErr != nil {
				return logoutErr
			}
			if !res.OK() {
				return fmt.Errorf("could not log out of GitHub: %s", strings.TrimSpace(res.Stderr))
			}
			ui.Report(engine.Event{StepID: stepID, Kind: "github-account"})
			reconnect = true
			continue
		}
		if err != nil {
			return err
		}
		if s.PriorRecovery {
			env.SetValue("existing_recovery", "true")
		}
		return s.ensureActions(ctx, repo, say)
	}
}

var errSwitchAccount = errors.New("switch GitHub account")

const switchAccountAnswer = "__switch_github_account__"
const reuseRepository = "Reuse an existing private repository"
const createRepository = "Create a new private repository"

// prepareSourceSnapshot makes an extracted source archive publishable without
// pretending that its files came from the current upstream HEAD. The marker
// makes an interrupted initialization resumable without adopting someone else's
// existing repository, including a worktree whose .git is a file.
func (s *Step) prepareSourceSnapshot(ctx context.Context, say func(string)) error {
	gitPath := filepath.Join(s.Dir, ".git")
	marker := filepath.Join(s.Dir, ".adaetum", "source-snapshot-pending")
	_, gitErr := os.Lstat(gitPath)
	_, markerErr := os.Stat(marker)
	if gitErr != nil && !os.IsNotExist(gitErr) {
		return fmt.Errorf("inspect checkout: %w", gitErr)
	}
	if markerErr != nil && !os.IsNotExist(markerErr) {
		return fmt.Errorf("inspect source snapshot: %w", markerErr)
	}
	if gitErr == nil && os.IsNotExist(markerErr) {
		return nil
	}
	if os.IsNotExist(markerErr) {
		if err := os.MkdirAll(filepath.Dir(marker), 0o700); err != nil {
			return fmt.Errorf("prepare source snapshot: %w", err)
		}
		if err := os.WriteFile(marker, []byte("Adaetum source snapshot initialization in progress\n"), 0o600); err != nil {
			return fmt.Errorf("prepare source snapshot: %w", err)
		}
	}
	say("Preparing the downloaded source as a private recovery snapshot...")
	if os.IsNotExist(gitErr) {
		if _, err := s.gitOut(ctx, "initialize the source snapshot", "init", "-b", "main"); err != nil {
			return err
		}
	}
	if res, err := s.git(ctx, "rev-parse", "--verify", "HEAD"); err != nil || !res.OK() {
		for _, identity := range [][2]string{{"user.name", "Adaetum Setup"}, {"user.email", "setup@adaetum.local"}} {
			if existing, e := s.git(ctx, "config", "--get", identity[0]); e != nil || !existing.OK() || strings.TrimSpace(existing.Stdout) == "" {
				if _, e := s.gitOut(ctx, "configure the snapshot commit identity", "config", "--local", identity[0], identity[1]); e != nil {
					return e
				}
			}
		}
		if _, err := s.gitOut(ctx, "stage the downloaded source", "add", "--all"); err != nil {
			return err
		}
		if _, err := s.gitOut(ctx, "commit the downloaded source", "commit", "-m", "Initialize recovery from downloaded source"); err != nil {
			return err
		}
	}
	if err := os.Remove(marker); err != nil {
		return fmt.Errorf("finish source snapshot: %w", err)
	}
	return nil
}

// ---- GitHub sign-in --------------------------------------------------------

// signIn makes sure gh holds a working GitHub session and returns the login.
//
// The sign-in happens in the window: the installer runs gh's own browser sign-in,
// reads the one-time code gh prints, and shows it with a button that opens GitHub.
// gh opens the browser itself and waits until the operator has approved, so nothing
// has to be typed into a terminal. The installer never sees the token; gh keeps it.
// If gh cannot be driven this way, it falls back to showing the command to run.
func (s *Step) signIn(ctx context.Context, ui engine.UI, say func(string)) (string, error) {
	if res, err := s.gh(ctx, "auth", "token", "--hostname", "github.com"); err != nil || !res.OK() {
		if err := s.githubLogin(ctx, ui, say, false); err != nil {
			return "", err
		}
	}

	login, bad, err := s.validateSession(ctx, say)
	if bad {
		say("GitHub rejected the saved sign-in; signing in again.")
		if err := s.githubLogin(ctx, ui, say, true); err != nil {
			return "", err
		}
		login, bad, err = s.validateSession(ctx, say)
		if bad {
			return "", errors.New("GitHub still rejects the sign-in after refreshing it")
		}
	}
	if err != nil {
		return "", err
	}
	say("Signed in to GitHub as " + login + ".")
	return login, nil
}

var oneTimeCode = regexp.MustCompile(`one-time code:\s*([A-Z0-9]{4}-[A-Z0-9]{4})`)

// githubLogin signs in (or refreshes the sign-in) through gh's browser flow.
func (s *Step) githubLogin(ctx context.Context, ui engine.UI, say func(string), refresh bool) error {
	args := []string{"auth", "login", "--hostname", "github.com", "--web", "--git-protocol", "https", "--scopes", loginScopes}
	hint := signInHint
	if refresh {
		args = []string{"auth", "refresh", "--hostname", "github.com", "--scopes", loginScopes}
		hint = refreshHint
	}

	if streamer, ok := s.Run.(sys.Streamer); ok {
		say("Opening GitHub in your browser. Enter the code shown here when GitHub asks for it.")
		// Two blank lines answer any "press Enter" prompt; if gh does not ask, they are ignored.
		res, err := streamer.RunStream(ctx, s.Dir, nil, "\n\n", "gh", args, func(line string) {
			if m := oneTimeCode.FindStringSubmatch(line); m != nil {
				ui.Report(engine.Event{StepID: stepID, Kind: engine.KindCode, Message: m[1], Link: "https://github.com/login/device"})
			}
		})
		// An empty code takes the code off the screen.
		ui.Report(engine.Event{StepID: stepID, Kind: engine.KindCode})
		if err == nil && res.OK() {
			return nil
		}
		if ctx.Err() != nil {
			return ctx.Err()
		}
		say("GitHub sign-in did not finish in the window; here is how to do it in a terminal instead.")
	}

	prompt := "Sign in to GitHub. In a terminal, run:\n\n" + hint + "\n\nThen choose Continue here."
	if !refresh {
		prompt = "Sign in to GitHub. In a terminal, run:\n\n" + hint + "\n\nIf you need a GitHub account, create one first:\nhttps://github.com/signup\n\nThen choose Continue here."
	}
	if err := engine.Approve(ctx, ui, "github-signin", prompt); err != nil {
		return err
	}
	if res, err := s.gh(ctx, "auth", "token", "--hostname", "github.com"); err != nil || !res.OK() {
		return errors.New("still not signed in to GitHub; run the sign-in command and try again")
	}
	return nil
}

// validateSession asks GitHub who we are. A brief outage is retried; rejected
// credentials are reported as bad so the caller can offer a refresh.
func (s *Step) validateSession(ctx context.Context, say func(string)) (login string, bad bool, err error) {
	var last string
	for attempt := 1; attempt <= 3; attempt++ {
		res, runErr := s.gh(ctx, "api", "user", "--jq", ".login")
		if runErr != nil {
			return "", false, runErr
		}
		if res.OK() {
			return strings.TrimSpace(res.Stdout), false, nil
		}
		last = strings.TrimSpace(res.Stderr)
		lower := strings.ToLower(last)
		if strings.Contains(lower, "http 401") || strings.Contains(lower, "bad credentials") {
			return "", true, nil
		}
		if attempt < 3 {
			say(fmt.Sprintf("GitHub was temporarily unavailable; retrying (%d/3).", attempt))
			s.Sleep(2e9)
		}
	}
	if strings.Contains(last, "invalid character '<'") {
		last = "GitHub returned an unavailable-service page instead of an API answer"
	}
	return "", false, fmt.Errorf("setup paused because GitHub could not validate the session (your credential was kept and nothing was changed): %s", last)
}

// ---- choosing the repository -----------------------------------------------

// configure ends with origin pointing at a usable private repository and main
// published there. It returns that repository as owner/name.
func (s *Step) configure(ctx context.Context, ui engine.UI, say func(string), login string) (string, error) {
	origin := s.origin(ctx)

	// Already on a usable private origin: normalise the remotes and publish.
	if current := github.Repo(origin); current != "" && !github.IsUpstream(origin) {
		if m, state, err := s.lookup(ctx, current); err != nil {
			return "", err
		} else if state == exists {
			if ok, _ := s.usable(ctx, current, m); ok {
				if err := s.setOrigin(ctx, origin); err != nil {
					return "", err
				}
				say("Using private origin: " + origin)
				return current, s.trackBranch(ctx, say)
			}
		}
	}

	owner := login
	_, interactive := ui.(engine.StageRetrier)
	if interactive {
		owners, err := s.repositoryOwners(ctx, login)
		if err != nil {
			return "", err
		}
		owner, err = chooseRepositoryOwner(ctx, ui, owners)
		if err != nil {
			return "", err
		}
	}
	question := engine.Screen{ID: "create-or-reuse-repository", Kind: engine.Confirm, Prompt: "Create or reuse a private recovery repository now?", Default: "yes"}
	if interactive {
		question = engine.Screen{ID: question.ID, Kind: engine.Choice, Prompt: "How should Adaetum handle your recovery repository?", Options: []string{reuseRepository, createRepository}}
	}
	answer, err := ui.Ask(ctx, question)
	if err != nil {
		return "", err
	}
	if answer == switchAccountAnswer {
		return "", errSwitchAccount
	}
	if answer == reuseRepository {
		return s.selectExisting(ctx, ui, say, owner)
	}
	if answer != createRepository && !strings.EqualFold(answer, "yes") && !strings.EqualFold(answer, "y") && !strings.EqualFold(answer, "true") {
		return "", engine.ErrDeclined
	}
	creating := answer == createRepository
	var suggested string
	if creating {
		suggested, err = s.findAvailable(ctx, owner)
	} else {
		suggested, err = s.findPreferred(ctx, login)
	}
	if err != nil {
		return "", err
	}

	for {
		question := engine.Screen{ID: "repository", Kind: engine.Text, Prompt: "Private recovery repository (owner/name)", Default: suggested}
		if creating {
			_, name, _ := strings.Cut(suggested, "/")
			question.Title = "Name your recovery repository"
			question.Intro = "It will be created as a private repository under " + owner + "."
			question.Prompt = "Repository name"
			question.Default = name
			question.YesLabel = "Create repository"
			question.Creates = true
		}
		repo, err := ui.Ask(ctx, question)
		if err != nil {
			return "", err
		}
		repo = strings.TrimSpace(repo)
		if creating {
			repo = owner + "/" + repo
		}
		owner, name, ok := strings.Cut(repo, "/")
		if !ok || owner == "" || name == "" || strings.Contains(name, "/") {
			say("Enter owner/name, for example " + suggested + ".")
			if !interactive {
				return "", fmt.Errorf("invalid repository %q", repo)
			}
			continue
		}

		m, state, err := s.lookup(ctx, repo)
		if err != nil {
			return "", err
		}
		if state == exists {
			if creating {
				say("That repository already exists. Choose another name or select reuse on retry.")
				if suggested, err = s.findAvailable(ctx, owner); err != nil {
					return "", err
				}
				continue
			}
			if usable, why := s.usable(ctx, repo, m); !usable {
				say(why)
				if suggested, err = s.findAvailable(ctx, owner); err != nil {
					return "", err
				}
				continue
			}
			if err := engine.Approve(ctx, ui, "reuse-repository", "Reuse the private Adaetum recovery repository "+repo+"?"); err != nil {
				return "", err
			}
		} else {
			if !creating {
				if err := engine.ApproveCreate(ctx, ui, "create-repository", "Create "+repo+" as a private repository now?"); err != nil {
					return "", err
				}
			}
			say("Creating private recovery repository...")
			if _, err := s.ghOut(ctx, "create the repository", "repo", "create", repo, "--private",
				"--description", "Private Adaetum cluster configuration and recovery"); err != nil {
				return "", err
			}
		}

		if err := s.setOrigin(ctx, "https://github.com/"+repo+".git"); err != nil {
			return "", err
		}
		if err := s.trackBranch(ctx, say); err != nil {
			return "", err
		}
		say("Private recovery repository ready: https://github.com/" + repo + ".git")
		return repo, nil
	}
}

func (s *Step) selectExisting(ctx context.Context, ui engine.UI, say func(string), owner string) (string, error) {
	say("Finding private repositories you can administer...")
	repos, err := s.reusableRepositories(ctx, owner)
	if err != nil {
		return "", err
	}
	if len(repos) == 0 {
		return "", fmt.Errorf("no eligible private recovery repositories found under %s; retry this step and choose to create one", owner)
	}
	repo, err := engine.Choose(ctx, ui, "reuse-repository-select", "Which recovery repository would you like to use?", repos, "")
	if err != nil {
		return "", err
	}
	// Recheck after selection: visibility or permissions may have changed while
	// the operator was deciding, and a missing repository must never be created.
	m, state, err := s.lookup(ctx, repo)
	if err != nil {
		return "", err
	}
	if state != exists {
		return "", fmt.Errorf("%s no longer exists or is inaccessible", repo)
	}
	if ok, why := s.usable(ctx, repo, m); !ok {
		return "", errors.New(why)
	}
	if err := s.setOrigin(ctx, "https://github.com/"+repo+".git"); err != nil {
		return "", err
	}
	if err := s.trackBranch(ctx, say); err != nil {
		return "", err
	}
	say("Private recovery repository ready: https://github.com/" + repo + ".git")
	return repo, nil
}

// setOrigin points origin (fetch and push) at the recovery repository and keeps
// canonical Adaetum as upstream. Both URLs are set on every run: `task reset`
// points both at public upstream, and a checkout that fetches privately but still
// pushes to Adaetum/Adaetum would publish the operator's configuration publicly.
func (s *Step) setOrigin(ctx context.Context, recoveryURL string) error {
	current := s.origin(ctx)
	if res, err := s.git(ctx, "remote", "get-url", "upstream"); err != nil || !res.OK() {
		upstream := github.UpstreamURL
		if current != "" && github.IsUpstream(current) {
			upstream = current
		}
		if _, err := s.gitOut(ctx, "add the upstream remote", "remote", "add", "upstream", upstream); err != nil {
			return err
		}
	}
	if current != "" {
		if _, err := s.gitOut(ctx, "set origin", "remote", "set-url", "origin", recoveryURL); err != nil {
			return err
		}
		_, err := s.gitOut(ctx, "set origin push URL", "remote", "set-url", "--push", "origin", recoveryURL)
		return err
	}
	_, err := s.gitOut(ctx, "add origin", "remote", "add", "origin", recoveryURL)
	return err
}

// ---- publishing main -------------------------------------------------------

func (s *Step) remoteHead(ctx context.Context) string {
	res, err := s.git(ctx, "ls-remote", "--heads", "origin", "main")
	if err != nil || !res.OK() {
		return ""
	}
	fields := strings.Fields(res.Stdout)
	if len(fields) == 0 {
		return ""
	}
	return fields[0]
}

func (s *Step) isAncestor(ctx context.Context, ancestor, descendant string) bool {
	res, err := s.git(ctx, "merge-base", "--is-ancestor", ancestor, descendant)
	return err == nil && res.OK()
}

// trackBranch publishes the selected checkout to private main without switching
// branches or replacing the tree with another branch's files. Existing private
// history is joined before the saved profile is restored and rendered.
func (s *Step) trackBranch(ctx context.Context, say func(string)) error {
	branch, err := s.gitOut(ctx, "read the current branch", "branch", "--show-current")
	if err != nil {
		return err
	}
	if branch == "" {
		return errors.New("setup requires a named Git branch before it can publish recovery state")
	}
	if dirty, err := s.gitOut(ctx, "check for changes", "status", "--porcelain", "--untracked-files=no"); err != nil {
		return err
	} else if dirty != "" {
		return errors.New("setup requires a clean tracked worktree; commit or stash changes, then rerun")
	}
	remoteHead := s.remoteHead(ctx)
	s.PriorRecovery = remoteHead != ""
	if remoteHead != "" {
		if _, err := s.gitOut(ctx, "fetch origin main", "fetch", "origin", "+refs/heads/main:refs/remotes/origin/main"); err != nil {
			return err
		}
	}
	var saved []byte
	if remoteHead != "" {
		if res, err := s.git(ctx, "show", "origin/main:platform.yaml"); err == nil && res.OK() {
			saved = []byte(res.Stdout)
		}
	}
	if saved == nil {
		saved, err = os.ReadFile(filepath.Join(s.Dir, "platform.yaml"))
		if err != nil {
			return errors.New("setup cannot preserve the recovery profile because platform.yaml is missing")
		}
	}
	mergePending := false
	if remoteHead != "" && !s.isAncestor(ctx, "origin/main", "HEAD") {
		if _, err := s.gitOut(ctx, "join recovery history", "merge", "--no-ff", "--no-commit", "--strategy=ours", "--allow-unrelated-histories", "origin/main"); err != nil {
			return err
		}
		mergePending = true
	}
	if err := os.WriteFile(filepath.Join(s.Dir, "platform.yaml"), saved, 0o644); err != nil {
		return fmt.Errorf("restore platform.yaml: %w", err)
	}
	say("Rendering the recovery profile on the selected checkout...")
	if err := s.Render(ctx); err != nil {
		return err
	}
	if _, err := s.gitOut(ctx, "stage the changes", "add", "-u"); err != nil {
		return err
	}
	if res, err := s.git(ctx, "diff", "--cached", "--quiet"); mergePending || err != nil || !res.OK() {
		if _, err := s.gitOut(ctx, "commit", "commit", "-m", commitReason); err != nil {
			return err
		}
	}
	localHead, err := s.gitOut(ctx, "read HEAD", "rev-parse", "HEAD")
	if err != nil {
		return err
	}
	say("Publishing the selected checkout to private recovery main...")
	if _, err := s.gitOut(ctx, "publish main", "push", "origin", "HEAD:refs/heads/main"); err != nil {
		return err
	}
	if s.remoteHead(ctx) != localHead {
		return errors.New("recovery publication failed: origin/main does not match the local HEAD")
	}
	if _, err := s.gitOut(ctx, "fetch origin main", "fetch", "origin", "+refs/heads/main:refs/remotes/origin/main"); err != nil {
		return err
	}
	say("Private recovery main now matches the selected checkout.")
	return nil
}

// ---- GitHub Actions --------------------------------------------------------

func (s *Step) workflowCount(ctx context.Context, repo string) (int, error) {
	res, err := s.gh(ctx, "api", "repos/"+repo+"/actions/workflows", "--jq", ".total_count // 0")
	if err != nil || !res.OK() {
		return 0, err
	}
	n, _ := strconv.Atoi(strings.TrimSpace(res.Stdout))
	return n, nil
}

// ensureActions makes main the default branch (recovery always runs from main) and
// waits for GitHub to register the workflows, then explains the specific reason if
// it does not.
func (s *Step) ensureActions(ctx context.Context, repo string, say func(string)) error {
	m, state, err := s.lookup(ctx, repo)
	if err != nil {
		return err
	}
	if state != exists {
		return fmt.Errorf("%s disappeared while setup was running", repo)
	}
	branch := m.DefaultBranch
	if branch != "main" {
		if res, err := s.gh(ctx, "api", "repos/"+repo+"/branches/main"); err != nil || !res.OK() {
			say("Publishing the main workflow branch...")
			if _, err := s.gitOut(ctx, "publish main", "push", "origin", "HEAD:refs/heads/main"); err != nil {
				return err
			}
		}
		say("Selecting main as the default workflow branch...")
		if _, err := s.ghOut(ctx, "set the default branch", "repo", "edit", repo, "--default-branch", "main"); err != nil {
			return err
		}
		branch = "main"
	}

	say("Waiting for GitHub to register workflows in " + repo + "...")
	for attempt := 1; attempt <= 5; attempt++ {
		if n, _ := s.workflowCount(ctx, repo); n > 0 {
			say(fmt.Sprintf("GitHub Actions is enabled with %d registered workflow(s).", n))
			return nil
		}
		s.Sleep(2e9)
	}

	perms, _ := s.gh(ctx, "api", "repos/"+repo+"/actions/permissions", "--jq", ".enabled // false")
	files, _ := s.gh(ctx, "api", "repos/"+repo+"/contents/.github/workflows?ref="+branch, "--jq", "length")
	switch {
	case m.Fork:
		return errors.New("this repository is still a public GitHub fork; GitHub needs one-time consent in the browser before its workflows run: https://github.com/" + repo + "/actions")
	case strings.TrimSpace(perms.Stdout) != "true":
		return fmt.Errorf("GitHub Actions is disabled for %s; enable it in Settings → Actions → General, then rerun", repo)
	case strings.TrimSpace(files.Stdout) == "0" || strings.TrimSpace(files.Stdout) == "":
		return fmt.Errorf("no workflow files are visible on %s's default branch (%s)", repo, branch)
	}
	return fmt.Errorf("GitHub can see %s workflow file(s) on %s but has not indexed them; check the repository's Actions tab or https://www.githubstatus.com, then rerun", strings.TrimSpace(files.Stdout), branch)
}
