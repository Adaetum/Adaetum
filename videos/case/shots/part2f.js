// Part 2, chapter D (compared with the industry standard) and chapter F (the close).
import * as THREE from '../../vendor/three.module.js';
import { feel, feelAt } from '../lib.js';
import { placed } from '../assets.js';
import { D, C, add, box, cyl, sph, mat, glow, brass, stone, V, arc, ease, seg, lerp, pulse, clamp, rng, room, person, server, setServerPower, lineBetween, dais, flame, altar, chest, coin, smoke, lightningBolt, setLightning, fireOn, animateIcon, ICONS, templeFloor, banner, ghost } from '../lib.js';
import { EMBLEMS } from '../emblems.js';

const camPath = (a, b, la, lb) => u => { const k = ease(u); return [V(lerp(a[0], b[0], k), lerp(a[1], b[1], k), lerp(a[2], b[2], k)), V(lerp(la[0], lb[0], k), lerp(la[1], lb[1], k), lerp(la[2], lb[2], k))]; };

// ---- the open ground and the road to a distant monument --------------------------------------------------
function ground(g, { mon = null, far = -12 } = {}) {
  templeFloor(g, 60, 50);
  const r = rng(5); for (let i = 0; i < 12; i++) { const m = add(g, new THREE.Mesh(new THREE.ConeGeometry(2 + r() * 3, 3 + r() * 4, 5), stone('#5a4a3c', 1, 1)), (r() - .5) * 44, 1.5, -22 - r() * 6); m.rotation.y = r() * 3; }
  const road = add(g, box(2.4, .04, 30, mat('#2a2230')), 0, .03, -2); for (let i = 0; i < 12; i++) add(g, box(.12, .05, .5, glow(C.brass, .5)), 0, .07, 10 - i * 2.4);
  const friends = [add(g, ghost(), -.9, 0, 8.5), add(g, person('#2f5fc4'), 0, 0, 8.5)]; friends.forEach(p => { p.rotation.y = Math.PI; p.scale.setScalar(.9); });
  if (mon) add(g, mon, 0, 0, far);
  return { friends, road };
}
const lookDown = (g) => { const p = ground(g); return p; };

// monuments
const fortress = () => { const g = new THREE.Group(); add(g, box(9, 4, 4, stone(C.wall, 3, 1)), 0, 2, 0); for (const x of [-4.5, -1.5, 1.5, 4.5]) { add(g, cyl(1.1, 6, stone(C.wall, 1, 2), 12), x, 3, 0); add(g, new THREE.Mesh(new THREE.ConeGeometry(1.4, 1.6, 12), mat(C.crimson)), x, 6.8, 0); } add(g, box(1.8, 2.6, .2, mat('#2a1a14')), 0, 1.3, 2.1); for (let i = 0; i < 4; i++) { const b = add(g, banner(.8, 1.8), -3.6 + i * 2.4, 3.4, 2.1); } return g; };
const obelisk = () => { const g = new THREE.Group(); const m = add(g, box(2.2, 8, 2.2, new THREE.MeshStandardMaterial({ color: '#1a1a26', metalness: .7, roughness: .3 })), 0, 4, 0); const seam = (y) => add(g, box(2.24, .06, 2.24, glow(C.cloud, 1.6)), 0, y, 0); [1.5, 3, 4.5, 6].forEach(seam); add(g, new THREE.Mesh(new THREE.ConeGeometry(1.6, 1.2, 4), new THREE.MeshStandardMaterial({ color: '#1a1a26', metalness: .7, roughness: .3 })), 0, 8.6, 0).rotation.y = Math.PI / 4; add(g, sph(.2, glow(C.cloud, 2.4)), 0, 9.5, 0); return g; };
const cloudMon = () => { const g = new THREE.Group(), cm = mat('#dfe8ff', { emissive: '#8fb0ff', emissiveIntensity: .15 }); for (const [x, y, z, r] of [[-3, 5.5, 0, 2.2], [0, 6.5, 0, 3], [3.4, 5.4, 0, 2.3], [0, 5, 1, 2.6]]) add(g, sph(r, cm), x, y, z); const t = add(g, box(2.2, 3.2, 2.2, stone(C.wall, 1, 1)), 0, 7.6, 0); add(g, new THREE.Mesh(new THREE.ConeGeometry(1.7, 1.5, 4), mat(C.crimson)), 0, 10, 0).rotation.y = Math.PI / 4; return g; };
const scaffoldBrick = () => { const g = new THREE.Group(); const r = rng(9); for (let i = 0; i < 30; i++) add(g, box(.8 + r(), .5, .8, stone(C.metal, 1, 1)), (r() - .5) * 9, .3 + Math.floor(i / 10) * .55, (r() - .5) * 4).rotation.y = r() * 3; for (const x of [-3.5, 3.5]) { add(g, cyl(.08, 7, mat('#6b4f35'), 6), x, 3.5, 0); add(g, box(7.4, .1, .1, mat('#6b4f35')), 0, 6.5, 0); add(g, box(7.4, .1, .1, mat('#6b4f35')), 0, 4, 0); } return g; };
const bareShrine = () => { const g = new THREE.Group(); add(g, dais(4), 0, -.9, 0); const s = add(g, server(C.blue), 0, 0, 0); s.scale.setScalar(1.4); setServerPower(s, 1, C.blue); return g; };

