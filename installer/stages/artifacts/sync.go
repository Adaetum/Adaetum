package artifacts

import (
	"context"
	"errors"
	"fmt"
	"regexp"
	"strings"

	"github.com/Adaetum/Adaetum/installer/githubapi"
)

// syncedSecrets is the contract for which .env values become GitHub secrets: the
// ones the recovery repository's workflows and the break-glass bundle read.
var syncedSecrets = []string{
	"CLOUDFLARE_ACCOUNT_ID", "CLOUDFLARE_API_TOKEN", "R2_ACCESS_KEY_ID", "R2_SECRET_ACCESS_KEY", "R2_ENDPOINT", "R2_BUCKET",
	"KS_BASE_URL", "KS_SHARED_TOKEN", "KS_UPLOAD_TOKEN", "CLOUDFLARE_ZONE_API_TOKEN",
	"TAILSCALE_AUTHKEY", "TAILSCALE_OAUTH_CLIENT_ID", "TAILSCALE_OAUTH_CLIENT_SECRET",
	"TAILSCALE_OPERATOR_OAUTH_CLIENT_ID", "TAILSCALE_OPERATOR_OAUTH_CLIENT_SECRET", "TAILSCALE_OAUTH_TTL",
	"TAILSCALE_DOMAIN", "TAILSCALE_CLUSTER_TAG", "ANSIBLE_RUNNER_IMAGE", "ANSIBLE_RUNNER_REGISTRY_HOST", "K3S_COREDNS_FORWARDERS",
	"RANCHER_PUBLIC_DOMAIN", "RANCHER_CLOUDFLARED_TUNNEL_TOKEN", "REGISTRY_PUBLIC_DOMAIN",
	"ARGOCD_GITHUB_REPO_URL", "ARGOCD_GITHUB_REPO_BRANCH", "ARGOCD_GITHUB_USERNAME", "ARGOCD_GITHUB_TOKEN",
	"GITHUB_APP_ID", "GITHUB_APP_INSTALLATION_ID", "GITHUB_APP_PRIVATE_KEY_B64",
	"MONOREPO_GITHUB_APP_ID", "MONOREPO_GITHUB_APP_INSTALLATION_ID", "MONOREPO_GITHUB_APP_PRIVATE_KEY_B64",
	"GITEA_SEED_SOURCE_USERNAME", "GITEA_SEED_SOURCE_TOKEN",
	"GITEA_PUSH_MIRROR_ENABLED", "GITEA_PUSH_MIRROR_REPO_URL", "GITEA_PUSH_MIRROR_USERNAME", "GITEA_PUSH_MIRROR_TOKEN",
	"BOOTSTRAP_BACKUP_PASSPHRASE", "BOOTSTRAP_BACKUP_PASSPHRASE_B64", "BOOTSTRAP_RUNTIME_ENV_CACHE_BUSTER",
}

var (
	endpointOK = regexp.MustCompile(`^https?://[^\s]+$`)
	bucketOK   = regexp.MustCompile(`^[a-z0-9]([a-z0-9-]{1,61}[a-z0-9])?$`)
)

// syncSecrets writes every non-empty .env value in syncedSecrets to the GitHub
// environment, falling back to repository-level secrets when environments are not
// available. It returns a summary line and an error if any secret failed.
func syncSecrets(ctx context.Context, gh githubapi.Client, repo, env string, values map[string]string) (string, error) {
	if v := values["R2_ENDPOINT"]; v != "" && !endpointOK.MatchString(v) {
		return "", fmt.Errorf("invalid .env R2_ENDPOINT %q; expected http:// or https://", v)
	}
	if v := values["R2_BUCKET"]; v != "" && !bucketOK.MatchString(v) {
		return "", fmt.Errorf("invalid .env R2_BUCKET %q; expected 3-63 lowercase letters, numbers and hyphens", v)
	}
	// Not fatal if it fails: the environment may already exist, or the secrets fall
	// back to the repository below.
	_ = gh.EnsureEnvironment(ctx, repo, env)

	scope := env
	synced, skipped, failed := 0, 0, 0
	var firstErr error
	for _, name := range syncedSecrets {
		value := strings.TrimSpace(values[name])
		if value == "" {
			skipped++
			continue
		}
		err := gh.SetSecret(ctx, repo, scope, name, value)
		if errors.Is(err, githubapi.ErrEnvironmentUnavailable) && scope != "" {
			scope = ""
			err = gh.SetSecret(ctx, repo, scope, name, value)
		}
		if err != nil {
			failed++
			if firstErr == nil {
				firstErr = fmt.Errorf("could not set GitHub secret %s: %w", name, err)
			}
			continue
		}
		synced++
	}
	target := "environment " + env
	if scope == "" {
		target = "repository (environment secrets were unavailable)"
	}
	summary := fmt.Sprintf("GitHub secrets (%s): %d synced, %d empty and skipped, %d failed", target, synced, skipped, failed)
	if failed > 0 {
		return summary, firstErr
	}
	return summary, nil
}
