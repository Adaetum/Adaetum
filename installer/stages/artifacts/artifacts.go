// Package artifacts is the stage that publishes everything the first server needs
// from outside this machine: the installer media and break-glass bundle in R2, the
// secrets in the GitHub environment, and the three workflows that build and publish
// the kickstart Worker and ISOs, alongside building the installer ISO locally.
//
// It ports tasks/scripts/run-initial-setup.sh (as run by setup) and replaces
// manage-github-secrets.py. Two steps still run through the Taskfile because they
// are not setup logic and the workflows use them too: `bundle:publish:external` and
// `build-iso`. They are called from one place (see tasks below).
//
// What stops setup and what does not follows the old script: a failed ISO upload,
// bundle publication or secret sync is reported and setup continues; bad repository
// credentials stop it; a failed workflow is collected and fails the stage at the end,
// except ks-worker when the bundle was already uploaded directly.
package artifacts

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"time"

	"github.com/Adaetum/Adaetum/installer/diagnostics"
	"github.com/Adaetum/Adaetum/installer/engine"
	"github.com/Adaetum/Adaetum/installer/envfile"
	"github.com/Adaetum/Adaetum/installer/github"
	"github.com/Adaetum/Adaetum/installer/githubapi"
	"github.com/Adaetum/Adaetum/installer/sys"
)

const stepID = "artifacts"

// Step is the artifacts stage.
type Step struct {
	Run     sys.Runner
	Root    string
	LogRoot string // retain redacted detail logs outside the disposable checkout
	// GH builds the GitHub client for a token. Tests point it at a local server.
	GH func(token string) githubapi.Client
	// Sleep, PollEvery and MaxPolls shape how long a workflow is watched.
	Sleep     func(time.Duration)
	PollEvery time.Duration
	MaxPolls  int
}

// New returns the stage for the checkout at root.
func New(run sys.Runner, root string) *Step {
	return &Step{
		Run: run, Root: root, LogRoot: root, Sleep: time.Sleep, PollEvery: 5 * time.Second, MaxPolls: 180,
		GH: func(token string) githubapi.Client { return githubapi.Client{Token: token} },
	}
}

func (s *Step) ID() string    { return stepID }
func (s *Step) Title() string { return "Publish bootstrap artifacts" }

func (s *Step) Check(context.Context, *engine.Env) (engine.Finding, error) {
	return engine.Finding{Status: engine.Needed, Detail: "runs after the configuration is published"}, nil
}

func (s *Step) Plan(context.Context, *engine.Env) ([]string, error) {
	return []string{
		"Upload the installer ISOs to your Cloudflare R2 bucket (an image of the same size already there is skipped).",
		"Check that the repository credentials work, then build and publish the break-glass bundle.",
		"Encrypt and sync the required secrets to your private repository's Prod GitHub environment.",
		"Back up the setup secrets in the recovery folder of your R2 bucket and verify the copy.",
		"Trigger the ks-worker, ks-publish and iso-build workflows, and build the installer ISO on this machine at the same time.",
		"Wait for the workflows and report any that fail.",
	}, nil
}

// ---- helpers ----------------------------------------------------------------------

type run struct {
	s       *Step
	say     func(string)
	log     string
	values  map[string]string // .env
	secrets []string
}

// logged runs a command, appending its output to the detail log (never secrets: they
// are in the environment, not the output).
func (r *run) logged(ctx context.Context, env []string, name string, args ...string) (sys.Result, error) {
	res, err := r.s.Run.RunEnv(ctx, r.s.Root, env, "", name, args...)
	if f, ferr := os.OpenFile(r.log, os.O_APPEND|os.O_WRONLY|os.O_CREATE, 0o600); ferr == nil {
		values := append(append([]string{}, r.secrets...), diagnostics.EnvValues(env)...)
		for key, value := range r.values {
			if diagnostics.SensitiveKey(key) {
				values = append(values, strings.Trim(value, "\"'"))
			}
		}
		fmt.Fprint(f, diagnostics.Redact(fmt.Sprintf("$ %s %s\n%s%s\n", name, strings.Join(args, " "), res.Stdout, res.Stderr), values...))
		f.Close()
	}
	return res, err
}

// dotenv is .env as KEY=VALUE, for the tasks that read it from their environment.
func (r *run) dotenv() []string {
	var out []string
	for k, v := range r.values {
		out = append(out, k+"="+v)
	}
	return out
}

