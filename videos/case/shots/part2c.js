// Part 2, chapter C: what happens when... Each scenario is three shots: the event, the usual way, the Adaetum way.
import * as THREE from '../../vendor/three.module.js';
import { feel, feelAt } from '../lib.js';
import { C, D, add, box, cyl, sph, mat, glow, brass, stone, V, arc, ease, seg, lerp, pulse, clamp, rng, room, person, server, setServerPower, lineBetween, dais, flame, altar, chest, coin, smoke, lightningBolt, setLightning, fireOn, animateIcon, ICONS, ICON_COL, balancer, seal, sconce, ghost } from '../lib.js';
import { EMBLEMS } from '../emblems.js';

const camPath = (a, b, la, lb) => u => { const k = ease(u); return [V(lerp(a[0], b[0], k), lerp(a[1], b[1], k), lerp(a[2], b[2], k)), V(lerp(la[0], lb[0], k), lerp(la[1], lb[1], k), lerp(la[2], lb[2], k))]; };
const CAM = camPath([0, 5.8, 12.5], [0, 4.8, 10.2], [0, 1.7, 0], [0, 1.9, 0]);
const NIGHT = '#0a0f1c';
const SX = [[-2.7, 1], [2.7, 1], [0, -1.8]];

// ---- shared stages -------------------------------------------------------------------------------------
function cluster(g, opts = {}) {
  g.add(room(22, 15)); add(g, dais(5.6), 0, -.9, 0);
  const srvs = SX.map(([x, z]) => { const s = add(g, server(C.blue, 0, false), x, 0, z); s.scale.setScalar(1.15); setServerPower(s, 1, C.blue); return s; });   // the apps below are the services, so the machines carry none of their own
  const apps = [0, 1, 2, 3, 4, 3].map((k, i) => { const o = ICONS[k](); o.scale.setScalar(.42); g.add(o); return { o, home: i, k }; });
  const homeOf = (i, srvIdx) => { const [x, z] = SX[srvIdx]; return V(x + (i % 2 ? .6 : -.6), 3.15, z); };
  return { srvs, apps, homeOf, own: [0, 0, 1, 1, 2, 2] };
}
function usual(g) {   // the usual road: a cold night, one tired acolyte, one lone shrine
  g.add(room(16, 11, '#262236'));
  add(g, new THREE.Mesh(new THREE.TorusGeometry(.7, .06, 8, 32), brass()), -3.6, 3.6, -5.35);
  add(g, new THREE.Mesh(new THREE.CircleGeometry(.68, 32), new THREE.MeshStandardMaterial({ color: '#e8dcc0', roughness: .8 })), -3.6, 3.6, -5.32);
  const hh = add(g, box(.05, .32, .02, mat('#222')), -3.6, 3.6, -5.3); hh.geometry.translate(0, .16, 0);
  const mh = add(g, box(.04, .5, .02, mat('#222')), -3.6, 3.6, -5.29); mh.geometry.translate(0, .25, 0);
  mh.rotation.z = 0; hh.rotation.z = -Math.PI / 2;
  const moon = add(g, sph(.5, glow('#cfd8ff', 1.2)), 4.2, 4.2, -5.3);
  const desk = add(g, new THREE.Group(), -2.2, 0, 0.6); add(desk, box(2.4, .12, 1.2, mat('#6b4f35')), 0, .8, 0); for (const sx of [-1, 1]) for (const sz of [-1, 1]) add(desk, box(.1, .8, .1, mat('#4a3624')), sx * 1.1, .4, sz * .5);
  add(desk, box(.8, .05, .55, mat('#4a2c2a')), -.4, .86, 0); add(desk, sph(.08, glow(C.candle, 1.2)), .6, .95, .2); flame(desk, .6, 1.1, .2, .5);
  const who = add(g, person('#4a5878'), -2.2, 0, 1.6); who.rotation.y = Math.PI; who.userData.head.rotation.x = .5; feel(who, 'worried');
  return { who, desk, moon };
}
const bellFn = () => { const g = new THREE.Group(); add(g, cyl(.03, .5, brass(), 6), 0, .3, 0); const b = new THREE.Group(); g.add(b); const body = add(b, new THREE.Mesh(new THREE.SphereGeometry(.4, 14, 10, 0, 6.3, 0, 1.6), new THREE.MeshStandardMaterial({ color: C.brass, metalness: .8, roughness: .3, side: THREE.DoubleSide })), 0, -.4, 0); add(b, sph(.07, brass()), 0, -.85, 0); g.userData.b = b; g.position.y = 3.4; return g; };
const intruder = () => { const p = person('#1a1224'); return p; };
const hoverApps = (apps, t) => apps.forEach((a, i) => animateIcon(a.o, t));
const place = (apps, homeOf, own, t, moves = {}) => apps.forEach((a, i) => { const m = moves[i]; let p = homeOf(i, own[i]); if (m) p = arc(homeOf(i, own[i]), homeOf(i, m.to), m.k(t), m.lift || 1.6); a.o.position.copy(p); a.o.position.y += .08 * Math.sin(t * 2 + i); animateIcon(a.o, t); });

