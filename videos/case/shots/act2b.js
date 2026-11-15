// Act 2b: failover by hand, testing, drift, and what high availability means (single site, region, global).
import * as THREE from '../../vendor/three.module.js';
import { feel, feelAt } from '../lib.js';
import { coin, balancer, lightningBolt, setLightning, fireOn, flame, sconce, stone, brass, templeFloor, room, C, D, add, box, cyl, sph, mat, glow, V, arc, ease, seg, lerp, pulse, clamp, rng, person, server, setServerPower, lineBetween, ghost } from '../lib.js';

const camPath = (a, b, la, lb) => u => { const k = ease(u); return [V(lerp(a[0], b[0], k), lerp(a[1], b[1], k), lerp(a[2], b[2], k)), V(lerp(la[0], lb[0], k), lerp(la[1], lb[1], k), lerp(la[2], lb[2], k))]; };
const floor = g => g.add(room(26, 18));
const orbs = (g, n, color, r = .16) => Array.from({ length: n }, () => { const o = add(g, new THREE.Mesh(new THREE.CapsuleGeometry(r * .5, r * 2.2, 4, 8), glow(color, 1.4)), 0, -40, 0); o.rotation.z = Math.PI / 2; return o; });
const hub = (g, x, y, z) => add(g, balancer(), x, y - .3, z);
const WARM_DEAD = '#ff5a5f';

// 8a. By hand, staying up is its own project: more machines, a balancer, health checks, replicated storage.
export const failoverByHand = {
  bg: C.bg, cam: camPath([-1.5, 5.0, 9.6], [0, 6.2, 9.5], [1.2, 1.6, -1.2], [0, .8, -.5]),
  build(g) {
    floor(g);
    const X = [-3.2, 0, 3.2];
    const srvs = X.map(x => { const s = add(g, server(C.blue), x, 0, 0); s.scale.set(0, 0, 0); return s; });
    const lb = hub(g, 0, 1.0, 4); lb.scale.setScalar(0);
    const drums = X.map(x => { const d = add(g, cyl(.9, 1.1, mat('#3c566f')), x, .55, -3.4); d.scale.set(0, 0, 0); return d; });
    const links = [];
    X.forEach((x, i) => { const a = lineBetween(V(0, 1, 4), V(x, .9, 0), C.blue, .04); const b = lineBetween(V(x, .9, 0), V(x, .9, -3.4), C.green, .04); g.add(a, b); links.push(a, b); a.scale.set(1, .001, 1); b.scale.set(1, .001, 1); });
    const rep = [add(g, sph(.14, glow(C.green, 2)), 0, 0, 0), add(g, sph(.14, glow(C.green, 2)), 0, 0, 0)];
    const blips = X.map(() => add(g, sph(.1, glow('#aef3c9', 2)), 0, 0, 0));
    const p1 = add(g, ghost(), -5, 0, 2.6), p2 = add(g, person('#2f5fc4'), 5, 0, 2.6); p1.rotation.y = Math.PI * .85; p2.rotation.y = -1.33;
    return (t) => {
      X.forEach((x, i) => { const k = ease(seg(t, .2 + i * .35, .8 + i * .35)); srvs[i].scale.setScalar(Math.max(.001, 1.2 * k)); setServerPower(srvs[i], seg(t, 1.2, 1.8), C.blue); });
      lb.scale.setScalar(Math.max(.001, ease(seg(t, 1.6, 2.2)))); lb.rotation.y = t * 1.5;
      const l1 = ease(seg(t, 2.0, 2.8)); links.forEach((m, i) => { if (i % 2 === 0) m.scale.y = Math.max(.001, l1); });
      drums.forEach((d, i) => d.scale.setScalar(Math.max(.001, ease(seg(t, 2.6 + i * .25, 3.1 + i * .25)))));
      const l2 = ease(seg(t, 3.2, 3.9)); links.forEach((m, i) => { if (i % 2 === 1) m.scale.y = Math.max(.001, l2); });
      const rp = seg(t, 3.8, 5.1);
      rep.forEach((r, i) => { r.visible = rp > 0; r.position.set(lerp(X[i], X[i + 1], (t * 1.2 + i * .5) % 1), 1.0, -3.4); });
      blips.forEach((b, i) => { const k = (t * .9 + i * .33) % 1; b.visible = t > 4; b.position.set(lerp(0, X[i], k), lerp(1, .9, k), lerp(4, 0, k)); });
      p1.userData.head.rotation.x = .12 * Math.sin(t * 2); p2.userData.head.rotation.x = .12 * Math.sin(t * 2 + 1); feelAt(p2, t, [[0, 'worried'], [2.5, 'thinking'], [4.2, 'relief']]);
    };
  }
};

