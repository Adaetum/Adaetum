#!/usr/bin/env python3
import argparse
import hashlib
import json
import os
import ssl
import sys
import time
import urllib.parse
import urllib.error
import urllib.request

API_BASE = "https://api.cloudflare.com/client/v4"


def cf_api(method: str, path: str, token: str, payload=None):
  url = f"{API_BASE}{path}"
  data = None
  if payload is not None:
    data = json.dumps(payload).encode("utf-8")
  req = urllib.request.Request(url=url, data=data, method=method.upper())
  req.add_header("Authorization", f"Bearer {token}")
  req.add_header("Content-Type", "application/json")
  req.add_header("User-Agent", "cluster-bootstrap")
  try:
    with urllib.request.urlopen(req) as resp:
      raw = resp.read().decode("utf-8")
  except urllib.error.URLError as e:
    reason = str(getattr(e, "reason", e))
    # Windows Python installs can miss CA trust in some shells.
    if "CERTIFICATE_VERIFY_FAILED" in reason and os.name == "nt":
      try:
        insecure_ctx = ssl._create_unverified_context()
        with urllib.request.urlopen(req, context=insecure_ctx) as resp:
          raw = resp.read().decode("utf-8")
        print(
          "Warning: TLS cert verification failed in this Windows shell; using insecure fallback for Cloudflare bootstrap call.",
          file=sys.stderr,
        )
      except Exception as e2:
        raise RuntimeError(f"Cloudflare API {method} {path} TLS verify failed and insecure fallback failed: {e2}") from e2
    else:
      raise RuntimeError(f"Cloudflare API {method} {path} network failure: {reason}") from e
  except urllib.error.HTTPError as e:
    body = e.read().decode("utf-8", errors="replace")
    raise RuntimeError(f"Cloudflare API {method} {path} failed: HTTP {e.code}: {body}") from e
  doc = json.loads(raw)
  if not doc.get("success", False):
    raise RuntimeError(f"Cloudflare API {method} {path} failed: {json.dumps(doc.get('errors', []))}")
  return doc.get("result")


def get_account_id(token: str, account_id: str):
  if account_id:
    return account_id
  result = cf_api("GET", "/accounts?page=1&per_page=50", token)
  accounts = result if isinstance(result, list) else result.get("result", [])
  if not accounts:
    raise RuntimeError("No Cloudflare accounts found for this token.")
  return accounts[0]["id"]


def get_permission_group_id(token: str, name: str):
  groups = cf_api("GET", "/user/tokens/permission_groups", token)
  for group in groups:
    if group.get("name") == name:
      return group.get("id")
  return ""


def get_permission_group_id_for_path(token: str, path: str, name: str):
  groups = cf_api("GET", path, token)
  for group in groups:
    if group.get("name") == name:
      return group.get("id")
  return ""


def ensure_bucket(token: str, account_id: str, bucket: str):
  try:
    cf_api(
      "POST",
      f"/accounts/{account_id}/r2/buckets",
      token,
      payload={"name": bucket},
    )
    return
  except RuntimeError as e:
    msg = str(e)
    # Existing bucket still satisfies setup.
    if "already exists" in msg.lower() or "bucket name is not available" in msg.lower():
      return
    # Verify bucket exists; if so continue.
    try:
      cf_api("GET", f"/accounts/{account_id}/r2/buckets/{bucket}", token)
      return
    except Exception:
      raise


def _result_items(result):
  if isinstance(result, list):
    return result
  if isinstance(result, dict):
    if isinstance(result.get("result"), list):
      return result.get("result", [])
    if isinstance(result.get("items"), list):
      return result.get("items", [])
  return []


def _extract_host(url_or_host: str):
  value = (url_or_host or "").strip()
  if not value:
    return ""
  if "://" not in value:
    return value.split("/")[0].strip().lower()
  parsed = urllib.parse.urlparse(value)
  return (parsed.netloc or "").split("@")[-1].split(":")[0].strip().lower()


def _extract_zone_candidates(hostname: str):
  host = (hostname or "").strip().lower().strip(".")
  parts = [p for p in host.split(".") if p]
  candidates = []
  for i in range(0, max(0, len(parts) - 1)):
    zone = ".".join(parts[i:])
    if zone and zone not in candidates:
      candidates.append(zone)
  return candidates


