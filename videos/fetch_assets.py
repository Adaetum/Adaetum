"""Download the Polyfork models the scenes use into assets/polyfork/ (they are not stored in git).
The ids come from PROPS in case/assets.js. Run once after cloning: python3 fetch_assets.py
Models are free to use under the Polyfork licence (https://polyfork.dev/licensing). Fetch on demand, never in bulk."""
import re, sys, time, urllib.request, urllib.error
from pathlib import Path

here = Path(__file__).resolve().parent
ids = re.findall(r"'([a-z0-9-]+-[0-9a-f]{6})'", (here / "case" / "assets.js").read_text())
out = here / "assets" / "polyfork"; out.mkdir(parents=True, exist_ok=True)
for i in ids:
    dst = out / f"{i}.glb"
    if dst.exists(): continue
    for attempt in range(4):
        try:
            req = urllib.request.Request(f"https://polyfork.dev/cdn/{i}.glb", headers={"User-Agent": "film-pipeline/0.1"})
            dst.write_bytes(urllib.request.urlopen(req, timeout=30).read()); print("fetched", i); break
        except urllib.error.HTTPError as e:
            if e.code == 429: time.sleep(5 * (attempt + 1)); continue   # back off when asked to
            print("failed", i, e); sys.exit(1)
    time.sleep(.5)
print(len(ids), "models in", out)
