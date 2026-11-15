import * as THREE from '../vendor/three.module.js';
import { makeEnvironment, decorate, updateGlows, makeEmbers, makeVignette } from './fx.js';
import { preloadProps } from './assets.js';
import { FPS, SHOT_FRAMES, D, C, clamp, updateAmbient, pruneAmbient, resetServers } from './lib.js';
import act1 from './shots/act1.js';
import { shot5, shot6, shot7, shot8, shot9 } from './shots/act2.js';
import act2b from './shots/act2b.js';
import act3 from './shots/act3.js';
import part2 from './shots/part2.js';
import { part as pc } from './shots/part2c.js';
import { part as pd } from './shots/part2d.js';
import { whatWhen } from './shots/what_when.js';
import partE from './shots/part2e.js';
import partF from './shots/part2f.js';

const W = 960, H = 544;
const [failoverByHand, failoverTest, drift, singlePoint, highAvailability, regional, global, tradeoff] = act2b;
// Chapter C: a lead-in, then one shot per scenario from a single default scene (see shots/what_when.js).
const P = { ...pc, ...pd };
const chapterC = [P.lead, ...whatWhen];
const SHOTS = [...act1, shot5, shot6, shot7, failoverByHand, failoverTest, drift, shot8, shot9, singlePoint, highAvailability, regional, global, tradeoff, ...act3, ...part2, ...chapterC, ...partE, ...partF];
// Shots are SHOT_FRAMES long unless objectives.json gives one a 'seconds' value (up to 6 s). FR holds each shot's frame count, START its first frame, N the total.
let OBJ = []; try { OBJ = await (await fetch(new URL('../objectives.json', import.meta.url))).json(); } catch (e) { console.warn('no objectives.json: all shots use the default length'); }
const FR = SHOTS.map((_, i) => Math.round(((OBJ[i] && OBJ[i].seconds) || SHOT_FRAMES / FPS) * FPS));
const START = []; let N = 0; FR.forEach(n => { START.push(N); N += n; });
const renderer = new THREE.WebGLRenderer({ antialias: true, preserveDrawingBuffer: true });
renderer.setSize(W, H); renderer.setPixelRatio(1);
renderer.outputColorSpace = THREE.SRGBColorSpace;
renderer.toneMapping = THREE.ACESFilmicToneMapping; renderer.toneMappingExposure = 1.15;
renderer.shadowMap.enabled = true; renderer.shadowMap.type = THREE.PCFSoftShadowMap;
document.body.appendChild(renderer.domElement);
const scene = new THREE.Scene();
const camera = new THREE.PerspectiveCamera(46, W / H, .1, 80); scene.add(camera);
scene.add(new THREE.HemisphereLight('#ffe7c8', '#4a2f22', 1.15));
const sun = new THREE.DirectionalLight('#ffd9a0', 2.4); sun.position.set(-9, 15, 11); scene.add(sun, sun.target);
sun.castShadow = true; sun.shadow.mapSize.set(2048, 2048);
Object.assign(sun.shadow.camera, { left: -22, right: 22, top: 22, bottom: -22, near: 1, far: 60 });
sun.shadow.bias = -.0004; sun.shadow.normalBias = .03; sun.shadow.radius = 4;
scene.environment = makeEnvironment(renderer);
const rim = new THREE.DirectionalLight('#5b86ff', 1.0); rim.position.set(6, 4, -8); scene.add(rim);

makeVignette(camera);
const embers = makeEmbers(scene);
// Shots are built the first time they are visited, so a long film loads quickly. Building is deterministic.
const updaters = SHOTS.map(s => ({ g: null, update: null, s, glows: null }));
function ensure(o) { if (o.g) return; resetServers(); o.g = new THREE.Group(); o.g.visible = false; scene.add(o.g); o.update = o.s.build(o.g); o.glows = decorate(o.g); }

// Free shots the film has moved well past (GPU buffers, textures, animation entries). They rebuild identically if visited again.
function release(o) {
  scene.remove(o.g);
  o.g.traverse(n => { if (n.geometry) n.geometry.dispose(); const mats = n.material ? [].concat(n.material) : []; mats.forEach(mt => { Object.values(mt).forEach(v => { if (v && v.isTexture) v.dispose(); }); mt.dispose(); }); });
  o.g = null; o.update = null; o.glows = null;
}
function seek(time) {
  const f = clamp(Math.round(time * FPS), 0, N - 1);
  let idx = SHOTS.length - 1; while (idx > 0 && START[idx] > f) idx--;
  const lt = (f - START[idx]) / FPS, u = lt / (FR[idx] / FPS);
  const cur = updaters[idx]; ensure(cur);
  let freed = false; updaters.forEach((o, i) => { if (o.g && Math.abs(i - idx) > 3) { release(o); freed = true; } }); if (freed) pruneAmbient();
  updaters.forEach((o, i) => { if (o.g) o.g.visible = i === idx; });
  scene.background = new THREE.Color(cur.s.bg); scene.fog = new THREE.Fog(cur.s.bg, 16, 40);
  cur.update(lt, u); updateAmbient(lt); updateGlows(cur.glows); embers(lt);
  const [pos, look] = cur.s.cam(u);
  camera.position.copy(pos); camera.lookAt(look); camera.updateMatrixWorld();
  // a long set (the gallery slide) keeps the key light, and so its shadows, with the camera
  const lx = cur.s.followLight ? pos.x : 0; sun.position.set(-9 + lx, 15, 11); sun.target.position.set(lx, 0, 0); sun.target.updateMatrixWorld();
  renderer.render(scene, camera);
}
await preloadProps();   // the film's GLB props must be in memory before any shot builds
window.reconstruction = {
  pause() {}, seek,
  starts() { return START.map(f => f / FPS); }, durations() { return FR.map(n => n / FPS); },
  stats() { const i = renderer.info; return { calls: i.render.calls, triangles: i.render.triangles, geometries: i.memory.geometries, textures: i.memory.textures, programs: i.programs.length, built: updaters.filter(o => o.g).length }; },
  getCameraState() { return { position: camera.position.toArray(), quaternion: camera.quaternion.toArray(), fov: camera.fov }; }
};
seek(0);