// tasks is the one place the installer calls the Taskfile. These steps are not
// setup logic and the GitHub workflows run them too; when the Taskfile changes,
// this is what changes with it.
func (r *run) task(ctx context.Context, extraEnv []string, name string, args ...string) error {
	res, err := r.logged(ctx, append(r.dotenv(), extraEnv...), "task", append([]string{name}, args...)...)
	if err != nil {
		return fmt.Errorf("task %s: %w", name, err)
	}
	if !res.OK() {
		return fmt.Errorf("task %s failed. Details: %s", name, r.log)
	}
	return nil
}

func python() string {
	if runtime.GOOS == "windows" {
		return "python"
	}
	return "python3"
}

// cacheBuster changes whenever the runtime payload does, so nodes that cached the
// old one fetch the new one. It hashes dist/bootstrap-runtime.env when that exists,
// and otherwise .env without its own cache-buster line.
func cacheBuster(root string) (string, error) {
	var payload []byte
	if data, err := os.ReadFile(filepath.Join(root, "dist", "bootstrap-runtime.env")); err == nil {
		payload = data
	} else {
		data, err := os.ReadFile(filepath.Join(root, ".env"))
		if err != nil {
			return "", err
		}
		var keep []string
		for _, line := range strings.Split(strings.TrimRight(string(data), "\n"), "\n") {
			if !strings.HasPrefix(line, "BOOTSTRAP_RUNTIME_ENV_CACHE_BUSTER=") {
				keep = append(keep, line)
			}
		}
		payload = []byte(strings.Join(keep, "\n"))
	}
	sum := sha256.Sum256(payload)
	return hex.EncodeToString(sum[:])[:16], nil
}

func (r *run) updateCacheBuster() error {
	v, err := cacheBuster(r.s.Root)
	if err != nil {
		return fmt.Errorf("could not compute BOOTSTRAP_RUNTIME_ENV_CACHE_BUSTER: %w", err)
	}
	if err := envfile.Upsert(filepath.Join(r.s.Root, ".env"), "BOOTSTRAP_RUNTIME_ENV_CACHE_BUSTER", v); err != nil {
		return err
	}
	r.values["BOOTSTRAP_RUNTIME_ENV_CACHE_BUSTER"] = v
	return nil
}

// ---- the stage ----------------------------------------------------------------------

