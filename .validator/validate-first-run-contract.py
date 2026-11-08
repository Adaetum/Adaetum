#!/usr/bin/env python3
"""Regression checks for the setup scripts that remain in bash.

Setup itself is the Go installer (installer/), tested by its own Go tests. These checks
guard the pieces it calls: the environment renderer, the Cloudflare bootstrap
validator, the public-safe clean task, the kickstart worker, and the shared console UI.
"""
from __future__ import annotations

import importlib.util
import os
import re
import subprocess
from pathlib import Path


ROOT = Path(__file__).resolve().parents[1]
UI = ROOT / "tasks" / "scripts" / "gum-ui.sh"
ENV_RENDERER = ROOT / "tasks" / "scripts" / "generate-env-files.sh"
CLOUDFLARE_BOOTSTRAP = ROOT / "tasks" / "scripts" / "bootstrap-cloudflare.py"
CLEAN_PUBLIC_CONFIG = ROOT / "tasks" / "scripts" / "clean-public-safe-config.py"
WORKER = ROOT / ".github" / "workflows" / "ks-worker.yml"
ROCKY_HEADER = ROOT / "ks-src" / "fragments" / "installers" / "kickstart" / "rocky10" / "00-header.ksfrag"
TASKFILE = ROOT / "Taskfile.yml"
ENV_TASKS = ROOT / "tasks" / "env.yml"
PLATFORM_BOOTSTRAP = ROOT / "ansible" / "playbooks" / "platform-bootstrap.yml"


def fail(message: str) -> None:
    raise SystemExit(f"first-run contract failed: {message}")


