#!/usr/bin/env python3
"""Print the hostname advertised by a container registry Bearer challenge."""
from __future__ import annotations

import re
import sys
from urllib.parse import urlparse


headers = sys.stdin.read()
match = re.search(
    r'^www-authenticate:\s*Bearer\s+[^\r\n]*realm="?([^",\s]+)',
    headers,
    flags=re.IGNORECASE | re.MULTILINE,
)
if match:
    print(urlparse(match.group(1)).hostname or "")