// ---- Chapter D -----------------------------------------------------------------------------------------
export const d1 = { bg: C.bg, cam: camPath([0, 5.0, 13], [0, 6.2, 9], [0, 2.4, -8], [0, 2.8, -10]), build(g) { const G = ground(g); const mons = [[-16, fortress, .35], [-9, obelisk, .55], [-3, cloudMon, .5], [3, scaffoldBrick, .6], [9, bareShrine, 1.0], [15, null, 1]]; const roads = [-22, -13, -5, 4, 12, 20].map((x, i) => { const sign = add(g, new THREE.Group(), x * .55, 0, 4 - Math.abs(i - 2.5) * .5); add(sign, cyl(.06, 2.2, brass(), 6), 0, 1.1, 0); add(sign, box(1.6, .5, .08, glow([C.crimson, '#9aa7b8', '#8fb0ff', '#c9a24b', C.blue, C.brass][i], .9)), 0, 2.2, 0); return sign; }); const m = [fortress, obelisk, cloudMon, scaffoldBrick, bareShrine].map((f, i) => { const o = add(g, f(), (i - 2) * 9, 0, -12); o.scale.setScalar(.5); return o; }); const temple = add(g, new THREE.Group(), 25 * .0 + 0, 0, -17); const own = add(g, new THREE.Group(), 22, 0, -14); add(own, dais(3), 0, -.9, 0); for (const [x, z] of [[-1.3, .6], [1.3, .6], [0, -1.2]]) { const s = add(own, server(C.blue), x, 0, z); setServerPower(s, 1, C.blue); } own.scale.setScalar(.7);
  return (t) => { G.friends.forEach((p, i) => { p.userData.head.rotation.x = .1 * Math.sin(t + i); }); m.forEach((o, i) => { o.scale.setScalar(Math.max(.001, .5 * ease(seg(t, .4 + i * .4, 1.2 + i * .4)))); }); own.scale.setScalar(Math.max(.001, .7 * ease(seg(t, 2.6, 3.4)))); }; } };
// Every option gets a pro and a con: the same monument twice, once with a green check, once with a red cross.
const mark = (g, ok, x, y, z, sc = 1.3) => { const m = add(g, new THREE.Group(), x, y, z); const col = ok ? C.green : C.red; if (ok) { const c1 = add(m, box(.18, .7, .1, glow(col, 2.2)), -.22, -.1, 0), c2 = add(m, box(.18, 1.3, .1, glow(col, 2.2)), .2, .15, 0); c1.rotation.z = .8; c2.rotation.z = -.7; } else { const x1 = add(m, box(1.4, .2, .1, glow(col, 2.2)), 0, 0, 0), x2 = add(m, box(1.4, .2, .1, glow(col, 2.2)), 0, 0, 0); x1.rotation.z = .78; x2.rotation.z = -.78; } m.scale.setScalar(.001); m.userData.sc = sc; return m; };
const markUpdate = (m, t, t0) => { const k = ease(seg(t, t0, t0 + .7)); m.scale.setScalar(Math.max(.001, m.userData.sc * k)); m.position.y += .0; m.rotation.y = .15 * Math.sin(t * 2); };
const friendsReact = (G, t, up) => G.friends.forEach((p, i) => { p.userData.head.rotation.x = up ? -.3 * ease(seg(t, .8, 1.6)) : .5 * ease(seg(t, .8, 1.6)); });

