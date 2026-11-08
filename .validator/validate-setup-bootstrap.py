#!/usr/bin/env python3
"""Exercise setup's missing-tool and reuse paths without installing host tools."""
from __future__ import annotations

import os
import shutil
import subprocess
import tempfile
from pathlib import Path

ROOT = Path(__file__).resolve().parents[1]


def write_exec(path: Path, content: str) -> None:
    path.write_text(content, encoding="utf-8")
    path.chmod(0o755)


def main() -> None:
    with tempfile.TemporaryDirectory(prefix="adaetum-setup-bootstrap-") as temp:
        root = Path(temp)
        bindir = root / "bin"
        bindir.mkdir()
        installer = root / "checkout" / "installer"
        installer.mkdir(parents=True)
        shutil.copy2(ROOT / "installer" / "install-bootstrap.sh", installer)
        (installer / "go.mod").write_text("module example\n\ngo 1.27.3\n", encoding="utf-8")
        for tool in ("dirname", "mkdir", "chmod", "awk"):
            source = shutil.which(tool)
            if not source:
                raise RuntimeError(f"test requires {tool}")
            (bindir / tool).symlink_to(source)
        for tool in ("git", "curl"):
            write_exec(bindir / tool, "#!/bin/sh\nexit 0\n")
        write_exec(bindir / "go", "#!/bin/sh\nif [ \"$1\" = env ]; then echo auto; fi\nexit 0\n")
        write_exec(bindir / "python3", """#!/bin/sh
if [ "$1" = -c ]; then exit 1; fi
if [ "$1" = -m ] && [ "$2" = venv ]; then
  mkdir -p "$3/bin"
  printf '%s\\n' '#!/bin/sh' 'if [ "$1" = -c ]; then test -f "'"$3"'/ready"; exit $?; fi' 'if [ "$1" = -m ]; then : > "'"$3"'/ready"; exit 0; fi' > "$3/bin/python3"
  chmod +x "$3/bin/python3"
  exit 0
fi
exit 1
""")
        write_exec(bindir / "brew", """#!/bin/sh
printf '%s\\n' "$*" >> "$MOCK_INSTALL_LOG"
if [ "$1" = install ] && [ "$2" = node ]; then
  printf '#!/bin/sh\\nexit 0\\n' > "$MOCK_BIN/node"
  printf '#!/bin/sh\\nexit 0\\n' > "$MOCK_BIN/npm"
  chmod +x "$MOCK_BIN/node" "$MOCK_BIN/npm"
  exit 0
fi
exit 1
""")
        env = os.environ.copy()
        env.update(HOME=str(root / "home"), PATH=str(bindir), MOCK_BIN=str(bindir), MOCK_INSTALL_LOG=str(root / "installs"))
        command = ["/bin/bash", str(installer / "install-bootstrap.sh")]
        subprocess.run(command, env=env, check=True, capture_output=True, text=True)
        installs = (root / "installs").read_text(encoding="utf-8").splitlines()
        assert installs == ["install node"], f"unexpected installs: {installs}"
        assert (root / "checkout" / ".adaetum" / "setup-python" / "ready").exists(), "PyYAML venv was not prepared"
        subprocess.run(command, env=env, check=True, capture_output=True, text=True)
        assert (root / "installs").read_text(encoding="utf-8").splitlines() == installs, "reused tools were reinstalled"
    print("setup bootstrap install and reuse checks passed")


if __name__ == "__main__":
    main()