// ---- the lead-in ----------------------------------------------------------------------------------------
export const lead = {
  bg: C.bg, cam: camPath([0, 4.5, 13], [0, 4.2, 9.5], [0, 1.7, 0], [0, 1.8, 0]),
  build(g) { const S = cluster(g); const bolts = [lightningBolt(), lightningBolt()]; add(g, bolts[0], -6, 1, -2); add(g, bolts[1], 6.5, 1, -3);
    const p = [add(g, ghost(), -5, 0, 4), add(g, person('#2f5fc4'), 5, 0, 4)]; p.forEach((x, i) => x.rotation.y = i ? -.3 : .3);
    return (t) => { place(S.apps, S.homeOf, S.own, t); setLightning(bolts[0], t, 1.6, .3); setLightning(bolts[1], t, 3.0, .3); }; }
};

// ---- C1. A machine dies ---------------------------------------------------------------------------------
const c1T = { bg: C.bg, cam: CAM, build(g) { const S = cluster(g); const bolt = add(g, lightningBolt(), SX[0][0], 1.1, SX[0][1]); const fire = fireOn(g, SX[0][0], 2.2, SX[0][1], 1.1); fire.scale.setScalar(.001); smoke(g, SX[0][0], 2.4, SX[0][1], 8, { color: '#8f867c', start: 1.4, size: 2, opacity: .55, rise: 4 });
  return (t) => { place(S.apps, S.homeOf, S.own, t); setLightning(bolt, t, 1.0, .4); const d = t > 1.0; setServerPower(S.srvs[0], d ? 0 : 1, d ? C.red : C.blue); fire.scale.setScalar(Math.max(.001, ease(seg(t, 1.2, 2.2)))); S.apps.forEach((a, i) => { if (S.own[i] === 0 && t > 1.2) a.o.position.x += .05 * Math.sin(t * 40); }); }; } };
const c1U = { bg: NIGHT, cam: camPath([0, 3.6, 8], [0, 3.2, 6.4], [-1.5, 1.8, 0], [-1.5, 1.8, 0]), build(g) { const U = usual(g); const bell = add(g, bellFn(), -.8, 0, .2); const lone = add(g, server(C.red), 3.3, 0, -.8); setServerPower(lone, .4, C.red); smoke(g, 3.3, 2.0, -.8, 6, { color: '#8f867c', size: 1.4, opacity: .45, rise: 3 }); const ring = add(g, new THREE.Mesh(new THREE.TorusGeometry(.5, .03, 8, 24), glow(C.red, 1.6)), -.8, 2.6, .2);
  return (t) => { bell.userData.b.rotation.z = .55 * Math.sin(t * 9); ring.scale.setScalar(1 + .3 * ((t * 4) % 1)); ring.material.emissiveIntensity = 1.6 * (1 - ((t * 4) % 1)); U.who.userData.head.rotation.z = .08 * Math.sin(t * 9); U.who.position.x = -2.2 + .06 * Math.sin(t * 9); }; } };
const c1A = { bg: C.bg, cam: CAM, build(g) { const S = cluster(g); const addr = add(g, new THREE.Mesh(new THREE.TorusGeometry(1.5, .06, 8, 40), glow(C.gold, 1.6)), SX[0][0], .1, SX[0][1]); addr.rotation.x = Math.PI / 2; const p = [add(g, ghost(), -6, 0, 3.5), add(g, person('#2f5fc4'), 6, 0, 3.5)]; p[0].rotation.y = .5; p[1].rotation.y = -.5;
  return (t) => { const d = t > .8; setServerPower(S.srvs[0], d ? 0 : 1, d ? C.red : C.blue); const mv = { }; S.apps.forEach((a, i) => { if (S.own[i] === 0) mv[i] = { to: i % 2 ? 2 : 1, k: tt => seg(tt, 1.4 + i * .2, 2.8 + i * .2) }; }); place(S.apps, S.homeOf, S.own, t, mv); addr.position.x = lerp(SX[0][0], SX[1][0], ease(seg(t, 2.0, 3.4))); addr.position.z = lerp(SX[0][1], SX[1][1], ease(seg(t, 2.0, 3.4))); }; } };

