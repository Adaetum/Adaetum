"""Make Homebrew tools findable when a validator runs under a minimal PATH.

GitHub Desktop and other GUI git clients start hooks with a bare PATH that lacks
/opt/homebrew/bin, /usr/local/bin and the sbin directories, so `kubectl` is "missing" even though it is
installed. Importing this module appends those directories (only if absent, and
last, so an existing PATH always wins). It installs nothing and reads no config.
"""
import os

for _directory in ("/opt/homebrew/bin", "/usr/local/bin", "/usr/sbin", "/sbin"):
    if os.path.isdir(_directory) and _directory not in os.environ.get("PATH", "").split(os.pathsep):
        os.environ["PATH"] = os.environ.get("PATH", "") + os.pathsep + _directory