func (s *Step) Apply(ctx context.Context, env *engine.Env, ui engine.UI) error {
	say := func(msg string) { ui.Report(engine.Event{StepID: stepID, Message: msg}) }

	token := env.Secret("github_token")
	if token == "" {
		return errors.New("the GitHub credential is missing; run setup from the start")
	}
	iso := env.Value("iso_path")
	if iso == "" {
		return errors.New("the source installer ISO is unknown; run setup from the start")
	}
	envPath := filepath.Join(s.Root, ".env")
	values := envfile.Read(envPath)
	if len(values) == 0 {
		return errors.New("the rendered .env is missing; run the render stage first")
	}

	logRoot := s.LogRoot
	if logRoot == "" {
		logRoot = s.Root
	}
	logPath := filepath.Join(logRoot, ".logs", "initial-setup-details.log")
	if err := os.MkdirAll(filepath.Dir(logPath), 0o700); err != nil {
		return err
	}
	r := &run{s: s, say: say, log: logPath, values: values}
	for _, value := range env.Secrets {
		r.secrets = append(r.secrets, value)
	}

	repo := github.RepoExact(gitConfig(ctx, s, "remote.origin.url"))
	if repo == "" {
		return errors.New("unable to determine the GitHub repository from origin")
	}
	branch := "main"
	if branch == "" {
		return errors.New("a checked-out branch is required to trigger workflows (a detached HEAD is not supported)")
	}
	gh := s.GH(token)
	ghEnv := os.Getenv("GITHUB_ENVIRONMENT")
	if ghEnv == "" {
		ghEnv = "Prod"
	}

	if err := r.updateCacheBuster(); err != nil {
		return err
	}

	// 1. Installer media. A failed upload is a warning: the workflows and a rerun can
	// still publish it.
	say("Uploading installer images to R2...")
	if err := r.goldenISOs(ctx); err != nil {
		say("Warning: installer upload failed; continuing. " + err.Error())
	}

	// 2. Repository credentials must work before anything is published.
	if err := r.validateSourceAuth(ctx); err != nil {
		return err
	}
	if err := r.validateMirror(ctx, gh); err != nil {
		return err
	}

	// 3. The break-glass bundle.
	bundleUploaded := false
	say("Building and publishing the break-glass bundle...")
	if err := r.task(ctx, nil, "bundle:publish:external"); err != nil {
		say("Warning: break-glass bundle publication failed; continuing. " + err.Error())
	} else {
		bundleUploaded = true
		say("Break-glass bundle built and published.")
	}
	if _, err := os.Stat(filepath.Join(s.Root, "dist", "bootstrap-runtime.env")); err == nil {
		if err := r.updateCacheBuster(); err != nil {
			return err
		}
	}

	// 4. Secrets.
	say("Syncing secrets to GitHub...")
	fresh := envfile.Read(envPath)
	if summary, err := syncSecrets(ctx, gh, repo, ghEnv, fresh); err != nil {
		say("Warning: GitHub secret sync failed; continuing. " + err.Error())
		say(summary)
	} else {
		say(summary)
	}

	// 5. Workflows, with the local ISO build running alongside.
	var errs []string
	var runs []dispatched
	for _, wf := range []struct{ file, input string }{
		{"ks-worker.yml", ""}, {"ks-publish.yml", ""}, {"iso-build.yml", env.Value("ks_template_file_name")},
	} {
		r.warnIfUncommitted(ctx, wf.file)
		inputs := map[string]string{}
		if wf.input != "" {
			inputs["ks_template_file_name"] = wf.input
		}
		id, err := s.dispatch(ctx, gh, repo, branch, wf.file, inputs, say)
		if err != nil {
			if wf.file == "ks-worker.yml" && bundleUploaded {
				say("Warning: ks-worker.yml could not be dispatched, but the live Worker bundle upload was already verified.")
				continue
			}
			errs = append(errs, wf.file+": "+err.Error())
			continue
		}
		runs = append(runs, dispatched{wf.file, id})
		say(fmt.Sprintf("%s triggered (run %d).", wf.file, id))
	}

	var wg sync.WaitGroup
	var isoErr error
	wg.Add(1)
	go func() {
		defer wg.Done()
		rel := iso
		if p, err := filepath.Rel(s.Root, iso); err == nil && !strings.HasPrefix(p, "..") {
			rel = p
		}
		say("Building the local installer ISO...")
		isoErr = r.task(ctx, []string{"ISO_PATH=" + rel}, "build-iso")
	}()

	for _, d := range runs {
		if err := s.wait(ctx, gh, repo, d, say); err != nil {
			if d.file == "ks-worker.yml" && bundleUploaded {
				say("Warning: " + err.Error() + " The break-glass bundle was already uploaded directly; re-run the workflow later if the Worker itself needs redeploying.")
				continue
			}
			errs = append(errs, err.Error())
		}
	}
	wg.Wait()
	if isoErr != nil {
		say("Warning: the local ISO build failed: " + isoErr.Error())
	} else {
		say("Local ISO build completed.")
	}

	// The recovery copy is intentionally readable by the bucket owner. Keep it
	// outside the Worker's supported artifact names and never log its contents.
	say("Saving setup recovery secrets to R2...")
	v := envfile.Read(envPath)
	for _, k := range []string{"R2_ENDPOINT", "R2_BUCKET", "R2_ACCESS_KEY_ID", "R2_SECRET_ACCESS_KEY"} {
		if v[k] == "" {
			return fmt.Errorf("setup recovery backup: missing %s", k)
		}
	}
	bucket := r2{run: s.Run, dir: s.Root, endpoint: v["R2_ENDPOINT"], bucket: v["R2_BUCKET"], key: v["R2_ACCESS_KEY_ID"], secret: v["R2_SECRET_ACCESS_KEY"]}
	key := "recovery/setup-" + time.Now().UTC().Format("20060102T150405.000000000Z")
	if err := bucket.backupSetup(ctx, envPath, key); err != nil {
		return fmt.Errorf("setup recovery backup: %w", err)
	}
	say("Setup recovery secrets saved and verified in R2: " + v["R2_BUCKET"] + "/" + key)

	if len(errs) > 0 {
		return fmt.Errorf("one or more workflow runs did not complete successfully:\n%s", strings.Join(errs, "\n"))
	}
	return nil
}

func gitConfig(ctx context.Context, s *Step, key string) string {
	res, err := s.Run.Run(ctx, s.Root, "git", "config", "--get", key)
	if err != nil || !res.OK() {
		return ""
	}
	return strings.TrimSpace(res.Stdout)
}

func currentBranch(ctx context.Context, s *Step) string {
	res, err := s.Run.Run(ctx, s.Root, "git", "rev-parse", "--abbrev-ref", "HEAD")
	if err != nil || !res.OK() {
		return ""
	}
	if b := strings.TrimSpace(res.Stdout); b != "HEAD" {
		return b
	}
	return ""
}

