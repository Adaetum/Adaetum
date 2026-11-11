#!/usr/bin/env python3
"""Pin down what the Tailscale and R2 reset may delete.

Reset removes things from the operator's real tailnet and bucket, so what it selects
is a safety contract: only devices carrying a tag setup applies, only OAuth clients
whose ID the operator recorded, only log objects under one host's exact prefix, and
nothing deleted that was not both confirmed and still selected on a fresh listing.
This loads the real scripts and exercises their selection and delete functions
against fake data; it makes no network calls.
"""
import importlib.util
import json
import sys
from pathlib import Path

ROOT = Path(__file__).resolve().parents[1]
SCRIPT = ROOT / "tasks" / "scripts" / "bootstrap-tailscale.py"
CLOUDFLARE = ROOT / "tasks" / "scripts" / "bootstrap-cloudflare.py"


def load(path=SCRIPT, name="bootstrap_tailscale"):
    spec = importlib.util.spec_from_file_location(name, path)
    module = importlib.util.module_from_spec(spec)
    spec.loader.exec_module(module)
    return module


def main() -> int:
    ts = load()
    failures: list[str] = []

    def check(condition: bool, message: str) -> None:
        if not condition:
            failures.append(message)

    devices = [
        {"id": "1", "hostname": "node-42187", "tags": ["tag:rocky10", "tag:server", "tag:cluster"]},
        {"id": "2", "hostname": "adaetum-ingress-0", "tags": ["tag:k8s"]},
        {"id": "3", "hostname": "operator", "tags": ["tag:k8s-operator"]},
        {"id": "4", "hostname": "laptop", "tags": []},
        {"id": "5", "hostname": "phone"},
        {"id": "6", "hostname": "someone-elses", "tags": ["tag:other"]},
        {"hostname": "no-id", "tags": ["tag:server"]},
        "not-a-device",
    ]
    chosen = {d["id"]: d for d in ts.select_reset_devices(devices, "tag:cluster")}
    check(set(chosen) == {"1", "2", "3"}, f"reset selected {sorted(chosen)}, want only the tagged cluster devices 1, 2, 3")
    check(chosen["1"]["isHost"] and not chosen["2"]["isHost"], "only hosts, not proxies, may have their install logs removed")

    custom = {d["id"] for d in ts.select_reset_devices([{"id": "9", "tags": ["tag:lab"]}], "lab")}
    check(custom == {"9"}, "a custom cluster tag is not honoured")

    keys = [
        {"id": "kA", "keyType": "client", "description": "Adaetum node enrollment"},
        {"id": "kB", "keyType": "client", "description": "Adaetum Kubernetes operator"},
        {"id": "kC", "keyType": "client", "description": "Adaetum node enrollment"},  # same name, not recorded
        {"id": "kD", "keyType": "auth", "description": "something"},
    ]
    clients = {c["id"] for c in ts.select_reset_clients(keys, ["kA", "kB", "kD", ""])}
    check(clients == {"kA", "kB"}, f"reset selected OAuth clients {sorted(clients)}; the description alone must never match and auth keys are not clients")

    calls: list[str] = []
    ts._request = lambda method, url, **kw: calls.append(f"{method} {url}") or (200, {}, "")
    current = ts.select_reset_devices(devices, "tag:cluster")
    gone = ts.delete_reset_devices("token", current, ["1", "4", "999"])
    check([d["id"] for d in gone] == ["1"], "deleted an ID that was not both confirmed and currently selected")
    check(calls == ["DELETE https://api.tailscale.com/api/v2/device/1"], f"unexpected delete calls: {calls}")

    calls.clear()
    current_clients = ts.select_reset_clients(keys, ["kA"])
    ts.delete_reset_clients("token", "example.ts.net", current_clients, ["kA", "kC"])
    check(calls == ["DELETE https://api.tailscale.com/api/v2/tailnet/example.ts.net/keys/kA"], f"unexpected client delete calls: {calls}")

    calls.clear()
    ts.delete_reset_devices("token", current, [])
    check(not calls, "an empty confirmation list must delete nothing")

    # The SSH rule: only setup's exact rule is selected or removed; an operator's own SSH
    # rules, even similar ones, and every other part of the policy are left alone.
    ours = ts.default_ssh_rule("tag:cluster")
    check(ours == {"action": "accept", "src": ["autogroup:owner"], "dst": ["tag:cluster"], "users": ["ansible"]}, f"setup's SSH rule is not what the docs promise: {ours}")
    theirs = [
        {"action": "accept", "src": ["autogroup:owner"], "dst": ["tag:cluster"], "users": ["root"]},
        {"action": "accept", "src": ["autogroup:owner"], "dst": ["tag:cluster"], "users": ["ansible"], "checkPeriod": "12h"},
        {"action": "check", "src": ["autogroup:owner"], "dst": ["tag:cluster"], "users": ["ansible"]},
        {"action": "accept", "src": ["autogroup:member"], "dst": ["tag:cluster"], "users": ["ansible"]},
        {"action": "accept", "src": ["autogroup:owner"], "dst": ["tag:other"], "users": ["ansible"]},
        "not-a-rule",
    ]
    policy = {"acls": [{"action": "accept", "src": ["*"], "dst": ["*:*"]}], "tagOwners": {"tag:cluster": ["autogroup:admin"]}, "ssh": theirs + [dict(ours)]}
    check(ts.select_ssh_rules(policy, "tag:cluster") == [ours], "selected an SSH rule that is not exactly setup's")
    check(ts.select_ssh_rules({"ssh": theirs}, "tag:cluster") == [], "an operator's own SSH rules were selected")
    check(ts.select_ssh_rules({}, "tag:cluster") == [] and ts.select_ssh_rules({"ssh": "x"}, "tag:cluster") == [], "a policy with no ssh list selected something")

    import copy
    posted: list = []

    def fake_policy_api(method, url, **kw):
        if method == "GET":
            return 200, {"acl": json.dumps(copy.deepcopy(policy))}, ""
        posted.append(kw.get("payload"))
        return 200, {}, ""

    ts._request = fake_policy_api
    check(len(ts.list_reset_ssh_rule("t", "example.ts.net", "tag:cluster")) == 1, "listing did not find setup's rule")
    check(not posted, "listing changed the policy")
    removed = ts.delete_reset_ssh_rule("t", "example.ts.net", "tag:cluster")
    check(len(removed) == 1 and len(posted) == 1, "removing did not write the policy exactly once")
    if posted:
        check(posted[0]["ssh"] == theirs, "removing setup's rule changed other SSH rules")
        check(posted[0]["acls"] == policy["acls"] and posted[0]["tagOwners"] == policy["tagOwners"], "removing the SSH rule changed the rest of the policy")

    posted.clear()
    only_ours = {"acls": [], "ssh": [dict(ours)]}
    ts._request = lambda method, url, **kw: (200, {"acl": json.dumps(only_ours)}, "") if method == "GET" else posted.append(kw.get("payload")) or (200, {}, "")
    ts.delete_reset_ssh_rule("t", "example.ts.net", "tag:cluster")
    check(posted and "ssh" not in posted[0], "an emptied ssh list should be dropped, not left as []")

    posted.clear()
    ts._request = lambda method, url, **kw: (200, {"acl": json.dumps({"ssh": theirs})}, "") if method == "GET" else posted.append(kw.get("payload")) or (200, {}, "")
    check(ts.delete_reset_ssh_rule("t", "example.ts.net", "tag:cluster") == [] and not posted, "wrote the policy back when there was nothing of setup's to remove")

    # Setup adds the same rule (add-only), so reset can remove exactly what setup put there.
    added = {"acls": [{"action": "accept", "src": ["*"], "dst": ["*:*"]}], "tagOwners": {}}
    check(ts._merge_ssh_rule(added, "tag:cluster") is True and added["ssh"] == [ours], "setup did not add its SSH rule to a policy without an ssh section")
    check(ts._merge_ssh_rule(added, "tag:cluster") is False and added["ssh"] == [ours], "setup added its SSH rule twice")
    default_rule = {"action": "check", "src": ["autogroup:member"], "dst": ["autogroup:self"], "users": ["autogroup:nonroot", "root"]}
    existing = {"ssh": [dict(default_rule), dict(theirs[0])]}
    check(ts._merge_ssh_rule(existing, "tag:cluster") is True, "setup did not add its SSH rule next to existing ones")
    check(existing["ssh"][:2] == [default_rule, theirs[0]] and existing["ssh"][2] == ours, "adding the SSH rule changed or reordered the operator's own rules")
    check(ts._merge_ssh_rule({"ssh": []}, "tag:cluster") is True, "setup did not add its SSH rule to an empty ssh list")
    custom_tag_policy: dict = {}
    ts._merge_ssh_rule(custom_tag_policy, "lab")
    check(custom_tag_policy["ssh"][0]["dst"] == ["tag:lab"], "the SSH rule did not follow a custom cluster tag")
    try:
        ts._merge_ssh_rule({"ssh": "oops"}, "tag:cluster")
        failures.append("an ssh section that is not a list was overwritten")
    except RuntimeError:
        pass

    # Through the real ensure function: one POST carrying the rule, and nothing posted when
    # the policy already has everything setup needs (so a rerun does not rewrite the policy).
    base = {"tagOwners": {}, "acls": [], "grants": [], "autoApprovers": {}}
    state = {"policy": copy.deepcopy(base)}
    writes: list = []

    def fake_ensure_api(method, url, **kw):
        if method == "GET":
            return 200, {"acl": json.dumps(copy.deepcopy(state["policy"]))}, ""
        writes.append(kw.get("payload"))
        state["policy"] = copy.deepcopy(kw.get("payload"))
        return 200, {}, ""

    ts._request = fake_ensure_api
    ts.ensure_required_tag_owners("example.ts.net", user_token="tok", cluster_tag="tag:cluster")
    check(len(writes) == 1 and ours in writes[0].get("ssh", []), "preparing the policy did not write the SSH rule")
    ts.ensure_required_tag_owners("example.ts.net", user_token="tok", cluster_tag="tag:cluster")
    check(len(writes) == 1, "a second run rewrote a policy that already had everything")

    cf = load(CLOUDFLARE, "bootstrap_cloudflare")
    check(cf.host_log_prefix("node-42187") == "logs/node-42187/", "host log prefix is not the exact host folder")
    for bad in ("", " ", "../x", "a/b", "node 1", "-x", "x" * 80, "node-1/", "*"):
        try:
            cf.host_log_prefix(bad)
        except RuntimeError:
            continue
        failures.append(f"host name {bad!r} produced a log prefix; it could match other hosts' logs")
    check(cf.host_log_prefix(" Node-7 ") == "logs/node-7/", "host names are not normalised")

    store = {"logs/node-1/a.log": 1, "logs/node-1/b.log": 1, "logs/node-10/c.log": 1, "logs/other/d.log": 1}
    deleted: list[str] = []

    def fake_cf_api(method, path, token, payload=None):
        if method == "GET":
            prefix = path.split("prefix=")[1].split("&")[0].replace("%2F", "/")
            return [{"key": k} for k in store if k.startswith(prefix)] + [{"key": "logs/leak/x.log"}]
        key = path.rsplit("/", 1)[1].replace("%2F", "/")
        deleted.append(key)
        store.pop(key, None)
        return {"key": key}

    cf.cf_api = fake_cf_api
    check(cf.delete_host_logs("t", "acct", "iso", "node-1") == 2, "wrong number of objects deleted for node-1")
    check(sorted(deleted) == ["logs/node-1/a.log", "logs/node-1/b.log"], f"deleted outside the host's exact prefix: {deleted}")
    check("logs/node-10/c.log" in store and "logs/other/d.log" in store, "deleting node-1 logs touched node-10 or another host")

    for failure in failures:
        print(failure, file=sys.stderr)
    if failures:
        return 1
    print("Cloud reset selection check passed: only tagged devices, recorded OAuth clients, setup's exact SSH rule and one host's exact log prefix; nothing unconfirmed.")
    return 0


if __name__ == "__main__":
    raise SystemExit(main())
