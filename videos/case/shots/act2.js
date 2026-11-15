// Act 2: the usual places to host, and what goes wrong with each.
import * as THREE from '../../vendor/three.module.js';
import { dimOf, feel, feelAt, ziggurat, chest, altar, flame, sconce, stone, templeFloor, brass, C, D, add, box, cyl, sph, mat, glow, V, arc, ease, seg, lerp, pulse, clamp, rng, room, table, laptop, setLaptop, person, server, setServerPower, ICONS, ICON_COL, animateIcon, coin, lineBetween, ghost } from '../lib.js';

const camPath = (a, b, la, lb) => u => { const k = ease(u); return [V(lerp(a[0], b[0], k), lerp(a[1], b[1], k), lerp(a[2], b[2], k)), V(lerp(la[0], lb[0], k), lerp(la[1], lb[1], k), lerp(la[2], lb[2], k))]; };

// Shots 6-8 continue the one hall from shots 1-5: the same room, the same two friends standing at the sides where shot 5 left them, and a camera that starts
// exactly where the previous shot's camera stopped. Each shot hands its last state to the next: the home altar dissolves into the big host altar, the host into the rented plot.
const friendsAt = (g, near = 0) => { const p1 = add(g, ghost(), -5, 0, 2.6), p2 = add(g, person('#2f5fc4'), lerp(5, 2.4, near), 0, lerp(2.6, 1.9, near)); p1.rotation.y = Math.PI * .85; p2.rotation.y = -1.33; return [p1, p2]; };
// the friends are still cowering at the end of shot 5; relax them over the first moments of the shot
const relax = (ps, t) => { const k = 1 - ease(seg(t, 0, .8)); ps[0].userData.head.rotation.set(-.25 * k, 0, .3 * k); ps[1].userData.head.rotation.set(-.25 * k, 0, -.3 * k); };
const altarRow = g => {}; // the altars sank away at the start of shot 4, so the hall stays empty from here on
const PED_X = [-6, -3, 0, 3, 6];
// the home machine is the acolyte's own altar with its open book (the same machine as every server); the services float above it
const tableAndLaptop = g => { const m = add(g, server(C.warm, 0, false), 0, 0, 0); m.scale.setScalar(1.15); setServerPower(m, 1, C.warm); return { tb: m, lap: m.userData.tome, m }; };
// The five services float above the home laptop. update(t, dead): at dead = 0 they orbit it, glowing; as dead goes to 1 (the power cut) they dim, wobble, and
// drop to the floor around the table.
const homeRelics = g => {
  const rr = rng(21), relics = ICONS.map((f, i) => { const o = f(); g.add(o); return { o, dim: dimOf(o), i, drift: V((rr() - .5) * 2.6, 0, 1.0 + rr() * 1.2), spin: V(1.3 + rr(), (rr() - .5) * 3, (rr() - .5) * 2) }; });
  const update = (t, dead, appear = 1) => relics.forEach(({ o, dim, i, drift, spin }) => {
    const tt = Math.min(t, 2.6), a = i / 5 * Math.PI * 2 + tt * .5, orb = V(Math.cos(a) * 2.0, 3.5 + Math.sin(tt * 1.4 + i) * .1, Math.sin(a) * .9), f = ease(dead);   // the orbit freezes at 2.6 s, the moment the power goes
    const y = Math.max(lerp(orb.y, .25, f * f), .25);
    o.position.set(lerp(orb.x, orb.x + drift.x, f), y, lerp(orb.z, orb.z + drift.z, f));
    o.rotation.set(spin.x * f * .5 + f * .9, spin.y * f + Math.sin(t + i) * .3 * (1 - f), spin.z * f * .5 + (dead > 0 && dead < 1 ? .3 * Math.sin(t * 25 + i) : 0));
    o.scale.setScalar(.6325 * ease(appear)); animateIcon(o, t); dim(1 - .88 * f);
  });
  return { relics, update };
};
// The VM host: one large stone altar with many open books on it. Each book is a virtual machine, with a small service relic above it. update(t, f, shakeAmt) with
// f = 0..1 shows the host failing: the big altar shudders and tilts, the ring goes red, and every book slumps, goes dark and drops its relic's glow.
const vmHost = g => {
  const all = add(g, new THREE.Group(), 0, 0, 0), host = add(all, new THREE.Group(), 0, 0, 0), S = 2.0, TOP = 1.41 * S;
  add(host, altar(), 0, 0, 0).scale.setScalar(S);
  const ring = add(host, new THREE.Mesh(new THREE.TorusGeometry(2.7, .06, 8, 56), glow(C.blue, 1.6)), 0, .08, 0); ring.rotation.x = Math.PI / 2;
  const books = [[0, 0], ...[0, 1, 2, 3, 4, 5].map(k => [Math.cos(k / 6 * Math.PI * 2 + .3) * 1.05, Math.sin(k / 6 * Math.PI * 2 + .3) * 1.05])].map(([x, z], i) => {
    const m = add(host, new THREE.Group(), x, TOP + .04, z); m.rotation.y = i * 1.3;
    const tome = add(m, laptop(1), 0, 0, 0); tome.scale.setScalar(.8);
    const relic = ICONS[i % ICONS.length](); relic.scale.setScalar(.34); relic.position.set(0, 1.05 + (i === 0 ? .55 : 0), 0); m.add(relic);
    return { m, tome, relic, dim: dimOf(relic), i, baseY: TOP + .04 };
  });
  const update = (t, f, shakeAmt = 0) => {
    const shake = Math.sin(t * 60) * .05 * shakeAmt;
    ring.material.color.set(f > .5 ? C.red : C.blue); ring.material.emissive.set(f > .5 ? C.red : C.blue); ring.material.emissiveIntensity = 1.6 * (1 - .5 * f);
    host.position.x = shake; host.rotation.z = f * .07;
    books.forEach(({ m, tome, relic, dim, i, baseY }) => {
      m.position.y = baseY - .12 * f; m.rotation.x = f * (i % 2 ? .22 : -.18) * (i ? 1 : 0); m.rotation.z = f * ((i % 3) - 1) * .12;
      setLaptop(tome, 1 - f); relic.rotation.y = t * .8 + i; animateIcon(relic, t); dim(1 - .88 * f); relic.position.y = (i === 0 ? 1.6 : 1.05) - .35 * f;
    });
  };
  return { all, update };
};

