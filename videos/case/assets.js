// Polyfork props (low-poly GLB models, https://polyfork.dev, free licence). They are fetched once at startup from ../assets/polyfork, then cloned
// synchronously by prop(). If a file is missing the caller falls back to the procedural version, so shots still build without the assets.
import * as THREE from '../vendor/three.module.js';
import { GLTFLoader } from '../vendor/addons/loaders/GLTFLoader.js';

export const PROPS = {
  brazier: 'standing-brazier-c9e4ee',
  sconce: 'wall-torch-sconce-6ddb52',
  coin: 'gold-coin-29afc1',
  sofa: 'sofa-9c0e7e',
  scroll: 'spell-scroll-29d32d',
  // set dressing for the hall
  lion: 'guardian-lion-statue-5bde18',
  candelabra: 'brass-candelabra-89ab76',
  urn: 'funerary-urn-688493',
  pot: 'clay-storage-pot-626452',
  lamp: 'bronze-lamp-stand-59b9d2',
  palm: 'gilded-urn-palm-435c00',
  scrollRack: 'scroll-rack-f465dd',
  censer: 'swinging-censer-bfd4ad',
  orb: 'divination-orb-115d77',
  banner: 'faction-banner-pole-39835b',
};
const CACHE = new Map();

export async function preloadProps() {
  const L = new GLTFLoader();
  await Promise.all(Object.entries(PROPS).map(([key, id]) => new Promise(res => L.load(new URL(`../assets/polyfork/${id}.glb`, import.meta.url).href, g => { CACHE.set(key, g.scene); res(); }, undefined, e => { console.warn('prop not loaded:', key, e); res(); }))));
}

// A fresh clone of a loaded prop, or null if it did not load. Geometry and materials are shared between clones.
export function prop(key) {
  const src = CACHE.get(key); if (!src) return null;
  const o = src.clone(true);
  o.traverse(n => { if (n.isMesh) { n.castShadow = true; n.receiveShadow = true; } });
  return o;
}
// Give a prop its own tinted copy of every material (so tinting one sofa does not recolor the rest).
export function tint(o, color) {
  o.traverse(n => { if (n.isMesh) { n.material = n.material.clone(); n.material.color.set(color); } });
  return o;
}

// A prop scaled to a target height, centred over its own origin and resting on y = 0, wrapped so callers can position and rotate it freely.
export function placed(key, height) {
  const o = prop(key); if (!o) return null;
  const b = new THREE.Box3().setFromObject(o), k = height / (b.max.y - b.min.y); o.scale.setScalar(k);
  b.setFromObject(o); const c = b.getCenter(new THREE.Vector3()); o.position.set(-c.x, -b.min.y, -c.z);
  const w = new THREE.Group(); w.add(o); return w;
}
