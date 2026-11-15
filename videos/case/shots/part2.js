// Part 2, chapters A and B: the turn, then the council of tools (one pair of shots per tool).
import * as THREE from '../../vendor/three.module.js';
import { C, D, add, box, cyl, sph, mat, glow, brass, stone, V, arc, ease, seg, lerp, pulse, clamp, rng, room, person, server, setServerPower, lineBetween, dais, flame, altar, animateIcon, ICONS, pillar, banner, sconce, brazier, keyMat } from '../lib.js';
import { TOOLS, EMBLEMS } from '../emblems.js';

const camPath = (a, b, la, lb) => u => { const k = ease(u); return [V(lerp(a[0], b[0], k), lerp(a[1], b[1], k), lerp(a[2], b[2], k)), V(lerp(la[0], lb[0], k), lerp(la[1], lb[1], k), lerp(la[2], lb[2], k))]; };
const N = TOOLS.length;

// Dim every emissive material in an emblem so a lit one stands out. Returns a setter for the glow level (0..1).
function dimmable(obj) {
  const mats = new Set(); obj.traverse(o => { if (o.isMesh && o.material && o.material.emissive && o.material.emissive.getHex() !== 0 && !mats.has(o.material)) mats.add(o.material); });
  const base = [...mats].map(m => [m, m.emissiveIntensity]);
  return k => base.forEach(([m, b]) => { m.emissiveIntensity = b * (.12 + .88 * k); });
}

// The council: all tools on small altars in an arc behind the stage. Index `lit` is bright. Returns handles to animate.
function council(g, { radius = 8.6, spread = 1.15, scale = .42 } = {}) {
  const items = TOOLS.map((tool, i) => {
    const a = lerp(-spread, spread, i / (N - 1)), x = Math.sin(a) * radius, z = -Math.cos(a) * radius * .55 - 1.8;
    const grp = add(g, new THREE.Group(), x, 0, z); grp.rotation.y = -a * .8;
    const AS = .62, top = 1.41 * AS; add(grp, altar(), 0, 0, 0).scale.setScalar(AS);   // each tool stands on one of the film's own altars
    const em = EMBLEMS[tool.key](); em.scale.setScalar(scale); em.position.y = top + scale * 1.1; grp.add(em);
    const dim = dimmable(em); dim(.0);
    return { grp, em, dim, tool, x, z };
  });
  return items;
}

