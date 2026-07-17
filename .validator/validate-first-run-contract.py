#!/usr/bin/env python3
"""Regression checks for the first-run ownership boundaries."""
from __future__ import annotations

import importlib.util
import os
import re
import subprocess
import tempfile
from pathlib import Path

import yaml


ROOT = Path(__file__).resolve().parents[1]
WIZARD = ROOT / "tasks" / "scripts" / "run-first-run-wizard.sh"
FIRST_RUN = ROOT / "tasks" / "scripts" / "first-run-preflight.sh"
ZONE_LISTER = ROOT / "tasks" / "scripts" / "list-cloudflare-zones.py"
TAILNET_LISTER = ROOT / "tasks" / "scripts" / "list-tailscale-tailnets.py"
SETUP = ROOT / "tasks" / "scripts" / "run-opinionated-setup.sh"
INITIAL_SETUP = ROOT / "tasks" / "scripts" / "run-initial-setup.sh"
UI = ROOT / "tasks" / "scripts" / "gum-ui.sh"
PROFILE = ROOT / "platform.yaml"
CONFIGURE = ROOT / "tasks" / "scripts" / "configure-platform-profile.py"
ISO = ROOT / "tasks" / "scripts" / "manage-rocky-installer-iso.sh"
ENV_RENDERER = ROOT / "tasks" / "scripts" / "generate-env-files.sh"
CLOUDFLARE_BOOTSTRAP = ROOT / "tasks" / "scripts" / "bootstrap-cloudflare.py"
CREDENTIAL_STORE = ROOT / "tasks" / "scripts" / "setup-credential-store.sh"
WINDOWS_CREDENTIAL_STORE = ROOT / "tasks" / "scripts" / "setup-credential-store-windows.ps1"
CLEAN_PUBLIC_CONFIG = ROOT / "tasks" / "scripts" / "clean-public-safe-config.py"
WORKER = ROOT / ".github" / "workflows" / "ks-worker.yml"
ROCKY_HEADER = ROOT / "ks-src" / "fragments" / "installers" / "kickstart" / "rocky10" / "00-header.ksfrag"
TASKFILE = ROOT / "Taskfile.yml"
ENV_TASKS = ROOT / "tasks" / "env.yml"


def fail(message: str) -> None:
    raise SystemExit(f"first-run contract failed: {message}")