// 6. A laptop at home: cheap, until someone trips on the cable. Same hall and table as shot 5; the dead server is gone and a laptop sits on the table.
export const shot5 = {
  bg: C.bg, cam: camPath([.6, 3.9, 7.4], [1, 4.6, 8], [0, 1.9, 0], [0, 2.4, 0]),
  build(g) {
    g.add(room(18, 12)); altarRow(g);
    const { lap, m: home } = tableAndLaptop(g); const hr = homeRelics(g);
    const plug = add(g, box(.25, .18, .18, mat('#ddd')), 1.6, .3, .9);
    const wall = add(g, box(.5, .5, .1, mat('#2f3f52')), 3.4, .6, -5.8);
    const cable = add(g, new THREE.Mesh(new THREE.CylinderGeometry(.04, .04, 1, 8), mat('#111')), 0, 0, 0);
    const router = add(g, box(.7, .15, .5, mat('#2f3f52')), -2.1, .08, .6);
    const rl = add(g, sph(.05, glow(C.green, 2)), -1.9, .18, .85);
    const fr = friendsAt(g), p = fr[1];
    return (t, u) => {
      const out = ease(seg(t, 2.0, 2.9));
      const a = V(3.4, .6, -5.7), b = V(lerp(1.6, 2.6, out), .3, lerp(.9, 1.6, out));
      cable.position.copy(a).lerp(b, .5); cable.scale.y = a.distanceTo(b); cable.quaternion.setFromUnitVectors(V(0, 1, 0), b.clone().sub(a).normalize());
      plug.position.copy(b);
      const walk = ease(seg(t, .6, 2.2)); p.position.x = lerp(5, 2.4, walk); p.position.z = lerp(2.6, 1.9, walk); p.rotation.y = -1.33;
      relax(fr, t); feelAt(p, t, [[0, 'sad'], [1.8, 'neutral'], [2.3, 'surprised'], [3.3, 'sad']]);
      const dead = ease(seg(t, 2.6, 3.0));
      setServerPower(home, 1 - dead, C.warm); rl.material.emissiveIntensity = 2 * (1 - dead);
      hr.update(t, seg(t, 2.6, 3.7), seg(t, .1, .9));   // the services float above the laptop and fall when the power goes
    };
  }
};

