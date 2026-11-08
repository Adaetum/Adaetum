// Package render is the second bootstrap stage: turn the collected credentials and
// the reviewed profile into the gitignored .env, the rendered GitOps files and the
// GitHub environment secrets, validate all of it, and publish the rendered state to
// the private recovery repository.
//
// It ports "Render and publish configuration" from tasks/scripts/run-opinionated-setup.sh.
// Two things still run in the existing scripts, because they are large and shared
// with the artifacts stage: generate-env-files.sh (writes .env and syncs the
// GitHub environment secrets) and the Python renderer and validators. Everything
// that decides what goes in, what gets committed and how it is pushed is here.
//
// Secrets reach those scripts through a 0600 temporary file that is removed when
// the stage ends, standard input, or environment variables. None is ever an
// argument, and the GitHub token reaches git through GIT_CONFIG_* environment
// variables rather than a "-c" argument.
package render

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"runtime"
	"strings"

	"github.com/Adaetum/Adaetum/installer/diagnostics"
	"github.com/Adaetum/Adaetum/installer/engine"
	"github.com/Adaetum/Adaetum/installer/envfile"
	"github.com/Adaetum/Adaetum/installer/github"
	"github.com/Adaetum/Adaetum/installer/platform"
	"github.com/Adaetum/Adaetum/installer/sys"
)

const (
	stepID       = "render"
	commitMsg    = "Render cluster GitOps state"
	detailLogRel = ".logs/task-init-details.log"
)

// Step is the render-and-publish stage.
type Step struct {
	Run      sys.Runner
	Root     string
	Platform platform.Scripts
	// Random fills b with random bytes. It is a field so tests can be deterministic.
	Random func(b []byte) (int, error)
}

// New returns the stage for the checkout at root.
func New(run sys.Runner, root string) *Step {
	return &Step{Run: run, Root: root, Platform: platform.Scripts{Run: run, Dir: root}, Random: rand.Read}
}

func (s *Step) ID() string    { return stepID }
func (s *Step) Title() string { return "Render and publish configuration" }

func python() string {
	if runtime.GOOS == "windows" {
		return "python"
	}
	return "python3"
}

func (s *Step) Check(ctx context.Context, _ *engine.Env) (engine.Finding, error) {
	if res, err := s.Run.Run(ctx, s.Root, python(), "--version"); err != nil || !res.OK() {
		return engine.Finding{Status: engine.Blocked, Detail: "python3 is required to render the platform profile."}, nil
	}
	return engine.Finding{Status: engine.Needed, Detail: "runs once the inputs are validated"}, nil
}

func (s *Step) Plan(ctx context.Context, env *engine.Env) ([]string, error) {
	f, err := s.Check(ctx, env)
	if err != nil {
		return nil, err
	}
	return []string{
		"Validate the Tailscale OAuth clients and mint the one-day, single-use node auth key.",
		"Keep (or, on a first run, create) the bootstrap backup passphrase. An existing one is never replaced silently.",
		"Write .env (gitignored) and sync the required secrets to your private repository's Prod GitHub environment.",
		"Render the GitOps files from platform.yaml and run the repository's validators.",
		"Commit the rendered files and push them to your private repository, then confirm origin matches.",
		"Right now: " + f.Detail + ".",
	}, nil
}

// ---- running the existing scripts, with a detail log --------------------------

type runCtx struct {
	s       *Step
	log     string
	secrets []string
}

// do runs a command and appends its output to the detail log, which the error
// points at. The log holds script output, never the secrets passed to it.
func (r *runCtx) do(ctx context.Context, what string, env []string, name string, args ...string) error {
	res, err := r.s.Run.RunEnv(ctx, r.s.Root, env, "", name, args...)
	if f, ferr := os.OpenFile(r.log, os.O_APPEND|os.O_WRONLY|os.O_CREATE, 0o600); ferr == nil {
		values := append(append([]string{}, r.secrets...), diagnostics.EnvValues(env)...)
		for key, value := range envfile.Read(filepath.Join(r.s.Root, ".env")) {
			if diagnostics.SensitiveKey(key) {
				values = append(values, strings.Trim(value, "\"'"))
			}
		}
		fmt.Fprint(f, diagnostics.Redact(fmt.Sprintf("$ %s %s\n%s%s\n", name, strings.Join(args, " "), res.Stdout, res.Stderr), values...))
		f.Close()
	}
	if err != nil {
		return fmt.Errorf("%s: %w", what, err)
	}
	if !res.OK() {
		return fmt.Errorf("%s failed. Details: %s", what, r.log)
	}
	return nil
}