// ---- installer media ---------------------------------------------------------------

func (r *run) goldenISOs(ctx context.Context) error {
	v := r.values
	for _, k := range []string{"R2_ENDPOINT", "R2_BUCKET", "R2_ACCESS_KEY_ID", "R2_SECRET_ACCESS_KEY"} {
		if v[k] == "" {
			return fmt.Errorf("missing %s in .env", k)
		}
	}
	// The kickstart templates declare where each image belongs in the bucket, so they
	// are compiled first.
	if err := r.compileKickstarts(ctx); err != nil {
		return err
	}
	isos := rootISOs(r.s.Root)
	if len(isos) == 0 {
		return errors.New("no *.iso files found in the checkout")
	}
	bucket := r2{run: r.s.Run, dir: r.s.Root, endpoint: v["R2_ENDPOINT"], bucket: v["R2_BUCKET"], key: v["R2_ACCESS_KEY_ID"], secret: v["R2_SECRET_ACCESS_KEY"]}
	golden := goldenKeys(r.s.Root)
	uploaded, skipped, failed := 0, 0, 0
	for _, path := range isos {
		file := filepath.Base(path)
		var keys []string
		for _, k := range golden {
			if filepath.Base(k) == file {
				keys = append(keys, k)
			}
		}
		if len(keys) == 0 {
			keys = []string{defaultKey(file)}
		}
		for _, key := range keys {
			result, err := bucket.upload(ctx, path, key)
			switch {
			case err != nil:
				failed++
				r.say("Could not upload " + file + ": " + err.Error())
			case result == "skipped":
				skipped++
			default:
				uploaded++
			}
		}
	}
	r.say(fmt.Sprintf("Installer media: %d uploaded, %d already in R2, %d failed.", uploaded, skipped, failed))
	if failed > 0 {
		return fmt.Errorf("%d upload(s) failed", failed)
	}
	return nil
}

func (r *run) compileKickstarts(ctx context.Context) error {
	if res, err := r.s.Run.Run(ctx, r.s.Root, "uv", "--version"); err == nil && res.OK() {
		res, err := r.logged(ctx, nil, "uv", "run", "--with", "jinja2", "python", "./tasks/scripts/compile-kickstarts.py", "--sync", "--self-test")
		if err != nil || !res.OK() {
			return fmt.Errorf("kickstart compilation failed. Details: %s", r.log)
		}
		return nil
	}
	if res, err := r.s.Run.Run(ctx, r.s.Root, python(), "-c", "import jinja2"); err == nil && res.OK() {
		res, err := r.logged(ctx, nil, python(), "./tasks/scripts/compile-kickstarts.py", "--sync", "--self-test")
		if err != nil || !res.OK() {
			return fmt.Errorf("kickstart compilation failed. Details: %s", r.log)
		}
		return nil
	}
	return errors.New("kickstart compilation requires uv or the Python jinja2 package")
}

// ---- repository credentials ------------------------------------------------------------

// validateSourceAuth proves the credentials Phase 50 will use to clone the recovery
// repository work, by listing its HEAD with them.
func (r *run) validateSourceAuth(ctx context.Context) error {
	v := r.values
	url := firstOf(v["GITEA_SEED_SOURCE_REPO_URL"], v["ARGOCD_GITHUB_REPO_URL"])
	user := firstOf(v["GITEA_SEED_SOURCE_USERNAME"], v["ARGOCD_GITHUB_USERNAME"])
	tok := firstOf(v["GITEA_SEED_SOURCE_TOKEN"], v["ARGOCD_GITHUB_TOKEN"])
	if tok == "" && gitCapable(v["GITHUB_SYNC_TOKEN"]) {
		tok = v["GITHUB_SYNC_TOKEN"]
	}
	if url == "" {
		r.say("Warning: skipping the source repository check; its URL is not set.")
		return nil
	}
	if !strings.HasPrefix(url, "http://") && !strings.HasPrefix(url, "https://") {
		r.say("Warning: skipping the source repository check for a non-HTTP URL.")
		return nil
	}
	if tok == "" {
		if v["GITHUB_APP_ID"] != "" && v["GITHUB_APP_INSTALLATION_ID"] != "" && v["GITHUB_APP_PRIVATE_KEY_B64"] != "" {
			r.say("Warning: skipping the direct source repository check; Phase 50 will mint a GitHub App token at runtime.")
			return nil
		}
		return errors.New("Phase 50 source repository access is not configured: set a git-capable GITHUB_SYNC_TOKEN in .env, or GitHub App credentials")
	}
	if user == "" {
		user = github.RepoExact(url)
		user, _, _ = strings.Cut(user, "/")
	}
	res, err := r.s.Run.RunEnv(ctx, r.s.Root, gitAuthEnv(url, user, tok), "", "git", "ls-remote", "--symref", url, "HEAD")
	if err != nil {
		return err
	}
	if !res.OK() {
		return fmt.Errorf("the source repository check failed for %s. Fix the source repository credentials in .env before continuing", url)
	}
	r.say("Source repository access validated.")
	return nil
}