// DIY

// ---- Chapter D as questions, not options ----------------------------------------------------------------
// Template "map of roads": the five roads side by side, each ending at its monument with a verdict lantern in front. A question lights only the roads it
// speaks about, one at a time, in the order the narration names them. good = green check, bad = red cross, warn = amber lantern only.
const ROADS = [scaffoldBrick, cloudMon, fortress, obelisk, bareShrine];   // DIY, managed cloud, OpenShift, Talos with Omni, Adaetum
const VERDICT = { good: C.green, bad: C.red, warn: '#ffb03a' };
const roadsQ = verdicts => ({
  bg: C.bg, cam: camPath([0, 5.4, 16.5], [0, 4.4, 13.5], [0, 2.4, -8], [0, 3.0, -9]),
  build(g) {
    templeFloor(g, 60, 50);
    [-13, -8, -3, 3, 8, 13].forEach((x, i) => { const L = placed('lamp', 2.1); if (L) { L.position.set(x, 0, 4 + (i % 2) * 2); g.add(L); } });
    [-15.5, 15.5].forEach(x => { const B = placed('banner', 3.2); if (B) { B.position.set(x, 0, -2); g.add(B); } });
    const lanterns = ROADS.map((mon, i) => {
      const x = (i - 2) * 5.2;
      add(g, box(2.0, .04, 26, mat('#2a2230')), x, .03, -1); for (let k = 0; k < 9; k++) add(g, box(.1, .05, .45, glow(C.brass, .5)), x, .07, 11 - k * 2.4);
      const m = add(g, mon(), x, 0, -12); m.scale.setScalar([.62, .58, .5, .62, 1.15][i]);
      const lamp = add(g, new THREE.Group(), x, 0, -4.5); add(lamp, cyl(.07, 2.2, brass(), 6), 0, 1.1, 0);
      const orb = add(lamp, sph(.42, glow('#3a4250', 0)), 0, 2.6, 0);
      const ring = add(lamp, new THREE.Mesh(new THREE.TorusGeometry(.75, .04, 6, 28), glow('#3a4250', 0)), 0, 2.6, 0); ring.rotation.x = Math.PI / 2;
      return { lamp, orb, ring, mark: null, x };
    });
    verdicts.forEach(([i, v]) => { if (v !== 'warn') { const L = lanterns[i]; L.mark = mark(g, v === 'good', L.x, 5.4, -4.5, 1.3); } });
    const friends = [add(g, ghost(), -.9, 0, 12), add(g, person('#2f5fc4'), 0, 0, 12)]; friends.forEach(p => { p.rotation.y = Math.PI; p.scale.setScalar(.9); });
    return t => {
      friends.forEach((p, i) => { p.userData.head.rotation.x = .1 * Math.sin(t + i); });
      verdicts.forEach(([i, v], n) => {
        const L = lanterns[i], t0 = .7 + n * .8, k = ease(seg(t, t0, t0 + .7)), col = VERDICT[v];
        [L.orb, L.ring].forEach(o => { o.material.color.set(k > 0 ? col : '#3a4250'); o.material.emissive.set(k > 0 ? col : '#000000'); o.material.emissiveIntensity = 2.6 * k; });
        L.orb.scale.setScalar(1 + .35 * Math.sin(Math.PI * clamp(k)) + .06 * Math.sin(t * 3 + i)); L.ring.rotation.z = t * 1.5;
        if (L.mark) markUpdate(L.mark, t, t0 + .3);
      });
    };
  }
});
// 139. what is in the box   140. what it costs   141. who you call   142. how mature
export const q1 = roadsQ([[2, 'good'], [4, 'good'], [0, 'bad'], [3, 'bad']]);
export const q2 = roadsQ([[2, 'warn'], [1, 'warn'], [4, 'good']]);
export const q3 = roadsQ([[2, 'good'], [3, 'good'], [4, 'bad']]);
export const q4 = roadsQ([[2, 'good'], [3, 'good'], [4, 'warn']]);