func (s *Step) Apply(ctx context.Context, env *engine.Env, ui engine.UI) error {
	say := func(msg string) { ui.Report(engine.Event{StepID: stepID, Message: msg}) }

	for _, need := range []struct{ kind, key string }{
		{"secret", "cloudflare_token"}, {"secret", "github_token"},
		{"secret", "tailscale_oauth_client_id"}, {"secret", "tailscale_oauth_client_secret"},
		{"secret", "tailscale_operator_oauth_client_id"}, {"secret", "tailscale_operator_oauth_client_secret"},
		{"value", "ks_base_url"}, {"value", "tailscale_domain"}, {"value", "iso_path"},
	} {
		var have string
		if need.kind == "secret" {
			have = env.Secret(need.key)
		} else {
			have = env.Value(need.key)
		}
		if have == "" {
			return fmt.Errorf("%s is missing; run setup from the start so the earlier stages provide it", need.key)
		}
	}

	envPath := filepath.Join(s.Root, ".env")
	existing := envfile.Read(envPath)
	if env.Clean {
		existing = map[string]string{}
	}
	logPath := filepath.Join(s.Root, detailLogRel)
	if err := os.MkdirAll(filepath.Dir(logPath), 0o700); err != nil {
		return err
	}
	if err := os.WriteFile(logPath, nil, 0o600); err != nil {
		return err
	}
	r := &runCtx{s: s, log: logPath}
	for _, value := range env.Secrets {
		r.secrets = append(r.secrets, value)
	}

	if err := s.tailscale(ctx, r, env, envPath, say); err != nil {
		return err
	}
	pass, passB64, err := s.passphrase(env, existing, envPath, say)
	if err != nil {
		return err
	}

	origin := s.originURL(ctx)
	repo := github.RepoExact(origin)
	branch := "main"
	if repo == "" {
		return errors.New("unable to determine the private recovery repository from origin")
	}
	if branch == "" {
		return errors.New("unable to determine the recovery repository branch (a detached HEAD is not supported)")
	}
	repoURL := "https://github.com/" + repo + ".git"

	gh := env.Secret("github_token")
	// Argo CD and the Gitea seed pull from the recovery repository with this token
	// when it can push; otherwise they are left for the operator to provide.
	repoToken := ""
	if gitCapable(gh) {
		repoToken = gh
	}
	pairs := [][2]string{
		{"CLOUDFLARE_API_TOKEN", env.Secret("cloudflare_token")},
		{"CLOUDFLARE_ACCOUNT_ID", env.Value("cloudflare_account_id")},
		{"TAILSCALE_USER_API_TOKEN", env.Secret("tailscale_token")},
		{"TAILSCALE_DOMAIN", env.Value("tailscale_domain")},
		{"TAILSCALE_OAUTH_CLIENT_ID", env.Secret("tailscale_oauth_client_id")},
		{"TAILSCALE_OAUTH_CLIENT_SECRET", env.Secret("tailscale_oauth_client_secret")},
		{"TAILSCALE_OPERATOR_OAUTH_CLIENT_ID", env.Secret("tailscale_operator_oauth_client_id")},
		{"TAILSCALE_OPERATOR_OAUTH_CLIENT_SECRET", env.Secret("tailscale_operator_oauth_client_secret")},
	}
	for _, k := range []string{"OPENBAO_TRANSIT_SEAL_ADDRESS", "OPENBAO_TRANSIT_SEAL_TOKEN", "OPENBAO_TRANSIT_SEAL_KEY_NAME",
		"OPENBAO_TRANSIT_SEAL_MOUNT_PATH", "OPENBAO_TRANSIT_SEAL_NAMESPACE", "OPENBAO_TRANSIT_SEAL_TLS_SERVER_NAME", "OPENBAO_TRANSIT_SEAL_CA_CERT_B64"} {
		pairs = append(pairs, [2]string{k, existing[k]})
	}
	pairs = append(pairs,
		[2]string{"KS_BASE_URL", env.Value("ks_base_url")},
		[2]string{"GITHUB_SYNC_TOKEN", gh},
		[2]string{"ARGOCD_GITHUB_REPO_URL", repoURL}, [2]string{"ARGOCD_GITHUB_REPO_BRANCH", branch},
		[2]string{"ARGOCD_GITHUB_USERNAME", existing["ARGOCD_GITHUB_USERNAME"]}, [2]string{"ARGOCD_GITHUB_TOKEN", repoToken},
		[2]string{"GITEA_SEED_SOURCE_REPO_URL", repoURL}, [2]string{"GITEA_SEED_SOURCE_REPO_BRANCH", branch},
		[2]string{"GITEA_SEED_SOURCE_USERNAME", existing["GITEA_SEED_SOURCE_USERNAME"]}, [2]string{"GITEA_SEED_SOURCE_TOKEN", repoToken},
		[2]string{"R2_BUCKET", env.Value("r2_bucket")},
		[2]string{"KS_SHARED_TOKEN", existing["KS_SHARED_TOKEN"]}, [2]string{"KS_UPLOAD_TOKEN", existing["KS_UPLOAD_TOKEN"]},
		[2]string{"BOOTSTRAP_BACKUP_PASSPHRASE", pass}, [2]string{"BOOTSTRAP_BACKUP_PASSPHRASE_B64", passB64},
	)

	// The collected credentials go to generate-env-files.sh in a temporary file that
	// only this user can read, removed whatever happens.
	tmp, err := os.CreateTemp("", "adaetum-setup-inputs-*")
	if err != nil {
		return err
	}
	tmpPath := tmp.Name()
	tmp.Close()
	defer os.Remove(tmpPath)
	if err := envfile.Write(tmpPath, pairs); err != nil {
		return err
	}

	iso := env.Value("iso_path")
	if rel, err := filepath.Rel(s.Root, iso); err == nil && !strings.HasPrefix(rel, "..") {
		iso = rel
	}
	githubEnv := os.Getenv("GITHUB_ENVIRONMENT")
	if githubEnv == "" {
		githubEnv = "Prod"
	}
	ignore := "0"
	if env.Clean {
		ignore = "1"
	}
	say("Rendering environment values and syncing the required secrets to GitHub...")
	if err := r.do(ctx, "Environment rendering", []string{
		"ADAETUM_LOCAL_ISO_PATH=" + iso, "WRITE_VM_ENV=0", "NON_INTERACTIVE=1", "OPINIONATED_GITHUB_TOKEN_MODE=1",
		"REQUIRE_CLOUDFLARE_BOOTSTRAP=1", "REQUIRE_TAILSCALE_BOOTSTRAP=1", "TAILSCALE_BOOTSTRAP_VALIDATED=1",
		"GITHUB_SYNC=1", "GITHUB_SYNC_REQUIRED=1", "GITHUB_ENVIRONMENT=" + githubEnv, "ADAETUM_IGNORE_EXISTING_ENV=" + ignore,
	}, "bash", "./tasks/scripts/generate-env-files.sh", ".env", tmpPath); err != nil {
		return err
	}
	if err := r.do(ctx, "The rendered GitHub credentials are invalid;", nil, "bash", "./tasks/scripts/validate-opinionated-github-token-contract.sh", ".env"); err != nil {
		return err
	}
	rendered := envfile.Read(envPath)
	if rendered["BOOTSTRAP_BACKUP_PASSPHRASE"] == "" || rendered["BOOTSTRAP_BACKUP_PASSPHRASE_B64"] == "" {
		return errors.New("environment rendering omitted the bootstrap backup passphrase; setup stopped before GitOps publication")
	}
	say("Environment file generated and required credentials synced to GitHub secrets.")

	say("Rendering the GitOps files from platform.yaml and validating them...")
	py := python()
	for _, step := range []struct {
		what, name string
		args       []string
	}{
		{"Rendering platform.yaml", py, []string{"./tasks/scripts/render-platform-profile.py", "--profile", "./platform.yaml", "--runtime-env", ".env",
			"--output-env", "./dist/platform.env", "--config-file", "./pods/cluster-config/cluster-config.env", "--render-pods"}},
		{"Pods config check", py, []string{"./tasks/scripts/render-pods-config.py", "--check"}},
		{"Pods consistency check", py, []string{"./.validator/validate-pods-consistency.py"}},
		{"Example-placeholder check", py, []string{"./.validator/validate-no-example-placeholders.py"}},
		{"Ingress contract check", py, []string{"./.validator/validate-ingress-contract.py"}},
		{"Bootstrap runtime payload check", "bash", []string{"./tasks/scripts/validate-bootstrap-runtime-env.sh"}},
	} {
		if err := r.do(ctx, step.what, nil, step.name, step.args...); err != nil {
			return err
		}
	}
	say("Rendered configuration validated.")

	return s.publish(ctx, env, repo, repoURL, branch, say)
}

