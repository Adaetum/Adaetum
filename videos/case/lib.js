import * as THREE from '../vendor/three.module.js';
import { prop, placed } from './assets.js';
export const FPS = 24, SHOT_FRAMES = 124, D = SHOT_FRAMES / FPS;
// Visual language: the ADYTUM (A-DAY-tum, the inner sanctum of a Greek temple). A Greco-Roman fantasy cult: honey sandstone and travertine, fluted doric columns under a
// triglyph frieze, leather hangings, bronze and gold trim, wall torches. Servers are arks on stone plinths (the thing the faithful worship), services are glowing spell runes.
// People are hooded acolytes in leather and cloth. Occult as normal, warm rather than grim.
export const C = {
  bg: '#120b08', bgDanger: '#240d0c', floor: '#a08866', wall: '#7d6445', metal: '#8f7a5c', dark: '#120b08', leather: '#5b3a22', bronze: '#a8743a', sandstone: '#b89a6a', rune: '#ffd98a',
  brass: '#d9a93a', candle: '#ffb35a', crimson: '#6e1f26', lapis: '#2a4fb0', terracotta: '#b5593a', ivory: '#d9cdb4', marble: '#d8ccb2',
  blue: '#5b8dff', warm: '#ff9a45', green: '#3ed598', red: '#ff5a5f', gold: '#ffc83d',
  skin: '#f0c9a4', cloud: '#5aa7ff', music: '#ff6b9a', video: '#ff5a4f', doc: '#ffd166', ai: '#a78bfa'
};
export const clamp = (x, a = 0, b = 1) => Math.min(b, Math.max(a, x));
export const seg = (t, a, b) => (b === a ? (t >= a ? 1 : 0) : clamp((t - a) / (b - a)));
export const ease = x => x * x * (3 - 2 * x);
export const lerp = (a, b, k) => a + (b - a) * k;
export const pulse = (t, a, b) => Math.sin(Math.PI * seg(t, a, b));
export const V = (x, y, z) => new THREE.Vector3(x, y, z);
export function rng(seed) { let s = seed >>> 0; return () => ((s = (s * 1664525 + 1013904223) >>> 0) / 4294967296); }
export const mat = (c, o = {}) => new THREE.MeshStandardMaterial({ color: c, roughness: .6, metalness: .1, flatShading: true, ...o });
export const glow = (c, i = 1.5) => new THREE.MeshStandardMaterial({ color: c, emissive: c, emissiveIntensity: i, roughness: .35 });
const TEX = new Map();
// Procedural stone: speckle, hairline cracks and tile joints, seeded so it is identical on every seek.
export function stone(color, rx = 1, ry = 1, tiles = 0, checker = false) {
  const key = color + rx + ry + tiles + checker;
  if (TEX.has(key)) return TEX.get(key);
  const c = document.createElement('canvas'); c.width = c.height = 256; const g = c.getContext('2d'), r = rng(rx * 31 + ry * 17 + color.length);
  g.fillStyle = color; g.fillRect(0, 0, 256, 256);
  if (checker && tiles) { const st = 256 / tiles; g.fillStyle = 'rgba(60,36,20,.2)'; for (let i = 0; i < tiles; i++) for (let j = 0; j < tiles; j++) if ((i + j) % 2) g.fillRect(i * st, j * st, st, st); }
  for (let i = 0; i < 1400; i++) { const v = r() > .5 ? 255 : 0; g.fillStyle = `rgba(${v},${v},${v},${.03 + r() * .06})`; g.fillRect(r() * 256, r() * 256, 1 + r() * 3, 1 + r() * 3); }
  g.strokeStyle = 'rgba(0,0,0,.28)'; g.lineWidth = 1;
  for (let i = 0; i < 5; i++) { g.beginPath(); let x = r() * 256, y = r() * 256; g.moveTo(x, y); for (let k = 0; k < 6; k++) { x += (r() - .5) * 50; y += (r() - .5) * 50; g.lineTo(x, y); } g.stroke(); }
  if (tiles) { g.strokeStyle = 'rgba(0,0,0,.45)'; g.lineWidth = 3; const st = 256 / tiles; for (let i = 0; i <= tiles; i++) { g.beginPath(); g.moveTo(i * st, 0); g.lineTo(i * st, 256); g.stroke(); g.beginPath(); g.moveTo(0, i * st); g.lineTo(256, i * st); g.stroke(); } }
  const t = new THREE.CanvasTexture(c); t.wrapS = t.wrapT = THREE.RepeatWrapping; t.repeat.set(rx, ry); t.colorSpace = THREE.SRGBColorSpace;
  const m = new THREE.MeshStandardMaterial({ map: t, bumpMap: t, bumpScale: 1.2, roughness: .88, metalness: .05, flatShading: true });
  TEX.set(key, m); return m;
}

// Greek key (meander) band as a repeating texture, used on carpets, plinths, banners and the frieze.
let KEY = new Map();
export function keyMat(fg = '#d9a93a', bg = '#5b3a22', repeat = 6, o = {}) {
  const k = fg + bg + repeat; if (KEY.has(k)) return KEY.get(k);
  const c = document.createElement('canvas'); c.width = 256; c.height = 64; const g = c.getContext('2d');
  g.fillStyle = bg; g.fillRect(0, 0, 256, 64); g.strokeStyle = fg; g.lineWidth = 5; g.lineCap = 'square'; g.lineJoin = 'miter';
  g.strokeRect(2, 2, 252, 60);
  for (let i = 0; i < 4; i++) { const x = 12 + i * 60; g.beginPath(); g.moveTo(x, 54); g.lineTo(x, 12); g.lineTo(x + 40, 12); g.lineTo(x + 40, 42); g.lineTo(x + 18, 42); g.lineTo(x + 18, 28); g.stroke(); }
  const t = new THREE.CanvasTexture(c); t.wrapS = t.wrapT = THREE.RepeatWrapping; t.repeat.set(repeat / 4, 1); t.colorSpace = THREE.SRGBColorSpace;
  const m = new THREE.MeshStandardMaterial({ map: t, roughness: .7, metalness: .2, ...o }); KEY.set(k, m); return m;
}
// Spell runes: random-looking but seeded glyphs built from a 3x3 node grid, drawn white so they can be tinted and made to glow.
const RUNES = new Map();
export function runeTexture(seed, ring = false) {
  const k = seed + (ring ? 'r' : ''); if (RUNES.has(k)) return RUNES.get(k);
  const c = document.createElement('canvas'); c.width = c.height = 128; const g = c.getContext('2d'), r = rng(seed * 977 + 13);
  g.strokeStyle = '#fff'; g.fillStyle = '#fff'; g.lineCap = 'round'; g.lineJoin = 'round';
  if (ring) {
    g.lineWidth = 4; g.beginPath(); g.arc(64, 64, 60, 0, 6.3); g.stroke(); g.lineWidth = 2; g.beginPath(); g.arc(64, 64, 46, 0, 6.3); g.stroke();
    for (let i = 0; i < 24; i++) { const a = i / 24 * Math.PI * 2; g.lineWidth = i % 2 ? 2 : 3; g.beginPath(); g.moveTo(64 + Math.cos(a) * 47, 64 + Math.sin(a) * 47); g.lineTo(64 + Math.cos(a) * (i % 3 ? 55 : 59), 64 + Math.sin(a) * (i % 3 ? 55 : 59)); g.stroke(); }
    g.lineWidth = 3; g.beginPath(); for (let i = 0; i < 5; i++) { const a = i * 4 * Math.PI / 5 - Math.PI / 2; g.lineTo(64 + Math.cos(a) * 34, 64 + Math.sin(a) * 34); } g.closePath(); g.stroke();
  } else {
    g.lineWidth = 9; const N = [0, 1, 2].flatMap(i => [0, 1, 2].map(j => [26 + i * 38, 22 + j * 42]));
    g.beginPath(); g.moveTo(64, 10); g.lineTo(64, 118); g.stroke();
    for (let e = 0; e < 3 + (seed % 2); e++) { const a = N[Math.floor(r() * 9)], b = N[Math.floor(r() * 9)]; g.beginPath(); g.moveTo(a[0], a[1]); g.lineTo(b[0], b[1]); g.stroke(); }
    const d = N[Math.floor(r() * 9)]; g.beginPath(); g.arc(d[0], d[1], 7, 0, 6.3); g.fill();
  }
  const t = new THREE.CanvasTexture(c); t.colorSpace = THREE.SRGBColorSpace; RUNES.set(k, t); return t;
}
export function runeMat(color, k = 1.6, seed = 1, ring = false) {
  const t = runeTexture(seed, ring);
  return new THREE.MeshStandardMaterial({ map: t, emissiveMap: t, color, emissive: color, emissiveIntensity: k, alphaTest: .35, side: THREE.DoubleSide, roughness: .5 });
}
export const runePlane = (w, h, color, k, seed, ring) => new THREE.Mesh(new THREE.PlaneGeometry(w, h), runeMat(color, k, seed, ring));
export const brass = () => mat(C.brass, { metalness: .7, roughness: .35 });
export function add(parent, mesh, x = 0, y = 0, z = 0) { mesh.position.set(x, y, z); parent.add(mesh); return mesh; }
export const box = (w, h, d, m) => new THREE.Mesh(new THREE.BoxGeometry(w, h, d), m);
export const cyl = (r, h, m, s = 24) => new THREE.Mesh(new THREE.CylinderGeometry(r, r, h, s), m);
export const sph = (r, m) => new THREE.Mesh(new THREE.SphereGeometry(r, 20, 14), m);
export function arc(a, b, k, lift) { const p = a.clone().lerp(b, ease(clamp(k))); p.y += Math.sin(Math.PI * clamp(k)) * lift; return p; }

