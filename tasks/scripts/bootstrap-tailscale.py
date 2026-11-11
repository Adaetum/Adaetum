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
  "tag:subnet-router": ["autogroup:admin", "tag:provisioner"],
  "tag:rancher": ["autogroup:admin", "tag:provisioner"],
  # The Kubernetes operator has a distinct identity from node enrollment. It
  # owns only the proxy tag used for private service routes.
  "tag:k8s-operator": ["autogroup:admin"],
  "tag:k8s": ["tag:k8s-operator"],
}
REQUIRED_SERVICE_APPROVERS = {"tag:k8s": ["tag:k8s"]}
REQUIRED_OPERATOR_GRANT = {
  "src": ["*"],
  "dst": ["tag:k8s"],
  # HTTPS serves the private web routes. This grant is additive: setup only appends
  # missing protocols and never removes ones an existing tailnet already allows.
  "ip": ["tcp:443", "icmp:*"],
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


def _merge_operator_access(policy_obj: dict):
  """Merge only the policy entries required by shared ingress ProxyGroups."""
  changed = False
  auto_approvers = policy_obj.get("autoApprovers")
  if not isinstance(auto_approvers, dict):
    auto_approvers = {}
    policy_obj["autoApprovers"] = auto_approvers
    changed = True
  services = auto_approvers.get("services")
  if not isinstance(services, dict):
    services = {}
    auto_approvers["services"] = services
    changed = True
  for tag, required_approvers in REQUIRED_SERVICE_APPROVERS.items():
    existing = services.get(tag)
    if not isinstance(existing, list):
      services[tag] = list(required_approvers)
      changed = True
      continue
    for approver in required_approvers:
      if approver not in existing:
        existing.append(approver)
        changed = True

  grants = policy_obj.get("grants")
  if not isinstance(grants, list):
    grants = []
    policy_obj["grants"] = grants
    changed = True
  operator_grant = next(
    (
      grant
      for grant in grants
      if isinstance(grant, dict)
      and grant.get("src") == REQUIRED_OPERATOR_GRANT["src"]
      and grant.get("dst") == REQUIRED_OPERATOR_GRANT["dst"]
      and isinstance(grant.get("ip"), list)
    ),
    None,
  )
  if operator_grant is None:
    grants.append({key: list(value) for key, value in REQUIRED_OPERATOR_GRANT.items()})
    changed = True
  else:
    for protocol in REQUIRED_OPERATOR_GRANT["ip"]:
      if protocol not in operator_grant["ip"]:
        operator_grant["ip"].append(protocol)
        changed = True
  return changed


def _merge_ssh_rule(policy_obj: dict, cluster_tag: str):
  """Add setup's SSH rule if it is not already there. Add-only: nothing else is touched.

  The rule lets the tailnet owner SSH to cluster hosts as ansible, so a person can read a
  host's agent token on a fresh install. Tailscale checks the login name as well as the
  host, so without a rule naming ansible the connection is refused. An existing ssh list
  (including Tailscale's default rule) is kept and ours is appended.
  """
  rule = default_ssh_rule(cluster_tag)
  ssh = policy_obj.get("ssh")
  if ssh is None:
    policy_obj["ssh"] = [rule]
    return True
  if not isinstance(ssh, list):
    raise RuntimeError("The tailnet policy's ssh section is not a list, so the SSH rule was not added.")
  if rule in ssh:
    return False
  ssh.append(rule)
  return True


def ensure_required_tag_owners(
  tailnet: str,
  oauth_client_id: str = "",
  oauth_client_secret: str = "",
  user_token: str = "",
  cluster_tag: str = "",
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
      ["policy_file", "devices:core:read", "devices:posture_attributes:read"],
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

  changed = _merge_tag_owners(policy_obj)
  changed = _merge_operator_access(policy_obj) or changed
  changed = _merge_ssh_rule(policy_obj, cluster_tag) or changed
  if not changed:
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


def create_bootstrap_auth_key_with_oauth(client_id: str, client_secret: str, tags):
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
    "expirySeconds": 86400,
    "description": "Adaetum first-node bootstrap",
  }
  code, parsed, body = _request(
    "POST",
    f"{TS_API_BASE}/api/v2/tailnet/-/keys",
    bearer_token=access,
    payload=key_payload,
  )
  if code not in (200, 201) or not parsed.get("key"):
    raise RuntimeError(f"OAuth bootstrap key creation failed (HTTP {code}): {body[:600]}")
  return parsed["key"]


def create_oauth_client_with_bearer(bearer_token: str, tailnet: str, tags):
  payload = {
    "keyType": "client",
    "description": "Adaetum node enrollment",
    "scopes": [
      "auth_keys",
      "policy_file",
      "devices:core:read",
      "devices:posture_attributes:read",
    ],
    "tags": tags,
  }
  code, parsed, body = _request(
    "POST",
    f"{TS_API_BASE}/api/v2/tailnet/{tailnet}/keys",
    bearer_token=bearer_token,
    payload=payload,
  )
  client_id = parsed.get("id", "")
  client_secret = parsed.get("key", "")
  if code not in (200, 201) or not client_id or not client_secret:
    raise RuntimeError(f"OAuth client creation failed (HTTP {code}): {body[:600]}")
  return client_id, client_secret


def create_operator_oauth_client_with_bearer(bearer_token: str, tailnet: str):
  """Create the least-privilege identity consumed only by the K8s operator."""
  payload = {
    "keyType": "client",
    "description": "Adaetum Kubernetes operator",
    "scopes": ["auth_keys", "devices:core", "services"],
    "tags": ["tag:k8s-operator"],
  }
  code, parsed, body = _request(
    "POST",
    f"{TS_API_BASE}/api/v2/tailnet/{tailnet}/keys",
    bearer_token=bearer_token,
    payload=payload,
  )
  client_id = parsed.get("id", "")
  client_secret = parsed.get("key", "")
  if code not in (200, 201) or not client_id or not client_secret:
    raise RuntimeError(f"Kubernetes operator OAuth client creation failed (HTTP {code}): {body[:600]}")
  return client_id, client_secret


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


# ---- reset ------------------------------------------------------------------
#
# Reset removes what setup created in the tailnet so the next install starts clean:
# the cluster's devices and the two Adaetum OAuth clients. Selection is deliberately
# narrow and lives in pure functions so a test can pin it down: a device is only
# selected when it carries one of the tags setup applies, and an OAuth client only
# when its ID is one the operator's .env recorded. Nothing is deleted without an
# explicit list of IDs, and every ID is re-checked against a fresh listing first.

# Tags setup puts on hosts (enrollment) and on the operator and its proxies.
HOST_TAGS = {"tag:rocky10", "tag:server"}
OPERATOR_TAGS = {"tag:k8s-operator", "tag:k8s"}


def reset_device_tags(cluster_tag: str):
  return HOST_TAGS | OPERATOR_TAGS | {normalize_tag(cluster_tag or DEFAULT_CLUSTER_TAG)}


def select_reset_devices(devices, cluster_tag: str):
  """Return the devices setup created, as small records, and nothing else."""
  wanted = reset_device_tags(cluster_tag)
  chosen = []
  for device in devices or []:
    if not isinstance(device, dict):
      continue
    tags = {t for t in (device.get("tags") or []) if isinstance(t, str)}
    if not tags & wanted or not device.get("id"):
      continue
    chosen.append({
      "id": str(device["id"]),
      "hostname": str(device.get("hostname") or ""),
      "name": str(device.get("name") or ""),
      "tags": sorted(tags),
      "lastSeen": str(device.get("lastSeen") or ""),
      # Only hosts (not the operator's proxies) have install logs to clean up.
      "isHost": bool(tags & HOST_TAGS),
    })
  return chosen


def select_reset_clients(keys, client_ids):
  """Return OAuth clients whose ID the operator recorded. Names alone never match."""
  recorded = {c.strip() for c in client_ids if c and c.strip()}
  chosen = []
  for key in keys or []:
    if not isinstance(key, dict):
      continue
    key_id = str(key.get("id") or "")
    if key_id in recorded and key.get("keyType") == "client":
      chosen.append({"id": key_id, "description": str(key.get("description") or "")})
  return chosen


def list_reset_devices(bearer_token: str, tailnet: str, cluster_tag: str):
  _, parsed, _ = _request(
    "GET",
    f"{TS_API_BASE}/api/v2/tailnet/{tailnet}/devices?fields=all",
    bearer_token=bearer_token,
  )
  return select_reset_devices(parsed.get("devices", []), cluster_tag)


def list_reset_clients(bearer_token: str, tailnet: str, client_ids):
  _, parsed, _ = _request(
    "GET",
    f"{TS_API_BASE}/api/v2/tailnet/{tailnet}/keys?all=true",
    bearer_token=bearer_token,
  )
  return select_reset_clients(parsed.get("keys", []), client_ids)


def delete_reset_devices(bearer_token: str, current, confirmed_ids):
  """Delete only devices that are both confirmed by the operator and still selected."""
  still_selected = {d["id"]: d for d in current}
  deleted = []
  for device_id in confirmed_ids:
    if device_id not in still_selected:
      continue
    _request("DELETE", f"{TS_API_BASE}/api/v2/device/{device_id}", bearer_token=bearer_token)
    deleted.append(still_selected[device_id])
  return deleted


def delete_reset_clients(bearer_token: str, tailnet: str, current, confirmed_ids):
  still_selected = {c["id"]: c for c in current}
  deleted = []
  for key_id in confirmed_ids:
    if key_id not in still_selected:
      continue
    _request("DELETE", f"{TS_API_BASE}/api/v2/tailnet/{tailnet}/keys/{key_id}", bearer_token=bearer_token)
    deleted.append(still_selected[key_id])
  return deleted


SSH_RULE_USER = "ansible"


def default_ssh_rule(cluster_tag: str):
  """The one SSH rule setup owns: the tailnet owner may SSH to cluster hosts as ansible."""
  return {
    "action": "accept",
    "src": ["autogroup:owner"],
    "dst": [normalize_tag(cluster_tag or DEFAULT_CLUSTER_TAG)],
    "users": [SSH_RULE_USER],
  }


def select_ssh_rules(policy_obj, cluster_tag: str):
  """Return the policy's ssh entries that are exactly setup's rule, and nothing else.

  A rule counts only if it has the same four fields and values and no others. One the
  operator wrote differently (another user, another source, an extra field such as
  checkPeriod) is theirs and is never selected, even if it looks similar.
  """
  wanted = default_ssh_rule(cluster_tag)
  ssh = policy_obj.get("ssh") if isinstance(policy_obj, dict) else None
  if not isinstance(ssh, list):
    return []
  return [rule for rule in ssh if isinstance(rule, dict) and rule == wanted]


def _read_policy(bearer_token: str, tailnet: str):
  acl_url = f"{TS_API_BASE}/api/v2/tailnet/{tailnet}/acl"
  _, parsed, body = _request("GET", acl_url, bearer_token=bearer_token)
  policy_obj = _extract_policy_obj(parsed if isinstance(parsed, dict) else {}, body)
  if not isinstance(policy_obj, dict):
    raise RuntimeError("Tailscale ACL policy did not parse as a JSON object.")
  return acl_url, policy_obj


def list_reset_ssh_rule(bearer_token: str, tailnet: str, cluster_tag: str):
  _, policy_obj = _read_policy(bearer_token, tailnet)
  return select_ssh_rules(policy_obj, cluster_tag)


def delete_reset_ssh_rule(bearer_token: str, tailnet: str, cluster_tag: str):
  """Remove setup's SSH rule from the policy and write the policy back; return what was removed.

  The policy is read again here, so only a rule still present now is removed, and every
  other part of the policy is written back unchanged.
  """
  acl_url, policy_obj = _read_policy(bearer_token, tailnet)
  doomed = select_ssh_rules(policy_obj, cluster_tag)
  if not doomed:
    return []
  wanted = default_ssh_rule(cluster_tag)
  remaining = [rule for rule in policy_obj["ssh"] if not (isinstance(rule, dict) and rule == wanted)]
  if remaining:
    policy_obj["ssh"] = remaining
  else:
    del policy_obj["ssh"]
  _request("POST", acl_url, bearer_token=bearer_token, payload=policy_obj)
  return doomed


def main():
  p = argparse.ArgumentParser()
  p.add_argument("--user-token", default="")
  p.add_argument("--user-token-stdin", action="store_true")
  p.add_argument("--oauth-credentials-stdin", action="store_true")
  p.add_argument("--tailnet", default="")
  p.add_argument("--oauth-client-id", default="")
  p.add_argument("--oauth-client-secret", default="")
  p.add_argument("--cluster-tag", default="")
  p.add_argument("--prepare-policy-only", action="store_true")
  p.add_argument("--create-oauth-client", action="store_true")
  p.add_argument("--create-operator-oauth-client", action="store_true")
  p.add_argument("--validate-oauth-only", action="store_true")
  p.add_argument("--validate-operator-oauth-only", action="store_true")
  # Reset: each mode prints JSON. Without --reset-confirm-ids it only lists.
  p.add_argument("--reset-devices", action="store_true")
  p.add_argument("--reset-oauth-clients", action="store_true")
  p.add_argument("--reset-client-ids", default="", help="comma-separated OAuth client IDs recorded in .env")
  p.add_argument("--reset-ssh-rule", action="store_true", help="list (or with --reset-confirm-ssh, remove) the SSH rule setup adds to the tailnet policy")
  p.add_argument("--reset-confirm-ssh", action="store_true")
  p.add_argument("--reset-confirm-ids", default="", help="comma-separated IDs to delete; empty means list only")
  args = p.parse_args()

  tags = build_default_tags(args.cluster_tag)
  client_id = (args.oauth_client_id or "").strip()
  client_secret = (args.oauth_client_secret or "").strip()
  stdin_value = sys.stdin.read() if args.user_token_stdin or args.oauth_credentials_stdin else ""
  user_token = (stdin_value if args.user_token_stdin else args.user_token or "").strip()
  if args.oauth_credentials_stdin:
    credential_lines = stdin_value.splitlines()
    if len(credential_lines) < 2:
      raise RuntimeError("OAuth client ID and secret are required on stdin.")
    client_id = credential_lines[0].strip()
    client_secret = credential_lines[1].strip()

  if args.validate_oauth_only:
    if not client_id or not client_secret:
      raise RuntimeError("OAuth client ID and secret are required for validation.")
    oauth_access_token(client_id, client_secret, ["auth_keys"])
    print(f"TAILSCALE_OAUTH_CLIENT_ID={client_id}")
    return


  if args.validate_operator_oauth_only:
    if not client_id or not client_secret:
      raise RuntimeError("Operator OAuth client ID and secret are required for validation.")
    oauth_access_token(client_id, client_secret, ["auth_keys", "devices:core", "services"])
    print(f"TAILSCALE_OPERATOR_OAUTH_CLIENT_ID={client_id}")
    return

  if args.reset_ssh_rule:
    if not args.tailnet:
      raise RuntimeError("--tailnet is required for reset.")
    # Editing the policy needs the node OAuth client (it holds policy_file); the operator's
    # client does not.
    if not client_id or not client_secret:
      raise RuntimeError("The node OAuth client ID and secret are required to read the tailnet policy.")
    bearer = oauth_access_token(client_id, client_secret, ["policy_file"])
    found = list_reset_ssh_rule(bearer, args.tailnet, args.cluster_tag)
    removed = delete_reset_ssh_rule(bearer, args.tailnet, args.cluster_tag) if args.reset_confirm_ssh else []
    print(json.dumps({"rules": found, "deleted": len(removed)}))
    return

  if args.reset_devices or args.reset_oauth_clients:
    if not args.tailnet:
      raise RuntimeError("--tailnet is required for reset.")
    confirmed = [i.strip() for i in args.reset_confirm_ids.split(",") if i.strip()]
    if args.reset_devices:
      # Devices are listed and deleted with the operator's own OAuth client, which
      # already holds devices:core, so no extra credential is needed.
      if not client_id or not client_secret:
        raise RuntimeError("The operator OAuth client ID and secret are required to reset devices.")
      bearer = oauth_access_token(client_id, client_secret, ["devices:core"])
      current = list_reset_devices(bearer, args.tailnet, args.cluster_tag)
      deleted = delete_reset_devices(bearer, current, confirmed) if confirmed else []
      print(json.dumps({"devices": current, "deleted": [d["id"] for d in deleted]}))
    else:
      # Deleting OAuth clients needs the temporary full-access token; the operator's
      # client cannot delete itself.
      if not user_token:
        raise RuntimeError("The temporary Tailscale API access token is required to remove OAuth clients.")
      ids = [i.strip() for i in args.reset_client_ids.split(",") if i.strip()]
      current = list_reset_clients(user_token, args.tailnet, ids)
      deleted = delete_reset_clients(user_token, args.tailnet, current, confirmed) if confirmed else []
      print(json.dumps({"clients": current, "deleted": [c["id"] for c in deleted]}))
    return

  ensure_required_tag_owners(
    args.tailnet,
    oauth_client_id=client_id,
    oauth_client_secret=client_secret,
    user_token=user_token,
    cluster_tag=args.cluster_tag,
  )

  if args.prepare_policy_only:
    print(f"TAILSCALE_CLUSTER_TAG={normalize_tag(args.cluster_tag or DEFAULT_CLUSTER_TAG)}")
    print(f"TAILSCALE_ADVERTISE_TAGS={','.join(tags)}")
    return

  if args.create_oauth_client:
    if not user_token:
      raise RuntimeError("The temporary Tailscale API access token is required to create the OAuth client.")
    client_id, client_secret = create_oauth_client_with_bearer(user_token, args.tailnet, tags)
    print(f"TAILSCALE_OAUTH_CLIENT_ID={client_id}")
    print(f"TAILSCALE_OAUTH_CLIENT_SECRET={client_secret}")
    return


  if args.create_operator_oauth_client:
    if not user_token:
      raise RuntimeError("The temporary Tailscale API access token is required to create the operator OAuth client.")
    operator_id, operator_secret = create_operator_oauth_client_with_bearer(
      user_token, args.tailnet
    )
    print(f"TAILSCALE_OPERATOR_OAUTH_CLIENT_ID={operator_id}")
    print(f"TAILSCALE_OPERATOR_OAUTH_CLIENT_SECRET={operator_secret}")
    return

  output_authkey = ""
  if client_id and client_secret:
    output_authkey = create_bootstrap_auth_key_with_oauth(client_id, client_secret, tags)
  elif user_token:
    output_authkey = create_auth_key_with_bearer(user_token, tags)
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