def _find_zone_for_hostname(token: str, hostname: str):
  for zone_name in _extract_zone_candidates(hostname):
    query = urllib.parse.urlencode({"name": zone_name, "status": "active", "per_page": "1"})
    zones = cf_api("GET", f"/zones?{query}", token)
    items = _result_items(zones)
    if items:
      zone_id = items[0].get("id", "")
      if zone_id:
        return zone_id, zone_name
  return "", ""


def _list_tunnels(token: str, account_id: str):
  result = cf_api("GET", f"/accounts/{account_id}/cfd_tunnel?is_deleted=false&per_page=100", token)
  return _result_items(result)


def _find_tunnel_id_from_dns(token: str, zone_id: str, hostname: str):
  query = urllib.parse.urlencode({"name": hostname, "per_page": "100"})
  existing = cf_api("GET", f"/zones/{zone_id}/dns_records?{query}", token)
  items = _result_items(existing)
  for rec in items:
    if (rec.get("type") or "").upper() != "CNAME":
      continue
    content = (rec.get("content") or "").strip().lower().rstrip(".")
    if not content.endswith(".cfargotunnel.com"):
      continue
    tunnel_id = content[: -len(".cfargotunnel.com")]
    if tunnel_id:
      return tunnel_id
  return ""


def _ensure_tunnel(token: str, account_id: str, tunnel_name: str, preferred_tunnel_id: str = ""):
  tunnels = _list_tunnels(token, account_id)
  tunnel_by_id = {item.get("id", ""): item for item in tunnels if item.get("id")}
  if preferred_tunnel_id and preferred_tunnel_id in tunnel_by_id:
    return preferred_tunnel_id
  for item in tunnels:
    if item.get("name") == tunnel_name:
      tunnel_id = item.get("id", "")
      if tunnel_id:
        return tunnel_id
  created = cf_api(
    "POST",
    f"/accounts/{account_id}/cfd_tunnel",
    token,
    payload={"name": tunnel_name, "config_src": "cloudflare"},
  )
  tunnel_id = (created or {}).get("id", "")
  if not tunnel_id:
    raise RuntimeError("Tunnel creation succeeded but tunnel id is missing.")
  return tunnel_id


def _set_tunnel_config(token: str, account_id: str, tunnel_id: str, routes):
  ingress = []
  for route in routes:
    hostname = (route.get("hostname") or "").strip()
    service_url = (route.get("service") or "").strip()
    host_header = (route.get("host_header") or "").strip() or hostname
    no_tls_verify = bool(route.get("no_tls_verify"))
    if not hostname or not service_url:
      continue
    origin_request = {
      "httpHostHeader": host_header,
    }
    if no_tls_verify:
      origin_request["noTLSVerify"] = True
    ingress.append(
      {
        "hostname": hostname,
        "service": service_url,
        "originRequest": origin_request,
      }
    )
  ingress.append({"service": "http_status:404"})
  payload = {"config": {"ingress": ingress}}
  cf_api("PUT", f"/accounts/{account_id}/cfd_tunnel/{tunnel_id}/configurations", token, payload=payload)


def _ensure_dns_record(token: str, zone_id: str, hostname: str, tunnel_id: str):
  content = f"{tunnel_id}.cfargotunnel.com"
  # Fetch all records for this hostname so we can clean conflicting types (A/AAAA/etc.).
  query = urllib.parse.urlencode({"name": hostname, "per_page": "100"})
  existing = cf_api("GET", f"/zones/{zone_id}/dns_records?{query}", token)
  items = _result_items(existing)
  payload = {"type": "CNAME", "name": hostname, "content": content, "proxied": True, "ttl": 1}
  cname_records = []
  for rec in items:
    rec_id = rec.get("id", "")
    rec_type = (rec.get("type") or "").upper()
    if not rec_id:
      continue
    if rec_type == "CNAME":
      cname_records.append(rec)
      continue
    # Remove conflicting records so CNAME can exist cleanly.
    cf_api("DELETE", f"/zones/{zone_id}/dns_records/{rec_id}", token)

  if cname_records:
    keep = cname_records[0]
    keep_id = keep.get("id", "")
    if not keep_id:
      raise RuntimeError("DNS CNAME lookup returned an entry without an id.")
    cf_api("PUT", f"/zones/{zone_id}/dns_records/{keep_id}", token, payload=payload)
    # Delete duplicate CNAME records for this hostname.
    for dup in cname_records[1:]:
      dup_id = dup.get("id", "")
      if dup_id:
        cf_api("DELETE", f"/zones/{zone_id}/dns_records/{dup_id}", token)
    return

  cf_api("POST", f"/zones/{zone_id}/dns_records", token, payload=payload)


