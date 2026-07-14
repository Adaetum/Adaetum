#!/usr/bin/env python3
"""Create or validate the Tailscale tags, ACL ownership, and auth inputs for setup.

The profile supplies the public tailnet/tag shape; this script performs the
provider-side mutations needed to make a new break-glass node eligible to join.
"""
import argparse
import base64
import json
import os
import re
import ssl
import sys
import urllib.error
import urllib.request


TS_API_BASE = "https://api.tailscale.com"
DEFAULT_CLUSTER_TAG = "tag:cluster"
REQUIRED_TAG_OWNERS = {
  "tag:provisioner": ["autogroup:admin"],
  "tag:rocky10": ["autogroup:admin", "tag:provisioner"],
  "tag:server": ["autogroup:admin", "tag:provisioner"],
  "tag:cluster": ["autogroup:admin", "tag:provisioner"],
  "tag:agent": ["autogroup:admin", "tag:provisioner"],
  "tag:rke2": ["autogroup:admin", "tag:provisioner"],
  "tag:subnet-router": ["autogroup:admin", "tag:provisioner"],
  "tag:rancher": ["autogroup:admin", "tag:provisioner"],
}


def _request(
  method: str,
  url: str,
  *,
  bearer_token: str = "",
  basic_user: str = "",
  basic_secret: str = "",
  payload=None,
):
  data = None
  if payload is not None:
    data = json.dumps(payload).encode("utf-8")
  req = urllib.request.Request(url=url, data=data, method=method.upper())
  req.add_header("Accept", "application/json")
  req.add_header("Content-Type", "application/json")
  req.add_header("User-Agent", "cluster-tailscale-bootstrap")
  if bearer_token:
    req.add_header("Authorization", f"Bearer {bearer_token}")
  elif basic_user:
    raw = f"{basic_user}:{basic_secret}".encode("utf-8")
    req.add_header("Authorization", f"Basic {base64.b64encode(raw).decode('ascii')}")

  try:
    with urllib.request.urlopen(req) as resp:
      body = resp.read().decode("utf-8", errors="replace")
      code = resp.getcode()
  except urllib.error.URLError as e:
    reason = str(getattr(e, "reason", e))
    if "CERTIFICATE_VERIFY_FAILED" in reason and os.name == "nt":
      insecure_ctx = ssl._create_unverified_context()
      with urllib.request.urlopen(req, context=insecure_ctx) as resp:
        body = resp.read().decode("utf-8", errors="replace")
        code = resp.getcode()
      print(
        "Warning: TLS cert verification failed in this Windows shell; using insecure fallback for Tailscale bootstrap call.",
        file=sys.stderr,
      )
    else:
      raise RuntimeError(f"Tailscale API {method} {url} network failure: {reason}") from e
  except urllib.error.HTTPError as e:
    body = e.read().decode("utf-8", errors="replace")
    raise RuntimeError(f"Tailscale API {method} {url} failed: HTTP {e.code}: {body}") from e

  parsed = {}
  if body:
    try:
      parsed = json.loads(body)
    except Exception:
      parsed = {}
  return code, parsed, body


def normalize_tag(value: str) -> str:
  v = (value or "").strip()
  if not v:
    return ""
  if not v.startswith("tag:"):
    v = f"tag:{v}"
  return v


def _strip_hujson(text: str) -> str:
  text = re.sub(r"/\*.*?\*/", "", text, flags=re.S)
  text = re.sub(r"(^|[^:])//.*?$", r"\1", text, flags=re.M)
  text = re.sub(r",\s*([}\]])", r"\1", text)
  return text


def _parse_policy(policy_text: str):
  try:
    return json.loads(policy_text)
  except json.JSONDecodeError:
    return json.loads(_strip_hujson(policy_text))


def _extract_policy_obj(parsed: dict, body: str):
  if isinstance(parsed, dict):
    if isinstance(parsed.get("tagOwners"), dict):
      return parsed
    for key in ("acl", "policy"):
      value = parsed.get(key)
      if isinstance(value, str) and value.strip():
        return _parse_policy(value)
      if isinstance(value, dict):
        return value
  body_clean = (body or "").strip()
  if body_clean:
    return _parse_policy(body_clean)
  return {}


def _merge_tag_owners(policy_obj: dict):
  changed = False
  tag_owners = policy_obj.get("tagOwners")
  if not isinstance(tag_owners, dict):
    tag_owners = {}
    policy_obj["tagOwners"] = tag_owners
    changed = True

  for tag, required_owners in REQUIRED_TAG_OWNERS.items():
    existing = tag_owners.get(tag)
    if not isinstance(existing, list):
      tag_owners[tag] = list(required_owners)
      changed = True
      continue
    merged = list(existing)
    for owner in required_owners:
      if owner not in merged:
        merged.append(owner)
        changed = True
    tag_owners[tag] = merged

  return changed


