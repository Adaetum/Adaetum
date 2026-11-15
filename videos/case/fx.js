// Fidelity layer: environment lighting, soft shadows, automatic glow halos, drifting embers, vignette.
import * as THREE from '../vendor/three.module.js';
import { rng, clamp } from './lib.js';

let glowTex = null;
export function glowTexture() {
  if (glowTex) return glowTex;
  const c = document.createElement('canvas'); c.width = c.height = 128; const g = c.getContext('2d');
  const gr = g.createRadialGradient(64, 64, 0, 64, 64, 64);
  gr.addColorStop(0, 'rgba(255,255,255,1)'); gr.addColorStop(.25, 'rgba(255,255,255,.45)'); gr.addColorStop(.6, 'rgba(255,255,255,.1)'); gr.addColorStop(1, 'rgba(255,255,255,0)');
  g.fillStyle = gr; g.fillRect(0, 0, 128, 128);
  glowTex = new THREE.CanvasTexture(c); glowTex.colorSpace = THREE.SRGBColorSpace; return glowTex;
}

// A small procedural environment so brass and stone catch warm and violet light instead of looking flat.
export function makeEnvironment(renderer) {
  const env = new THREE.Scene();
  env.add(new THREE.Mesh(new THREE.BoxGeometry(40, 40, 40), new THREE.MeshBasicMaterial({ color: '#3a2a22', side: THREE.BackSide })));
  const panel = (color, w, h, x, y, z) => { const m = new THREE.Mesh(new THREE.PlaneGeometry(w, h), new THREE.MeshBasicMaterial({ color, side: THREE.DoubleSide })); m.position.set(x, y, z); m.lookAt(0, 0, 0); env.add(m); };
  panel('#ffb35a', 14, 9, -14, 8, 10); panel('#4f7dff', 12, 8, 15, 6, -8); panel('#ffffff', 9, 9, 0, 18, 0); panel('#ff8a4a', 10, 4, 0, -6, 16);
  const pm = new THREE.PMREMGenerator(renderer); const tex = pm.fromScene(env, .04).texture; pm.dispose(); return tex;
}

// Shadows, env intensity, and a soft additive halo on every small emissive object. Returns the glow list for per-frame updates.
export function decorate(group) {
  const glows = [], tmp = new THREE.Vector3();
  const meshes = []; group.traverse(o => { if (o.isMesh) meshes.push(o); });
  meshes.forEach(m => {
    const mt = m.material;
    if (mt && mt.isMeshStandardMaterial) {
      mt.envMapIntensity = .75;
      if (!mt.transparent) { m.castShadow = true; m.receiveShadow = true; }
      if (mt.emissive && mt.emissive.getHex() !== 0) {
        m.geometry.computeBoundingSphere(); const r = m.geometry.boundingSphere.radius;
        if (r < .6) {
          const sp = new THREE.Sprite(new THREE.SpriteMaterial({ map: glowTexture(), color: mt.emissive.clone(), transparent: true, opacity: 0, depthWrite: false, blending: THREE.AdditiveBlending, fog: false }));
          sp.scale.setScalar(Math.max(.7, r * 7)); sp.renderOrder = 5; m.add(sp); glows.push({ m, sp });
        }
      }
    }
  });
  return glows;
}
export function updateGlows(glows) {
  glows.forEach(({ m, sp }) => { const mt = m.material; sp.material.color.copy(mt.emissive); sp.material.opacity = clamp(mt.emissiveIntensity * .17, 0, .55); });
}

export function makeEmbers(scene) {
  const N = 160, r = rng(99), base = [], ph = [];
  const pos = new Float32Array(N * 3);
  for (let i = 0; i < N; i++) { base.push([(r() - .5) * 30, r() * 10, (r() - .35) * 26]); ph.push(r() * 6.28); }
  const geo = new THREE.BufferGeometry(); geo.setAttribute('position', new THREE.BufferAttribute(pos, 3));
  const pts = new THREE.Points(geo, new THREE.PointsMaterial({ map: glowTexture(), color: '#ffc070', size: .28, transparent: true, opacity: .75, depthWrite: false, blending: THREE.AdditiveBlending, sizeAttenuation: true, fog: false }));
  pts.frustumCulled = false; scene.add(pts);
  return t => { for (let i = 0; i < N; i++) { const b = base[i]; pos[i * 3] = b[0] + Math.sin(t * .5 + ph[i]) * .5; pos[i * 3 + 1] = (b[1] + t * (.25 + (i % 5) * .06)) % 10; pos[i * 3 + 2] = b[2] + Math.cos(t * .4 + ph[i]) * .4; } geo.attributes.position.needsUpdate = true; };
}

export function makeVignette(camera) {
  const c = document.createElement('canvas'); c.width = 256; c.height = 144; const g = c.getContext('2d');
  const gr = g.createRadialGradient(128, 72, 40, 128, 72, 150);
  gr.addColorStop(0, 'rgba(6,3,12,0)'); gr.addColorStop(.6, 'rgba(6,3,12,.15)'); gr.addColorStop(1, 'rgba(6,3,12,.7)');
  g.fillStyle = gr; g.fillRect(0, 0, 256, 144);
  const tex = new THREE.CanvasTexture(c); tex.colorSpace = THREE.SRGBColorSpace;
  const h = 2 * Math.tan(THREE.MathUtils.degToRad(camera.fov / 2)) * 1.0;
  const m = new THREE.Mesh(new THREE.PlaneGeometry(h * camera.aspect * 1.02, h * 1.02), new THREE.MeshBasicMaterial({ map: tex, transparent: true, depthTest: false, depthWrite: false, fog: false }));
  m.position.z = -1; m.renderOrder = 999; camera.add(m);
}