def main() -> int:
    ui = UI.read_text(encoding="utf-8")
    env_renderer = ENV_RENDERER.read_text(encoding="utf-8")
    clean_public_config = CLEAN_PUBLIC_CONFIG.read_text(encoding="utf-8")
    worker = WORKER.read_text(encoding="utf-8")
    rocky_header = ROCKY_HEADER.read_text(encoding="utf-8")
    taskfile = TASKFILE.read_text(encoding="utf-8")
    env_tasks = ENV_TASKS.read_text(encoding="utf-8")
    platform_bootstrap = PLATFORM_BOOTSTRAP.read_text(encoding="utf-8")
    gum_ui = (ROOT / "tasks" / "scripts" / "gum-ui.sh").read_text(encoding="utf-8")
    setup_recovery_contract = (
        "replaces every tracked path from the public tree",
        "restores the private\n`platform.yaml`",
        "Untracked and ignored recovery material is not changed",
    )
    for expected in setup_recovery_contract:
        if expected not in (ROOT / "setup.md").read_text(encoding="utf-8"):
            fail(f"setup guide is missing recovery refresh contract: {expected}")
    if "TAILSCALE_USER_API_TOKEN=\"\"" not in (ROOT / "tasks" / "scripts" / "generate-env-files.sh").read_text(encoding="utf-8"):
        fail("the temporary Tailscale user token is still persisted after OAuth bootstrap")
    if "validate_r2_credentials()" not in env_renderer or 'rclone lsf "adaetum_r2:${bucket}/.adaetum-credential-validation"' not in env_renderer:
        fail("setup does not make a signed read-only request before reusing R2 credentials")
    if "Existing R2 credentials were rejected; creating a replacement scoped credential." not in env_renderer:
        fail("setup does not replace a rejected existing R2 credential pair")
    if "Refusing to rotate credentials during a transient failure." not in env_renderer:
        fail("setup can rotate R2 credentials during a transient endpoint failure")
    if "Refusing to write .env or synchronize GitHub secrets." not in env_renderer:
        fail("setup can persist or synchronize an unvalidated R2 credential pair")
    if '["git", "show", "HEAD:platform.yaml"]' not in clean_public_config or "reset_profile()" not in clean_public_config:
        fail("task clean does not restore the public-safe platform profile")
    for safe_value in (
        "adaetum.invalid",
        "adaetum.local",
        "example-tailnet.ts.net",
        "https://bootstrap.adaetum.invalid",
    ):
        if safe_value not in clean_public_config:
            fail(f"task clean does not own public-safe placeholder {safe_value}")
    if "validate_public_tree()" not in clean_public_config or '"--public-handoff"' not in clean_public_config:
        fail("task clean does not scan the completed public-safe worktree")
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
    if 'tailscale_retag_platform_tag: "tag:rocky10"' not in platform_bootstrap:
        fail("platform bootstrap does not retag nodes with the OAuth-authorized Rocky tag")
    if "tag:rke2" in platform_bootstrap:
        fail("platform bootstrap still requests the retired, unauthorized tag:rke2")
    single_node_pod_contract = (
        'rke2_single_node_max_pods: 150',
        'rke2_default_max_pods: 110',
        'max-pods={{ rke2_single_node_max_pods }}',
        'rke2_ready_control_plane_count.stdout | default(\'0\') | int >= 3',
        'Restart only servers leaving single-node pod capacity',
        'Verify restored servers advertise the default pod limit',
    )
    if any(fragment not in platform_bootstrap for fragment in single_node_pod_contract):
        fail("RKE2 bootstrap pod headroom does not safely return to the upstream default after HA expansion")
    tailscale_packet_mark_contract = (
        "Wait for Calico Felix configuration to exist",
        "Reserve non-overlapping Felix packet-mark bits for Tailscale routing",
        'desired_mark_mask="4278190080"',
        '--patch=\'{"spec":{"iptablesMarkMask":4278190080}}\'',
        "default 0xffff0000 mask overlaps it",
    )
    if any(fragment not in platform_bootstrap for fragment in tailscale_packet_mark_contract):
        fail("Calico packet marks can bypass Tailscale routing for cross-node pod traffic")
    tailscale_calico_mtu_contract = (
        "rke2_calico_tailnet_mtu: 1230",
        "Keep Calico VXLAN within the effective node network MTU",
        "name: rke2-calico",
        "mtu: {{ rke2_calico_mtu_effective }}",
    )
    if any(fragment not in platform_bootstrap for fragment in tailscale_calico_mtu_contract):
        fail("Calico VXLAN MTU can exceed the Tailnet node path MTU")
    direct_script = re.compile(r"(?m)^\s*\./tasks/scripts/[^ ]+\.sh(?:\s|$)")
    if 'default_gh_branch="main"' not in env_renderer or 'ARGOCD_GITHUB_REPO_BRANCH="main"' not in env_renderer:
        fail("runtime generation can retain a stale GitHub recovery branch")
    if 'GITEA_SEED_SOURCE_REPO_BRANCH="main"' not in env_renderer:
        fail("runtime generation can retain a stale Gitea seed branch")
    if 'local registry_public_domain="${13:-}"' not in env_renderer:
        fail("environment renderer requires optional Cloudflare ingress arguments")
    if 'mktemp "${target}.tmp.XXXXXX"' not in env_renderer:
        fail("environment renderer does not write secret files atomically")
    if "BOOTSTRAP_BACKUP_PASSPHRASE=${BOOTSTRAP_BACKUP_PASSPHRASE}" not in env_renderer:
        fail("environment renderer drops the generated recovery passphrase")
    if "Rocky-10.1" in env_renderer:
        fail("environment renderer still hardcodes the retired Rocky 10.1 installer")
    for component in ("adaetum_ui_phase", "adaetum_ui_panel", "adaetum_ui_milestone", "adaetum_ui_completion"):
        if f"{component}()" not in ui:
            fail(f"shared console UI is missing {component}")
    if 'gum style --foreground "${ADAETUM_UI_ACCENT}" --bold "${label}"' not in ui:
        fail("Gum inputs hide their labels when a default value is present")
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
    reset_block = taskfile.split("\n  reset:\n", 1)[1].split("\n  maintainer-on:\n", 1)[0]
    reset_clean = "python3 ./tasks/scripts/clean-public-safe-config.py"
    reset_public_fetch = 'git fetch "${public_origin}" "+refs/heads/main:${public_ref}"'
    if reset_block.find('git status --porcelain') > reset_block.find(reset_clean):
        fail("task reset checks for worktree changes only after creating public-safe output changes")
    if reset_block.find(reset_public_fetch) > reset_block.find(reset_clean):
        fail("task reset can dirty the profile before proving canonical public main is available")
    if "git merge-base --is-ancestor HEAD origin/main" not in reset_block:
        fail("task reset can discard commits that are not published to the recovery origin")
    if 'git reset --hard "${public_ref}"' not in reset_block:
        fail("task reset does not actually return main to canonical public history")
    if "git switch" in reset_block or "- task: clean" in reset_block:
        fail("task reset still switches branches or dirties the checkout before its safety checks")
    if "adaetum_ui_silent_enabled" not in gum_ui or "Silent replay" not in gum_ui:
        fail("first-run questions cannot replay saved/default decisions")
    if "ADAETUM_IGNORE_EXISTING_ENV" not in env_renderer:
        fail("environment rendering can fall back to the previous .env during clean initialization")
    if 'const allowed = [".iso",' not in worker or "application/x-iso9660-image" not in worker:
        fail("bootstrap delivery worker does not serve ISO artifacts")
    for secret_name in ("R2_ENDPOINT", "R2_ACCESS_KEY_ID", "R2_SECRET_ACCESS_KEY"):
        if f"{secret_name}: ${{{{ secrets.{secret_name} }}}}" not in worker:
            fail(f"KS Worker cannot fall back to direct R2 upload without {secret_name}")
    if "GOLDEN_ISO_KEY=rocky10/Rocky-10.2-aarch64-minimal.iso" not in rocky_header:
        fail("kickstart golden ISO key does not match the supported Rocky release")

    print("first-run contract passed")
    return 0


if __name__ == "__main__":
    raise SystemExit(main())