export const d7 = { bg: C.bg, cam: camPath([0, 4.6, 12], [0, 4.2, 8.6], [0, 2.6, -5], [0, 2.6, -5]), build(g) { const G = ground(g); const own = add(g, new THREE.Group(), 0, 0, -6); own.scale.setScalar(1.15); add(own, dais(5.6), 0, -.9, 0); const srvs = [[-3, .6], [3, .6], [0, -1.8]].map(([x, z]) => { const s = add(own, server(C.blue), x, 0, z); s.scale.setScalar(1.15); setServerPower(s, 1, C.blue); return s; }); const emb = [EMBLEMS.rke2, EMBLEMS.longhorn, EMBLEMS.gitea, EMBLEMS.argocd, EMBLEMS.openbao, EMBLEMS.authentik, EMBLEMS.kubewarden, EMBLEMS.tailscale].map((f, i) => { const e = f(); e.scale.setScalar(.8); own.add(e); return e; }); const chestG = add(g, chest(), 6.0, 0, 3.4); chestG.rotation.y = -.6; const ok = mark(g, true, 0, 7.6, -9, 1.8);
  return (t) => { emb.forEach((e, i) => { const a = i / 8 * Math.PI * 2 + t * .15, k = ease(seg(t, .3 + i * .25, 1.0 + i * .25)); e.position.set(Math.cos(a) * 6.2 * k, 2.4 + .5 * Math.sin(t * 1.5 + i), Math.sin(a) * 2.6 * k); e.scale.setScalar(Math.max(.001, .8 * k)); }); markUpdate(ok, t, 2.4); }; } };
export const d9 = { bg: C.bg, cam: camPath([0, 5.0, 12], [0, 4.6, 9], [0, 2.4, -6], [0, 2.4, -6]), build(g) { const G = ground(g); const own = add(g, new THREE.Group(), 0, 0, -9); own.scale.setScalar(1.3); add(own, dais(5.0), 0, -.9, 0); for (const [x, z] of [[-2.6, .6], [2.6, .6], [0, -1.6]]) { const s = add(own, server(C.blue), x, 0, z); s.scale.setScalar(1.1); setServerPower(s, 1, C.blue); } const lamps = [-3, 0, 3].map(x => add(g, sph(.2, glow(C.gold, 0)), x * 1.0, 2.2, 2.0)); const beam = add(g, new THREE.Mesh(new THREE.CylinderGeometry(.18, 1.4, 12, 16, 1, true), new THREE.MeshBasicMaterial({ color: '#ffe08a', transparent: true, opacity: .0, side: THREE.DoubleSide })), 0, 6, -12);
  return (t) => { const f = ease(seg(t, 1.0, 3.0)); G.friends.forEach((p, i) => { p.position.z = 8.5 - 6 * f; p.position.y = Math.abs(Math.sin(t * 6)) * .05 * (f > 0 && f < 1 ? 1 : 0); }); beam.material.opacity = .22 * ease(seg(t, 2.4, 3.6)); }; } };

