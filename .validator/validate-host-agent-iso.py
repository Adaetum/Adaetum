#!/usr/bin/env python3
"""Exercise the host-agent media download and checksum boundary."""
import hashlib
import importlib.util
import json
import sys
import tempfile
from io import BytesIO
from pathlib import Path
from unittest.mock import patch

ROOT = Path(__file__).resolve().parents[1]
spec = importlib.util.spec_from_file_location("fetch_host_agent", ROOT / "tasks/scripts/fetch-host-agent.py")
module = importlib.util.module_from_spec(spec)
spec.loader.exec_module(module)

with tempfile.TemporaryDirectory() as directory:
    root = Path(directory)
    payload = b"test agent binary"
    lock = root / "release.json"
    lock.write_text(json.dumps({"version": "0.1.2-beta.1", "sha256": {"amd64": hashlib.sha256(payload).hexdigest()}}))
    output = root / "agent"
    with patch.object(module, "LOCK", lock), patch.object(sys, "argv", ["fetch", "--arch", "amd64", "--output", str(output)]), patch.object(module.urllib.request, "urlopen", return_value=BytesIO(payload)) as request:
        module.main()
        assert output.read_bytes() == payload
        assert "/adaetum-v0.1.2-beta.1/adaetum-agent-linux-amd64" in request.call_args.args[0]
        with patch.object(module.urllib.request, "urlopen", return_value=BytesIO(b"corrupt")):
            try:
                module.main()
            except ValueError as error:
                assert "checksum" in str(error)
            else:
                raise AssertionError("incorrect agent checksum was accepted")
        assert output.read_bytes() == payload, "failed download replaced verified binary"
print("host agent media download and checksum verified")
