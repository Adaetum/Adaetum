# Videos

The explainer film for Adaetum, and the player that shows it. Every scene is a small Three.js
program that is a pure function of time, so any frame can be rendered on its own. The same scenes
feed the narration (Kokoro), the player, and the per-shot proxy packages (`export_h3.py`).

## Run the player

```bash
python3 fetch_assets.py   # once: downloads the Polyfork models the scenes use into assets/
python3 serve.py          # http://127.0.0.1:8001/player.html (narration included)
```

`serve.py` binds all interfaces so the player can be opened from another machine on the LAN.
Subtitles are drawn by the player only, never inside the scenes.

## What is here

| Path | What it is |
| --- | --- |
| `player.html` | the player: scrubber, chapter and episode bars, subtitles, narration audio |
| `case/` | the scenes. `main.js` assembles them; `lib.js` is the shared set (hall, altars, people); `shots/` holds the shots |
| `case/shots/what_when.js` | chapter C, "what happens when": one default scene driven by a table of scenarios |
| `narration.json` | one narration line per shot |
| `objectives.json` | per shot: title, objective, what is on screen, and optionally `seconds` (up to 6; default 5.17) |
| `build_audio.py` | narration to `audio/narration.mp3` (Kokoro or Piper) |
| `export_h3.py` | builds a package per shot (`proxy.mp4`, `reference.png`, `prompt.txt`) for a video model. It only builds them; nothing here sends them anywhere |
| `storyboard*.md`, `REFACTOR_PLAN.md` | the script and the design notes |
| `vendor/` | Three.js r160 and its glTF loader (MIT) |

## Visual language

A server or machine is always a stone altar carrying an open book, with a service relic floating above it.
The five services are the relics. Where there is a person, there is one: you.

## Rebuilding the narration

`audio/narration.mp3` is committed so the player has sound out of the box. Only that file is kept; the per-line clips are not.

```bash
pip install kokoro-onnx soundfile
KOKORO_DIR=/path/to/kokoro-models python3 build_audio.py
```

`KOKORO_DIR` holds `kokoro-v1.0.onnx` and `voices-v1.0.bin`. The script warns when a line is too long for its shot,
and fixes the pronunciation of "Adaetum" (a-DAY-tum), "Gitea" and "Cloudflared" before speaking.

## Credits

The props are low-poly models from [Polyfork](https://polyfork.dev), used under its free licence. They are
downloaded on demand and not stored in this repository.
