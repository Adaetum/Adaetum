// Act 3: introduce the answer, a small cluster that installs itself and looks after the apps.
import * as THREE from '../../vendor/three.module.js';
import { feel, feelAt } from '../lib.js';
import { prop, tint, placed } from '../assets.js';
import { rng, flame, dais, chest, templeFloor, stone, brass, C, D, add, box, cyl, sph, mat, glow, V, arc, ease, seg, lerp, pulse, clamp, room, table, person, server, setServerPower, ICONS, ICON_COL, animateIcon, lineBetween, coin, ghost } from '../lib.js';

const camPath = (a, b, la, lb) => u => { const k = ease(u); return [V(lerp(a[0], b[0], k), lerp(a[1], b[1], k), lerp(a[2], b[2], k)), V(lerp(la[0], lb[0], k), lerp(la[1], lb[1], k), lerp(la[2], lb[2], k))]; };
const NODES = [V(-3.2, 0, 1.6), V(3.2, 0, 1.6), V(0, 0, -3.4)];

// 10. The cluster: three tidy machines sharing the five services
export const shot10 = {
  bg: C.bg, cam: u => { const a = lerp(-.5, .5, ease(u)); return [V(Math.sin(a) * 13, 6.2, Math.cos(a) * 13), V(0, 2.2, 0)]; },
  build(g) {
    add(g, dais(7), 0, -.9, 0);
    const srvs = NODES.map(p => { const s = add(g, server(C.blue, 0, false), p.x, 0, p.z); s.scale.setScalar(1.2); return s; });
    const links = [[0, 1], [1, 2], [2, 0]].map(([a, b]) => g.add(lineBetween(NODES[a].clone().setY(.9), NODES[b].clone().setY(.9), C.blue, .05)));
    const pulses = [0, 1, 2].map(() => add(g, sph(.12, glow(C.blue, 2)), 0, 0, 0));
    const icons = ICONS.map((f, i) => { const o = f(); g.add(o); return o; });
    return (t, u) => {
      srvs.forEach((s, i) => setServerPower(s, ease(seg(t, .1 + i * .2, .8 + i * .2)), C.blue));
      pulses.forEach((p, i) => { const a = NODES[i].clone().setY(.9), b = NODES[(i + 1) % 3].clone().setY(.9); p.position.lerpVectors(a, b, ((t * .5 + i / 3) % 1)); });
      icons.forEach((o, i) => {
        const n = NODES[i % 3], k = ease(seg(t, 1 + i * .15, 2 + i * .15));
        const slot = Math.floor(i / 3);
        o.position.set(n.x + (slot ? .6 : -.6) * k, 3.7 + .1 * Math.sin(t * 2 + i), n.z + (slot ? .3 : 0));
        o.scale.setScalar(.5 * k); animateIcon(o, t);
      });
    };
  }
};

// 11. Install: put a disc into a blank machine and it turns itself into a cluster node
export const shot11 = {
  bg: C.bg, cam: camPath([-3, 4.6, 12.5], [0, 4.4, 11], [0, 2.4, 0], [0, 3.2, 0]),
  build(g) {
    add(g, box(18, .2, 14, stone(C.floor, 12, 8, 2, true)), 0, -.1, 0);
    // a blank machine: just the empty altar to begin with. Its book and service pop up when it comes alive, then it links to two others. Nothing falls into it.
    const blank = add(g, server(C.blue, 0), 0, 0, 0); blank.scale.setScalar(1.6);
    const U = blank.userData, parts = [U.tome, U.relic, U.halo, U.halo2, U.finial], base = parts.map(p => p.scale.x);
    const ring = add(g, new THREE.Mesh(new THREE.TorusGeometry(2.2, .05, 8, 64), glow(C.blue, 1.2)), 0, .05, 0); ring.rotation.x = Math.PI / 2;
    const others = [V(-4.5, 0, -2.5), V(4.5, 0, -2.5)].map(p => { const s = add(g, server(C.blue), p.x, 0, p.z); s.scale.setScalar(1.2); setServerPower(s, 1, C.blue); return s; });
    const beams = others.map(s => { const m = lineBetween(V(0, 1.2, 0), s.position.clone().setY(1.2), C.blue, .05); g.add(m); return m; });
    return (t, u) => {
      const pop = ease(seg(t, 1.0, 1.8)), k = pop * (1 + .18 * Math.sin(Math.PI * pop)), power = seg(t, 1.5, 2.6), join = ease(seg(t, 3.0, 4.0));
      parts.forEach((p, i) => p.scale.setScalar(Math.max(.001, base[i] * k)));
      setServerPower(blank, power, C.blue);
      ring.scale.setScalar(1 + .15 * Math.sin(t * 3)); ring.material.emissiveIntensity = 1.8 * power;
      beams.forEach(b => { b.scale.set(1, Math.max(join, .001), 1); b.visible = join > .02; });
    };
  }
};