// Candle and brazier flames flicker deterministically from the shot time.
const AMBIENT = [], SMOKE = [], GYRO = [];
let SOFT = null;
function softTex() { if (SOFT) return SOFT; const c = document.createElement('canvas'); c.width = c.height = 64; const g = c.getContext('2d'); const gr = g.createRadialGradient(32, 32, 0, 32, 32, 32); gr.addColorStop(0, 'rgba(255,255,255,.9)'); gr.addColorStop(1, 'rgba(255,255,255,0)'); g.fillStyle = gr; g.fillRect(0, 0, 64, 64); SOFT = new THREE.CanvasTexture(c); return SOFT; }
// Incense and brazier smoke: soft puffs that rise, widen and fade, driven by shot time.
export function smoke(parent, x, y, z, n = 6, o = {}) {
  for (let i = 0; i < n; i++) { const sp = new THREE.Sprite(new THREE.SpriteMaterial({ map: softTex(), color: o.color || '#cdb9a0', transparent: true, opacity: 0, depthWrite: false, fog: false })); sp.position.set(x, y, z); parent.add(sp); SMOKE.push({ sp, x, y, z, ph: i / n, k: i, start: o.start || 0, size: o.size || 1, op: o.opacity || .26, rise: o.rise || 2.6, parent }); }
}
// Drop animation entries whose objects were removed from the scene (a released shot), so the lists do not grow across a long film.
const attached = o => { while (o.parent) o = o.parent; return !!o.isScene; };
export function pruneAmbient() {
  for (const [arr, get] of [[AMBIENT, m => m], [SMOKE, o => o.sp], [GYRO, o => o.rings[0]], [PERSONS, g => g], [SERVERS, g => g]]) { let j = 0; for (const e of arr) if (attached(get(e))) arr[j++] = e; arr.length = j; }
}
export function updateAmbient(t) {
  updatePersons(t);
  SERVERS.forEach(g => { const r = g.userData.relic; if (r) { animateIcon(r, t); r.position.y = g.userData.relicY + .08 * Math.sin(t * 1.3 + g.userData.ph); r.rotation.y = Math.sin(t * .8 + g.userData.ph) * .3; } });
  GYRO.forEach(o => o.rings.forEach((r, i) => { r.rotation.x = t * (.9 + .4 * i) + i; r.rotation.y = t * (.6 - .3 * i) + i * 2; }));
  SMOKE.forEach(o => { const f = (t * .28 + o.ph) % 1; o.sp.position.set(o.x + Math.sin(t * .8 + o.k) * .25 * f, o.y + f * o.rise, o.z + Math.cos(t * .6 + o.k) * .2 * f); o.sp.scale.setScalar((.35 + f * 1.3) * o.size); o.sp.material.opacity = o.op * Math.sin(Math.PI * f) * (o.start ? seg(t, o.start, o.start + .8) : 1); });
  AMBIENT.forEach((m, i) => { m.scale.y = 1 + .22 * Math.sin(t * 9 + i * 2.1) + .1 * Math.sin(t * 17 + i); m.scale.x = m.scale.z = 1 - .08 * Math.sin(t * 11 + i); }); }