// 7. A virtual machine: the table and dead laptop sink away and a glass host rises with four small machines inside; then the host fails and they all go dark.
export const shot6 = {
  bg: C.bg, cam: camPath([1, 4.6, 8], [0, 6.0, 9.6], [0, 2.4, 0], [0, 2.6, 0]),
  build(g) {
    g.add(room(18, 12)); altarRow(g);
    const old = add(g, new THREE.Group(), 0, 0, 0); const { tb, lap, m: oldHome } = tableAndLaptop(old); setServerPower(oldHome, 0, C.warm); const hrOld = homeRelics(old); hrOld.update(2.6, 1);   // the dead services still lie where they fell
    const h = vmHost(g); const fr = friendsAt(g, 1);
    return (t, u) => {
      const sink = ease(seg(t, .2, 1.0)), rise = ease(seg(t, .6, 1.6));
      old.scale.setScalar(Math.max(.001, 1 - sink)); old.visible = sink < 1; h.all.scale.set(1, Math.max(.001, rise), 1); h.all.visible = rise > 0;
      const f = ease(seg(t, 2.8, 3.4));
      const back = ease(seg(t, .4, 1.8)); fr[1].position.set(lerp(2.4, 5, back), 0, lerp(1.9, 2.6, back)); fr[1].rotation.y = -1.33;
      h.update(t, f, pulse(t, 2.7, 4.0)); relax(fr, t + 5); feelAt(fr[1], t, [[0, 'sad'], [1.6, 'worried'], [3.2, 'alarm']]);
    };
  }
};

// 8. A rented server far away: the dead host dissolves and the meter runs. The rented machine is one altar with its book, and all five services crowd above it.
export const shot7 = {
  bg: C.bg, cam: camPath([0, 6.0, 9.6], [-1.5, 5.0, 9.8], [0, 2.6, 0], [1.2, 2.4, -1.2]),
  build(g) {
    g.add(room(18, 12)); altarRow(g);
    const old = vmHost(g); old.update(9, 1);
    const wallet = add(g, chest(), -5.2, 0, 3); wallet.rotation.y = .5;
    const dc = add(g, ziggurat(5.4, 3.6), 6.2, 0, -4.2); dc.scale.setScalar(.7);
    const rented = add(g, server(C.blue, 0), 6.2, 0, -1.0); setServerPower(rented, 1, C.blue);
    const crowd = [1, 2, 3, 4].map(i => { const o = ICONS[i](); o.scale.setScalar(.5); g.add(o); return o; });
    const coins = []; for (let i = 0; i < 9; i++) coins.push(add(g, coin(.3), 0, -50, 0));
    const fr = friendsAt(g);
    const a = V(-5.2, 1.8, 3), b = V(6.2, 3.4, -1.0);
    return (t, u) => {
      const gone = ease(seg(t, .2, 1.0)), come = ease(seg(t, .6, 1.4));
      old.all.scale.setScalar(Math.max(.001, 1 - gone)); old.all.visible = gone < 1;
      [wallet, dc, rented].forEach(o => { o.visible = come > 0; });
      wallet.scale.setScalar(Math.max(.001, come)); dc.scale.setScalar(Math.max(.001, .7 * come)); rented.scale.setScalar(Math.max(.001, come));
      coins.forEach((c, n) => { const k = ((t * .5 + n / 9) % 1); c.visible = come > .95; c.position.copy(arc(a, b, k, 3)); c.rotation.y = t * 5; });
      wallet.userData.setLevel(1 - .6 * ease(u));
      crowd.forEach((o, i) => { const ang = t * .7 + i / 4 * Math.PI * 2, rise = ease(seg(t, 1.4 + i * .2, 2.4 + i * .2)); o.position.set(6.2 + Math.cos(ang) * 1.7, 3.7 + rise * .2 + Math.sin(t * 1.5 + i) * .1, -1.0 + Math.sin(ang) * .8); o.scale.setScalar(Math.max(.001, .5 * come * rise)); animateIcon(o, t); });
    };
  }
};