// ---- Tailscale auth key ---------------------------------------------------------

// tailscale validates both OAuth clients and mints the node auth key, all with the
// credentials on standard input, then records the results in .env.
func (s *Step) tailscale(ctx context.Context, r *runCtx, env *engine.Env, envPath string, say func(string)) error {
	id, secret := env.Secret("tailscale_oauth_client_id"), env.Secret("tailscale_oauth_client_secret")
	say("Validating the Tailscale OAuth credentials before rendering...")
	tag := env.Value("cluster_tag")
	if tag == "" {
		tag = "tag:cluster"
	}
	res, err := s.Run.RunInput(ctx, s.Root, id+"\n"+secret+"\n", python(), "./tasks/scripts/bootstrap-tailscale.py",
		"--oauth-credentials-stdin", "--tailnet", env.Value("tailscale_domain"), "--cluster-tag", tag)
	if err != nil {
		return err
	}
	if !res.OK() || strings.TrimSpace(res.Stdout) == "" {
		return fmt.Errorf("unable to validate the Tailscale OAuth credentials: %s", strings.TrimSpace(res.Stderr))
	}
	out := map[string]string{}
	for _, line := range strings.Split(res.Stdout, "\n") {
		if k, v, ok := strings.Cut(strings.TrimRight(line, "\r"), "="); ok {
			out[k] = v
		}
	}
	for k, v := range map[string]string{
		"TAILSCALE_OAUTH_CLIENT_ID": id, "TAILSCALE_OAUTH_CLIENT_SECRET": secret,
		"TAILSCALE_CLUSTER_TAG": out["TAILSCALE_CLUSTER_TAG"], "TAILSCALE_ADVERTISE_TAGS": out["TAILSCALE_ADVERTISE_TAGS"],
		"TAILSCALE_AUTHKEY": out["TAILSCALE_AUTHKEY"],
	} {
		if v == "" {
			continue
		}
		if err := envfile.Upsert(envPath, k, v); err != nil {
			return err
		}
	}
	say("Tailscale OAuth credentials validated and a single-use node auth key minted.")

	oid, osecret := env.Secret("tailscale_operator_oauth_client_id"), env.Secret("tailscale_operator_oauth_client_secret")
	res, err = s.Run.RunInput(ctx, s.Root, oid+"\n"+osecret+"\n", python(), "./tasks/scripts/bootstrap-tailscale.py",
		"--oauth-credentials-stdin", "--validate-operator-oauth-only")
	if err != nil {
		return err
	}
	if !res.OK() {
		return errors.New("unable to validate the Tailscale Kubernetes Operator OAuth client")
	}
	for k, v := range map[string]string{"TAILSCALE_OPERATOR_OAUTH_CLIENT_ID": oid, "TAILSCALE_OPERATOR_OAUTH_CLIENT_SECRET": osecret} {
		if err := envfile.Upsert(envPath, k, v); err != nil {
			return err
		}
	}
	say("Tailscale Kubernetes Operator OAuth client validated.")
	return nil
}