export function flame(parent, x, y, z, s = 1) {
  const f = add(parent, new THREE.Mesh(new THREE.ConeGeometry(.12 * s, .4 * s, 8), glow(C.candle, 2.2)), x, y, z); AMBIENT.push(f); return f;
}
export function brazier() {
  const m = prop('brazier'); if (!m) return brazierProc();
  const g = new THREE.Group(), s = 1.2; m.scale.setScalar(s); g.add(m);
  add(g, sph(.3, glow('#ff6a2a', 1.4)), 0, 1.11 * s - .05, 0).scale.set(1, .35, 1);
  flame(g, 0, 1.11 * s + .22, 0, 1.7); flame(g, .16, 1.11 * s + .1, .05, 1.0); flame(g, -.14, 1.11 * s + .1, -.08, 1.15); smoke(g, 0, 1.11 * s + .6, 0); return g;
}
function brazierProc() {
  const g = new THREE.Group(), bz = () => mat(C.bronze, { metalness: .7, roughness: .4 });
  for (let i = 0; i < 3; i++) { const a = i / 3 * Math.PI * 2; const leg = add(g, cyl(.045, 1.1, bz(), 8), Math.cos(a) * .24, .5, Math.sin(a) * .24); leg.rotation.z = -Math.cos(a) * .22; leg.rotation.x = Math.sin(a) * .22; add(g, sph(.07, brass()), Math.cos(a) * .34, .02, Math.sin(a) * .34); }
  add(g, new THREE.Mesh(new THREE.CylinderGeometry(.5, .28, .3, 20, 1, true), mat(C.bronze, { metalness: .7, roughness: .4, side: THREE.DoubleSide })), 0, 1.12, 0);
  add(g, cyl(.5, .05, brass(), 20), 0, 1.28, 0); add(g, cyl(.28, .05, brass(), 20), 0, .96, 0);
  add(g, sph(.36, glow('#ff6a2a', 1.4)), 0, 1.2, 0).scale.set(1, .35, 1);
  flame(g, 0, 1.5, 0, 1.7); flame(g, .16, 1.38, .05, 1.0); flame(g, -.14, 1.38, -.08, 1.15); smoke(g, 0, 1.95, 0); return g;
}
export function pillar(h = 5) {
  const g = new THREE.Group(), sand = () => stone(C.marble, 1, 3), gold = () => brass();
  add(g, box(1.25, .16, 1.25, stone(C.sandstone, 1, 1)), 0, .08, 0); add(g, box(1.05, .12, 1.05, stone(C.metal, 1, 1)), 0, .22, 0);
  add(g, cyl(.46, h - .95, sand(), 24).clone(), 0, h / 2 - .05, 0);
  // flutes: dark vertical grooves all around the shaft
  for (let i = 0; i < 20; i++) { const a = i / 20 * Math.PI * 2; add(g, box(.035, h - 1.2, .035, mat('#6a5640')), Math.cos(a) * .462, h / 2 - .05, Math.sin(a) * .462); }
  const band = add(g, new THREE.Mesh(new THREE.TorusGeometry(.47, .04, 6, 28), gold()), 0, h * .24, 0); band.rotation.x = Math.PI / 2;
  const band2 = add(g, new THREE.Mesh(new THREE.TorusGeometry(.47, .04, 6, 28), gold()), 0, h * .86, 0); band2.rotation.x = Math.PI / 2;
  add(g, new THREE.Mesh(new THREE.CylinderGeometry(.66, .46, .3, 24), stone(C.marble, 1, 1)), 0, h - .62, 0);
  add(g, box(1.3, .2, 1.3, stone(C.sandstone, 1, 1)), 0, h - .38, 0);
  const sig = add(g, new THREE.Mesh(new THREE.TorusGeometry(.47, .02, 6, 28), glow(C.brass, .9)), 0, h * .55, 0); sig.rotation.x = Math.PI / 2; return g;
}
export function banner(w = 1.7, h = 3.4) {
  const g = new THREE.Group();
  const c = document.createElement('canvas'); c.width = 128; c.height = 256; const x = c.getContext('2d');
  x.fillStyle = '#6a1c24'; x.fillRect(0, 0, 128, 256);
  x.strokeStyle = '#d9a93a'; x.lineWidth = 4; x.strokeRect(6, 6, 116, 244); x.lineWidth = 2; x.strokeRect(13, 13, 102, 230);
  x.lineWidth = 3; for (let i = 0; i < 7; i++) { const yy = 20 + i * 4; if (i === 0) for (let k = 0; k < 6; k++) { const xx = 18 + k * 16; x.strokeRect(xx, 20, 10, 8); } }
  for (let k = 0; k < 6; k++) { const xx = 18 + k * 16; x.strokeRect(xx, 228, 10, 8); }
  x.beginPath(); x.arc(64, 118, 52, 0, 6.3); x.stroke();
  const gr = x.createLinearGradient(0, 60, 0, 180); gr.addColorStop(0, '#fff0a8'); gr.addColorStop(1, '#d9a63a');
  x.strokeStyle = gr; x.lineWidth = 13; x.lineJoin = 'miter'; x.lineCap = 'butt';
  x.beginPath(); x.moveTo(30, 170); x.lineTo(64, 62); x.lineTo(98, 170); x.stroke();
  x.lineWidth = 10; x.beginPath(); x.moveTo(43, 140); x.lineTo(85, 140); x.stroke();
  x.fillStyle = '#ffe08a'; x.beginPath(); x.arc(64, 205, 6, 0, 6.3); x.fill();
  for (let i = 0; i < 3; i++) { x.fillRect(48 + i * 16, 222, 8, 8); }
  const t = new THREE.CanvasTexture(c); t.colorSpace = THREE.SRGBColorSpace;
  add(g, new THREE.Mesh(new THREE.PlaneGeometry(w, h), new THREE.MeshStandardMaterial({ map: t, side: THREE.DoubleSide, roughness: .9, emissive: '#ffffff', emissiveMap: t, emissiveIntensity: .12 })), 0, 0, 0);
  add(g, cyl(.05, w + .3, brass(), 8), 0, h / 2 + .05, 0).rotation.z = Math.PI / 2;
  return g;
}
export function sigilRings(parent, y = .01, r = 3.2) {
  [r, r * 1.7].forEach((rr, i) => { const t = add(parent, new THREE.Mesh(new THREE.TorusGeometry(rr, .03, 6, 80), glow(C.brass, .2)), 0, y, 0); t.rotation.x = Math.PI / 2; });
}
export function templeFloor(g, w = 44, d = 30) {
  add(g, box(w, .2, d, stone(C.floor, w / 2.2, d / 2.2, 2, true)), 0, -.1, 0); sigilRings(g, .01, 3.4);
}

