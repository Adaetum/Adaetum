#!/usr/bin/env python3
import argparse
import base64
import json
import socket
import sys
import time
import urllib.error
import urllib.request
from typing import Optional

API_BASE = "https://api.github.com"
DEFAULT_TIMEOUT_SECONDS = 20
MAX_ATTEMPTS = 4


def _retry_delay_seconds(attempt: int, response_headers=None) -> float:
    if response_headers is not None:
        retry_after = response_headers.get("Retry-After")
        if retry_after:
            try:
                return max(1.0, float(retry_after))
            except ValueError:
                pass
    # 1s, 2s, 4s for follow-up attempts.
    return float(2 ** max(0, attempt - 1))


def _is_retryable_http_error(err: urllib.error.HTTPError) -> bool:
    return err.code == 429 or 500 <= err.code < 600


def api_request(method: str, path: str, token: str, payload=None):
    url = f"{API_BASE}{path}"
    body = None
    if payload is not None:
        body = json.dumps(payload).encode("utf-8")
    req = urllib.request.Request(url=url, data=body, method=method.upper())
    req.add_header("Accept", "application/vnd.github+json")
    req.add_header("Authorization", f"Bearer {token}")
    req.add_header("X-GitHub-Api-Version", "2022-11-28")
    req.add_header("User-Agent", "cluster-bootstrap-scripts")
    if body is not None:
        req.add_header("Content-Type", "application/json")
    for attempt in range(1, MAX_ATTEMPTS + 1):
        try:
            with urllib.request.urlopen(req, timeout=DEFAULT_TIMEOUT_SECONDS) as resp:
                status = resp.getcode()
                raw = resp.read()
                text = raw.decode("utf-8") if raw else ""
                return status, text
        except urllib.error.HTTPError as e:
            raw = e.read()
            text = raw.decode("utf-8", errors="replace") if raw else ""
            if attempt < MAX_ATTEMPTS and _is_retryable_http_error(e):
                time.sleep(_retry_delay_seconds(attempt, e.headers))
                continue
            raise RuntimeError(f"HTTP {e.code}: {text}") from e
        except (urllib.error.URLError, TimeoutError, socket.timeout) as e:
            if attempt < MAX_ATTEMPTS:
                time.sleep(_retry_delay_seconds(attempt))
                continue
            raise RuntimeError(f"Network error: {e}") from e


def ensure_env(repo: str, env_name: str, token: str):
    path = f"/repos/{repo}/environments/{env_name}"
    api_request("PUT", path, token, payload={})
    return 0


def encrypt_secret(public_key_b64: str, value: str) -> str:
    try:
        from nacl import encoding, public  # type: ignore
    except Exception:
        print(
            "PyNaCl is required for GitHub secret encryption. Install with: python3 -m pip install pynacl",
            file=sys.stderr,
        )
        return ""

    pk = public.PublicKey(public_key_b64.encode("utf-8"), encoding.Base64Encoder())
    sealed_box = public.SealedBox(pk)
    encrypted = sealed_box.encrypt(value.encode("utf-8"))
    return base64.b64encode(encrypted).decode("utf-8")


def set_secret(repo: str, name: str, value: str, token: str, env_name: Optional[str]):
    if env_name:
        key_path = f"/repos/{repo}/environments/{env_name}/secrets/public-key"
        put_path = f"/repos/{repo}/environments/{env_name}/secrets/{name}"
    else:
        key_path = f"/repos/{repo}/actions/secrets/public-key"
        put_path = f"/repos/{repo}/actions/secrets/{name}"

    try:
        _, key_text = api_request("GET", key_path, token)
    except RuntimeError as e:
        msg = str(e)
        if env_name and ("HTTP 404" in msg or "HTTP 403" in msg):
            print(msg, file=sys.stderr)
            return 4
        print(msg, file=sys.stderr)
        return 1

    try:
        key_obj = json.loads(key_text)
        public_key = key_obj["key"]
        key_id = key_obj["key_id"]
    except Exception as e:
        print(f"Invalid public key payload: {e}", file=sys.stderr)
        return 1

    encrypted_value = encrypt_secret(public_key, value)
    if not encrypted_value:
        return 3

    payload = {"encrypted_value": encrypted_value, "key_id": key_id}
    try:
        api_request("PUT", put_path, token, payload=payload)
    except RuntimeError as e:
        print(str(e), file=sys.stderr)
        return 1
    return 0


def main() -> int:
    parser = argparse.ArgumentParser()
    sub = parser.add_subparsers(dest="cmd", required=True)

    p_env = sub.add_parser("ensure-env")
    p_env.add_argument("--repo", required=True)
    p_env.add_argument("--env", required=True)
    p_env.add_argument("--token", required=True)

    p_set = sub.add_parser("set-secret")
    p_set.add_argument("--repo", required=True)
    p_set.add_argument("--name", required=True)
    p_set.add_argument("--value", required=True)
    p_set.add_argument("--token", required=True)
    p_set.add_argument("--env", required=False, default="")

    args = parser.parse_args()
    if args.cmd == "ensure-env":
        try:
            return ensure_env(args.repo, args.env, args.token)
        except RuntimeError as e:
            print(str(e), file=sys.stderr)
            return 1
    if args.cmd == "set-secret":
        return set_secret(args.repo, args.name, args.value, args.token, args.env or None)
    return 2


if __name__ == "__main__":
    raise SystemExit(main())