// ---- backup passphrase ----------------------------------------------------------

// passphrase keeps the bootstrap backup passphrase stable. Emergency kits already
// made are encrypted with it, so replacing it silently would orphan them: an
// existing one is reused, and a missing one is only generated on a first run or
// when rotation is explicitly allowed.
func (s *Step) passphrase(env *engine.Env, existing map[string]string, envPath string, say func(string)) (string, string, error) {
	pass := strings.TrimSpace(existing["BOOTSTRAP_BACKUP_PASSPHRASE"])
	b64 := strings.TrimSpace(existing["BOOTSTRAP_BACKUP_PASSPHRASE_B64"])
	if pass == "" && b64 != "" {
		if raw, err := base64.StdEncoding.DecodeString(b64); err == nil {
			pass = strings.TrimSpace(string(raw))
		}
	}
	if pass == "" {
		rotate := strings.ToLower(os.Getenv("SETUP_ALLOW_BACKUP_PASSPHRASE_ROTATE"))
		if hasPriorState(existing) && rotate != "1" && rotate != "true" && rotate != "yes" {
			return "", "", errors.New("BOOTSTRAP_BACKUP_PASSPHRASE is missing even though prior setup state exists. A new one would orphan older emergency kits, so setup will not generate one automatically. Restore the original passphrase into .env, or set SETUP_ALLOW_BACKUP_PASSPHRASE_ROTATE=1 if you are rotating it on purpose")
		}
		buf := make([]byte, 18)
		if _, err := s.Random(buf); err != nil {
			return "", "", err
		}
		pass = base64.RawURLEncoding.EncodeToString(buf)
		sum := sha256.Sum256([]byte(pass))
		say("Created a new backup passphrase (fingerprint " + hex.EncodeToString(sum[:])[:12] + "). Keep a copy of the one in .env.")
	}
	if b64 == "" {
		b64 = base64.StdEncoding.EncodeToString([]byte(pass))
	}
	if err := envfile.Upsert(envPath, "BOOTSTRAP_BACKUP_PASSPHRASE", pass); err != nil {
		return "", "", err
	}
	if err := envfile.Upsert(envPath, "BOOTSTRAP_BACKUP_PASSPHRASE_B64", b64); err != nil {
		return "", "", err
	}
	return pass, b64, nil
}

