# Consolidation plan: 151 shots to about 95

Goal: keep every fact, drop the formulaic rhythm (pair or triple of beats repeated for every item). Each section is built so it can be published as its own episode and also cut back into the full film.

## Targets
| Section | Now | Target | New form |
|---|---|---|---|
| Part 1 (1-25) | 25 | ~22 | trim hosting-option transitions only |
| A (26-28) | 3 | 3 | unchanged |
| B tools (29-62) | 34 | ~16 | "life of a commit": follow one change through the system; most tools get one shot where they act, 3 get a second beat |
| C scenarios (63-115) | 53 | ~25 | 4-5 headline scenarios in full (machine dies, bad change, attacker, cluster lost, +1); the rest as a montage or one split-screen shot; merge scale out/up, fold limits into scaling, drop second removal option |
| E start (116-137) | 22 | ~16 | merge shots 123-127 (roles, lifetimes, secrets) into a two-shot "three accounts" block |
| D compared (138-151) | 14 | ~7 | by buyer question (who runs it, cost, when it breaks, support), five roads on one map per shot; keep "what it doesn't do yet" and "choose your road" |

## Reusable shot templates (data driven)
1. split-screen compare (usual left, Adaetum right): one world, one camera, two staged halves, so each shot is still a single frame for H3 (not ArrayCamera/scissor)
2. montage (3-4 quick beats in one shot)
3. follow-the-object path (one object travels through stations)
4. map of roads (five paths, highlight one)
Shot content lives in a data file, so reordering or rewording an episode does not touch scene code.