// 8b. Failover that was never tested does not work: the balancer keeps sending traffic to a dead machine.
export const failoverTest = {
  bg: C.bgDanger, cam: camPath([0, 6.2, 9.5], [2, 5.6, 10.5], [0, .8, -.5], [0, .8, 1]),
  build(g) {
    floor(g);
    const X = [-3.2, 0, 3.2];
    const srvs = X.map(x => { const s = add(g, server(C.blue), x, 0, 0); s.scale.setScalar(1.2); setServerPower(s, 1, C.blue); return s; });
    const lb = hub(g, 0, 1.0, 4);
    X.forEach(x => g.add(lineBetween(V(0, 1, 4), V(x, .9, 0), C.blue, .04)));
    const bolt = add(g, lightningBolt(), 0, 1.0, 0); const fire = fireOn(g, 0, 1.9, 0, 1.9); fire.scale.setScalar(.001);
    const os = orbs(g, 16, C.blue, .17);
    return (t) => {
      const strike = 2.0, dead = t > strike;
      setLightning(bolt, t, 1.9, .4); fire.scale.setScalar(Math.max(.001, ease(seg(t, 2.0, 2.8))));
      setServerPower(srvs[1], dead ? 0 : 1, dead ? WARM_DEAD : C.blue);
      let pile = 0;
      os.forEach((o, n) => {
        const k = n % 3, sp = .6 + n * .3, ta = sp + 1.7, p = (t - sp) / 1.7;
        o.visible = p > 0;
        if (p <= 0) return;
        if (p < 1) { o.material.color.set(C.blue); o.material.emissive.set(C.blue);
          o.position.copy(p < .45 ? V(0, lerp(1.2, 1.0, p / .45), lerp(11, 4, p / .45)) : V(lerp(0, X[k], (p - .45) / .55), 1.0, lerp(4, 0, (p - .45) / .55))); }
        else if (k === 1 && ta > strike) { o.material.color.set(C.red); o.material.emissive.set(C.red); o.position.set(((pile % 3) - 1) * .3, 2.2 + Math.floor(pile / 3) * .3, .3); pile++; }
        else o.visible = false;
      });
    };
  }
};

