#!/usr/bin/env python3
"""Check that release metadata only names complete, immutable artifacts."""
import hashlib
import json
import os
import subprocess
import sys
import tempfile
from pathlib import Path

ROOT = Path(__file__).resolve().parents[1]
SCRIPT = ROOT / "adaetum/release-manifest.py"


def main() -> None:
    with tempfile.TemporaryDirectory(prefix="adaetum-release-") as temporary:
        directory = Path(temporary)
        env = {**os.environ, "ADAETUM_VERSION": "1.2.3", "ADAETUM_RELEASE_TAG": "adaetum-v1.2.3", "GITHUB_REPOSITORY": "Adaetum/Adaetum", "GITHUB_SHA": "a" * 40}
        for component in ("agent", "auth", "embed"):
            (directory / f"{component}.image").write_text(f"ghcr.io/adaetum/adaetum-{component}@sha256:" + "b" * 64 + "\n")
        for component in ("agent", "auth"):
            for arch in ("amd64", "arm64"):
                (directory / f"adaetum-{component}-linux-{arch}").write_bytes(f"{component}-{arch}".encode())
        subprocess.run([sys.executable, str(SCRIPT), str(directory)], env=env, check=True)
        manifest = json.loads((directory / "release.json").read_text())
        assert manifest["source_commit"] == "a" * 40
        assert manifest["binaries"]["adaetum-agent-linux-amd64"]["sha256"] == hashlib.sha256(b"agent-amd64").hexdigest()
        assert all("@sha256:" in image for image in manifest["images"].values())
        beta_env = {**env, "ADAETUM_VERSION": "1.2.3-beta.7", "ADAETUM_RELEASE_TAG": "adaetum-v1.2.3-beta.7"}
        subprocess.run([sys.executable, str(SCRIPT), str(directory)], env=beta_env, check=True)
        beta = json.loads((directory / "release.json").read_text())
        assert beta["version"] == "1.2.3-beta.7"
        assert "/adaetum-v1.2.3-beta.7/" in beta["binaries"]["adaetum-agent-linux-amd64"]["url"]
        (directory / "agent.image").write_text("ghcr.io/attacker/adaetum-agent@sha256:" + "b" * 64)
        invalid = subprocess.run([sys.executable, str(SCRIPT), str(directory)], env=env, capture_output=True)
        assert invalid.returncode != 0, "release accepted an unrelated registry owner"
    print("agent release artifact manifest verified")


if __name__ == "__main__":
    main()