// Chapter A ---------------------------------------------------------------------------------------------
// A1. So how does Adaetum pull this off? The cluster stands alone while the council waits in the dark behind it.
export const turn1 = {
  bg: C.bg, cam: camPath([0, 3.2, 12], [0, 7.5, 14], [0, 1.6, 0], [0, 2.0, -3]),
  build(g) {
    g.add(room(26, 18)); add(g, dais(5), 0, -.9, 0);
    const srvs = [[-2.4, 0, 1], [2.4, 0, 1], [0, 0, -1.6]].map(([x, y, z]) => { const s = add(g, server(C.blue), x, y, z); s.scale.setScalar(1.1); setServerPower(s, 1, C.blue); return s; });
    const items = council(g, { radius: 11, spread: 1.0, scale: .5 });
    const veil = add(g, new THREE.Mesh(new THREE.PlaneGeometry(16, 7, 12, 1), new THREE.MeshStandardMaterial({ color: C.crimson, side: THREE.DoubleSide, roughness: .95 })), 0, 3.5, -6);
    return (t, u) => {
      items.forEach((o, i) => { const k = ease(seg(t, 1.2 + i * .1, 2.0 + i * .1)); o.dim(k * .35); o.grp.scale.setScalar(Math.max(.001, k)); });
      veil.position.y = lerp(3.5, 11, ease(seg(t, .4, 2.2))); veil.visible = veil.position.y < 10.5;
      const pos = veil.geometry.attributes.position; for (let i = 0; i < pos.count; i++) pos.setZ(i, Math.sin(pos.getX(i) * 1.3 + t * 2) * .25); pos.needsUpdate = true;
    };
  }
};
// A2. It invents nothing. It wires proven tools together: each tool flies in and links to the cluster.
export const turn2 = {
  bg: C.bg, cam: camPath([0, 5.5, 13], [0, 4.5, 10], [0, 1.8, -1], [0, 1.8, 0]),
  build(g) {
    g.add(room(26, 18)); add(g, dais(5), 0, -.9, 0);
    const srvs = [[-2.4, 1], [2.4, 1], [0, -1.6]].map(([x, z]) => { const s = add(g, server(C.blue), x, 0, z); s.scale.setScalar(1.1); setServerPower(s, 1, C.blue); return s; });
    const items = council(g, { radius: 11, spread: 1.0, scale: .5 }); items.forEach(o => o.dim(.35));
    const flyers = TOOLS.map((tool, i) => { const e = EMBLEMS[tool.key](); e.scale.setScalar(.5); g.add(e); const d = dimmable(e); return { e, d, i }; });
    const links = TOOLS.map((tool, i) => { const l = add(g, new THREE.Mesh(new THREE.TorusGeometry(.2 + i * .02, .015, 6, 24), glow(tool.color, 1.2)), 0, 0, 0); l.rotation.x = Math.PI / 2; return l; });
    return (t) => {
      flyers.forEach(({ e, d, i }) => {
        const k = ease(seg(t, .3 + i * .17, 1.4 + i * .17)), o = items[i], a = i / N * Math.PI * 2 + t * .15;
        const target = V(Math.cos(a) * 3.0, 2.6 + (i % 3) * .5, Math.sin(a) * 2.2 - .3);
        e.position.copy(arc(V(o.x, 1.45, o.z), target, k, 1.6)); e.rotation.y = a * .6; e.scale.setScalar(lerp(.5, .6, k)); d(.35 + .65 * k); e.visible = k > .001;
        links[i].position.copy(target).setY(.1); links[i].scale.setScalar(Math.max(.001, k)); links[i].visible = k > .5;
      });
      items.forEach((o, i) => o.dim(.35 * (1 - ease(seg(t, .3 + i * .17, 1.0 + i * .17)))));
    };
  }
};
// A3 is the first slice of the gallery slide (see 'The gallery' below).