func hasPriorState(existing map[string]string) bool {
	for _, k := range []string{"SETUP_CLOUDFLARE_API_TOKEN", "SETUP_GITHUB_SYNC_TOKEN", "SETUP_TAILSCALE_USER_API_TOKEN", "SETUP_TAILSCALE_DOMAIN",
		"KS_SHARED_TOKEN", "KS_UPLOAD_TOKEN", "GITHUB_SYNC_TOKEN", "CLOUDFLARE_API_TOKEN", "TAILSCALE_USER_API_TOKEN"} {
		if existing[k] != "" {
			return true
		}
	}
	return false
}

func gitCapable(token string) bool {
	for _, p := range []string{"github_pat_", "ghp_", "gho_", "ghu_", "ghs_"} {
		if strings.HasPrefix(token, p) {
			return true
		}
	}
	return false
}

// ---- publishing -----------------------------------------------------------------

func (s *Step) originURL(ctx context.Context) string {
	// The configured URL, not `git remote get-url`, which applies the operator's own
	// insteadOf rewrites and would name some other address for the same repository.
	res, err := s.Run.Run(ctx, s.Root, "git", "config", "--get", "remote.origin.url")
	if err != nil || !res.OK() {
		return ""
	}
	return strings.TrimSpace(res.Stdout)
}

func (s *Step) branch(ctx context.Context) string {
	res, err := s.Run.Run(ctx, s.Root, "git", "rev-parse", "--abbrev-ref", "HEAD")
	if err != nil || !res.OK() {
		return ""
	}
	if b := strings.TrimSpace(res.Stdout); b != "HEAD" {
		return b
	}
	return ""
}

var safeName = regexp.MustCompile(`^[A-Za-z0-9._-]+$`)