def main() -> int:
    wizard = WIZARD.read_text(encoding="utf-8")
    first_run = FIRST_RUN.read_text(encoding="utf-8")
    setup = SETUP.read_text(encoding="utf-8")
    initial_setup = INITIAL_SETUP.read_text(encoding="utf-8")
    ui = UI.read_text(encoding="utf-8")
    iso = ISO.read_text(encoding="utf-8")
    env_renderer = ENV_RENDERER.read_text(encoding="utf-8")
    credential_store = CREDENTIAL_STORE.read_text(encoding="utf-8")
    windows_credential_store = WINDOWS_CREDENTIAL_STORE.read_text(encoding="utf-8")
    clean_public_config = CLEAN_PUBLIC_CONFIG.read_text(encoding="utf-8")
    worker = WORKER.read_text(encoding="utf-8")
    rocky_header = ROCKY_HEADER.read_text(encoding="utf-8")
    taskfile = TASKFILE.read_text(encoding="utf-8")
    env_tasks = ENV_TASKS.read_text(encoding="utf-8")
    gum_ui = (ROOT / "tasks" / "scripts" / "gum-ui.sh").read_text(encoding="utf-8")
    if "ADAETUM_FIRST_RUN=1" not in wizard:
        fail("first-run launcher does not enter the shared setup program")
    if "I have updated platform.yaml" in first_run or "placed the Rocky Linux" in first_run:
        fail("wizard still asks the operator to prepare profile or ISO manually")
    if ".setup-opinionated.cache.env" in setup:
        fail("setup still persists the retired local secret cache")
    if "Continue with simulated GitHub browser sign-in?" not in first_run:
        fail("dry-run bypasses the GitHub sign-in decision")
    if "gh auth token --hostname github.com" not in first_run:
        fail("first-run does not detect an existing GitHub CLI credential")
    if "gh auth refresh --hostname github.com" not in first_run:
        fail("first-run does not refresh an explicitly rejected GitHub credential")
    if "temporarily unavailable; retrying" not in first_run:
        fail("first-run treats transient GitHub API failures as missing credentials")
    if 'first_run_heading "GitHub temporarily unavailable"' not in first_run:
        fail("first-run exposes raw parser errors during a GitHub API outage")
    if "https://www.githubstatus.com" not in first_run:
        fail("GitHub outage guidance omits the status page")
    if "Choose the private repository" not in first_run or "Suggested repository" not in first_run:
        fail("private recovery repository input is missing visible instructions and a default")
    if 'gh repo create "${repository}" --private' not in first_run:
        fail("first-run does not create the recovery repository as private")
    if "gh repo fork" in first_run:
        fail("first-run still creates a public GitHub fork")
    if "first_run_find_available_recovery_destination" not in first_run:
        fail("first-run does not search for an available private repository name after a collision")
    if "first_run_find_preferred_recovery_destination" not in first_run:
        fail("first-run does not prefer an existing valid recovery repository")
    if 'candidate="${owner}/Adaetum-cluster-${suffix}"' not in first_run:
        fail("private repository collision recovery can repeat the same occupied suggestion")
    if "first_run_lookup_repository" not in first_run or 'first_run_repository_state="available"' not in first_run:
        fail("first-run does not distinguish an available repository name from a lookup failure")
    if 'existing="$(gh api "repos/${repository}"' in first_run:
        fail("repository lookup mistakes GitHub's 404 response body for an existing repository")
    if ".visibility // empty" not in first_run or ".permissions.admin // false" not in first_run:
        fail("first-run does not verify private visibility and administrative access")
    if ".fork // false" not in first_run or "contents/Taskfile.yml" not in first_run:
        fail("first-run can accept a fork or unrelated private repository")
    if "first_run_track_recovery_branch" not in first_run or "Refreshing the existing recovery branch" not in first_run:
        fail("reused recovery repositories do not restore branch tracking")
    if 'git merge --no-edit "origin/${current_branch}"' not in first_run:
        fail("reused recovery repositories do not reconcile existing cluster history")
    if "this checkout has uncommitted changes" not in first_run:
        fail("recovery branch reconciliation can overwrite an uncommitted worktree")
    if "git remote rename origin upstream" in first_run:
        fail("recovery remote setup can leave origin missing after a partial rename")
    if "The current repository will not be deleted" not in first_run:
        fail("public-origin migration does not preserve the existing repository safely")
    if "first_run_move_resume_credentials" not in first_run or "Moved protected resume credentials" not in first_run:
        fail("repository migration strands credentials in the previous secure-store namespace")
    if 'git remote add upstream "https://github.com/Adaetum/Adaetum.git"' not in first_run:
        fail("first-run does not preserve canonical Adaetum as the upstream remote")
    if "first_run_select_tailscale_domain" not in first_run:
        fail("first-run does not discover the Tailscale tailnet")
    load_profile_body = first_run.split("first_run_load_profile() {", 1)[1].split("\n}", 1)[0]
    if "return 0" not in load_profile_body:
        fail("a configured real tailnet silently terminates the fail-fast wizard")
    if "first_run_capture_tailscale_oauth" not in first_run:
        fail("first-run does not collect the Tailscale enrollment client")
    if "Why this token is temporary" not in first_run or "person's Tailscale membership" not in first_run:
        fail("Tailscale setup does not explain why the user token is temporary")
    if 'adaetum_ui_key_value "Expiration" "1 day' not in first_run:
        fail("Tailscale setup does not state the temporary API token expiration")
    if "non-reusable auth key with a 1-day expiration" not in first_run:
        fail("Tailscale setup does not explain automatic auth-key lifetime")
    if "gitignored local .env" not in first_run or "generated auth key" not in first_run:
        fail("Tailscale setup does not explain local auth-key persistence")
    if "TAILSCALE_BOOTSTRAP_VALIDATED=1" not in setup:
        fail("environment rendering can mint a duplicate Tailscale auth key")
    if "Devices → Core" not in first_run or "Devices → Posture Attributes" not in first_run:
        fail("Tailscale setup omits current policy-file dependency permissions")
    if "Scopes → Auth Keys" not in first_run or "Scopes → Policy File" not in first_run:
        fail("Tailscale setup does not state its write permissions explicitly")
    if "OAuth Keys" not in first_run or "--create-oauth-client" not in first_run:
        fail("Tailscale enrollment does not create the OAuth client automatically")
    if "I created the OAuth client" in first_run or "Tailscale OAuth client ID" in first_run:
        fail("Tailscale enrollment still asks the operator to create or transcribe OAuth credentials")
    if "tailscale-oauth-client-secret" not in first_run or "--validate-oauth-only" not in first_run:
        fail("automatically created Tailscale OAuth credentials cannot be securely resumed")
    if "never writes it to platform.yaml, the local .env, or GitHub secrets" not in first_run:
        fail("Tailscale setup does not identify its excluded persistence destinations")
    if "TAILSCALE_USER_API_TOKEN=\"\"" not in (ROOT / "tasks" / "scripts" / "generate-env-files.sh").read_text(encoding="utf-8"):
        fail("the temporary Tailscale user token is still persisted after OAuth bootstrap")
    if "Use ${selected_zone} as the cluster domain root?" in first_run:
        fail("Cloudflare zone selection still asks a redundant confirmation")
    if 'run --config .pre-commit-config.yaml --files platform.yaml' not in first_run:
        fail("profile-only commits cannot validate safely while the hook configuration is under development")
    if 'git commit --no-verify -m "Configure Adaetum platform profile"' not in first_run:
        fail("validated development-worktree profile commits still invoke the incompatible staged-config hook")
    if 'first_run_status success "Profile commit checks passed."' not in first_run or '>"${hook_log}" 2>&1' not in first_run:
        fail("successful profile-only validation still exposes skipped hook noise")
    if 'first_run_heading "Profile commit checks failed"' not in first_run or 'cat "${hook_log}"' not in first_run:
        fail("profile validation details are not shown on failure")
    if "first_run_domain=\"\"" not in first_run:
        fail("safe profile placeholders are still accepted as first-run defaults")
    fixture = subprocess.run(
        ["python3", str(ZONE_LISTER), "--fixture", "--details"],
        text=True,
        capture_output=True,
        check=False,
    )
    if fixture.returncode or "example.net\tdry-run-account-a\tAdaetum test account" not in fixture.stdout:
        fail("dry-run Cloudflare zone/account fixture is unavailable")
    if "Choose the public DNS zone and owning Cloudflare account" not in first_run:
        fail("Cloudflare selection does not distinguish zones from accounts")
    if 'export SETUP_CLOUDFLARE_ACCOUNT_ID="${first_run_cloudflare_account_id}"' not in first_run:
        fail("selected Cloudflare zone owner is not carried into bootstrap")
    if "Account API token" not in first_run or '"Account API tokens" "Read and Write"' not in first_run:
        fail("Cloudflare guidance does not recommend an account-owned service token")
    if "cfat_" not in first_run or "Continue with this non-account token anyway?" not in first_run:
        fail("Cloudflare setup does not distinguish account-owned and user-owned tokens")
    if "https://dash.cloudflare.com/profile/api-tokens" not in first_run:
        fail("Cloudflare account-token handoff lacks a generic dashboard entrypoint")
    if "Reusing the validated Cloudflare account token" not in first_run:
        fail("cancelled first-run cannot reuse a validated Cloudflare token")
    if "so a cancelled setup can resume?" not in first_run:
        fail("Cloudflare setup never offers secure resume storage")
    if "security add-generic-password" not in credential_store or "secret-tool store" not in credential_store:
        fail("setup credential storage is not backed by an OS credential store")
    if "Windows DPAPI" not in credential_store or "ConvertFrom-SecureString" not in windows_credential_store:
        fail("setup credential storage does not support Windows current-user protection")
    if ".setup-opinionated.cache.env" in credential_store:
        fail("setup credential storage reintroduces the retired plaintext cache")
    if '["git", "show", "HEAD:platform.yaml"]' not in clean_public_config or "reset_profile()" not in clean_public_config:
        fail("task clean does not restore the public-safe platform profile")
    if "Cloudflare Tunnel: Write" not in first_run:
        fail("Cloudflare token guidance omits the permission required by the tunnel API")
    if '"Connectivity Directory" "Read, Bind, and Admin"' not in first_run:
        fail("Cloudflare token guidance omits the validated connectivity permission set")
    if "--validate-access-only" not in first_run or "--token-stdin" not in first_run:
        fail("first-run does not validate Cloudflare tunnel access before provisioning")
    cloudflare_spec = importlib.util.spec_from_file_location("adaetum_bootstrap_cloudflare", CLOUDFLARE_BOOTSTRAP)
    if cloudflare_spec is None or cloudflare_spec.loader is None:
        fail("Cloudflare bootstrap validator cannot be loaded")
    cloudflare_module = importlib.util.module_from_spec(cloudflare_spec)
    cloudflare_spec.loader.exec_module(cloudflare_module)

    def cloudflare_api_with(permission: str):
        account_permissions = [
            "Account API Tokens Read",
            "Account API Tokens Write",
            "Workers R2 Storage Read",
            "Workers R2 Storage Write",
            permission,
            "Connectivity Directory Read",
            "Connectivity Directory Bind",
            "Connectivity Directory Admin",
            "Workers Scripts Read",
            "Workers Scripts Write",
        ]

        def fake_api(method: str, path: str, token: str, payload=None):
            del method, token, payload
            if path.endswith("/tokens/verify"):
                return {"id": "test-token-id", "status": "active"}
            if path.endswith("/tokens/test-token-id"):
                return {
                    "policies": [
                        {
                            "effect": "allow",
                            "permission_groups": [{"name": name} for name in account_permissions],
                            "resources": {"com.cloudflare.api.account.test-account": "*"},
                        }
                    ]
                }
            if "/cfd_tunnel?" in path:
                return []
            raise AssertionError(f"unexpected Cloudflare test path: {path}")

        return fake_api

    cloudflare_module.cf_api = cloudflare_api_with("Cloudflare Tunnel Write")
    cloudflare_module.validate_bootstrap_access("test-token", "test-account")
    cloudflare_module.cf_api = cloudflare_api_with("Cloudflare Tunnel Read")
    try:
        cloudflare_module.validate_bootstrap_access("test-token", "test-account")
    except RuntimeError:
        pass
    else:
        fail("read-only Cloudflare tunnel permission passes bootstrap validation")
    if "Workers Scripts: Read and Write" not in first_run or "Workers Routes: Read and Write" not in first_run:
        fail("Cloudflare token guidance omits required bootstrap permissions")
    if "CLOUDFLARE_ACCOUNT_ID=${default_cf_account_id}" not in setup:
        fail("bootstrap ignores the Cloudflare account selected during first-run")
    if "GITHUB_SYNC_REQUIRED=1" not in setup or 'ok "Required setup credentials were synced to GitHub secrets."' not in setup:
        fail("first-run does not require and report GitHub secret synchronization")
    fixture = subprocess.run(["python3", str(TAILNET_LISTER), "--fixture"], text=True, capture_output=True, check=False)
    if fixture.returncode or "tailnet-a1b2.ts.net" not in fixture.stdout:
        fail("dry-run Tailscale tailnet fixture is unavailable")
    empty_fixture = subprocess.run(["python3", str(TAILNET_LISTER), "--fixture-empty"], text=True, capture_output=True, check=False)
    if empty_fixture.returncode or empty_fixture.stdout:
        fail("empty-tailnet discovery fixture is unavailable")
    if 'first_run_heading "New or empty tailnet"' not in first_run or "Open Tailscale's DNS page now?" not in first_run:
        fail("new Tailscale tailnets do not have an inline DNS-name fallback")
    if "tailscale-api-token" not in first_run or "so a cancelled setup can resume?" not in first_run:
        fail("validated Tailscale access tokens cannot resume interrupted setup")
    if "--prepare-policy-only" not in first_run or "--user-token-stdin" not in first_run:
        fail("temporary Tailscale API access is not used to prepare OAuth tag ownership")
    if "tag:rocky10, tag:server, tag:cluster" not in first_run:
        fail("Tailscale OAuth guidance does not name the prepared node tags")
    if 'Using the Cloudflare token authorized during provider setup.' not in setup:
        fail("setup re-prompts for the Cloudflare token captured during first run")
    if 'Using the GitHub credential authorized during repository setup.' not in setup:
        fail("setup re-prompts for the GitHub credential captured during first run")
    if "ARGOCD_GITHUB_REPO_URL=${recovery_repo_url}" not in setup or "GITEA_SEED_SOURCE_REPO_URL=${recovery_repo_url}" not in setup:
        fail("environment rendering can retain a previous public repository URL")
    if 'platform_profile="${ADAETUM_PLATFORM_PROFILE:-${platform_profile}}"' not in setup:
        fail("dry-run reviewed profile is not carried into the shared setup steps")
    if 'bash ./tasks/scripts/run-initial-setup.sh' not in setup:
        fail("embedded bootstrap runner depends on an executable file mode")
    expected_sections = (
        'first_run_phase 1 "Repository"',
        'first_run_phase 2 "Providers"',
        'first_run_phase 3 "Profile"',
        'first_run_phase 4 "Installer"',
        'first_run_phase 5 "Bootstrap"',
    )
    if any(section not in first_run for section in expected_sections):
        fail("first-run does not carry the five-section journey through preparation")
    if 'adaetum_ui_milestone "5.${idx}"' not in setup:
        fail("bootstrap exposes a competing top-level step hierarchy")
    if 'INITIAL_SETUP_EMBEDDED_PREFIX=5.3' not in setup:
        fail("embedded publishing stages are not nested beneath bootstrap milestone 5.3")
    if 'adaetum_ui_completion "Dry run complete"' not in setup:
        fail("first-run has no five-section completion state")
    if 'adaetum_ui_panel "Your installer"' not in setup:
        fail("first-run does not present the completed installer")
    if 'adaetum_ui_key_value "ISO file"' not in setup:
        fail("installer handoff omits the generated ISO path")
    if 'adaetum_ui_panel "Download the machine installer"' not in setup:
        fail("installer handoff does not offer a Downloads-folder copy")
    if 'adaetum_ui_confirm "Download the installer ISO now?" y' not in setup:
        fail("installer download is not an explicit default-yes operator choice")
    if 'if [ "${dry_run}" = "1" ]; then' not in setup or "Dry run would save the generated machine installer" not in setup:
        fail("installer download does not preserve the no-mutation dry-run contract")
    if "attach this ISO to the target physical host or VM" not in setup:
        fail("installer handoff omits the next physical-host action")
    if 'Rocky-10.2-${dry_run_arch}-minimal.iso' not in setup:
        fail("dry-run installer handoff does not preview a realistic ISO filename")
    if 'first_run_choose "Rocky Linux release"' not in first_run:
        fail("installer setup does not ask for a Rocky release")
    if 'first_run_choose "Installer image type"' not in first_run or "Minimal (offline installer)" not in first_run:
        fail("installer setup does not default to Minimal while offering supported media types")
    if "DVD (offline installer)" not in first_run or "Boot ISO (online installer)" not in first_run:
        fail("installer choices do not explain online versus offline Rocky media")
    if '$1 == "SHA256" && $3 == "="' not in iso:
        fail("installer download does not parse Rocky's BSD-style checksum format")
    if "Reusing verified installer ISO" not in iso or "--continue-at -" not in iso:
        fail("installer download does not reuse verified media or resume partial downloads")
    direct_script = re.compile(r"(?m)^\s*\./tasks/scripts/[^ ]+\.sh(?:\s|$)")
    if direct_script.search(setup) or direct_script.search(initial_setup):
        fail("setup directly invokes a repository script that may not be executable")
    if "INITIAL_SETUP_COMPACT=1" not in setup or "run_with_details()" not in initial_setup:
        fail("first-run exposes detailed bootstrap command output by default")
    if ".adaetum/logs/task-init-details.log" not in setup or "dist/logs" in setup:
        fail("first-run detail logs are stored under disposable build output")
    if 'if [ "${#golden_keys[@]}" -gt 0 ]; then' not in initial_setup:
        fail("golden ISO upload expands an empty array under macOS Bash 3.2 nounset")
    if "uv run --with jinja2 python ./tasks/scripts/compile-kickstarts.py --sync --self-test" not in initial_setup:
        fail("golden ISO upload bypasses the managed kickstart compiler dependency")
    if "compile-kickstarts.py >/dev/null 2>&1 || true" in initial_setup:
        fail("golden ISO upload silently ignores kickstart compiler failures")
    if "Details: ${setup_detail_log}" not in setup:
        fail("bootstrap failures omit the retained detail-log path")
    if "first_run_ensure_github_actions" not in first_run or "GitHub Actions registration simulated for the private recovery repository" not in first_run:
        fail("private recovery repositories do not validate GitHub Actions registration")
    if "adaetum_github_repository_from_url" not in first_run:
        fail("GitHub API calls can retain the github.com URL prefix")
    if "Initialize the workflow branch" not in first_run or "HEAD:refs/heads/main" not in first_run:
        fail("new private repositories do not establish the main workflow branch")
    if '--default-branch main' not in first_run:
        fail("new private repositories can use an incompatible development branch as their default")
    secret_sync_block = initial_setup.split("done <<'EOF'", 1)[1].split("EOF", 1)[0]
    if "GITHUB_SYNC_TOKEN" in secret_sync_block:
        fail("bootstrap tries to create a GitHub secret with the reserved GITHUB_ prefix")
    if 'local registry_public_domain="${13:-}"' not in env_renderer:
        fail("environment renderer requires optional Cloudflare ingress arguments")
    if 'mktemp "${target}.tmp.XXXXXX"' not in env_renderer:
        fail("environment renderer does not write secret files atomically")
    if "BOOTSTRAP_BACKUP_PASSPHRASE=${BOOTSTRAP_BACKUP_PASSPHRASE}" not in env_renderer:
        fail("environment renderer drops the generated recovery passphrase")
    if 'ADAETUM_LOCAL_ISO_PATH="${setup_iso_for_env}"' not in setup:
        fail("environment rendering ignores the installer selected by first-run")
    if "Rocky-10.1" in env_renderer:
        fail("environment renderer still hardcodes the retired Rocky 10.1 installer")
    if 'first_run_choose "Target machine architecture"' not in first_run:
        fail("installer setup assumes the setup computer and target host share an architecture")
    if "Rocky's separate Boot ISO" not in first_run or "bootable offline installer" not in first_run:
        fail("installer setup does not explain why unsupported Boot media is excluded")
    for component in ("adaetum_ui_phase", "adaetum_ui_panel", "adaetum_ui_milestone", "adaetum_ui_completion"):
        if f"{component}()" not in ui:
            fail(f"shared console UI is missing {component}")
    if 'gum style --foreground "${ADAETUM_UI_ACCENT}" --bold "${label}"' not in ui:
        fail("Gum inputs hide their labels when a default value is present")
    if 'adaetum_ui_task "${initial_setup_embedded_prefix}.${idx}"' not in initial_setup:
        fail("embedded setup stages bypass the shared console hierarchy")
    ui_smoke = subprocess.run(
        [
            "bash",
            "-c",
            f'. "{UI}"; '
            'adaetum_ui_phase 5 5 "Bootstrap" "Prepare the installer."; '
            'adaetum_ui_milestone "5.3" "Publish bootstrap artifacts"; '
            'adaetum_ui_subtask "5.3.3.1" "Upload installer media"; '
            'adaetum_ui_completion "Dry run complete" "All five sections completed."',
        ],
        text=True,
        capture_output=True,
        check=False,
        env={**os.environ, "ADAETUM_GUM_UI": "0"},
    )
    if ui_smoke.returncode:
        fail(f"shared console UI smoke test failed: {ui_smoke.stderr.strip()}")
    for expected in ("SECTION 5/5", "[5.3] Publish bootstrap artifacts", "5.3.3.1", "Dry run complete"):
        if expected not in ui_smoke.stdout:
            fail(f"shared console UI did not render {expected!r}")
    if "${HOME}/Downloads" not in iso or "adopt)" not in iso:
        fail("installer discovery/adoption contract is incomplete")
    if 'report="$(bash "${helper}" list' not in first_run:
        fail("installer discovery depends on an executable helper file mode")
    if 'if [ "${#media_options[@]}" -eq 1 ]; then' not in first_run or 'verify "${selected}"' not in first_run:
        fail("one discovered installer ISO is not automatically verified and reused")
    if "init:clean:" not in taskfile or "ADAETUM_INIT_CLEAN" not in env_tasks:
        fail("fresh-input first-run mode is not exposed as task init:clean")
    if "init:auto:" not in taskfile or "ADAETUM_INIT_AUTO" not in env_tasks:
        fail("saved-state first-run replay is not exposed as task init:auto")
    if "adaetum_ui_auto_enabled" not in gum_ui or "Automatic replay" not in gum_ui:
        fail("first-run questions cannot replay saved/default decisions")
    if "Automatic replay could not load a valid saved Cloudflare token" not in first_run:
        fail("automatic replay can fall through to interactive Cloudflare credential capture")
    if "durable saved OAuth client replaces the expired temporary Tailscale setup token" not in first_run:
        fail("automatic replay still depends on the one-day Tailscale setup token")
    if "Automatic saved-state replay" not in first_run or "No plaintext answer file is used" not in first_run:
        fail("automatic replay does not explain its saved-state ownership boundary")
    if '[ "${clean_run}" != 1 ]' not in first_run or "ADAETUM_IGNORE_EXISTING_ENV" not in setup:
        fail("clean initialization can reuse saved provider or runtime values")
    if "ADAETUM_IGNORE_EXISTING_ENV" not in env_renderer:
        fail("environment rendering can fall back to the previous .env during clean initialization")
    if 'const allowed = [".iso",' not in worker or "application/x-iso9660-image" not in worker:
        fail("bootstrap delivery worker does not serve ISO artifacts")
    for secret_name in ("R2_ENDPOINT", "R2_ACCESS_KEY_ID", "R2_SECRET_ACCESS_KEY"):
        if f"{secret_name}: ${{{{ secrets.{secret_name} }}}}" not in worker:
            fail(f"KS Worker cannot fall back to direct R2 upload without {secret_name}")
    if "GOLDEN_ISO_KEY=rocky10/Rocky-10.2-aarch64-minimal.iso" not in rocky_header:
        fail("kickstart golden ISO key does not match the supported Rocky release")

    with tempfile.TemporaryDirectory() as directory:
        output = Path(directory) / "platform.yaml"
        result = subprocess.run(
            [
                "python3", str(CONFIGURE), "--profile", str(PROFILE), "--output", str(output),
                "--domain", "example.test", "--local-domain", "example.test.local",
                "--overlay-domain", "example-tailnet.ts.net", "--overlay-cluster-tag", "tag:cluster",
                "--repository-owner", "gitea-admin", "--repository-name", "cluster",
                "--bootstrap-base-url", "https://bootstrap.example.test", "--r2-bucket", "iso",
            ],
            text=True,
            capture_output=True,
            check=False,
        )
        if result.returncode:
            fail(f"profile proposal failed: {result.stderr.strip()}")
        proposed = yaml.safe_load(output.read_text(encoding="utf-8"))
        if proposed["spec"]["cluster"]["domain"] != "example.test":
            fail("profile proposal did not write collected public domain")
        if proposed["spec"]["delivery"]["bootstrapBaseUrl"] != "https://bootstrap.example.test":
            fail("profile proposal did not preserve reviewed bootstrap URL")
    print("first-run contract passed")
    return 0


if __name__ == "__main__":
    raise SystemExit(main())