def ensure_required_tag_owners(
  tailnet: str,
  oauth_client_id: str = "",
  oauth_client_secret: str = "",
  user_token: str = "",
):
  tailnet_clean = (tailnet or "").strip()
  if not tailnet_clean:
    raise RuntimeError(
      "Missing TAILSCALE_DOMAIN (tailnet name) required for ACL/tagOwners validation. "
      "Set TAILSCALE_DOMAIN to your tailnet, e.g. company.com or company.ts.net."
    )
  policy_access = ""
  if oauth_client_id and oauth_client_secret:
    policy_access = oauth_access_token(
      oauth_client_id,
      oauth_client_secret,
      ["policy_file"],
    )
  elif user_token:
    policy_access = user_token
  else:
    raise RuntimeError(
      "Missing credentials for ACL/tagOwners ensure. Provide OAuth client credentials or TAILSCALE_USER_API_TOKEN."
    )
  acl_url = f"{TS_API_BASE}/api/v2/tailnet/{tailnet_clean}/acl"
  _, parsed, body = _request("GET", acl_url, bearer_token=policy_access)
  policy_obj = _extract_policy_obj(parsed if isinstance(parsed, dict) else {}, body)
  if not isinstance(policy_obj, dict):
    raise RuntimeError("Tailscale ACL policy did not parse as a JSON object.")

  if not _merge_tag_owners(policy_obj):
    return

  _request("POST", acl_url, bearer_token=policy_access, payload=policy_obj)


def build_default_tags(cluster_tag: str):
  tags = ["tag:rocky10", "tag:server"]
  ctag = normalize_tag(cluster_tag or DEFAULT_CLUSTER_TAG)
  if ctag and ctag not in tags:
    tags.append(ctag)
  return tags


def oauth_access_token(client_id: str, client_secret: str, scopes):
  form_url = f"{TS_API_BASE}/api/v2/oauth/token"
  scope_value = " ".join([s for s in scopes if s]).strip()
  code, parsed, body = _request(
    "POST",
    form_url,
    basic_user=client_id,
    basic_secret=client_secret,
    payload={"grant_type": "client_credentials", "scope": scope_value},
  )
  token = parsed.get("access_token", "")
  if code != 200 or not token:
    raise RuntimeError(f"OAuth token request failed (HTTP {code}): {body[:600]}")
  return token


def validate_oauth_can_mint_key(client_id: str, client_secret: str, tags):
  access = oauth_access_token(client_id, client_secret, ["auth_keys"])
  key_payload = {
    "capabilities": {
      "devices": {
        "create": {
          "reusable": False,
          "ephemeral": False,
          "preauthorized": True,
          "tags": tags,
        }
      }
    },
    "expirySeconds": 120,
    "description": "cluster setup oauth validation",
  }
  code, parsed, body = _request(
    "POST",
    f"{TS_API_BASE}/api/v2/tailnet/-/keys",
    bearer_token=access,
    payload=key_payload,
  )
  if code not in (200, 201) or not parsed.get("key"):
    raise RuntimeError(f"OAuth key mint validation failed (HTTP {code}): {body[:600]}")


def create_auth_key_with_bearer(bearer_token: str, tags):
  key_payload = {
    "capabilities": {
      "devices": {
        "create": {
          # Reusable key avoids one-time/short-lived bootstrap key drift across staged installs.
          "reusable": True,
          "ephemeral": False,
          "preauthorized": True,
          "tags": tags,
        }
      }
    },
    # Tailscale auth keys are still time-bound; keep this long-lived for setup workflows.
    "expirySeconds": 7776000,
    "description": "cluster setup user-token bootstrap authkey",
  }
  code, parsed, body = _request(
    "POST",
    f"{TS_API_BASE}/api/v2/tailnet/-/keys",
    bearer_token=bearer_token,
    payload=key_payload,
  )
  key = parsed.get("key", "")
  if code not in (200, 201) or not key:
    raise RuntimeError(f"User token auth key bootstrap failed (HTTP {code}): {body[:600]}")
  return key


def main():
  p = argparse.ArgumentParser()
  p.add_argument("--user-token", default="")
  p.add_argument("--tailnet", default="")
  p.add_argument("--oauth-client-id", default="")
  p.add_argument("--oauth-client-secret", default="")
  p.add_argument("--cluster-tag", default="")
  args = p.parse_args()

  tags = build_default_tags(args.cluster_tag)
  client_id = (args.oauth_client_id or "").strip()
  client_secret = (args.oauth_client_secret or "").strip()

  ensure_required_tag_owners(
    args.tailnet,
    oauth_client_id=client_id,
    oauth_client_secret=client_secret,
    user_token=(args.user_token or "").strip(),
  )

  output_authkey = ""
  if client_id and client_secret:
    validate_oauth_can_mint_key(client_id, client_secret, tags)
  elif (args.user_token or "").strip():
    output_authkey = create_auth_key_with_bearer((args.user_token or "").strip(), tags)
  else:
    raise RuntimeError(
      "Missing OAuth credentials and TAILSCALE_USER_API_TOKEN. "
      "Provide OAuth credentials or a user API token for bootstrap."
    )

  print(f"TAILSCALE_OAUTH_CLIENT_ID={client_id}")
  print(f"TAILSCALE_OAUTH_CLIENT_SECRET={client_secret}")
  print(f"TAILSCALE_CLUSTER_TAG={normalize_tag(args.cluster_tag or DEFAULT_CLUSTER_TAG)}")
  print(f"TAILSCALE_ADVERTISE_TAGS={','.join(tags)}")
  print(f"TAILSCALE_AUTHKEY={output_authkey}")


if __name__ == "__main__":
  try:
    main()
  except Exception as exc:
    print(str(exc), file=sys.stderr)
    sys.exit(1)