// ---- C2. A disk fails -----------------------------------------------------------------------------------
const drum = () => { const g = new THREE.Group(); add(g, cyl(.5, .8, stone('#5a5478', 1, 1), 14), 0, .4, 0); add(g, new THREE.Mesh(new THREE.TorusGeometry(.5, .04, 6, 20), brass()), 0, .8, 0).rotation.x = Math.PI / 2; add(g, new THREE.Mesh(new THREE.TorusGeometry(.5, .04, 6, 20), brass()), 0, .05, 0).rotation.x = Math.PI / 2; add(g, sph(.12, glow(C.green, 1.6)), 0, .4, .5); return g; };
function drums(g, S) { return S.srvs.map((s, i) => { const d = add(g, drum(), SX[i][0] + 1.5, 0, SX[i][1] + .7); d.scale.setScalar(.7); return d; }); }
const c2T = { bg: C.bg, cam: CAM, build(g) { const S = cluster(g); const dr = drums(g, S); const crack = add(g, new THREE.Mesh(new THREE.TorusGeometry(.55, .05, 6, 6), glow(C.red, 2)), SX[0][0] + 1.5, .5, SX[0][1] + .7); crack.scale.setScalar(.001);
  return (t) => { place(S.apps, S.homeOf, S.own, t); const f = ease(seg(t, 1.2, 1.7)); dr[0].children[3].material.emissive.set(f > .5 ? C.red : C.green); dr[0].children[3].material.color.set(f > .5 ? C.red : C.green); dr[0].rotation.z = .25 * f; dr[0].position.y = -.2 * f; crack.scale.setScalar(Math.max(.001, f)); crack.rotation.x = t * 3; }; } };
const c2U = { bg: NIGHT, cam: camPath([0, 3.4, 8], [0, 3.0, 6.2], [-1, 1.4, 0], [-1, 1.4, 0]), build(g) { const U = usual(g); const ch = add(g, chest(), 1.8, 0, 0); ch.userData.setLevel(0); ch.rotation.y = -.4; smoke(g, 1.8, 1.0, 0, 6, { color: '#a89880', size: 1, opacity: .35, rise: 1.8 });
  const scrap = add(g, box(.5, .02, .7, mat('#cdbf9a')), 1.8, .8, .1); return (t) => { U.who.position.x = lerp(-2.2, .4, ease(seg(t, .4, 2.0))); U.who.position.z = 1.8; U.who.rotation.y = lerp(Math.PI, Math.PI * .75, ease(seg(t, .4, 2.0))); U.who.userData.head.rotation.x = .4 + .15 * Math.sin(t * 2); scrap.rotation.y = t * .3; scrap.position.y = .8 + .05 * Math.sin(t * 2); }; } };
const c2A = { bg: C.bg, cam: CAM, build(g) { const S = cluster(g); const dr = drums(g, S); const copies = S.srvs.map((s, i) => add(g, sph(.14, glow(C.green, 2)), 0, -40, 0));
  return (t) => { place(S.apps, S.homeOf, S.own, t); const f = ease(seg(t, .8, 1.3)), heal = ease(seg(t, 2.6, 3.6)); dr[0].children[3].material.emissive.set(f > .5 && heal < .5 ? C.red : C.green); dr[0].children[3].material.color.set(f > .5 && heal < .5 ? C.red : C.green); dr[0].scale.setScalar(.7 * (1 - .5 * f * (1 - heal))); copies.forEach((c, i) => { const k = ((t * .7 + i / 3) % 1); c.visible = t > 1.2; const a = V(SX[1][0] + 1.5, .6, SX[1][1] + .7), b = V(SX[0][0] + 1.5, .6, SX[0][1] + .7), c2 = V(SX[2][0] + 1.5, .6, SX[2][1] + .7); c.position.lerpVectors(i % 2 ? c2 : a, b, k); c.position.y += Math.sin(Math.PI * k) * .6; }); }; } };