// ---- Expression ----------------------------------------------------------------------------------------
// A mood is a set of parameters; a shot sets one every frame with feel() or feelAt() (so it stays a pure function of time) and updatePersons() applies it.
// eye: lid openness; brow: +worried / -angry; browUp: raised; smile: +smile / -frown; o: round open mouth; nod: head forward; tilt: head sideways;
// arms: [left pitch, left inward, right pitch, right inward]. Pitch lifts the arm forward and up; inward brings the hand toward the middle.
const PERSONS = [];
const NEUTRAL = { eye: 1, brow: 0, browUp: 0, smile: .15, o: 0, nod: 0, tilt: 0, arms: [1.0, .41, 1.0, .41] };
export const MOODS = {
  neutral: NEUTRAL,
  relaxed: { ...NEUTRAL, eye: .85, smile: .55, arms: [.2, -.1, .2, -.1] },
  worried: { ...NEUTRAL, eye: 1.15, brow: 1, browUp: .2, smile: -.5, nod: .1, arms: [1.25, .6, 1.25, .6] },
  sad: { ...NEUTRAL, eye: .6, brow: .9, smile: -.8, nod: .38, arms: [.45, .25, .45, .25] },
  alarm: { ...NEUTRAL, eye: 1.5, brow: .6, browUp: .8, smile: 0, o: 1, nod: -.15, arms: [2.3, .6, 2.3, .6] },
  cower: { ...NEUTRAL, eye: 1.3, brow: 1, browUp: .5, smile: -.6, o: .6, nod: .3, arms: [2.1, 1.0, 2.1, 1.0] },
  joy: { ...NEUTRAL, eye: .85, brow: -.1, browUp: .3, smile: 1, nod: -.12, arms: [2.5, -.3, 2.5, -.3] },
  relief: { ...NEUTRAL, eye: .75, brow: .4, smile: .7, nod: -.05, arms: [.5, .1, .5, .1] },
  proud: { ...NEUTRAL, eye: .9, brow: -.3, smile: .8, nod: -.2, arms: [.35, -.6, .35, -.6] },
  thinking: { ...NEUTRAL, eye: .9, brow: .2, smile: 0, tilt: .25, arms: [1.1, .4, 2.0, .7] },
  determined: { ...NEUTRAL, eye: .8, brow: -.8, smile: .1, nod: .05, arms: [.7, .1, .7, .1] },
  praise: { ...NEUTRAL, eye: 1, smile: .6, nod: -.2, arms: [1.4, -.7, 1.4, -.7] },
  point: { ...NEUTRAL, smile: .3, arms: [1.0, .41, 1.6, -.1] },
  surprised: { ...NEUTRAL, eye: 1.45, browUp: 1, smile: 0, o: .8, nod: -.2, arms: [1.6, .2, 1.6, .2] },
};
const mixMood = (a, b, k) => { const o = {}; for (const key in NEUTRAL) { if (key === 'arms') o.arms = a.arms.map((v, i) => lerp(v, b.arms[i], k)); else o[key] = lerp(a[key], b[key], k); } return o; };
// feel(p, 'alarm', .7): that much of a mood, from neutral
export function feel(p, name, k = 1) { p.userData.params = mixMood(NEUTRAL, MOODS[name], clamp(k)); }
// feelAt(p, t, [[0, 'worried'], [1.2, 'alarm'], [3, 'cower']]): move through moods over time, easing between keyframes
export function feelAt(p, t, keys) {
  if (t <= keys[0][0]) return feel(p, keys[0][1]);
  for (let i = 0; i < keys.length - 1; i++) { const [t0, a] = keys[i], [t1, b] = keys[i + 1]; if (t < t1) { p.userData.params = mixMood(MOODS[a], MOODS[b], ease(seg(t, t0, t1))); return; } }
  feel(p, keys[keys.length - 1][1]);
}
export function updatePersons(t) {
  PERSONS.forEach(g => {
    const F = g.userData.face; if (!F) return; const m = g.userData.params || NEUTRAL, ph = g.userData.ph;
    const blink = (() => { const c = (t * .31 + ph * .37) % 1; return c < .045 ? Math.abs(c / .045 - .5) * 2 : 1; })();
    F.eyes.forEach(e => e.scale.set(1.1, Math.max(.06, m.eye * blink), .5));
    F.brows.forEach((b, i) => { const sx = i ? 1 : -1; b.position.y = .095 + .05 * m.browUp - .02 * Math.max(0, -m.brow); b.rotation.z = -sx * m.brow * .45; });
    const sm = m.smile, showO = m.o > .25;
    F.mouth.visible = !showO; F.mouth.rotation.z = sm >= 0 ? Math.PI : 0; F.mouth.scale.set(.6 + .7 * Math.abs(sm), .4 + .9 * Math.abs(sm), 1); F.mouth.position.y = -.1 + (sm >= 0 ? .035 * sm : -.03 * Math.abs(sm));
    F.mouthO.visible = showO; F.mouthO.scale.setScalar(.6 + .8 * m.o);
    F.hRig.rotation.set(m.nod + .03 * Math.sin(t * 1.3 + ph), .16 * Math.sin(t * .45 + ph) * (1 - .6 * Math.abs(m.nod)), m.tilt + .02 * Math.sin(t * .9 + ph));
    F.arms.forEach((a, i) => { const sx = a.userData.sx, side = i ? 2 : 0, breathe = .03 * Math.sin(t * 1.6 + ph + i), wring = m.brow > .5 ? .05 * Math.sin(t * 9 + i * 2) : 0;
      a.rotation.set(-(m.arms[side] + breathe), 0, -sx * (m.arms[side + 1] + wring)); });
  });
}
export function person(shirt, hair = '#2b2118') {
  const g = new THREE.Group(), cloth = () => mat(shirt, { side: THREE.DoubleSide, roughness: .85 });
  // The body is two parts joined at the waist: a lower robe that stays put, and an upper body (chest, hood, arms) that can bow forward about the waist.
  const PIV = 1.0, up = new THREE.Group(); up.position.y = PIV; g.add(up); const addU = (m, x, y, z) => add(up, m, x, y - PIV, z);
  const prof = []; for (let i = 0; i <= 12; i++) { const y = i / 12 * 1.7, k = 1 - i / 12; prof.push([.2 + .42 * k * k + .05 * k, y]); }
  const lowP = prof.filter(p => p[1] <= PIV + .01).map(p => new THREE.Vector2(p[0], p[1])), upP = prof.filter(p => p[1] >= PIV - .15).map(p => new THREE.Vector2(p[0], p[1] - PIV));
  add(g, new THREE.Mesh(new THREE.LatheGeometry(lowP, 18), cloth()), 0, 0, 0).geometry.computeVertexNormals();
  add(up, new THREE.Mesh(new THREE.LatheGeometry(upP, 18), cloth()), 0, 0, 0).geometry.computeVertexNormals();
  const hem = add(g, new THREE.Mesh(new THREE.TorusGeometry(.64, .03, 6, 24), brass()), 0, .03, 0); hem.rotation.x = Math.PI / 2;
  const belt = add(g, new THREE.Mesh(new THREE.TorusGeometry(.35, .045, 6, 20), mat(C.leather, { roughness: .8 })), 0, PIV - .04, 0); belt.rotation.x = Math.PI / 2;
  add(g, box(.1, .1, .04, brass()), 0, PIV - .04, .36);
  addU(new THREE.Mesh(new THREE.TorusGeometry(.33, .03, 6, 18), glow(C.brass, .5)), 0, 1.05, 0).rotation.x = Math.PI / 2;
  addU(new THREE.Mesh(new THREE.CylinderGeometry(.3, .46, .42, 14, 1, true), cloth()), 0, 1.5, -.03);
  addU(new THREE.Mesh(new THREE.TorusGeometry(.46, .028, 6, 20), brass()), 0, 1.3, -.03).rotation.x = Math.PI / 2;
  addU(new THREE.Mesh(new THREE.TorusGeometry(.1, .02, 6, 16), glow(C.gold, 1.4)), 0, 1.15, .36);
  addU(sph(.03, glow(C.gold, 1.6)), 0, 1.15, .36);
  // leather shoulder mantle with gold studs
  addU(new THREE.Mesh(new THREE.CylinderGeometry(.33, .5, .22, 14, 1, true), mat(C.leather, { side: THREE.DoubleSide, roughness: .8 })), 0, 1.5, -.03);
  for (let i = 0; i < 8; i++) { const a = i / 8 * Math.PI * 2; addU(sph(.02, brass()), Math.cos(a) * .5, 1.41, Math.sin(a) * .5 - .03); }
  // hood: a sphere shell open at the front, with a deep shadowed face and two small candle-coloured eyes
  const head = new THREE.Group(); head.position.set(0, 1.82 - PIV, 0); up.add(head);
  add(head, new THREE.Mesh(new THREE.SphereGeometry(.37, 20, 14, Math.PI / 2 + .75, Math.PI * 2 - 1.5), cloth()), 0, .02, -.02).scale.set(1, 1.1, 1.08);
  const drape = add(head, new THREE.Mesh(new THREE.ConeGeometry(.33, .8, 12, 1, true), cloth()), 0, -.28, -.2); drape.rotation.x = -.3;
  add(head, sph(.27, mat('#0d0813')), 0, -.02, .02);
  // the face: two glowing eyes with lids, two brows and a mouth, so the acolyte can show a mood
  const eyes = [-1, 1].map(sx => { const e = add(head, sph(.045, glow(C.candle, 2.4)), sx * .09, .02, .285); e.scale.set(1.1, 1, .5); return e; });
  const brows = [-1, 1].map(sx => add(head, box(.11, .022, .02, glow(C.candle, 1.6)), sx * .09, .095, .295));
  const mouth = add(head, new THREE.Mesh(new THREE.TorusGeometry(.065, .014, 6, 14, Math.PI), glow(C.candle, 1.4)), 0, -.1, .29);
  const mouthO = add(head, new THREE.Mesh(new THREE.TorusGeometry(.035, .014, 6, 12), glow(C.candle, 1.4)), 0, -.11, .29); mouthO.visible = false;
  // arms hang from the shoulders as bell sleeves ending in a hand, so they can fold, rise, open and point
  const arms = [-1, 1].map(sx => {
    const a = add(up, new THREE.Group(), sx * .32, 1.38 - PIV, .02);
    add(a, new THREE.Mesh(new THREE.CylinderGeometry(.08, .2, .75, 10, 1, true), cloth()), 0, -.375, 0);
    add(a, new THREE.Mesh(new THREE.TorusGeometry(.19, .02, 6, 14), brass()), 0, -.74, 0).rotation.x = Math.PI / 2;
    add(a, sph(.1, mat(C.skin)), 0, -.82, 0); a.userData.sx = sx; return a;
  });
  // everything on the head moves as one rig, so mood can tilt and nod it without fighting a shot's own head animation
  const hRig = new THREE.Group(); head.children.slice().forEach(c => hRig.add(c)); head.add(hRig);
  g.userData.face = { eyes, brows, mouth, mouthO, hRig, arms }; g.userData.ph = PERSONS.length * 1.7; PERSONS.push(g);
  g.userData.head = head; g.userData.upper = up; return g;
}
function withHalo(make) {
  return () => { const g = make(); const h = add(g, new THREE.Group(), 0, 0, -.25); add(h, new THREE.Mesh(new THREE.TorusGeometry(1.15, .03, 6, 40), glow(C.brass, .8)));
    add(h, runePlane(2.3, 2.3, C.rune, .9, 3, true), 0, 0, -.02); const h2 = add(g, runePlane(1.8, 1.8, C.rune, .6, 5, true), 0, 0, -.32); g.userData.halo = h; g.userData.halo2 = h2; return g; };
}
function cloudIconRaw() {
  const g = new THREE.Group(), m = mat(C.cloud, { emissive: C.cloud, emissiveIntensity: .25 });
  add(g, sph(.5, m), -.5, 0, 0); add(g, sph(.65, m), .05, .25, 0); add(g, sph(.45, m), .6, 0, 0);
  add(g, box(1.6, .45, .8, m), .05, -.2, 0).scale.set(1, 1, .6); g.scale.setScalar(.7); return g;
}
function musicIconRaw() {
  const g = new THREE.Group();
  const disc = add(g, cyl(.8, .08, mat('#15131a', { metalness: .4 })), 0, 0, 0); disc.rotation.x = Math.PI / 2;
  const lab = add(g, cyl(.3, .1, glow(C.music, .8)), 0, 0, 0); lab.rotation.x = Math.PI / 2;
  g.userData.spin = disc; g.userData.spin2 = lab; return g;
}
function videoIconRaw() {
  const g = new THREE.Group();
  add(g, box(1.8, 1.1, .16, mat('#1a1f2b')), 0, 0, 0);
  add(g, box(1.65, .95, .02, glow(C.video, .5)), 0, 0, .09);
  const tri = new THREE.Shape(); tri.moveTo(-.2, -.28); tri.lineTo(.3, 0); tri.lineTo(-.2, .28);
  add(g, new THREE.Mesh(new THREE.ShapeGeometry(tri), new THREE.MeshBasicMaterial({ color: '#fff' })), 0, 0, .11);
  return g;
}
function docIconRaw() {
  const g = new THREE.Group();
  for (let i = 0; i < 3; i++) { const p = add(g, box(1.0, 1.3, .06, mat(i === 2 ? C.doc : '#e8dcc0')), i * .08, i * .05, i * .08); p.rotation.z = -.08 * i; }
  for (let i = 0; i < 4; i++) add(g, box(.6, .05, .02, mat('#7a6a3a')), .16, .35 - i * .22, .27);
  return g;
}
function aiIconRaw() {
  const g = new THREE.Group();
  const core = add(g, sph(.35, glow(C.ai, 1.4)), 0, 0, 0);
  const shell = add(g, new THREE.Mesh(new THREE.IcosahedronGeometry(.75, 1), new THREE.MeshBasicMaterial({ color: C.ai, wireframe: true })), 0, 0, 0);
  g.userData.shell = shell; g.userData.core = core; return g;
}
export const cloudIcon = withHalo(cloudIconRaw), musicIcon = withHalo(musicIconRaw), videoIcon = withHalo(videoIconRaw), docIcon = withHalo(docIconRaw), aiIcon = withHalo(aiIconRaw);
export const ICONS = [cloudIcon, musicIcon, videoIcon, docIcon, aiIcon];
export const ICON_COL = [C.cloud, C.music, C.video, C.doc, C.ai];
export function animateIcon(g, t) {
  if (g.userData.spin) { g.userData.spin.rotation.y = t * 2; }
  if (g.userData.shell) { g.userData.shell.rotation.y = t * .8; g.userData.shell.rotation.x = t * .5; g.userData.core.scale.setScalar(1 + .12 * Math.sin(t * 4)); }
  if (g.userData.halo) g.userData.halo.rotation.z = t * .6; if (g.userData.halo2) g.userData.halo2.rotation.z = -t * .4;
}
let COIN_FACE = null;
function coinFace() {
  if (COIN_FACE) return COIN_FACE;
  const c = document.createElement('canvas'); c.width = c.height = 128; const g = c.getContext('2d');
  const gr = g.createRadialGradient(50, 44, 4, 64, 64, 64); gr.addColorStop(0, '#fff0a8'); gr.addColorStop(.5, '#e8b93a'); gr.addColorStop(1, '#a8741c');
  g.fillStyle = gr; g.fillRect(0, 0, 128, 128);
  g.strokeStyle = '#7a4f10'; g.lineWidth = 6; g.beginPath(); g.arc(64, 64, 52, 0, 6.3); g.stroke();
  g.fillStyle = '#8a5a14'; g.font = 'bold 70px serif'; g.textAlign = 'center'; g.textBaseline = 'middle'; g.fillText('A', 64, 68);
  const t = new THREE.CanvasTexture(c); t.colorSpace = THREE.SRGBColorSpace;
  COIN_FACE = [new THREE.MeshStandardMaterial({ color: C.gold, metalness: .85, roughness: .3, emissive: C.gold, emissiveIntensity: .18 }), new THREE.MeshStandardMaterial({ map: t, metalness: .8, roughness: .35, emissive: '#ffcc55', emissiveIntensity: .12 })];
  return COIN_FACE;
}
// A tribute coin: gold disc with a raised rim and an A on both faces.
export function coin(r = .2) {
  const m = prop('coin'); if (m) { const k = 2 * r / .06; m.scale.setScalar(k); m.position.y = -.03 * k; const w = new THREE.Group(); w.add(m); return w; }
  return coinProc(r);
}
function coinProc(r) {
  const [side, face] = coinFace(), g = new THREE.Group();
  g.add(new THREE.Mesh(new THREE.CylinderGeometry(r, r, .055, 28), [side, face, face]));
  const rim = new THREE.Mesh(new THREE.TorusGeometry(r, .02, 6, 28), side); rim.rotation.x = Math.PI / 2; g.add(rim);
  g.rotation.x = Math.PI / 2; const w = new THREE.Group(); w.add(g); return w;
}
// The offering chest: banded wood, open lid, and a mound of coins whose height shows how much you still own.
export function chest() {
  const g = new THREE.Group(), wood = mat('#5a3322', { roughness: .8 });
  add(g, box(1.8, .7, 1.15, wood), 0, .35, 0);
  for (const x of [-.7, .7]) add(g, box(.12, .74, 1.2, brass()), x, .36, 0);
  add(g, box(1.84, .1, 1.2, brass()), 0, .72, 0);
  const lid = add(g, box(1.8, .1, 1.15, wood), 0, 1.15, -.78); lid.rotation.x = -1.1;
  add(g, box(.5, .2, .06, brass()), 0, .55, .6);
  const coins = [], r = rng(5);
  const layers = 9; let n = 0;
  for (let L = 0; L < layers; L++) { const cnt = Math.max(1, 9 - L), rad = (layers - L) * .08; for (let k = 0; k < cnt; k++) { const a = k / cnt * Math.PI * 2 + L, c = coin(.19); c.position.set(Math.cos(a) * rad * (L === layers - 1 ? 0 : 1), .78 + L * .075, Math.sin(a) * rad * .8); c.rotation.y = r() * 3; g.add(c); coins.push(c); } }
  g.userData.coins = coins;
  g.userData.setLevel = level => { const n = Math.floor(coins.length * clamp(level)); coins.forEach((c, i) => { c.visible = i < n; }); };
  g.userData.setLevel(1); return g;
}
export function altar() {
  const g = new THREE.Group();
  add(g, cyl(1.15, .18, stone(C.metal, 1, 1), 8), 0, .09, 0); add(g, cyl(1.0, .18, stone(C.sandstone, 1, 1), 8), 0, .27, 0);
  add(g, cyl(.66, .85, stone(C.marble, 1, 2), 8), 0, .8, 0);
  add(g, cyl(.7, .07, brass(), 8), 0, .42, 0); add(g, cyl(.7, .07, brass(), 8), 0, 1.18, 0);
  for (let i = 0; i < 8; i++) { const a = i / 8 * Math.PI * 2 + Math.PI / 8; add(g, runePlane(.2, .2, C.rune, .8, i + 1), Math.cos(a) * .665, .8, Math.sin(a) * .665).rotation.y = -a + Math.PI / 2; }
  add(g, cyl(.98, .14, stone(C.sandstone, 1, 1), 8), 0, 1.33, 0); add(g, new THREE.Mesh(new THREE.TorusGeometry(.8, .025, 6, 32), glow(C.brass, .7)), 0, 1.41, 0).rotation.x = Math.PI / 2;
  for (let i = 0; i < 4; i++) { const a = i / 4 * Math.PI * 2 + Math.PI / 4; const cx = Math.cos(a) * .82, cz = Math.sin(a) * .82; add(g, cyl(.05, .3, mat('#f0e4cc'), 8), cx, 1.55, cz); flame(g, cx, 1.8, cz, .55); }
  return g;
}
// Lightning: three jagged variants that flicker, plus a bright base flash. Call setLightning each frame.
export function lightningBolt() {
  const g = new THREE.Group(), r = rng(77), mt = glow('#fff2b8', 3);
  g.userData.vars = [0, 1, 2].map(() => {
    const pts = []; let x = 0, z = 0; for (let i = 0; i <= 9; i++) { const y = 9 - i * 1.0; pts.push(V(x, y, z)); x = i === 8 ? 0 : (r() - .5) * 1.6; z = i === 8 ? 0 : (r() - .5) * 1.0; }
    const m = new THREE.Mesh(new THREE.TubeGeometry(new THREE.CatmullRomCurve3(pts, false, 'catmullrom', .1), 40, .09, 6), mt); m.visible = false; g.add(m); return m;
  });
  const flash = add(g, new THREE.Mesh(new THREE.SphereGeometry(.28, 12, 8), glow('#fff2b8', 2.2)), 0, .5, 0); flash.visible = false; g.userData.flash = flash;
  return g;
}
export function setLightning(g, t, t0, dur = .35) {
  const on = t >= t0 && t < t0 + dur, v = Math.floor((t - t0) * 30) % 3;
  g.userData.vars.forEach((m, i) => { m.visible = on && i === v; });
  g.userData.flash.visible = on; g.userData.flash.scale.setScalar(.6 + 1.2 * clamp(1 - (t - t0) / dur));
}
// Fire that takes hold of a struck shrine or chapel. Grow it with the group scale; the flames flicker by themselves.
export function fireOn(parent, x, y, z, size = 1) {
  const g = new THREE.Group(); g.position.set(x, y, z); parent.add(g);
  [['#ff4a1c', 0, 0, .9], ['#ff8a2a', .35, .1, .7], ['#ffd24a', -.3, -.15, .55], ['#ff6a2a', .1, .3, .75], ['#ffb03a', -.15, .25, .5]].forEach(([col, dx, dz, hh]) => {
    const f = add(g, new THREE.Mesh(new THREE.ConeGeometry(.28 * size, hh * 1.3 * size, 8), glow(col, 2.4)), dx * size, hh * .6 * size, dz * size); AMBIENT.push(f);
  });
  return g;
}
export function sconce() {
  const m = prop('sconce'); if (!m) return sconceProc();
  const g = new THREE.Group(), s = 1.7; m.scale.setScalar(s); m.position.y = -1.96 * s; g.add(m);
  g.userData.flame = add(g, new THREE.Group(), 0, 2.2 * s - 1.96 * s, .3);   // the model carries its own flame; this stands in for shots that animate the old one
  return g;
}
function sconceProc() {
  const g = new THREE.Group(), iron = mat('#2a211a', { metalness: .6, roughness: .5 });
  add(g, box(.12, .5, .06, iron), 0, 0, 0); const arm = add(g, cyl(.025, .3, iron, 6), 0, .12, .15); arm.rotation.x = Math.PI / 2;
  add(g, new THREE.Mesh(new THREE.CylinderGeometry(.1, .06, .22, 10, 1, true), mat(C.bronze, { metalness: .7, side: THREE.DoubleSide })), 0, .3, .3);
  add(g, cyl(.06, .3, mat(C.leather), 8), 0, .36, .3); add(g, sph(.09, mat('#3a2616')), 0, .55, .3);
  g.userData.flame = flame(g, 0, .74, .3, 1.15); flame(g, .04, .66, .3, .6); return g;
}
// A server is a machine in the film's one visual language: a stone altar carrying an open book, with a service relic floating above it. The altar's runes are the
// status lights, the book's pages glow while it has power, and the relic dims and drops its glow when the power goes. idx picks which service floats above;
// it counts up in build order and is reset before every shot is built (see resetServers), so a shot always builds the same way.
let SRV_N = 0; const SERVERS = [];
export function resetServers() { SRV_N = 0; }
export function server(color = C.warm, idx = SRV_N++, withRelic = true) {   // withRelic = false for shots that float their own services above the machines
  const g = new THREE.Group(), S = 1.25, TOP = 1.41 * S;
  add(g, altar(), 0, 0, 0).scale.setScalar(S);
  const leds = [0, 1, 2, 3].map(i => { const a = i / 4 * Math.PI * 2 + Math.PI / 4; return add(g, sph(.07, glow(color, 0)), Math.cos(a) * .93, 1.0, Math.sin(a) * .93); });
  const tome = add(g, laptop(1), 0, TOP + .03, 0); tome.scale.setScalar(1.3);
  g.userData.leds = leds; g.userData.tome = tome; g.userData.color = color;
  if (withRelic) {
    const relic = ICONS[idx % ICONS.length](); relic.scale.setScalar(.55); relic.position.set(0, TOP + 1.3, 0); g.add(relic);
    const halo = add(g, new THREE.Mesh(new THREE.TorusGeometry(.72, .035, 6, 40), glow(color, 0)), 0, TOP + 1.3, 0); halo.rotation.x = Math.PI / 2;
    const halo2 = add(g, new THREE.Mesh(new THREE.TorusGeometry(.5, .02, 6, 32), glow(color, 0)), 0, TOP + 1.3, 0); halo2.rotation.x = Math.PI / 2;
    const finial = add(g, sph(.07, glow(color, 0)), 0, TOP + 2.15, 0);
    g.userData.halo = halo; g.userData.halo2 = halo2; g.userData.finial = finial; g.userData.relic = relic;
    g.userData.dimRelic = dimOf(relic); g.userData.relicY = TOP + 1.3; g.userData.ph = SERVERS.length * 1.3; SERVERS.push(g);
  }
  return g;
}
// Dim every emissive material under an object: returns a setter taking a 0..1 level (1 = as built).
export function dimOf(o) { const ms = new Set(); o.traverse(n => { if (n.isMesh && n.material && n.material.emissive && n.material.emissive.getHex() !== 0) ms.add(n.material); }); const base = [...ms].map(m => [m, m.emissiveIntensity]); return k => base.forEach(([m, b]) => { m.emissiveIntensity = b * k; }); }
export function setServerPower(g, k, color) {
  if (g.userData.tome) setLaptop(g.userData.tome, clamp(k)); if (g.userData.dimRelic) g.userData.dimRelic(.1 + .9 * clamp(k));
  g.userData.leds.forEach((l, i) => { l.material.emissiveIntensity = 2.4 * clamp(k * 1.4 - i * .1); if (color) { l.material.color.set(color); l.material.emissive.set(color); } });
  for (const key of ['halo', 'halo2', 'finial']) { const h = g.userData[key]; if (h) { h.material.emissiveIntensity = (key === 'halo2' ? 1.2 : 1.8) * clamp(k); if (color) { h.material.color.set(color); h.material.emissive.set(color); } } }
}
let CARPET = null;
function carpetMat() {
  if (CARPET) return CARPET;
  const c = document.createElement('canvas'); c.width = 128; c.height = 512; const g = c.getContext('2d');
  g.fillStyle = '#6a1c24'; g.fillRect(0, 0, 128, 512); g.strokeStyle = '#d9a93a'; g.lineWidth = 5; g.strokeRect(5, 5, 118, 502); g.lineWidth = 2; g.strokeRect(24, 24, 80, 464);
  g.lineWidth = 3; for (let i = 0; i < 14; i++) { const y = 10 + i * 36; g.beginPath(); g.moveTo(9, y + 28); g.lineTo(9, y); g.lineTo(17, y); g.lineTo(17, y + 20); g.stroke(); g.beginPath(); g.moveTo(119, y + 28); g.lineTo(119, y); g.lineTo(111, y); g.lineTo(111, y + 20); g.stroke(); }
  for (let i = 0; i < 6; i++) { const y = 50 + i * 80; g.lineWidth = 3; g.beginPath(); g.moveTo(64, y - 28); g.lineTo(92, y); g.lineTo(64, y + 28); g.lineTo(36, y); g.closePath(); g.stroke(); g.fillStyle = '#d9a93a'; g.beginPath(); g.arc(64, y, 5, 0, 6.3); g.fill(); }
  const t = new THREE.CanvasTexture(c); t.colorSpace = THREE.SRGBColorSpace; CARPET = new THREE.MeshStandardMaterial({ map: t, roughness: .95 }); return CARPET;
}
export function drape(h = 4.6, w = 1.1) {
  const c = document.createElement('canvas'); c.width = 128; c.height = 256; const g = c.getContext('2d');
  for (let i = 0; i < 16; i++) { g.fillStyle = i % 2 ? '#4e2f1c' : '#654027'; g.fillRect(i * 8, 0, 8, 256); } g.fillStyle = '#d9a93a'; g.fillRect(0, 238, 128, 10); g.fillStyle = '#8a5a30'; g.fillRect(0, 0, 128, 12);
  const t = new THREE.CanvasTexture(c); t.colorSpace = THREE.SRGBColorSpace;
  const m = new THREE.Mesh(new THREE.PlaneGeometry(w, h, 8, 1), new THREE.MeshStandardMaterial({ map: t, side: THREE.DoubleSide, roughness: .95 }));
  const pos = m.geometry.attributes.position; for (let i = 0; i < pos.count; i++) pos.setZ(i, Math.sin(pos.getX(i) * 7) * .08); m.geometry.computeVertexNormals(); return m;
}
export function room(w = 14, d = 12, wallColor = C.wall) {
  const g = new THREE.Group();
  add(g, box(w, .2, d, stone(C.floor, w / 2.2, d / 2.2, 2, true)), 0, -.1, 0);
  add(g, box(w, 6, .2, stone(wallColor, w / 2.4, 1.6, 4)), 0, 3, -d / 2);
  add(g, box(.2, 6, d, stone(wallColor, d / 2.4, 1.6, 4)), -w / 2, 3, 0);
  sigilRings(g, .01, Math.min(w, d) * .28);
  add(g, box(2.2, .03, d * .94, carpetMat()), 0, .02, 0);
  // raised stylobate step along the back wall, with the colonnade on it
  add(g, box(w, .22, 1.7, stone(C.sandstone, w / 3, 1, 3)), 0, .11, -d / 2 + .85);
  [-.34, .34].forEach(f => add(g, pillar(5), w * f, .22, -d / 2 + .8)); add(g, pillar(5), -w / 2 + .7, .22, -d / 2 + .8); add(g, pillar(5), w / 2 - .7, .22, -d / 2 + .8);
  // entablature: architrave, triglyph frieze and a gold cornice running across the top of the colonnade
  const ex = add(g, new THREE.Group(), 0, 5.22, -d / 2 + .8);
  add(ex, box(w, .34, 1.0, stone(C.sandstone, w / 3, 1, 0)), 0, 0, 0);
  add(ex, box(w, .46, .96, stone(C.marble, w / 3, 1, 0)), 0, .4, 0);
  for (let i = 0; i < Math.floor(w * 1.4); i++) { const x = (i + .5) / Math.floor(w * 1.4) * w - w / 2; add(ex, box(.16, .4, .05, mat('#5a4430')), x, .4, .5); add(ex, box(.04, .4, .05, mat('#5a4430')), x + .07, .4, .52); }
  add(ex, box(w, .16, 1.15, brass()), 0, .68, 0.03); add(ex, box(w, .1, 1.1, keyMat(C.brass, '#3a2616', Math.round(w * 1.2))), 0, -.2, .02);
  // the inner doorway of the adytum, framed in gold, with a faint warm light inside
  const door = add(g, new THREE.Group(), 0, 0, -d / 2 + .12);
  add(door, box(2.6, 3.4, .1, mat('#0c0705')), 0, 1.7, 0);
  add(door, box(.2, 3.5, .22, stone(C.marble, 1, 1)), -1.4, 1.75, .05); add(door, box(.2, 3.5, .22, stone(C.marble, 1, 1)), 1.4, 1.75, .05); add(door, box(3.1, .28, .24, brass()), 0, 3.55, .06);
  add(door, box(2.3, 3.1, .02, new THREE.MeshBasicMaterial({ color: '#2a170b' })), 0, 1.65, .07);
  add(door, runePlane(.9, .9, C.rune, .9, 7, true), 0, 2.6, .09);
  [-.5, .5].forEach(f => add(g, banner(), w * f * .6, 3.0, -d / 2 + .15));
  [-.5, .5].forEach(f => add(g, drape(), w * f * .93, 2.9, -d / 2 + .25));
  // wall torches along the back and side walls, each with a pool of light
  [-.22, .22].forEach(f => add(g, sconce(), w * f, 2.6, -d / 2 + .12).scale.setScalar(1.6));
  [-.2, .15].forEach(f => { const t = add(g, sconce(), -w / 2 + .12, 2.6, d * f); t.rotation.y = Math.PI / 2; t.scale.setScalar(1.6); });
  add(g, brazier(), -w * .5 + 1.2, 0, d * .22); add(g, brazier(), w * .42, 0, -d * .3);
  // set dressing from the Polyfork props (each is skipped if its model did not load): guardian lions at the doorway, candelabra either side of the carpet,
  // urns and pots along the left wall, a lamp stand and a gilded palm on the right, a scroll rack for the plans
  const dress = (key, h, x, y, z, ry = 0) => { const o = placed(key, h); if (o) { o.position.set(x, y, z); o.rotation.y = ry; g.add(o); } return o; };
  dress('lion', 1.9, -2.9, .22, -d / 2 + 1.5, .25); dress('lion', 1.9, 2.9, .22, -d / 2 + 1.5, -.25);
  dress('candelabra', 2.2, -1.7, .22, -d / 2 + 1.9); dress('candelabra', 2.2, 1.7, .22, -d / 2 + 1.9);
  dress('urn', 1.3, -w / 2 + .9, 0, -d * .05, .5); dress('pot', 1.25, -w / 2 + .95, 0, -d * .34, 1.2);
  dress('scrollRack', 2.1, -w / 2 + .5, 0, d * .4, Math.PI / 2);
  dress('lamp', 2.0, w / 2 - 1.3, 0, -d * .08); dress('palm', 1.7, w / 2 - 1.0, 0, d * .14);
  return g;
}
export function table(w = 3.4, d = 1.6, h = .8) {
  const g = new THREE.Group();
  add(g, box(w, .14, d, mat(C.metal)), 0, h, 0);
  add(g, box(w + .1, .06, d + .1, brass()), 0, h - .09, 0);
  add(g, box(w * .94, .02, d * .9, mat(C.crimson)), 0, h + .08, 0);
  for (const sx of [-1, 1]) for (const sz of [-1, 1]) add(g, box(.16, h, .16, mat('#38334d')), sx * (w / 2 - .14), h / 2, sz * (d / 2 - .14));
  return g;
}
// A laptop is a tome: leather cover and two glowing pages.
export function laptop(on = 1) {
  const g = new THREE.Group();
  add(g, box(.9, .05, .6, mat('#4a2c2a')), 0, 0, 0);
  const pm = glow('#ffe9b8', 0);
  const l = add(g, box(.42, .02, .5, pm), -.22, .05, 0); l.rotation.z = .12; const r = add(g, box(.42, .02, .5, pm), .22, .05, 0); r.rotation.z = -.12;
  g.userData.screen = l; setLaptop(g, on); return g;
}
export function setLaptop(g, k) { g.userData.screen.material.emissiveIntensity = 1.0 * k; }
export function lineBetween(a, b, color, r = .04) {
  const len = a.distanceTo(b);
  const m = new THREE.Mesh(new THREE.CylinderGeometry(r, r, len, 8), glow(color, 1.1));
  m.position.copy(a).lerp(b, .5); m.quaternion.setFromUnitVectors(V(0, 1, 0), b.clone().sub(a).normalize()); return m;
}