// 8c. Drift: quick fixes pile up as patches, bolted-on parts, sconces and scrawled runes, while the plan outline stays put.
export const drift = {
  bg: C.bgDanger, cam: camPath([2, 5.6, 10.5], [2.5, 3.6, 10], [0, .8, 1], [0, 1.2, 0]),
  build(g) {
    floor(g);
    const r = rng(23), N = 5, X = Array.from({ length: N }, (_, i) => -6 + i * 3);
    X.forEach(x => { const e = new THREE.LineSegments(new THREE.EdgesGeometry(new THREE.BoxGeometry(2.5, 3.9, 2.4)), new THREE.LineBasicMaterial({ color: C.blue })); e.position.set(x, 1.95, 0); g.add(e); });
    const cols = ['#8b5a3c', '#6b7a8f', '#7a3f3f', '#9a8a4a', '#4a6a5a'];
    const machines = X.map((x, i) => {
      const s = add(g, server(C.blue), x, 0, 0); setServerPower(s, 1, C.blue);
      const mods = [];
      // 1. riveted patch plate over the glyph panel or side
      const patch = new THREE.Group(); patch.position.set((r() - .5) * .8, .8 + r() * .5, .95); patch.rotation.z = (r() - .5) * .5;
      add(patch, box(.8, .55, .06, mat(cols[i], { metalness: .5 })), 0, 0, 0); for (const [dx, dy] of [[-.32, .2], [.32, .2], [-.32, -.2], [.32, -.2]]) add(patch, sph(.045, brass()), dx, dy, .04);
      s.add(patch); mods.push([patch, .5 + i * .5]);
      // 2. bolted-on extra module with an antenna
      const mod = new THREE.Group(); mod.position.set(i % 2 ? 1.45 : -1.45, .8 + r() * .4, 0);
      add(mod, box(.55, .7, .6, mat(cols[(i + 2) % 5], { metalness: .4 })), 0, 0, 0); add(mod, cyl(.035, .8, brass(), 6), 0, .65, 0); add(mod, sph(.08, glow(C.red, 1.6)), 0, 1.07, 0);
      s.add(mod); mods.push([mod, 1.1 + i * .5]);
      // 3. a sconce with a candle somebody stuck on the side
      const sc = sconce(); sc.scale.setScalar(1.5); sc.position.set(i % 2 ? -1.2 : 1.2, 1.2, .1); sc.rotation.y = i % 2 ? -Math.PI / 2 : Math.PI / 2; s.add(sc); mods.push([sc, 1.7 + i * .5]);
      // 4. crooked cable loop and plug
      const cab = new THREE.Mesh(new THREE.TorusGeometry(.32, .035, 6, 20, Math.PI * 1.6), mat(['#c44', '#48c', '#ca4', '#4c8', '#a6c'][i])); cab.position.set((r() - .5) * .8, .35, 1.05); cab.rotation.x = Math.PI / 2 - .5; s.add(cab); mods.push([cab, 2.1 + i * .5]);
      // 5. scrawled red runes on the stone
      const rune = new THREE.Group(); rune.position.set(.2, 1.3, .92);
      for (let k = 0; k < 4; k++) { const b2 = add(rune, box(.2, .035, .02, glow(C.red, 1.4)), 0, -k * .09, 0); b2.rotation.z = (r() - .5) * 1.2; }
      s.add(rune); mods.push([rune, 2.5 + i * .5]);
      const ring = add(g, new THREE.Mesh(new THREE.TorusGeometry(1.5, .05, 8, 40), glow(C.red, 1.4)), x, .05, 0); ring.rotation.x = Math.PI / 2; ring.scale.setScalar(.001);
      const flames = [sc.userData.flame];
      // the altar itself drifts: its own copy of every stone and brass material, tinted away from the plan's colour as the fixes pile up
      const tints = []; s.children[0].traverse(n => { if (n.isMesh && n.material && !(n.material.emissive && n.material.emissive.getHex() !== 0)) { n.material = n.material.clone(); tints.push([n.material, n.material.color.clone()]); } });
      return { s, mods, ring, tints, hue: new THREE.Color(['#d98a3a', '#b03a3a', '#6a5acd', '#c9a82e', '#c4547a'][i]), tilt: (r() - .5) * .14, col: ['#ffb347', C.red, '#a78bfa', '#ffd166', '#ff6b9a'][i] };
    });
    machines.forEach(m => m.mods.forEach(([o]) => o.scale.setScalar(.001)));
    return (t) => {
      machines.forEach((m, i) => {
        m.mods.forEach(([o, at]) => { const k = ease(seg(t, at * .7, at * .7 + .5)); o.scale.setScalar(Math.max(.001, k)); });
        const d = ease(seg(t, .6 + i * .5, 3.4 + i * .3)); m.s.rotation.z = m.tilt * d; setServerPower(m.s, 1, d > .5 ? m.col : C.blue);
        m.ring.scale.setScalar(Math.max(.001, d * (1 + .08 * Math.sin(t * 5)))); m.ring.visible = d > .05;
        m.tints.forEach(([mt, c0]) => mt.color.copy(c0).lerp(m.hue, .78 * d));   // the altar's colour wanders off the original
      });
    };
  }
};

// HA-A. One of anything is a single point of failure.
export const singlePoint = {
  bg: C.bg, cam: camPath([0, 3.4, 9], [0, 4.2, 10.5], [0, 6, 0], [0, 1, 0]),
  build(g) {
    floor(g);
    const s = add(g, server(C.blue), 0, 0, 0); s.scale.setScalar(1.6); setServerPower(s, 1, C.blue);
    const spot = add(g, new THREE.Mesh(new THREE.TorusGeometry(2, .06, 8, 48), glow(C.blue, 1)), 0, .05, 0); spot.rotation.x = Math.PI / 2;
    const os = orbs(g, 14, C.blue, .16);
    return (t) => {
      const dead = t > 2.3; setServerPower(s, dead ? 0 : 1, dead ? WARM_DEAD : C.blue);
      spot.material.color.set(dead ? C.red : C.blue); spot.material.emissive.set(dead ? C.red : C.blue);
      let pile = 0;
      os.forEach((o, n) => {
        const sp = .2 + n * .33, p = (t - sp) / 2.2; o.visible = p > 0; if (p <= 0) return;
        const x = lerp(-9, 9, clamp(p));
        if (dead && x > -2.2 && (t - sp) > 0) { const stop = -2.4 - pile * .0; o.position.set(-2.4 - pile * .38, 1.0, 0); pile++; o.material.color.set(C.red); o.material.emissive.set(C.red); }
        else { o.position.set(x, 1.0 + Math.sin(p * 9) * .1, 0); o.material.color.set(C.blue); o.material.emissive.set(C.blue); }
        if (p >= 1 && !dead) o.visible = false;
      });
    };
  }
};