// validateMirror proves the sync token can push to the mirror target, which the
// in-cluster Gitea push mirror needs.
func (r *run) validateMirror(ctx context.Context, gh githubapi.Client) error {
	v := r.values
	if !gitCapable(v["GITHUB_SYNC_TOKEN"]) {
		return errors.New("GitHub mirror validation failed: GITHUB_SYNC_TOKEN is missing or not git-capable in .env")
	}
	url := firstOf(v["GITEA_PUSH_MIRROR_REPO_URL"], v["GITEA_SEED_SOURCE_REPO_URL"], v["ARGOCD_GITHUB_REPO_URL"])
	if url == "" {
		return errors.New("GitHub mirror validation failed: the mirror target repository is not configured")
	}
	name := github.RepoExact(url)
	if name == "" {
		return fmt.Errorf("GitHub mirror validation failed: the mirror target is not a GitHub repository: %s", url)
	}
	if firstOf(v["GITEA_PUSH_MIRROR_USERNAME"], v["GITEA_SEED_SOURCE_USERNAME"], v["ARGOCD_GITHUB_USERNAME"]) == "" {
		return fmt.Errorf("GitHub mirror validation failed: no mirror username is configured for %s", name)
	}
	info, err := gh.Repo(ctx, name)
	if err != nil {
		return fmt.Errorf("GitHub mirror validation failed: could not read repository metadata for %s with the sync token", name)
	}
	if !info.Permissions.Push {
		return fmt.Errorf("GitHub mirror validation failed: the sync token does not have push access to %s", name)
	}
	r.say("GitHub push mirror access validated (" + name + ").")
	return nil
}

func firstOf(vals ...string) string {
	for _, v := range vals {
		if v != "" {
			return v
		}
	}
	return ""
}

func gitCapable(token string) bool {
	for _, p := range []string{"github_pat_", "ghp_", "gho_", "ghu_", "ghs_"} {
		if strings.HasPrefix(token, p) {
			return true
		}
	}
	return false
}

// gitAuthEnv gives git a credential for one command through environment config, so
// the token is never on the command line.
func gitAuthEnv(url, user, token string) []string {
	return []string{
		"GIT_TERMINAL_PROMPT=0", "GIT_CONFIG_COUNT=2",
		"GIT_CONFIG_KEY_0=credential.helper", "GIT_CONFIG_VALUE_0=",
		"GIT_CONFIG_KEY_1=http." + url + "/.extraheader",
		"GIT_CONFIG_VALUE_1=AUTHORIZATION: basic " + base64Std(user+":"+token),
	}
}

// warnIfUncommitted tells the operator when files a workflow reads have local edits
// that are not committed: GitHub runs the committed branch, not this working copy.
func (r *run) warnIfUncommitted(ctx context.Context, workflow string) {
	paths := map[string][]string{
		"ks-worker.yml":  {".github/workflows/ks-worker.yml", ".env.template"},
		"ks-publish.yml": {".github/workflows/ks-publish.yml", "ks-src", "tasks/ks.yml", "tasks/scripts/compile-kickstarts.py", "Taskfile.yml", ".env.template"},
		"iso-build.yml":  {".github/workflows/iso-build.yml", "ks-src", "tasks/ks.yml", "tasks/scripts/compile-kickstarts.py", "Taskfile.yml", ".env.template"},
	}[workflow]
	for _, args := range [][]string{{"diff", "--quiet", "--"}, {"diff", "--cached", "--quiet", "--"}} {
		res, err := r.s.Run.Run(ctx, r.s.Root, "git", append(args, paths...)...)
		if err == nil && !res.OK() {
			r.say("Warning: " + workflow + " has relevant uncommitted changes. GitHub uses the committed branch, so commit and push them before installing from the generated ISO.")
			return
		}
	}
}
