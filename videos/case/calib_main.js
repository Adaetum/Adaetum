import * as THREE from '../vendor/three.module.js';
import { makeEnvironment, decorate, updateGlows, makeEmbers, makeVignette } from './fx.js';
import { FPS, SHOT_FRAMES, D, C, clamp, updateAmbient } from './lib.js';
import { calibration } from './shots/calibration.js';

const W = 960, H = 544;
const SHOTS = [calibration];
const N = SHOTS.length * SHOT_FRAMES;
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
function ensure(o) { if (o.g) return; o.g = new THREE.Group(); o.g.visible = false; scene.add(o.g); o.update = o.s.build(o.g); o.glows = decorate(o.g); }

function seek(time) {
  const f = clamp(Math.round(time * FPS), 0, N - 1);
  const idx = Math.floor(f / SHOT_FRAMES), lt = (f % SHOT_FRAMES) / FPS, u = lt / D;
  const cur = updaters[idx]; ensure(cur);
  updaters.forEach((o, i) => { if (o.g) o.g.visible = i === idx; });
  scene.background = new THREE.Color(cur.s.bg); scene.fog = new THREE.Fog(cur.s.bg, 16, 40);
  cur.update(lt, u); updateAmbient(lt); updateGlows(cur.glows); embers(lt);
  const [pos, look] = cur.s.cam(u);
  camera.position.copy(pos); camera.lookAt(look); camera.updateMatrixWorld();
  renderer.render(scene, camera);
}
window.reconstruction = {
  pause() {}, seek,
  getCameraState() { return { position: camera.position.toArray(), quaternion: camera.quaternion.toArray(), fov: camera.fov }; }
};
seek(0);