// ---- C3. An app crashes ---------------------------------------------------------------------------------
const c3T = { bg: C.bg, cam: CAM, build(g) { const S = cluster(g); const halo = add(g, sph(.8, new THREE.MeshBasicMaterial({ color: C.red, transparent: true, opacity: 0 })), 0, 0, 0);
  return (t) => { const f = seg(t, 1.0, 1.4), hp = V(SX[1][0] + .6, 3.15, SX[1][1]); const moves = {}; place(S.apps, S.homeOf, S.own, t); const a = S.apps[3]; a.o.position.copy(hp); a.o.position.y += .08 * Math.sin(t * 2); a.o.rotation.z = ease(seg(t, 1.8, 2.6)) * 1.1; a.o.position.y -= ease(seg(t, 2.2, 3.2)) * 1.8 * 0 ; halo.position.copy(a.o.position); halo.material.opacity = .45 * f * (.6 + .4 * Math.sin(t * 20)); a.o.scale.setScalar(.42 * (1 - .4 * ease(seg(t, 2.4, 3.4)))); }; } };
const c3U = { bg: NIGHT, cam: camPath([0, 3.8, 9], [0, 3.4, 7.4], [0, 1.8, -.5], [0, 1.8, -.5]), build(g) { const U = usual(g); U.who.position.set(-3.4, 0, .6); U.who.userData.head.rotation.x = .9; const dead = add(g, server(C.red), 1.5, 0, -1); setServerPower(dead, 0, C.red); smoke(g, 1.5, 2, -1, 4, { color: '#8f867c', size: 1.2, opacity: .3, rise: 2 }); const crowd = Array.from({ length: 5 }, (_, i) => { const p = add(g, person(['#6b5a8a', '#8a5a4a', '#4a6a7a'][i % 3]), 4.2 + (i % 3) * .9, 0, 1.0 + Math.floor(i / 3) * 1.1); p.rotation.y = -Math.PI / 2; p.scale.setScalar(.85); const s = add(g, seal(), 0, 2.4, 0); s.scale.setScalar(.35); p.add(s); return { p, s }; });
  return (t) => { crowd.forEach((c, i) => { c.s.rotation.z = t * 2; c.s.scale.setScalar(.3 + .06 * Math.sin(t * 8 + i)); c.p.position.y = Math.abs(Math.sin(t * 6 + i)) * .08; }); U.who.userData.head.rotation.x = .9 + .05 * Math.sin(t * .8); }; } };
const c3A = { bg: C.bg, cam: CAM, build(g) { const S = cluster(g); const halo = add(g, sph(.8, new THREE.MeshBasicMaterial({ color: C.red, transparent: true, opacity: 0 })), 0, 0, 0); const ping = add(g, sph(.12, glow(C.gold, 2.4)), 0, -40, 0); const phone = add(g, box(.5, .8, .08, glow(C.gold, .8)), 7.5, 1.4, 3); const p = add(g, person('#2f5fc4'), 6.6, 0, 3); p.rotation.y = -.7;
  return (t) => { place(S.apps, S.homeOf, S.own, t); const a = S.apps[3], hp = V(SX[1][0] + .6, 3.15, SX[1][1]); const f = seg(t, .8, 1.2), re = seg(t, 1.8, 2.6); halo.position.copy(hp); halo.material.color.set(re > 0 ? C.green : C.red); halo.material.opacity = .45 * (f - re * f) + .0; a.o.scale.setScalar(.42 * (1 - .5 * f * (1 - re))); a.o.rotation.z = (1 - re) * f * .5; const k = seg(t, 1.2, 2.8); ping.visible = k > 0 && k < 1; ping.position.copy(hp.clone().lerp(V(7.5, 1.9, 3), ease(k))); ping.position.y += Math.sin(Math.PI * k) * 1.2; phone.material.emissiveIntensity = .8 + 2 * pulse(t, 2.7, 3.4); p.userData.head.rotation.x = .3 * ease(seg(t, 2.8, 3.6)); }; } };

export default [];
export const _helpers = { cluster, usual, bellFn, intruder, place, CAM, NIGHT, SX, camPath, drums, drum };
export const part = { lead, c1T, c1U, c1A, c2T, c2U, c2A, c3T, c3U, c3A };