// ---- Chapter F: the close ------------------------------------------------------------------------------
// The finale is one continuous animation across two shots: both shots build the same scene and play the same clock
// (the second starts where the first ends), and one camera path runs through both.
function finaleScene(g) {
  g.add(room(34, 22)); add(g, dais(5.6), 0, -.9, 0);
  const srvs = [[-3, 1], [3, 1], [0, -1.8]].map(([x, z]) => { const s = add(g, server(C.blue), x, 0, z); s.scale.setScalar(1.15); setServerPower(s, 1, C.blue); return s; });
  const emb = [EMBLEMS.rocky, EMBLEMS.ansible, EMBLEMS.rke2, EMBLEMS.longhorn, EMBLEMS.certmanager, EMBLEMS.gitea, EMBLEMS.argocd, EMBLEMS.openbao, EMBLEMS.eso, EMBLEMS.authentik, EMBLEMS.kubewarden, EMBLEMS.tailscale, EMBLEMS.ingress, EMBLEMS.cloudflare, EMBLEMS.prometheus, EMBLEMS.kured, EMBLEMS.agent].map(f => { const e = f(); e.scale.setScalar(.7); g.add(e); return e; });
  const sc = add(g, new THREE.Mesh(new THREE.BoxGeometry(1.6, .05, 1.0), glow('#ffe9b8', 1.4)), 0, 4.4, 0);
  const ban = add(g, banner(5.4, 10.8), 0, 5.4, -10.8); const ring = add(g, new THREE.Mesh(new THREE.TorusGeometry(2.6, .08, 8, 48), glow(C.gold, 0)), 0, 6.6, -10.6);
  const friends = [add(g, ghost(), -1.6, 0, 5.2), add(g, person('#2f5fc4'), 0, 0, 5.2)]; friends.forEach(p => p.rotation.y = Math.PI);
  const key = add(g, new THREE.Group(), 0, 2.8, 5.2); add(key, new THREE.Mesh(new THREE.TorusGeometry(.28, .06, 8, 20), glow(C.gold, 2)), 0, .5, 0); add(key, cyl(.06, 1.0, glow(C.gold, 1.6), 8), 0, -.1, 0); add(key, box(.2, .08, .06, glow(C.gold, 1.6)), .12, -.5, 0); key.scale.setScalar(.001);
  const lever = add(g, new THREE.Group(), 5.6, 0, 4.0); add(lever, box(.8, .4, .5, brass()), 0, .2, 0); const arm = add(lever, cyl(.06, 2.0, brass(), 8), 0, .4, 0); arm.geometry.translate(0, 1.0, 0); add(arm, sph(.16, mat(C.crimson)), 0, 2.0, 0); lever.scale.setScalar(.001);
  const cloud = add(g, new THREE.Group(), 5.6, 3.6, 4.0); for (const [x, y, r] of [[-.4, 0, .3], [0, .15, .4], [.45, 0, .3]]) add(cloud, sph(r, mat('#dfe8ff', { emissive: '#8fb0ff', emissiveIntensity: .2 })), x, y, 0); cloud.scale.setScalar(.001);
  const DD = D;
  return (T) => {
    const fade = 1 - ease(seg(T, DD + .6, DD + 3.2));
    emb.forEach((e, i) => { const a = i / 17 * Math.PI * 2 + T * .12; e.position.set(Math.cos(a) * 8, 2.6 + Math.sin(a * 2 + T) * .4, Math.sin(a) * 4 - .5); e.rotation.y = a; e.scale.setScalar(Math.max(.001, .7 * fade)); e.visible = fade > .01; });
    sc.rotation.y = T * .5; sc.position.y = 4.4 + .15 * Math.sin(T * 2); sc.scale.setScalar(Math.max(.001, fade));
    const ks = ease(seg(T, DD + .4, DD + 1.2)); key.scale.setScalar(Math.max(.001, .55 * ks)); key.position.y = 3.4 + .1 * Math.sin(T * 2); key.rotation.y = T * .8;
    const ls = ease(seg(T, DD + .2, DD + .9)); lever.scale.setScalar(Math.max(.001, ls)); cloud.scale.setScalar(Math.max(.001, ls * (1 - .85 * ease(seg(T, DD + 1.4, DD + 3.4)))));
    arm.rotation.z = lerp(-.9, .8, ease(seg(T, DD + 1.4, DD + 3.4)));
    ring.material.emissiveIntensity = 1.4 * ease(seg(T, DD + 1.6, DD + 3.6));
    friends.forEach(p => { p.userData.head.rotation.x = -.2 * ease(seg(T, DD + 1.2, DD + 2.2)); });
    friends[1].rotation.y = lerp(Math.PI, .25, ease(seg(T, DD + 1.4, DD + 3.0))); feelAt(friends[1], T, [[0, 'neutral'], [DD + .8, 'proud'], [DD + 2.4, 'joy']]);
  };
}
// One camera path through both shots: close on the cluster, pull far back (end of shot 1), then swing in to the friends under the banner.
const KF = [[0, [0, 4.4, 10.5], [0, 2.4, 0]], [.5, [0, 12, 21], [0, 2.0, -3]], [1, [0, 3.6, 9.4], [0, 4.4, -6]]];
const finaleCam = w => { const i = w < .5 ? 0 : 1, a = KF[i], b = KF[i + 1], k = ease((w - a[0]) / (b[0] - a[0])); return [V(lerp(a[1][0], b[1][0], k), lerp(a[1][1], b[1][1], k), lerp(a[1][2], b[1][2], k)), V(lerp(a[2][0], b[2][0], k), lerp(a[2][1], b[2][1], k), lerp(a[2][2], b[2][2], k))]; };
export const f1 = { bg: C.bg, cam: u => finaleCam(u * .5), build(g) { const upd = finaleScene(g); return (t) => upd(t); } };
export const f2 = { bg: C.bg, cam: u => finaleCam(.5 + u * .5), build(g) { const upd = finaleScene(g); return (t) => upd(t + D); } };

export default [d1, q1, q2, q3, q4, d7, d9, f1, f2];