// 12. The payoff: the friends relax while the apps just run
export const shot12 = {
  bg: C.bg, cam: camPath([0, 3.2, 8], [0, 4.2, 12], [0, 1.9, 0], [0, 2.4, -1]),
  build(g) {
    g.add(room(16, 12, C.wall));
    const sofa = add(g, new THREE.Group(), 0, 0, 1.2);
    const sofaM = prop('sofa');
    if (sofaM) { tint(sofaM, '#8a3340'); sofaM.scale.setScalar(1.9); sofa.add(sofaM); }
    else { add(sofa, box(4.2, .6, 1.6, mat('#7a2a3a')), 0, .3, 0); add(sofa, box(4.2, 1.1, .4, mat('#7a2a3a')), 0, .9, -.7); }
    const p1 = add(g, ghost(), -.9, .1, 1.3), p2 = add(g, person('#2f5fc4'), 0, .1, 1.3);
    p1.rotation.y = Math.PI; p2.rotation.y = .1;
    p2.position.set(0, .62, 1.4);   // sunk into the seat cushion: sitting back on the couch
    // a striped tub of popcorn in the lap, and one kernel at a time thrown to the mouth
    const stripes = (() => { const c = document.createElement('canvas'); c.width = 128; c.height = 64; const x = c.getContext('2d'); for (let i = 0; i < 8; i++) { x.fillStyle = i % 2 ? '#f4efe4' : '#c4302b'; x.fillRect(i * 16, 0, 16, 64); } const t = new THREE.CanvasTexture(c); t.colorSpace = THREE.SRGBColorSpace; return t; })();
    const tub = add(g, new THREE.Group(), 0, 1.05, 2.0);
    add(tub, new THREE.Mesh(new THREE.CylinderGeometry(.36, .27, .55, 16, 1, true), new THREE.MeshStandardMaterial({ map: stripes, side: THREE.DoubleSide, roughness: .7 })), 0, 0, 0);
    add(tub, cyl(.27, .04, mat('#c4302b'), 16), 0, -.27, 0); add(tub, new THREE.Mesh(new THREE.TorusGeometry(.36, .025, 6, 20), mat('#f4efe4')), 0, .28, 0).rotation.x = Math.PI / 2;
    const pr = rng(8); const corn = ['#f6e7b4', '#f1d98a', '#fff3cf'];
    for (let i = 0; i < 22; i++) { const a = pr() * 6.28, r = .3 * Math.sqrt(pr()); add(tub, sph(.085 + pr() * .05, mat(corn[i % 3])), Math.cos(a) * r, .3 + pr() * .16 * (1 - r), Math.sin(a) * r); }
    const kernel = add(g, sph(.07, mat('#fff3cf')), 0, -40, 0);
    // the five altars from the first scene are back, each now a machine of your own: an altar, its open book, and its service floating above it
    const machines = [-6, -3, 0, 3, 6].map((x, i) => { const s = add(g, server(C.blue, i), x, 0, -3.2); s.scale.setScalar(.95); setServerPower(s, 1, C.blue); return s; });
    const w = add(g, chest(), 5.2, 0, 2.4); w.rotation.y = -.5;
    const lamp = add(g, new THREE.PointLight('#ffb870', 6, 14, 2), -5, 3.5, 2);
    return (t, u) => {
      p1.userData.head.rotation.x = .05 * Math.sin(t); p2.userData.head.rotation.x = .05 * Math.sin(t + 1); feelAt(p2, t, [[0, 'relief'], [1.2, 'relaxed']]);
      // eating: every 1.6 s the right hand goes from the tub to the mouth, the mouth opens as the kernel arrives, then chews
      const c = (t % 1.6) / 1.6, toMouth = pulse(c, .25, .7), m = p2.userData.params;
      m.arms = [m.arms[0], m.arms[1], lerp(1.0, 2.15, toMouth), lerp(.41, .2, toMouth)];
      m.o = .55 * pulse(c, .5, .72); m.smile = .55 + .25 * Math.sin(t * 11) * (c > .72 ? 1 : 0); m.eye = .85;
      const kp = seg(c, .3, .62); kernel.visible = kp > 0 && kp < 1; kernel.position.copy(arc(V(0, 1.4, 2.0), V(0, 2.4, 1.72), kp, .45));
    };
  }
};