def _delete_dns_records_for_hostname(token: str, zone_id: str, hostname: str):
  query = urllib.parse.urlencode({"name": hostname, "per_page": "100"})
  existing = cf_api("GET", f"/zones/{zone_id}/dns_records?{query}", token)
  items = _result_items(existing)
  for rec in items:
    rec_id = rec.get("id", "")
    if not rec_id:
      continue
    cf_api("DELETE", f"/zones/{zone_id}/dns_records/{rec_id}", token)


def _get_tunnel_token(token: str, account_id: str, tunnel_id: str):
  result = cf_api("GET", f"/accounts/{account_id}/cfd_tunnel/{tunnel_id}/token", token)
  if isinstance(result, str) and result.strip():
    return result.strip()
  if isinstance(result, dict):
    value = result.get("token") or result.get("value")
    if isinstance(value, str) and value.strip():
      return value.strip()
  raise RuntimeError("Tunnel token endpoint did not return a token string.")


def _extract_token_id_and_value(result: dict):
  token_id = result.get("id", "")
  token_value = result.get("value", "")
  if not token_id and isinstance(result.get("result"), dict):
    token_id = result["result"].get("id", "")
    token_value = result["result"].get("value", "")
  return token_id, token_value


def create_r2_token_account_owned(token: str, account_id: str, bucket: str):
  perm_path = f"/accounts/{account_id}/tokens/permission_groups"
  read_group_id = get_permission_group_id_for_path(token, perm_path, "Workers R2 Storage Bucket Item Read")
  write_group_id = get_permission_group_id_for_path(token, perm_path, "Workers R2 Storage Bucket Item Write")
  if not write_group_id:
    write_group_id = get_permission_group_id_for_path(token, perm_path, "Workers R2 Storage Write")
  if not read_group_id:
    read_group_id = get_permission_group_id_for_path(token, perm_path, "Workers R2 Storage Read")
  permission_groups = []
  if read_group_id:
    permission_groups.append({"id": read_group_id})
  if write_group_id and write_group_id != read_group_id:
    permission_groups.append({"id": write_group_id})
  if not permission_groups:
    raise RuntimeError("Could not find required account token permission groups for R2.")

  resources = {f"com.cloudflare.edge.r2.bucket.{account_id}_default_{bucket}": "*"}
  policy = [{
    "effect": "allow",
    "permission_groups": permission_groups,
    "resources": resources,
  }]
  payload = {"name": f"cluster-r2-{int(time.time())}", "policies": policy}
  result = cf_api("POST", f"/accounts/{account_id}/tokens", token, payload=payload)
  token_id, token_value = _extract_token_id_and_value(result)
  if not token_id or not token_value:
    raise RuntimeError("Account token create succeeded but did not return token id/value.")
  secret_access_key = hashlib.sha256(token_value.encode("utf-8")).hexdigest()
  return token_id, secret_access_key


def create_r2_token_user_owned(token: str, account_id: str, bucket: str):
  read_group_id = get_permission_group_id(token, "Workers R2 Storage Bucket Item Read")
  write_group_id = get_permission_group_id(token, "Workers R2 Storage Bucket Item Write")
  if not write_group_id:
    write_group_id = get_permission_group_id(token, "Workers R2 Storage Write")
  if not read_group_id:
    read_group_id = get_permission_group_id(token, "Workers R2 Storage Read")
  permission_groups = []
  if read_group_id:
    permission_groups.append({"id": read_group_id})
  if write_group_id and write_group_id != read_group_id:
    permission_groups.append({"id": write_group_id})
  if not permission_groups:
    raise RuntimeError("Could not find required Cloudflare permission group for R2 token creation.")

  resources = {
    f"com.cloudflare.edge.r2.bucket.{account_id}_default_{bucket}": "*"
  }
  policy = [{
    "effect": "allow",
    "permission_groups": permission_groups,
    "resources": resources
  }]

  payload = {
    "name": f"cluster-r2-{int(time.time())}",
    "policies": policy,
  }
  result = cf_api("POST", "/user/tokens", token, payload=payload)
  token_id, token_value = _extract_token_id_and_value(result)
  if not token_id or not token_value:
    raise RuntimeError("User token create succeeded but did not return token id/value.")

  secret_access_key = hashlib.sha256(token_value.encode("utf-8")).hexdigest()
  return token_id, secret_access_key