// The balancer is a brass gyroscope: three turning rings around a glowing core, so it reads as a thing that spins and decides.
export function balancer() {
  const g = new THREE.Group();
  add(g, cyl(.38, .2, brass(), 10), 0, -.3, 0); add(g, cyl(.14, .4, brass(), 8), 0, -.05, 0);
  const core = add(g, sph(.26, glow(C.blue, 2)), 0, .4, 0);
  const rings = [0, 1, 2].map(i => { const r = new THREE.Mesh(new THREE.TorusGeometry(.5 + .14 * i, .04, 6, 40), brass()); r.position.y = .4; g.add(r); return r; });
  GYRO.push({ rings }); g.userData.core = core; return g;
}
// A tiered dais for the cluster: round stone steps, a brass rim, and a ring of glyphs.
export function dais(r = 7) {
  const g = new THREE.Group();
  add(g, cyl(r, .3, stone('#6a5a48', 4, 1), 40), 0, .15, 0); add(g, cyl(r * .88, .3, stone('#7a6a56', 4, 1), 40), 0, .45, 0); add(g, cyl(r * .76, .3, stone(C.floor, 4, 1, 2, true), 40), 0, .75, 0);
  [r, r * .88, r * .76].forEach((rr, i) => { const t = add(g, new THREE.Mesh(new THREE.TorusGeometry(rr, .05, 6, 80), brass()), 0, .3 + i * .3, 0); t.rotation.x = Math.PI / 2; });
  for (let i = 0; i < 24; i++) { const a = i / 24 * Math.PI * 2; add(g, box(.12, .14, .03, glow(C.brass, .6)), Math.cos(a) * r * .94, .28, Math.sin(a) * r * .94).rotation.y = -a + Math.PI / 2; }
  g.position.y = -.8; return g;
}
// A warning seal for the cursed questions of hosting: a red ring around a turning cross and an eye.
export function seal() {
  const g = new THREE.Group();
  add(g, new THREE.Mesh(new THREE.TorusGeometry(.4, .04, 6, 32), glow(C.red, 1.8)), 0, 0, 0);
  for (const a of [0, Math.PI / 4, Math.PI / 2, 3 * Math.PI / 4]) { const b = add(g, box(.8, .035, .02, glow(C.red, 1.4)), 0, 0, 0); b.rotation.z = a; }
  add(g, sph(.1, glow('#ffd2a0', 2)), 0, 0, 0); return g;
}
// A tiered ziggurat for the far-off rented data center: stepped stone with rows of lit windows and a flame on top.
export function ziggurat(w = 5, d = 3.4) {
  const g = new THREE.Group();
  for (let i = 0; i < 4; i++) { const k = 1 - i * .2; add(g, box(w * k, .85, d * k, stone(i % 2 ? '#7a6a56' : C.wall, 2, 1)), 0, .43 + i * .85, 0); add(g, box(w * k + .1, .07, d * k + .1, brass()), 0, .86 + i * .85, 0);
    for (let c = 0; c < 6 - i; c++) add(g, box(.22, .3, .03, glow(c % 3 ? C.cloud : C.green, .9)), (c - (5 - i) / 2) * (w * k / (6.2 - i)), .45 + i * .85, d * k / 2 + .02); }
  flame(g, 0, 3.7, 0, 1.4); return g;
}

// A stand-in with the person() animation hooks but nothing to draw. The film follows one person, so the second acolyte's slot is empty.
export function ghost() { const g = new THREE.Group(); g.userData.head = new THREE.Group(); g.userData.upper = new THREE.Group(); return g; }