// ---- Shots 10b-10e: how the cluster looks after itself, and what it looks like ----
function clusterBase(g) {
  add(g, dais(7), 0, -.9, 0);
  const srvs = NODES.map(p => { const s = add(g, server(C.blue, 0, false), p.x, 0, p.z); s.scale.setScalar(1.2); setServerPower(s, 1, C.blue); return s; });
  [[0, 1], [1, 2], [2, 0]].forEach(([a, b]) => g.add(lineBetween(NODES[a].clone().setY(.9), NODES[b].clone().setY(.9), C.blue, .05)));
  const icons = ICONS.map(f => { const o = f(); g.add(o); return o; });
  const home = i => { const n = NODES[i % 3], slot = Math.floor(i / 3); return V(n.x + (slot ? .6 : -.6), 3.7, n.z + (slot ? .3 : 0)); };
  return { srvs, icons, home };
}
const orbitCam = (r, y, a0, a1, ly = 1.4) => u => { const a = lerp(a0, a1, ease(u)); return [V(Math.sin(a) * r, y, Math.cos(a) * r), V(0, ly, 0)]; };
const AMBER = '#ffb347';

// 10b. Patching: one machine at a time. Its apps step aside, it updates, it returns.
export const shot10b = {
  bg: C.bg, cam: orbitCam(10, 5.6, -.45, .15, 2.5),
  build(g) {
    const { srvs, icons, home } = clusterBase(g);
    const gear = add(g, new THREE.Mesh(new THREE.TorusGeometry(.7, .09, 8, 6), glow(AMBER, 1.6)), 0, 0, 0); gear.rotation.x = Math.PI / 2;
    return (t) => {
      const away = [0, 1, 2].map(k => { const p = seg(t, .3 + k * 1.4, 1.7 + k * 1.4); return ease(seg(p, 0, .25)) - ease(seg(p, .75, 1)); });
      const patching = [0, 1, 2].map(k => { const p = seg(t, .3 + k * 1.4, 1.7 + k * 1.4); return p > .2 && p < .8; });
      srvs.forEach((s, k) => { setServerPower(s, 1 - .6 * away[k], patching[k] ? AMBER : C.blue); });
      icons.forEach((o, i) => {
        const k = i % 3, alt = home(i + 1 + (i >= 3 ? 0 : 0)), h = home(i);
        const to = V(NODES[(k + 1) % 3].x + (i >= 3 ? .6 : -.6), 3.7, NODES[(k + 1) % 3].z + .9);
        o.position.copy(h.clone().lerp(to, away[k])); o.position.y += .1 * Math.sin(t * 2 + i); o.scale.setScalar(.5); animateIcon(o, t);
      });
      const act = patching.findIndex(Boolean);
      gear.visible = act >= 0;
      if (act >= 0) { gear.position.set(NODES[act].x, 3.3, NODES[act].z); gear.rotation.z = t * 5; }
    };
  }
};

// 10c. Healing: a failed app restarts, and when a machine dies its apps move to the others.
export const shot10c = {
  bg: C.bg, cam: orbitCam(10.5, 5.4, .2, -.3, 2.5),
  build(g) {
    const { srvs, icons, home } = clusterBase(g);
    const halo = add(g, sph(.8, new THREE.MeshBasicMaterial({ color: C.red, transparent: true, opacity: 0 })), 0, 0, 0);
    return (t) => {
      const fail = seg(t, .4, .7), restart = seg(t, 1.5, 2.1), clear = seg(t, 2.1, 2.5);
      const target = icons[1], hp = home(1);
      halo.position.copy(hp).setY(3.8); halo.material.color.set(restart > 0 ? C.green : C.red);
      halo.material.opacity = .45 * (fail - clear) * (restart > 0 ? 1 : .6 + .4 * Math.sin(t * 20));
      const dead = ease(seg(t, 2.9, 3.3));
      setServerPower(srvs[1], 1 - dead, dead > .4 ? C.red : C.blue);
      icons.forEach((o, i) => {
        const k = i % 3; let p = home(i);
        if (k === 1) { const dest = V(NODES[i < 3 ? 0 : 2].x + (i < 3 ? -.6 : .6), 3.7, NODES[i < 3 ? 0 : 2].z + 1.0); p = arc(p, dest, seg(t, 3.3 + (i < 3 ? 0 : .3), 4.5 + (i < 3 ? 0 : .3)), 1.2); }
        o.position.copy(p); if (k !== 1 || t < 3.3) o.position.y += .1 * Math.sin(t * 2 + i);
        o.scale.setScalar(.5 * (i === 1 ? 1 - .5 * (fail - restart) : 1)); animateIcon(o, t);
      });
    };
  }
};