// A chapel diorama: three stone walls, an open timber roof and windows. The front is open so the machines inside show.
function houseOutline(w = 4.2, d = 2.4, h = 1.8, color = C.blue) {
  const g = new THREE.Group(), trim = new THREE.MeshStandardMaterial({ color, emissive: color, emissiveIntensity: .55, roughness: .5, metalness: .3 });
  add(g, box(w + .4, .12, d + .4, stone('#6a5a48', 2, 1)), 0, .06, 0);
  add(g, box(w, h, .16, stone(C.wall, 2, 1)), 0, h / 2 + .1, -d / 2); add(g, box(.16, h, d, stone(C.wall, 1, 1)), -w / 2, h / 2 + .1, 0); add(g, box(.16, h, d, stone(C.wall, 1, 1)), w / 2, h / 2 + .1, 0);
  for (let k = 0; k < 3; k++) { add(g, box(.34, .7, .05, glow(C.candle, .9)), (k - 1) * w * .28, h * .6 + .1, -d / 2 + .1); add(g, new THREE.Mesh(new THREE.CircleGeometry(.17, 12, 0, Math.PI), glow(C.candle, .9)), (k - 1) * w * .28, h * .6 + .45, -d / 2 + .1); }
  for (const sx of [-1, 1]) { add(g, cyl(.1, h, brass(), 8), sx * w / 2, h / 2 + .1, d / 2); add(g, sph(.12, brass()), sx * w / 2, h + .18, d / 2); }
  const rise = Math.min(1.2, w * .28), n = 5, len = Math.hypot(w / 2, rise), ang = Math.atan2(rise, w / 2);
  for (let i = 0; i < n; i++) { const z = -d / 2 + i * d / (n - 1); for (const sx of [-1, 1]) { const b2 = add(g, box(len, .09, .09, trim), sx * w / 4, h + .1 + rise / 2, z); b2.rotation.z = -sx * ang; } }
  add(g, box(.1, .1, d, trim), 0, h + .1 + rise, 0);
  // (no steeple: nothing stands in front of the machines inside)
  g.userData.mat = trim; return g;
}
const roofFill = (g, color) => { g.userData.mat.color.set(color); g.userData.mat.emissive.set(color); };

// HA-B. Local high availability: two machines at your place cover for each other.
export const highAvailability = {
  bg: C.bg, cam: camPath([0, 4.2, 10.5], [2, 7, 10], [0, 1.4, 0], [1, 1.4, 0]),
  build(g) {
    floor(g);
    const house = add(g, houseOutline(4.8, 4.4, 3.3), 4, 0, 0);
    const Z = [-1.1, 1.1];
    const srvs = Z.map(z => { const s = add(g, server(C.green), 4, 0, z); s.scale.setScalar(.72); s.rotation.y = -Math.PI / 2; setServerPower(s, 1, C.green); return s; });
    const lb = hub(g, -2, 1.0, 0); Z.forEach(z => g.add(lineBetween(V(-2, 1, 0), V(3.2, .9, z), C.blue, .04)));
    const os = orbs(g, 16, C.blue, .16);
    return (t) => {
      const strike = 2.2, dead = t > strike; setServerPower(srvs[0], dead ? 0 : 1, dead ? WARM_DEAD : C.green);
      os.forEach((o, n) => {
        const sp = .1 + n * .28, p = (t - sp) / 1.7; o.visible = p > 0 && p < 1; if (!o.visible) return;
        const alive = (sp + .6) > strike ? [1] : [0, 1], k = alive[n % alive.length];
        o.position.copy(p < .4 ? V(lerp(-9, -2, p / .4), 1.0, 0) : V(lerp(-2, 3.4, (p - .4) / .6), 1.0, lerp(0, Z[k], (p - .4) / .6)));
      });
    };
  }
};