// 8. A ceremonial tower of the five apps, on an altar. One block pulled without a rite, and it all comes down.
export const shot8 = {
  bg: C.bg, cam: u => { const k = ease(u); const sh = .06 * pulse(u, .5, .75) * Math.sin(u * 240); return [V(lerp(2.5, 2.4, k) + sh, lerp(3.6, 3.6, k), lerp(10, 7.6, k)), V(0, lerp(1.2, 2.6, k), 0)]; },
  build(g) {
    g.add(room(18, 12));
    const plinth = add(g, altar(), 0, 0, 0); plinth.scale.setScalar(1.7);
    const TOP = 1.41 * 1.7;
    const candles = []; for (let i = 0; i < 10; i++) { const a = i / 10 * Math.PI * 2; const c = add(g, cyl(.07, .5, mat('#f0e4cc'), 8), Math.cos(a) * 3.4, .25, Math.sin(a) * 3.4); candles.push(flame(g, Math.cos(a) * 3.4, .62, Math.sin(a) * 3.4, .8)); }
    const flare = add(g, new THREE.Mesh(new THREE.TorusGeometry(2.6, .07, 8, 64), glow(C.red, 0)), 0, .05, 0); flare.rotation.x = Math.PI / 2;
    const blocks = [];
    for (let i = 0; i < 5; i++) {
      const col = ICON_COL[i], b = new THREE.Group(); g.add(b);
      add(b, box(1.6, .7, 1.6, stone(C.metal, 1, 1)), 0, 0, 0);
      for (const sx of [-1, 1]) for (const sz of [-1, 1]) add(b, box(.1, .74, .1, brass()), sx * .78, 0, sz * .78);
      add(b, box(1.66, .06, 1.66, brass()), 0, .36, 0); add(b, box(1.66, .06, 1.66, brass()), 0, -.36, 0);
      for (const z of [-1, 1]) { add(b, new THREE.Mesh(new THREE.TorusGeometry(.2, .03, 6, 20), glow(col, 1.6)), 0, 0, z * .82); add(b, sph(.07, glow(col, 2)), 0, 0, z * .82); }
      blocks.push({ b, y0: TOP + .38 + i * .74 });
    }
    const staff = new THREE.Group(); g.add(staff);
    add(staff, cyl(.05, 3.6, brass(), 8), 0, 1.8, 0); add(staff, new THREE.Mesh(new THREE.OctahedronGeometry(.2), glow(C.gold, 2)), 0, 3.7, 0); add(staff, new THREE.Mesh(new THREE.TorusGeometry(.26, .03, 6, 16), brass()), 0, 3.5, 0);
    const p = add(g, ghost(), -4.4, 0, 1.2); p.rotation.y = Math.PI * .5 - .25; const p2 = add(g, person('#2f5fc4'), 4.4, 0, 1.2); p2.rotation.y = Math.PI * 1.5 + .25; feel(p2, 'surprised');
    return (t, u) => {
      const reach = ease(seg(t, .8, 2.0)), pull = ease(seg(t, 2.0, 2.8)), fall = 2.6;
      staff.position.set(lerp(-3.6, -1.9, reach), lerp(0, .0, reach), .4); staff.rotation.z = lerp(.25, 1.28, reach) * -1 + (t > 2 ? -.05 * Math.sin(t * 20) : 0);
      staff.position.y = lerp(0, TOP + 1.9, reach) - 1.4 * reach;
      blocks.forEach((o, i) => {
        o.b.position.set(0, o.y0, 0); o.b.rotation.set(0, 0, 0);
        if (i === 2) { o.b.position.x = lerp(0, 2.4, pull); o.b.position.y = o.y0 - .0 * pull; }
        if (i > 2) { const d = (i - 3) * .18, k = ease(seg(t, fall + d, fall + d + 1.1)); const dir = i === 3 ? 1 : -1;
          o.b.position.set(lerp(0, dir * (2.8 + (i - 3) * .9), k), lerp(o.y0, .38, k * k), lerp(0, 1.2 + (i - 3) * .8, k)); o.b.rotation.set(k * 1.4 * dir, k * .9, k * 1.9 * dir); }
        if (i === 2 && t > fall) { const k = ease(seg(t, fall, fall + 1.0)); o.b.position.set(lerp(2.4, 4.2, k), lerp(o.y0, .38, k * k), lerp(0, 1.6, k)); o.b.rotation.set(0, k * .6, k * 1.2); }
      });
      candles.forEach((f, i) => { f.visible = t < fall + .15 + i * .1; });
      flare.material.emissiveIntensity = 2.2 * pulse(t, fall, fall + 1.6);
    };
  }
};