// 10d. Staying on plan: Git holds the blueprint, and anything that drifts is pulled back.
export const shot10d = {
  bg: C.bg, cam: orbitCam(12, 6.0, -.2, .25, 3.2),
  build(g) {
    const { srvs, icons, home } = clusterBase(g);
    const plan = add(g, new THREE.Group(), 0, 7.4, -1);
    add(plan, box(4.6, 2.6, .12, mat('#0f2a4a', { emissive: C.blue, emissiveIntensity: .25 })), 0, 0, 0);
    for (let r = 0; r < 4; r++) for (let c = 0; c < 3; c++) add(plan, box(1.0, .28, .04, glow(c === 2 && r === 1 ? C.green : C.blue, .6)), -1.4 + c * 1.4, .8 - r * .55, .08);
    const ring = add(g, new THREE.Mesh(new THREE.TorusGeometry(.7, .05, 8, 32), glow(C.red, 1.6)), 0, 0, 0);
    const beam = add(g, sph(.12, glow(C.blue, 2)), 0, 0, 0);
    return (t) => {
      const drift = ease(seg(t, .9, 1.9)), back = ease(seg(t, 2.8, 3.8));
      icons.forEach((o, i) => {
        let p = home(i);
        if (i === 3) { const out = V(p.x + 2.6, 4.7, p.z + 1.2); p = p.clone().lerp(out, drift * (1 - back)); }
        o.position.copy(p); o.position.y += .08 * Math.sin(t * 2 + i); o.scale.setScalar(.5); animateIcon(o, t);
      });
      const away = home(3).clone().lerp(V(home(3).x + 2.6, 4.7, home(3).z + 1.2), drift * (1 - back));
      ring.position.copy(away); ring.visible = drift > .05 && back < .98;
      ring.material.color.set(back > .1 ? C.green : C.red); ring.material.emissive.set(back > .1 ? C.green : C.red);
      ring.rotation.x = Math.PI / 2; ring.scale.setScalar(1 + .15 * Math.sin(t * 6));
      const k = ((t * .8) % 1); beam.position.lerpVectors(V(0, 6.4, -1), away, seg(t, 2.2, 3.6)); beam.visible = t > 2.2 && back < .98;
      plan.rotation.y = .1 * Math.sin(t);
    };
  }
};