// HA-C. Regional high availability: machines at a friend's place too, so one home losing power does not stop you.
export const regional = {
  bg: C.bg, cam: camPath([2, 7, 10], [2, 8, 12.5], [1, 1.2, 0], [0, 1.2, 0]),
  build(g) {
    floor(g);
    const SX = [[-6.5, -3.9], [3.9, 6.5]], homeX = [-5.2, 5.2];
    const houses = homeX.map(x => add(g, houseOutline(6.0, 3.4, 3.3, C.blue), x, 0, -2));
    const srvs = SX.flat().map(x => { const s = add(g, server(C.green), x, 0, -2); s.scale.setScalar(.7); setServerPower(s, 1, C.green); return s; });
    const lb = hub(g, 0, 1.0, 3);
    srvs.forEach(s => g.add(lineBetween(V(0, 1, 3), V(s.position.x, .8, -1.4), C.blue, .035)));
    const bolt = add(g, lightningBolt(), homeX[0], 1.4, -2); const fire = fireOn(g, homeX[0], 3.0, -2, 1.4); fire.scale.setScalar(.001);
    const os = orbs(g, 18, C.blue, .15);
    return (t) => {
      const strike = 2.2, dead = t > strike;
      setLightning(bolt, t, 1.9, .4); fire.scale.setScalar(Math.max(.001, ease(seg(t, 2.1, 2.9))));
      roofFill(houses[0], dead ? WARM_DEAD : C.blue);
      srvs.forEach((s, i) => { const ours = i < 2; setServerPower(s, ours && dead ? 0 : 1, ours && dead ? WARM_DEAD : C.green); });
      os.forEach((o, n) => {
        const sp = .1 + n * .26, p = (t - sp) / 1.7; o.visible = p > 0 && p < 1; if (!o.visible) return;
        const alive = (sp + .6) > strike ? [2, 3] : [0, 1, 2, 3], k = alive[n % alive.length], tx = srvs[k].position.x;
        o.position.copy(p < .4 ? V(0, 1.0, lerp(12, 3, p / .4)) : V(lerp(0, tx, (p - .4) / .6), 1.0, lerp(3, -1.4, (p - .4) / .6)));
      });
    };
  }
};