// 9. Kubernetes: an all-seeing, many-eyed wheel of rings. Powerful, enormous, and not built for two people.
function eye(r = .22, iris = C.gold) {
  const g = new THREE.Group();
  add(g, sph(r, mat('#f4eee0', { roughness: .4 })), 0, 0, 0);
  add(g, sph(r * .55, glow(iris, 1.3)), 0, 0, r * .62);
  add(g, sph(r * .25, mat('#0a0710')), 0, 0, r * .98);
  return g;
}
export const shot9 = {
  bg: C.bg, cam: camPath([2.4, 3.6, 7.6], [0, 3.4, 9], [0, 2.6, 0], [0, 6, 0]),
  build(g) {
    g.add(room(18, 12));
    const angel = add(g, new THREE.Group(), 0, 6.6, -2);
    const rings = [[3.0, 12, 'x', .32, .6, C.gold], [4.2, 16, 'y', -.26, .3, C.blue], [5.4, 22, 'z', .2, .1, C.gold]].map(([R, n, axis, spd, tilt, col]) => {
      const holder = new THREE.Group(); holder.rotation.x = tilt; angel.add(holder);
      const ring = new THREE.Group(); holder.add(ring);
      ring.add(new THREE.Mesh(new THREE.TorusGeometry(R, .09, 8, 80), brass()));
      for (let i = 0; i < n; i++) {
        const a = i / n * Math.PI * 2, e = eye(.24, col), dir = V(Math.cos(a), Math.sin(a), 0);
        e.position.set(dir.x * R, dir.y * R, 0); e.quaternion.setFromUnitVectors(V(0, 0, 1), dir); ring.add(e);
      }
      return { ring, axis, spd };
    });
    const core = add(angel, new THREE.Group(), 0, 0, 0);
    const bigEye = add(core, sph(1.1, mat('#f4eee0', { roughness: .4 })), 0, 0, 0);
    add(core, sph(.62, glow(C.blue, 1.6)), 0, 0, .7); const pupil = add(core, sph(.3, mat('#05030a')), 0, 0, 1.05);
    const lids = add(core, sph(1.14, mat('#2a2140')), 0, 0, 0); lids.scale.set(1, .01, 1);
    for (let i = 0; i < 16; i++) { const a = i / 16 * Math.PI * 2; const ray = add(angel, box(.08, 2.2, .04, glow(C.brass, .7)), Math.cos(a) * 6.6, Math.sin(a) * 6.6, 0); ray.rotation.z = a - Math.PI / 2; }
    const relics = Array.from({ length: 10 }, (_, i) => { const o = ICONS[i % 5](); o.scale.setScalar(i < 5 ? .95 : .6); g.add(o); return o; });
    const p1 = add(g, ghost(), -4.4, 0, 1.2), p2 = add(g, person('#2f5fc4'), 4.4, 0, 1.2); feel(p2, 'worried');
    p1.rotation.y = Math.PI * .5 - .25; p2.rotation.y = Math.PI * 1.5 + .25;
    return (t) => {
      rings.forEach(o => { o.ring.rotation[o.axis] = t * o.spd; });
      const blink = pulse(t, 3.0, 3.4); lids.scale.y = Math.max(.01, blink * .98);
      pupil.scale.setScalar(1 + .12 * Math.sin(t * 1.5));
      angel.rotation.y = .12 * Math.sin(t * .5);
      relics.forEach((c, i) => { const a = (i < 5 ? i / 5 : (i - 5) / 5 + .1) * Math.PI * 2 + t * (i < 5 ? .22 : -.16), R = i < 5 ? 7.4 : 9.2; c.position.set(Math.cos(a) * R, 6.6 + Math.sin(a) * R * .42, -3.4 + Math.sin(a) * 1.2); c.rotation.set(0, Math.sin(a) * .4, 0); animateIcon(c, t); });
      p1.userData.head.rotation.x = -.4 * ease(seg(t, .5, 2)); p2.userData.head.rotation.x = -.4 * ease(seg(t, .5, 2));
    };
  }
};
export default [shot5, shot6, shot7, shot8, shot9];