def create_r2_token(token: str, account_id: str, bucket: str):
  errors = []
  try:
    return create_r2_token_account_owned(token, account_id, bucket)
  except Exception as e:
    errors.append(f"account-owned token path failed: {e}")

  try:
    return create_r2_token_user_owned(token, account_id, bucket)
  except Exception as e:
    errors.append(f"user-owned token path failed: {e}")

  msg = "Unable to create R2 API token.\n" + "\n".join(errors)
  msg += (
    "\nHint: bootstrap PAT must allow creating tokens via API "
    "(Cloudflare template: 'Create additional tokens')."
  )
  raise RuntimeError(msg)


def main():
  parser = argparse.ArgumentParser()
  parser.add_argument("--token", required=True)
  parser.add_argument("--account-id", default="")
  parser.add_argument("--bucket", default="iso")
  parser.add_argument("--ks-base-url", default="https://bootstrap.example.services")
  parser.add_argument("--rancher-public-domain", default="")
  parser.add_argument("--rancher-origin-url", default="http://rancher.cattle-system.svc.cluster.local:80")
  parser.add_argument("--rancher-http-host-header", default="")
  parser.add_argument("--ingress-public-domains", default="")
  parser.add_argument("--ingress-public-domains-cleanup", default="")
  parser.add_argument("--ingress-origin-url", default="https://rke2-ingress-nginx-controller.kube-system.svc.cluster.local:443")
  parser.add_argument("--ingress-origin-no-tls-verify", default="false")
  parser.add_argument("--cloudflared-tunnel-name", default="")
  parser.add_argument("--existing-rancher-cloudflared-tunnel-token", default="")
  parser.add_argument("--existing-r2-access-key-id", default="")
  parser.add_argument("--existing-r2-secret-access-key", default="")
  parser.add_argument("--existing-r2-endpoint", default="")
  args = parser.parse_args()

  account_id = args.account_id
  if not account_id and args.existing_r2_endpoint:
    prefix = ".r2.cloudflarestorage.com"
    if args.existing_r2_endpoint.startswith("https://") and args.existing_r2_endpoint.endswith(prefix):
      account_id = args.existing_r2_endpoint[len("https://") : -len(prefix)]
  account_id = get_account_id(args.token, account_id)
  ensure_bucket(args.token, account_id, args.bucket)
  key_id = args.existing_r2_access_key_id
  secret = args.existing_r2_secret_access_key
  if not key_id or not secret:
    key_id, secret = create_r2_token(args.token, account_id, args.bucket)
  endpoint = f"https://{account_id}.r2.cloudflarestorage.com"
  rancher_tunnel_token = args.existing_rancher_cloudflared_tunnel_token.strip()
  rancher_tunnel_id = ""
  tunnel_routes = []

  rancher_public_domain = (args.rancher_public_domain or "").strip().lower()
  if rancher_public_domain:
    if "/" in rancher_public_domain:
      rancher_public_domain = _extract_host(rancher_public_domain)
    zone_id, zone_name = _find_zone_for_hostname(args.token, rancher_public_domain)
    if not zone_id:
      raise RuntimeError(
        f"Could not find a Cloudflare zone for hostname '{rancher_public_domain}'. "
        "Ensure the domain is in this account and your token includes Zone DNS Edit."
      )
    tunnel_name = (args.cloudflared_tunnel_name or "").strip()
    if not tunnel_name:
      zone_label = zone_name.replace(".", "-")
      tunnel_name = f"rancher-{zone_label}"
    preferred_tunnel_id = _find_tunnel_id_from_dns(args.token, zone_id, rancher_public_domain)
    rancher_tunnel_id = _ensure_tunnel(args.token, account_id, tunnel_name, preferred_tunnel_id=preferred_tunnel_id)
    host_header = (args.rancher_http_host_header or "").strip() or rancher_public_domain
    tunnel_routes.append(
      {
        "hostname": rancher_public_domain,
        "service": args.rancher_origin_url,
        "host_header": host_header,
      }
    )
    _ensure_dns_record(args.token, zone_id, rancher_public_domain, rancher_tunnel_id)

  ingress_public_domains = []
  for raw_value in (args.ingress_public_domains or "").split(","):
    hostname = (raw_value or "").strip().lower()
    if not hostname:
      continue
    if "/" in hostname:
      hostname = _extract_host(hostname)
    if hostname and hostname not in ingress_public_domains:
      ingress_public_domains.append(hostname)

  ingress_cleanup_domains = []
  for raw_value in (args.ingress_public_domains_cleanup or "").split(","):
    hostname = (raw_value or "").strip().lower()
    if not hostname:
      continue
    if "/" in hostname:
      hostname = _extract_host(hostname)
    if hostname and hostname not in ingress_cleanup_domains:
      ingress_cleanup_domains.append(hostname)

  ingress_no_tls_verify = str(args.ingress_origin_no_tls_verify or "").strip().lower() not in {"", "0", "false", "no"}
  for ingress_public_domain in ingress_public_domains:
    zone_id, zone_name = _find_zone_for_hostname(args.token, ingress_public_domain)
    if not zone_id:
      raise RuntimeError(
        f"Could not find a Cloudflare zone for hostname '{ingress_public_domain}'. "
        "Ensure the domain is in this account and your token includes Zone DNS Edit."
      )
    tunnel_name = (args.cloudflared_tunnel_name or "").strip()
    if not tunnel_name:
      zone_label = zone_name.replace(".", "-")
      tunnel_name = f"rancher-{zone_label}"
    preferred_tunnel_id = _find_tunnel_id_from_dns(args.token, zone_id, ingress_public_domain)
    rancher_tunnel_id = _ensure_tunnel(args.token, account_id, tunnel_name, preferred_tunnel_id=preferred_tunnel_id or rancher_tunnel_id)
    tunnel_routes.append(
      {
        "hostname": ingress_public_domain,
        "service": args.ingress_origin_url,
        "host_header": ingress_public_domain,
        "no_tls_verify": ingress_no_tls_verify,
      }
    )
    _ensure_dns_record(args.token, zone_id, ingress_public_domain, rancher_tunnel_id)

  desired_ingress_hosts = set(ingress_public_domains)
  for cleanup_hostname in ingress_cleanup_domains:
    if cleanup_hostname in desired_ingress_hosts:
      continue
    zone_id, _zone_name = _find_zone_for_hostname(args.token, cleanup_hostname)
    if not zone_id:
      continue
    existing = cf_api(
      "GET",
      f"/zones/{zone_id}/dns_records?{urllib.parse.urlencode({'name': cleanup_hostname, 'per_page': '100'})}",
      args.token,
    )
    records = _result_items(existing)
    tunnel_records = [
      rec for rec in records
      if (rec.get("content") or "").strip().lower().endswith(".cfargotunnel.com")
    ]
    if tunnel_records:
      _delete_dns_records_for_hostname(args.token, zone_id, cleanup_hostname)

  if rancher_tunnel_id and tunnel_routes:
    _set_tunnel_config(args.token, account_id, rancher_tunnel_id, tunnel_routes)
    rancher_tunnel_token = _get_tunnel_token(args.token, account_id, rancher_tunnel_id)

  print(f"CLOUDFLARE_ACCOUNT_ID={account_id}")
  print(f"R2_BUCKET={args.bucket}")
  print(f"R2_ENDPOINT={endpoint}")
  print(f"R2_ACCESS_KEY_ID={key_id}")
  print(f"R2_SECRET_ACCESS_KEY={secret}")
  print(f"KS_BASE_URL={args.ks_base_url}")
  print(f"RANCHER_CLOUDFLARED_TUNNEL_TOKEN={rancher_tunnel_token}")
  print(f"RANCHER_CLOUDFLARED_TUNNEL_ID={rancher_tunnel_id}")


if __name__ == "__main__":
  try:
    main()
  except Exception as exc:
    print(str(exc), file=sys.stderr)
    sys.exit(1)