// 10e. What it looks like: one screen shows what is healthy and what needs attention.
export const shot10e = {
  bg: C.bg, cam: u => { const k = ease(u); return [V(0, lerp(3.2, 2.6, k), lerp(11, 7.6, k)), V(0, 2.4, 0)]; },
  build(g) {
    templeFloor(g);
    const panel = add(g, new THREE.Group(), 0, 2.6, 0);
    add(panel, box(7.4, 4.4, .16, mat('#0e1b29')), 0, 0, 0);
    add(panel, box(7.6, 4.6, .08, mat('#2a4260')), 0, 0, -.1);
    // carved frame: brass border, a lapis arch, finial, and candle posts so the screen reads as a scrying mirror
    for (const [w, h, x, y] of [[7.9, .28, 0, 2.38], [7.9, .28, 0, -2.38], [.28, 5.0, -3.95, 0], [.28, 5.0, 3.95, 0]]) add(panel, box(w, h, .3, brass()), x, y, .1);
    const arch = add(panel, new THREE.Mesh(new THREE.TorusGeometry(3.95, .16, 8, 48, Math.PI), mat(C.lapis, { metalness: .4 })), 0, 2.38, .1);
    add(panel, sph(.22, glow(C.gold, 2)), 0, 6.45, .1);
    for (const sx of [-1, 1]) { add(panel, cyl(.2, 6, stone(C.marble, 1, 2), 12), sx * 4.5, .1, .1); add(panel, cyl(.3, .18, brass(), 12), sx * 4.5, 3.1, .1); flame(panel, sx * 4.5, 3.5, .1, 1.1); }
    const tiles = [];
    for (let r = 0; r < 2; r++) for (let c = 0; c < 3; c++) {
      const bad = (r === 1 && c === 2);
      tiles.push(add(panel, box(1.2, .8, .06, glow(bad ? AMBER : C.green, .5)), -2.4 + c * 1.35, 1.5 - r * 1.0, .12));
    }
    const bars = []; for (let i = 0; i < 14; i++) bars.push(add(panel, box(.2, 1, .06, glow(C.blue, .7)), -2.9 + i * .28 + 3.0 * 0, -.9, .12));
    bars.forEach((b, i) => { b.position.x = -3.0 + i * .28; });
    const rows = []; for (let i = 0; i < 5; i++) {
      const bad = i === 3;
      add(panel, sph(.1, glow(bad ? AMBER : C.green, 1.2)), 1.5, 1.7 - i * .5, .12);
      rows.push(add(panel, box(1.9 - (i % 3) * .3, .14, .04, mat('#c9d6e4')), 2.5, 1.7 - i * .5, .12));
    }
    // the oracle you appoint: a crystal orb on a tripod with a watching eye, who mends the amber tile
    const oracle = add(g, new THREE.Group(), 4.8, 0, 1.2);
    const orbM = placed('orb', 2.7);
    if (orbM) oracle.add(orbM); else {
    for (let i = 0; i < 3; i++) { const a = i * Math.PI * 2 / 3; const leg = add(oracle, cyl(.05, 1.6, brass(), 6), Math.cos(a) * .35, .75, Math.sin(a) * .35); leg.rotation.z = -Math.cos(a) * .22; leg.rotation.x = Math.sin(a) * .22; }
    add(oracle, new THREE.Mesh(new THREE.TorusGeometry(.5, .05, 8, 24), brass()), 0, 1.55, 0).rotation.x = Math.PI / 2;
    add(oracle, new THREE.Mesh(new THREE.SphereGeometry(.7, 24, 16), new THREE.MeshStandardMaterial({ color: '#b8a6ff', transparent: true, opacity: .35, roughness: .1, metalness: .2 })), 0, 2.2, 0);
    }
    const oeye = add(oracle, new THREE.Group(), 0, 2.2, 0); add(oeye, sph(.32, mat('#f4eee0')), 0, 0, 0); add(oeye, sph(.18, glow(C.ai, 1.8)), 0, 0, .22); add(oeye, sph(.08, mat('#05030a')), 0, 0, .35);
    if (orbM) { oeye.position.y = 1.95; oeye.scale.setScalar(.5); }   // the eye sits inside the crystal
    const obeam = add(g, sph(.1, glow(C.ai, 2.4)), 0, -40, 0);
    const mini = add(g, new THREE.Group(), 0, 0, -3); mini.scale.setScalar(.45);
    NODES.forEach(p => { const s = add(mini, server(C.blue), p.x, 0, p.z); setServerPower(s, 1, C.blue); });
    return (t, u) => {
      const open = ease(seg(t, .1, 1.2));
      panel.scale.set(open, open, 1);
      const fix = ease(seg(t, 3.0, 3.6)); tiles[5].material.color.set(fix > .5 ? C.green : AMBER); tiles[5].material.emissive.set(fix > .5 ? C.green : AMBER);
      tiles.forEach((m, i) => { m.material.emissiveIntensity = (i === 5 && fix < .5 ? .5 + .5 * Math.sin(t * 5) : .5) * open; });
      oeye.rotation.y = -.5 + .15 * Math.sin(t); const bk = seg(t, 1.8, 3.0); obeam.visible = bk > 0 && bk < 1; obeam.position.copy(V(4.8, 2.2, 1.2).lerp(V(.3, 3.1, .3), ease(bk))); obeam.position.y += Math.sin(Math.PI * bk) * .8;
      bars.forEach((b, i) => { const h = .2 + .9 * (.5 + .5 * Math.sin(t * 1.6 + i * .6)) * ease(seg(t, .6 + i * .05, 1.4 + i * .05)); b.scale.y = h; b.position.y = -1.5 + h / 2; });
      mini.rotation.y = t * .2;
    };
  }
};

export default [shot10, shot10b, shot10c, shot10d, shot10e, shot11, shot12];