// HA-D. Global high availability: optional failover into AWS, Google or Azure when both homes are gone.
function cloudMarker(color) {
  const g = new THREE.Group(), m = new THREE.MeshStandardMaterial({ color, emissive: color, emissiveIntensity: 0, roughness: .4, transparent: true, opacity: .85 });
  add(g, sph(.2, m), -.18, 0, 0); add(g, sph(.26, m), .05, .1, 0); add(g, sph(.18, m), .27, 0, 0); g.userData.mat = m; return g;
}
export const global = {
  bg: C.bg, cam: camPath([2, 8, 12], [0, 2, 9], [0, .8, 0], [0, 0, 0]),
  build(g) {
    const R = 3.4, globe = add(g, new THREE.Group(), 0, 0, 0);
    // painted world: lapis oceans and sandstone continents, seeded so it never changes
    const cv = document.createElement('canvas'); cv.width = 1024; cv.height = 512; const cx = cv.getContext('2d'), rr = rng(12);
    cx.fillStyle = '#1d3a78'; cx.fillRect(0, 0, 1024, 512);
    [[200, 170], [260, 330], [520, 160], [560, 330], [760, 180], [850, 360], [120, 100], [640, 90]].forEach(([x, y]) => { for (let i = 0; i < 26; i++) { cx.fillStyle = i % 3 ? '#b99a5e' : '#8a7a4a'; cx.beginPath(); cx.ellipse(x + (rr() - .5) * 150, y + (rr() - .5) * 90, 18 + rr() * 38, 12 + rr() * 26, rr() * 3, 0, 6.3); cx.fill(); } });
    cx.strokeStyle = 'rgba(255,215,120,.25)'; cx.lineWidth = 1.5; for (let i = 0; i <= 16; i++) { cx.beginPath(); cx.moveTo(i * 64, 0); cx.lineTo(i * 64, 512); cx.stroke(); } for (let i = 0; i <= 8; i++) { cx.beginPath(); cx.moveTo(0, i * 64); cx.lineTo(1024, i * 64); cx.stroke(); }
    const tex = new THREE.CanvasTexture(cv); tex.colorSpace = THREE.SRGBColorSpace;
    globe.add(new THREE.Mesh(new THREE.SphereGeometry(R, 48, 32), new THREE.MeshStandardMaterial({ map: tex, roughness: .75, metalness: .1 })));
    const arm = []; [[R + .55, .0, 0], [R + .8, 1.1, .6], [R + 1.05, .5, 1.5]].forEach(([rad, rx, rz]) => { const m = new THREE.Mesh(new THREE.TorusGeometry(rad, .045, 6, 90), brass()); m.rotation.set(rx + Math.PI / 2, 0, rz); g.add(m); arm.push(m); });
    add(g, cyl(.35, 1.1, brass(), 12), 0, -R - .9, 0); add(g, cyl(1.5, .25, stone(C.metal, 1, 1), 16), 0, -R - 1.6, 0); add(g, cyl(.9, .12, brass(), 16), 0, -R - 1.4, 0);
    g.userData.arm = arm;
    const ll = (lat, lon, r = R) => { const la = lat * Math.PI / 180, lo = lon * Math.PI / 180; return V(Math.cos(la) * Math.sin(lo) * r, Math.sin(la) * r, Math.cos(la) * Math.cos(lo) * r); };
    const place = (la, lo, obj, lift) => { const p = ll(la, lo); obj.position.copy(p.clone().multiplyScalar(1 + lift / R)); obj.quaternion.setFromUnitVectors(V(0, 1, 0), p.clone().normalize()); globe.add(obj); return p; };
    const homes = [[38, -95], [36, -90]].map(([la, lo]) => { const m = glowMat(C.green); const o = new THREE.Group(); add(o, new THREE.Mesh(new THREE.BoxGeometry(.26, .22, .26), m), 0, 0, 0); add(o, new THREE.Mesh(new THREE.ConeGeometry(.22, .22, 4), m), 0, .22, 0).rotation.y = Math.PI / 4; add(o, new THREE.Mesh(new THREE.ConeGeometry(.05, .2, 4), m), 0, .45, .1); const p = place(la, lo, o, .12); return { m, p }; });
    const clouds = [[40, -75], [50, 8], [-30, 150]].map(([la, lo], i) => { const c = cloudMarker(['#ff9a45', '#4f8dff', '#3ed598'][i]); const p = place(la, lo, c, .35); return { c, p }; });
    // each dot on the globe is a server altar (an altar with its open book and a service floating above it), standing on the surface
    const users = [[45, -120], [20, -80], [52, 20], [5, 25], [30, 80], [-25, 140], [10, -50], [45, 100]].map(([la, lo]) => { const p = ll(la, lo, R + .02); const m = server(C.blue); m.scale.setScalar(.12); setServerPower(m, 1, C.blue); m.position.copy(p); m.quaternion.setFromUnitVectors(V(0, 1, 0), p.clone().normalize()); globe.add(m); return p; });
    const os = users.map(() => add(globe, sph(.085, glow(C.blue, 2)), 0, 0, 0));
    function glowMat(c) { return new THREE.MeshStandardMaterial({ color: c, emissive: c, emissiveIntensity: 1.6 }); }
    return (t) => {
      globe.rotation.y = -.7 + t * .1; g.userData.arm.forEach((m, i) => { m.rotation.y = t * (.18 + .08 * i) * (i % 2 ? -1 : 1); });
      const dead = t > 2.4;
      homes.forEach(h => { h.m.color.set(dead ? WARM_DEAD : C.green); h.m.emissive.set(dead ? WARM_DEAD : C.green); h.m.emissiveIntensity = dead ? .5 : 1.6; });
      const standby = ease(seg(t, 2.4, 3.2));
      clouds.forEach(c => { c.c.userData.mat.emissiveIntensity = 1.8 * standby; c.c.userData.mat.opacity = .45 + .45 * standby; c.c.scale.setScalar(1 + .25 * standby); });
      users.forEach((u, i) => {
        const targets = dead ? clouds : homes, best = targets.map((r, j) => [u.distanceTo(r.p), j]).sort((a, b) => a[0] - b[0])[0][1], tp = targets[best].p;
        const p = ((t * .55 + i * .13) % 1), a = u.clone().normalize(), b = tp.clone().normalize();
        os[i].position.copy(a.lerp(b, ease(p)).normalize().multiplyScalar(R + .1 + Math.sin(Math.PI * p) * .9));
      });
    };
  }
};