// Chapter B ---------------------------------------------------------------------------------------------
// The gallery: all 17 tools stand in one long hall, each on its own altar with room between them, and the camera slides along the row in one continuous take across
// shots 28 to 37. Shot 28 sweeps in from the far end to the first tool; after that every tool holds the frame for exactly 2.5 seconds (two tools per shot, with a
// sliver of slack at each end of the shot). The narration is written two tools to a shot, in this order.
const ORDER = [0, 1, 2, 3, 5, 6, 7, 8, 9, 10, 4, 12, 11, 13, 14, 15, 16];   // TOOLS indices: rocky ansible | rke2 longhorn | gitea argocd | openbao eso | authentik kubewarden | cert-manager ingress | tailscale cloudflared | prometheus kured | agent
const SPX = 5.6, gx = j => (j - 8) * SPX, HALF = SPX * 8.5 + 6;
const HOLD = 2.5, SLACK = .0833;                                      // each tool's window; shots are 2 x HOLD + 2 x SLACK long
const winStart = j => Math.floor(j / 2) * D + SLACK + (j % 2) * HOLD;  // seconds after the start of shot 29 at which tool j (gallery slot) takes the frame
// camera x as a function of seconds after the start of shot 29 (negative in shot 28): sweep in, then rest on each tool and glide to the next
const KEYS = (() => { const k = [[-D, gx(16) + 4], [-D + 3.6, gx(0)]]; for (let j = 0; j < 17; j++) { k.push([winStart(j) + .35, gx(j)], [winStart(j) + HOLD - .35, gx(j)]); } return k; })();
const camX = G => { if (G <= KEYS[0][0]) return KEYS[0][1]; for (let i = 0; i < KEYS.length - 1; i++) { const [t0, x0] = KEYS[i], [t1, x1] = KEYS[i + 1]; if (G < t1) { const u = (G - t0) / (t1 - t0), e = u * u * u * (u * (u * 6 - 15) + 10); return lerp(x0, x1, e); } } return KEYS[KEYS.length - 1][1]; };
function gallery(g) {
  const W = HALF * 2 + 20;
  add(g, box(W, .2, 14, stone(C.floor, W / 2.2, 14 / 2.2, 2, true)), 0, -.1, 0);
  add(g, box(W, 6, .2, stone(C.wall, W / 2.4, 1.6, 4)), 0, 3, -6.5);
  add(g, box(W, .03, 2.0, mat(C.crimson)), 0, .02, 3.6); add(g, box(W, .035, .1, brass()), 0, .03, 2.65); add(g, box(W, .035, .1, brass()), 0, .03, 4.55);
  add(g, box(W, .22, 1.7, stone(C.sandstone, W / 3, 1, 3)), 0, .11, -5.7);
  for (let j = -1; j < 17; j++) add(g, pillar(5), gx(j) + SPX / 2, .22, -5.6);
  const ex = add(g, new THREE.Group(), 0, 5.22, -5.6);
  add(ex, box(W, .34, 1.0, stone(C.sandstone, W / 3, 1, 0)), 0, 0, 0); add(ex, box(W, .46, .96, stone(C.marble, W / 3, 1, 0)), 0, .4, 0);
  add(ex, box(W, .16, 1.15, brass()), 0, .68, .03); add(ex, box(W, .1, 1.1, keyMat(C.brass, '#3a2616', Math.round(W * 1.2))), 0, -.2, .02);
  for (let j = 0; j < 17; j++) { const sc = add(g, sconce(), gx(j) + (j % 2 ? 2.1 : -2.1), 3.0, -6.25); sc.scale.setScalar(1.6); }
  for (let j = 1; j < 17; j += 4) add(g, brazier(), gx(j) + SPX / 2, 0, -3.6);
}
// slice k (0 = shot 28) of the slide. The scene is identical in every slice; only the camera's window on it and the clock differ.
const slide = k => ({
  followLight: true, bg: C.bg,
  cam: u => { const x = camX((k - 1) * D + u * D); return [V(x, 3.9, 9.2), V(x, 2.8, 0)]; },
  build(g) {
    gallery(g);
    const stops = ORDER.map((ti, j) => {
      const tool = TOOLS[ti], alt = add(g, altar(), gx(j), 0, 0); alt.scale.setScalar(1.4);
      const em = EMBLEMS[tool.key](); em.scale.setScalar(1.3); em.position.set(gx(j), 3.0, 0); g.add(em);
      const ring = add(g, new THREE.Mesh(new THREE.TorusGeometry(1.5, .05, 8, 48), glow(tool.color, 1.4)), gx(j), 2.9, 0); ring.rotation.x = Math.PI / 2 - .3;
      return { em, ring, dim: dimmable(em), j };
    });
    return (t) => {
      const G = (k - 1) * D + t, cx = camX(G);
      stops.forEach(({ em, ring, dim, j }) => {
        const near = clamp(1 - Math.abs(cx - gx(j)) / (SPX * .8)), f = near * near * (3 - 2 * near);     // 1 when the camera is centred on it
        dim(.3 + .7 * f); em.scale.setScalar(1.3 + .45 * f); em.position.y = 3.0 + .25 * f + .06 * Math.sin(G * 1.5 + j); em.rotation.y = .5 * Math.sin(G * .8 + j) * f;
        ring.scale.setScalar(.8 + .3 * f); ring.rotation.z = G * .7; ring.material.emissiveIntensity = .4 + 1.4 * f;
      });
    };
  }
});
export const slides = Array.from({ length: 10 }, (_, k) => slide(k));
export default [turn1, turn2, ...slides];