## Order of work
0. Foundations (new, from the Three.js skills review; do before any template):
   - Measure first: log `renderer.info.render.calls/triangles` and `info.memory.geometries/textures` for a few shots (hall, a B tool shot, a C scenario) and set a per-shot budget.
   - Shots are built once and never freed (`ensure()` in main.js only hides them), so GPU memory climbs across a full render. Dispose a shot's group (traverse geometry/material/textures) when the film moves 2+ shots past it, or share one hall and one prop cache across shots.
   - Cache repeated geometry and materials in lib.js; use InstancedMesh for repeats (columns, floor rings, the five roads' markers) and `mergeGeometries` for the static hall.
   - Stay on r160 APIs. Two of the skills target r186 (Timer, WebGPU/TSL, newer BatchedMesh); do not copy those.
   - Keep the deterministic `seek(t)` model. Do not adopt AnimationMixer's delta clock; if a keyframe clip is ever needed, drive it with `mixer.setTime(t)`.
1. Pilot D: build the map-of-roads and split-screen templates, rewrite D narration and objectives (7 shots).
2. Chapter E merge (shots 123-127).
3. B as "life of a commit".
4. C: choose headline scenarios, build the montage template, rewrite.
5. Part 1 trim.
6. Re-cut episodes (see the episode list in chat: 13 episodes) and add an episode row to the player.

## Everything that must change with each step
- case/main.js shot list and case/shots/*.js
- narration.json (one line per shot) and objectives.json
- audio/narration.mp3 (rebuild with build_audio.py; the player assumes one line per shot)
- player.html chapter ranges (CHAPTERS array) and any shot-number references in storyboard docs
- export_h3.py shot indexes if H3 renders have started (they have not for the full film)

## Rules while refactoring
- Facts only from the Adaetum docs; do not claim Adaetum beats a paid vendor on support or scale.
- Every shot still stands alone for H3.
- Keep a backup of each file before the first edit (scratchpad) and run audit_all.py after every step (must report 0 errors).
- Keep about 13 words of narration per shot.

## Subtitles (player only)
Done in player.html: narration phrases shown over the video, timed by length across each shot, with a toggle. They are not rendered into the Three.js scene, so they never reach H3. When narration is rewritten per step, subtitles follow automatically from narration.json.

## Status (2026-11-15): steps 0-4 done, film is now 90 shots (was 151)
| Section | Shots now | Was |
|---|---|---|
| Part 1 | 1-25 | 1-25 (not trimmed yet) |
| A | 26-28 | 26-28 |
| B (journey shots, nine groups of one or two tools) | 29-37 | 29-62 |
| C (4 headline scenarios in full, the rest one shot each) | 38-62 | 63-115 |
| E (three accounts merged into one shot; credentials + secrets merged into one) | 63-81 | 116-137 |
| D (map of roads, one question per shot) | 82-88 | 138-149 |
| F (close) | 89-90 | 150-151 |

Done: shot disposal in main.js (released 4+ shots away, `pruneAmbient()` in lib.js), `window.reconstruction.stats()` for draw calls, roads template (`roadsQ` in part2f.js), accounts shot (part2e.js), journey template (`journey` in part2.js), chapter C selection (`chapterC` in main.js), narration and objectives rewritten, player chapter bar updated.
Measured draw calls: hall shots about 500-750 per frame, D and map shots fewer; budget stays under 800.
Not done: Part 1 trim, merging the hall geometry, splitting into episodes in the player, H3 passes.
Backups of every edited file are in the session scratchpad as `*.before_refactor.*`, `*.before_B.*` and `*.before_C.*`.

## Props from Polyfork (started 2026-11-15)
Source: https://polyfork.dev catalog (free models, no attribution needed under the Polyfork licence; do not redistribute the files). Loaded locally from `assets/polyfork/*.glb` through the vendored r160 `GLTFLoader` (`vendor/addons/`), preloaded in `case/main.js`, cloned by `prop(key)` in `case/assets.js`. If a file is missing the procedural prop is used instead.
Swapped so far: brazier, wall sconce (with its own flame), coin, sofa (tinted crimson), scroll. Our own: pillars, room, arks (servers), altars, dais, emblems, person, chest, laptop tome.
Next candidates: statue, lectern, hourglass, rune stones, rug; Hooded Cultist as an acolyte (T-pose, needs posing); Stone Altar and Overflowing Treasure Chest need their animation hooks rebuilt first.
Preview any model: `case/preview_models.html?ids=<id>,<id>` (served from the product_demo folder).

Polish pass (2026-11-15): `room()` in lib.js now dresses every hall with Polyfork props through `placed(key, height)`: guardian lions at the doorway, brass candelabra beside the carpet, a funerary urn and clay pot and a scroll rack along the left wall, a bronze lamp stand and gilded palm on the right. Models downloaded for the next steps and not yet used: legendary chest, chest dais, divination orb (for the oracle), carved stele, sarcophagus, swinging censer, offering box, plaza fountain, wizard hat, oak barrel, buddhist monk, hearthstone, looted throne, iron chandelier, goblet, amphora, faction banner pole.

Visual language rule (2026-11-15): a server/machine is always a stone altar carrying an open book with a service relic floating above it. `server()` in lib.js builds exactly that (the ark is gone); `setServerPower(s, k, color)` lights the altar runes and the book's pages and dims or brightens the relic. The relic is chosen by build order (`resetServers()` runs before every shot builds). The home laptop has the five services floating above it (`homeRelics` in act2.js) so they can die when the power goes.

"What happens when" refactor (2026-11-15): chapter C is now ONE default scene (`case/shots/what_when.js`): the hall split by the carpet, the usual way on the left (one lone machine, an acolyte who despairs, a red cross) and Adaetum's way on the right (three machines, an acolyte who is relieved, a green check). A scenario is a row in `SCENARIOS` (event `hit`, fix `fix`, title, one line of narration, on-screen description). 15 scenarios plus the lead-in = 16 shots (was 25). To add or reorder scenarios, edit that table; `case/main.js` and `narration.json`/`objectives.json` follow it. Film is now 81 shots; player chapters and episodes updated.

Variable shot length (2026-11-15): a shot is 124 frames (5.17 s) unless its entry in `objectives.json` has `"seconds"` (up to 6, the longest clip Viggle takes). `case/main.js` reads it and builds the frame table; `window.reconstruction.starts()` and `.durations()` expose it; `build_audio.py` places each line by it; `export_h3.py` renders the right number of frames; `player.html` maps time to shots through it. The 15 "what happens when" scenarios are 6 s: the camera rests on the usual way on the left, glides across the carpet, and rests on Adaetum's way on the right, which plays 2.2 s after the left.