// HA-E. Each level costs more and is harder to run. Choose the level you need.
const coinStack = n => { const g = new THREE.Group(); for (let i = 0; i < n; i++) { const c = coin(.32); c.rotation.x = Math.PI / 2; c.position.set((i % 3 - 1) * .015, i * .075, (i % 2) * .015); c.rotation.y = i * .7; g.add(c); } return g; };
export const tradeoff = {
  bg: C.bg, cam: camPath([0, 2, 9], [0, 5.2, 12.5], [0, 0, 0], [0, 2, 0]),
  build(g) {
    floor(g);
    const X = [-5, 0, 5], H = [1.2, 2.4, 3.8], stacks = [], knots = [], tiers = [];
    X.forEach((x, i) => {
      const t0 = new THREE.Group(); g.add(t0); tiers.push(t0);
      add(t0, box(3.9, H[i], 3.9, stone(C.wall, 1, 1)), x, H[i] / 2, 0); add(t0, box(4.05, .1, 4.05, brass()), x, H[i], 0); add(t0, box(4.05, .08, 4.05, brass()), x, .06, 0);
      for (let k = 0; k <= i; k++) add(t0, box(4.4 + k * .5, .3, 4.4 + k * .5, stone('#6a5a48', 1, 1)), x, .15 - k * 0, 0);
      const top = H[i];
      if (i === 0) { add(t0, houseOutline(2.4, 1.8, 1.0), x, top, 0); for (let k = 0; k < 2; k++) add(t0, server(C.blue), x - .5 + k * 1.0, top, 0).scale.setScalar(.34); }
      if (i === 1) for (let k = 0; k < 2; k++) { add(t0, houseOutline(1.7, 1.4, .8), x - 1.0 + k * 2.0, top, 0); add(t0, server(C.blue), x - 1.0 + k * 2.0, top, 0).scale.setScalar(.3); }
      if (i === 2) { const orr = add(t0, new THREE.Mesh(new THREE.SphereGeometry(.85, 20, 14), new THREE.MeshStandardMaterial({ color: '#1d3a78', roughness: .7 })), x, top + 1.05, 0); add(t0, new THREE.Mesh(new THREE.TorusGeometry(1.15, .035, 6, 40), brass()), x, top + 1.05, 0).rotation.x = Math.PI / 2 - .4; add(t0, new THREE.Mesh(new THREE.TorusGeometry(1.3, .035, 6, 40), brass()), x, top + 1.05, 0).rotation.set(.5, 0, 1.1); add(t0, cyl(.12, .5, brass(), 8), x, top + .25, 0); }
      const cs = add(g, coinStack(5 + i * 9), x + 2.7, .1, 2.3); stacks.push(cs);
      const kn = add(g, new THREE.Mesh(new THREE.TorusKnotGeometry(.5, .09, 90, 8, 2 + i, 3), mat(['#ff9a45', C.red, C.blue][i], { metalness: .5, emissive: ['#ff9a45', C.red, C.blue][i], emissiveIntensity: .4 })), x - 2.7, .9 + i * .5, 2.3); knots.push(kn);
    });
    const pole = add(g, cyl(.05, 2.2, brass(), 8), X[1], H[1] + 3.6, 1.6);
    const flagShape = new THREE.Shape(); flagShape.moveTo(0, 0); flagShape.lineTo(1.1, -.35); flagShape.lineTo(0, -.7);
    const flag = add(g, new THREE.Mesh(new THREE.ShapeGeometry(flagShape), new THREE.MeshBasicMaterial({ color: C.green, side: THREE.DoubleSide })), X[1], H[1] + 4.5, 1.6);
    return (t) => {
      X.forEach((x, i) => { const k = ease(seg(t, .3 + i * .7, 1.0 + i * .7)); const st = stacks[i]; st.scale.setScalar(Math.max(.001, k)); const kn = knots[i]; kn.scale.setScalar(Math.max(.001, k * (.7 + i * .5))); kn.rotation.y = t * .6; kn.rotation.x = t * .4; });
      const f = ease(seg(t, 3.4, 4.0)); pole.scale.y = Math.max(.001, f); pole.position.y = H[1] + 1.1 * f; flag.visible = f > .6; flag.position.y = H[1] + 2.2 * f; flag.scale.setScalar(Math.max(.001, f));
    };
  }
};
export default [failoverByHand, failoverTest, drift, singlePoint, highAvailability, regional, global, tradeoff];