// gitEnv gives git the GitHub credential for one command through environment
// config, so the token is never part of the command line.
func gitEnv(url, username, token string) []string {
	header := "AUTHORIZATION: basic " + base64.StdEncoding.EncodeToString([]byte(username+":"+token))
	return []string{
		"GIT_TERMINAL_PROMPT=0", "GIT_CONFIG_COUNT=2",
		"GIT_CONFIG_KEY_0=credential.helper", "GIT_CONFIG_VALUE_0=",
		"GIT_CONFIG_KEY_1=http." + url + "/.extraheader", "GIT_CONFIG_VALUE_1=" + header,
	}
}

func (s *Step) publish(ctx context.Context, env *engine.Env, repo, url, branch string, say func(string)) error {
	owner, _, _ := strings.Cut(repo, "/")
	username := owner
	for _, k := range []string{"ARGOCD_GITHUB_USERNAME", "GITEA_SEED_SOURCE_USERNAME", "GITEA_PUSH_MIRROR_USERNAME"} {
		if v := os.Getenv(k); v != "" {
			username = v
			break
		}
	}
	if !safeName.MatchString(username) {
		return fmt.Errorf("unable to determine a GitHub username for the GitOps push from %q", username)
	}
	token := env.Secret("github_token")
	if !gitCapable(token) {
		return errors.New("the GitHub credential must be a git-capable token to push rendered GitOps state")
	}

	// The renderer owns the list of generated files; commit exactly those, so an
	// operator's unrelated edits are never swept into the recovery commit.
	outputs, err := s.Platform.Outputs(ctx)
	if err != nil {
		return err
	}
	var paths []string
	for _, p := range outputs {
		if _, err := os.Stat(filepath.Join(s.Root, p)); err == nil {
			paths = append(paths, p)
		}
	}
	git := func(what string, args ...string) (string, error) {
		res, err := s.Run.Run(ctx, s.Root, "git", args...)
		if err != nil {
			return "", fmt.Errorf("%s: %w", what, err)
		}
		if !res.OK() {
			return "", fmt.Errorf("%s: %s", what, strings.TrimSpace(res.Stderr+" "+res.Stdout))
		}
		return strings.TrimSpace(res.Stdout), nil
	}

	if _, err := git("stage the rendered files", append([]string{"add", "--"}, paths...)...); err != nil {
		return err
	}
	if res, err := s.Run.Run(ctx, s.Root, "git", append([]string{"diff", "--cached", "--quiet", "--"}, paths...)...); err != nil {
		return err
	} else if !res.OK() {
		// Identity is only needed if a commit is made, and only set when missing.
		if r, _ := s.Run.Run(ctx, s.Root, "git", "config", "user.name"); !r.OK() {
			git("set a commit name", "config", "user.name", "initialize")
		}
		if r, _ := s.Run.Run(ctx, s.Root, "git", "config", "user.email"); !r.OK() {
			git("set a commit email", "config", "user.email", "initialize@local")
		}
		if _, err := git("create the rendered GitOps commit", append([]string{"commit", "--only", "-m", commitMsg, "--"}, paths...)...); err != nil {
			return err
		}
		say("Rendered GitOps files committed.")
	}

	authEnv := gitEnv(url, username, token)
	res, err := s.Run.RunEnv(ctx, s.Root, authEnv, "", "git", "push", url, "HEAD:"+branch)
	if err != nil {
		return err
	}
	if !res.OK() {
		return fmt.Errorf("failed to push rendered GitOps state to %s on %s; check the GitHub credential and branch permissions", repo, branch)
	}
	local, err := git("read HEAD", "rev-parse", "HEAD")
	if err != nil {
		return err
	}
	ls, err := s.Run.RunEnv(ctx, s.Root, authEnv, "", "git", "ls-remote", url, "refs/heads/"+branch)
	if err != nil || !ls.OK() || len(strings.Fields(ls.Stdout)) == 0 {
		return fmt.Errorf("push verification failed: could not read branch %s from %s", branch, repo)
	}
	if remote := strings.Fields(ls.Stdout)[0]; remote != local {
		return fmt.Errorf("push verification failed: remote %s is %s, local HEAD is %s", branch, remote, local)
	}
	say("Rendered GitOps state pushed to GitHub and verified.")
	return nil
}
