// Act 1: the dream. Two friends tired of paying for five subscriptions want to self-host.
import * as THREE from '../../vendor/three.module.js';
import { feel, feelAt, lightningBolt, setLightning, fireOn, smoke, seal, chest, altar, stone, C, D, add, box, cyl, sph, mat, glow, V, arc, ease, seg, lerp, pulse, clamp, rng, room, table, laptop, setLaptop, person, server, setServerPower, ICONS, ICON_COL, animateIcon, coin, ghost } from '../lib.js';

const camPath = (a, b, la, lb) => u => { const k = ease(u); return [V(lerp(a[0], b[0], k), lerp(a[1], b[1], k), lerp(a[2], b[2], k)), V(lerp(la[0], lb[0], k), lerp(la[1], lb[1], k), lerp(la[2], lb[2], k))]; };
const makeIcons = (g) => ICONS.map((f, i) => { const o = f(); g.add(o); return o; });
const PED_X = [-6, -3, 0, 3, 6];

const wallet = () => chest();
const setWallet = (w, level) => w.userData.setLevel(level);

// Shots 1-5 are one continuous take cut into slices. Everything is driven by one timeline T (seconds since shot 1 began), so the camera, coins,
// bows, bobbing relics, the table and the server carry straight across each cut. Each shot builds its own copy of the set, so it also stands alone.
const SPAN = 4 * D;
const lerp3 = (a, b, k) => [lerp(a[0], b[0], k), lerp(a[1], b[1], k), lerp(a[2], b[2], k)];
// One camera move: a curved sweep from the right of the room round to the front over shots 1-3, then a slow pull back in shot 4.
const sweep = T => {
  const s0 = clamp(T / (3 * D)), s = lerp(s0, ease(s0), .3);
  const q = (a, c, b) => (1 - s) * (1 - s) * a + 2 * (1 - s) * s * c + s * s * b;
  const P = [[6, 3.4, 8.8], [-4, 3.2, 10.4], [-.6, 3.7, 8.2]], L = [[0, 1.9, 0], [-.6, 1.9, 0], [0, 1.7, 0]];
  let pos = [0, 1, 2].map(i => q(P[0][i], P[1][i], P[2][i])), look = [0, 1, 2].map(i => q(L[0][i], L[1][i], L[2][i]));
  const back = clamp((T - 3 * D) / D); pos = lerp3(pos, [0, 3.6, 9.8], back); look = lerp3(look, [0, 2.2, -.8], back);
  return [V(...pos), V(...look)];
};
const PEOPLE = [[0, 1.6, '#2f5fc4', 0]];   // the film follows one person: you
const tributeSet = (g, withServer = false) => {
  g.add(room(18, 12));
  const altars = PED_X.map(x => add(g, altar(), x, 0, -2.5));
  const icons = ICONS.map((f, i) => { const o = f(); add(g, o, PED_X[i], 2.6, -2.5); return o; });
  const rings = PED_X.map((x, i) => { const r = add(g, new THREE.Mesh(new THREE.TorusGeometry(1.0, .04, 8, 48), glow(ICON_COL[i], .9)), x, .06, -2.5); r.rotation.x = Math.PI / 2; return r; });
  const people = PEOPLE.map(([x, z, col, off]) => { const w = add(g, new THREE.Group(), x, 0, z); w.rotation.y = Math.PI; const p = add(w, person(col), 0, 0, 0); return { w, p, x, z, off }; });
  const chestObj = withServer ? null : add(g, chest(), -2.3, 0, 1.9); if (chestObj) chestObj.rotation.y = .25;   // the purse sits right beside the acolyte until it is empty and gone
  const coins = []; for (let i = 0; i < 20; i++) coins.push(add(g, coin(.3), 0, -50, 0));
  let table = null, srv = null;
  // the thing the services move to is the acolyte's own altar with an open book: the same machine the whole film uses for a server, and the one in the home shot
  if (withServer) { srv = add(g, server(C.warm, 0, false), 0, 0, 0); srv.scale.setScalar(.001); setServerPower(srv, 0); }
  return { icons, rings, people, chestObj, coins, table, srv, altars };
};
const table_ = table;
// an acolyte bows by folding the upper body forward at the waist while the robe stays planted
const bowTo = (pp, k) => { k = clamp(k, 0, 1.1); pp.p.userData.upper.rotation.x = 1.05 * k; pp.p.userData.head.rotation.x = .3 * k; };
const orbitAt = (T, i) => { const a = i / 5 * Math.PI * 2 + T * .5; return V(Math.cos(a) * 2.2, 3.4 + Math.sin(T * 1.4 + i) * .1, Math.sin(a) * 1.0); };
// a line of other acolytes still walking tribute across the back of the hall; they enter from the left, so none are on screen at the first cut
const marchBuild = g => Array.from({ length: 9 }, (_, i) => { const m = add(g, person(['#6b5a8a', '#8a5a4a', '#4a6a7a'][i % 3]), 0, 0, -4.1 - (i % 2) * .6); m.scale.setScalar(.8); m.rotation.y = Math.PI / 2; add(m, coin(.16), 0, 1.15, .55); return m; });
const marchAt = (ms, T) => ms.forEach((m, i) => { m.position.x = -12 - i * 3.2 + (T - 3 * D) * 2.4; m.position.y = Math.abs(Math.sin(T * 5 + i)) * .06; m.visible = m.position.x < 12; });
// The acolyte bows once every BOW seconds starting at B0; at the bottom of each bow a coin leaves for every altar (they fly for FLY seconds, a hair apart).
const BOW = 1.5, B0 = .3, FLY = 1.1, STAG = .07, LASTBOW = 2 * D + .2;
const bowPhase = T => { const c = (T - B0) / BOW; return c < 0 ? 0 : Math.sin(Math.PI * (c % 1)); };
const launchAt = (b, i) => B0 + (b + .5) * BOW + i * STAG;       // when coin i of bow b leaves the acolyte
// The whole story state as a pure function of T. Used by every slice.
const story = (S, T) => {
  const { icons, rings, people, chestObj, coins, table, srv, altars } = S, T3 = 2 * D, T4 = 3 * D;
  const exit = seg(T, T3 + .2, T3 + 1.0);                       // coins stop and people rise in shot 3
  icons.forEach((o, i) => {
    const fly = seg(T, T3 + .4 + i * .2, T3 + 1.9 + i * .2), a = V(PED_X[i], 2.6 + Math.sin(T * 1.2 + i) * .1, -2.5), b = V(Math.cos(i * 1.256) * .5, 3.3, Math.sin(i * 1.256) * .4);
    const spread = ease(seg(T, T4 + .4, T4 + 2.2));
    o.position.copy(arc(a, b, fly, 1.5).lerp(orbitAt(T, i), spread)); o.scale.setScalar(lerp(1, .55, ease(fly)) * ease(seg(T, .1 + i * .2, .8 + i * .2)) * lerp(1, 1.15, spread));
    animateIcon(o, T); o.rotation.y = Math.sin(T + i) * .3;
  });
  people.forEach(pp => {
    bowTo(pp, bowPhase(T) * (1 - ease(seg(T, T3 + .1, T3 + .8))));   // one bow per BOW seconds, until the people rise in shot 3
    const turn = ease(seg(T, T3 + 1.0, T3 + 2.4)); pp.w.rotation.y = Math.PI * (1 - turn) + .35 * turn;   // after rising, they turn to face us
    feelAt(pp.p, T, [[0, 'worried'], [T3 + .2, 'sad'], [T3 + 1.2, 'relief'], [T3 + 2.4, 'joy'], [T4 + .8, 'proud']]);
  });
  // coins: one per altar at the bottom of every bow, then none once the bowing stops
  const who = people[0], lastB = Math.floor((LASTBOW - B0) / BOW - .5);
  coins.forEach((c, n) => {
    const i = n % 5, slot = Math.floor(n / 5);
    const b = Math.floor((T - i * STAG - B0 - .5 * BOW) / BOW);   // latest bow whose coin i has left; coin slots rotate every 4 bows, so a coin is never reused in flight
    const local = b >= 0 && b <= lastB && b % 4 === slot ? (T - launchAt(b, i)) / FLY : -1;
    c.visible = local >= 0 && local <= 1; if (!c.visible) return;
    c.position.copy(arc(V(who.x, 1.0, who.z - .6), V(PED_X[i], 3.2, -2.5), local, 2.2)); c.rotation.y = T * 6 + n;
  });
  // each altar's ring pulses as its coin lands
  rings.forEach((r, i) => { r.visible = T < T3 + 3.2;   // the rings go with the altars
    let b = Math.floor((T - FLY - i * STAG - B0 - .5 * BOW) / BOW); b = Math.min(b, lastB);
    const k = b >= 0 ? clamp((T - (launchAt(b, i) + FLY)) / .9) : 1; r.scale.setScalar(1 + .6 * k); r.material.emissiveIntensity = 1.6 * (1 - k) * (1 - seg(T, T3 + .4, T3 + 1.6)); });
  if (chestObj) chestObj.userData.setLevel(1 - .8 * seg(T, 1.5, T3 + .2) + .5 * seg(T, T3 + 2.4, T3 + 4.2));
  if (srv) {   // the altar pops up when they decide to bring it all home, then its book wakes
    const pop = ease(seg(T, T3 + .4, T3 + 1.3)); srv.scale.setScalar(Math.max(.001, 1.15 * pop * (1 + .12 * Math.sin(Math.PI * pop))));
    setServerPower(srv, ease(seg(T, T3 + 2.4, T3 + 4.2)), C.warm);
  }
  // the altars have done their job: they sink into the floor as the shot ends, so shot 4 opens on an empty hall
  const gone = ease(seg(T, T3 + 3.2, T3 + 4.5)); altars.forEach(a => { a.position.y = -3.2 * gone; a.visible = gone < 1; });
};
const slice = k => ({
  bg: C.bg, cam: u => sweep((k + u) * D),
  build(g) {
    const S = tributeSet(g, k >= 2);
    return t => { const T = k * D + t; story(S, T); };
  }
});
// 1. Two friends bow before the five altars and coins begin to fly out of them
export const shot1 = slice(0);
// 2. The coins keep flying out of the people, month after month, and the purse drains
export const shot2 = slice(1);
// 3. The people rise; a table and a server of their own arrive and the five services leave their altars for it; the coins stop
export const shot3 = slice(2);
// 4. So easy to start: the same hall, the server awake on its table with the five relics circling it, with the altars gone
export const shotEasy = slice(3);

// 5. The problems arise: calamities hit the five services one after another. Lightning strikes, fire takes hold, they flicker and drop out of the air
// and go dark, until all five have failed and the laptop's screen dies with them.
const relics = [];
const FAIL = [1.0, 2.0, 2.2, 2.9, 3.5];             // the moment each service drops: storage, music, video, docs, AI
const STRIKE = { 0: .4, 2: 1.8, 4: 3.1 };           // services hit by lightning, and when
const BURN = [0, 1, 3];                             // services that catch fire first
const dimOf = o => { const ms = new Set(); o.traverse(n => { if (n.isMesh && n.material && n.material.emissive && n.material.emissive.getHex() !== 0) ms.add(n.material); }); const base = [...ms].map(m => [m, m.emissiveIntensity]); return k => base.forEach(([m, b]) => { m.emissiveIntensity = b * k; }); };
export const shot4 = {
  bg: C.bgDanger, cam: u => { const k = ease(u), sh = (.07 * pulse(u, .1, .22) + .06 * pulse(u, .46, .56) + .06 * pulse(u, .7, .8)) * Math.sin(u * 300); return [V(lerp(0, .6, k) + sh, lerp(3.6, 3.9, k), lerp(9.8, 7.4, k)), V(0, lerp(1.8, 1.9, k), lerp(-.8, 0, k))]; },
  build(g) {
    g.add(room(18, 12));
    relics.length = 0; ICONS.forEach(f => { const o = f(); relics.push(o); g.add(o); });
    const dim = relics.map(dimOf);
    const srv = add(g, server(C.warm, 0, false), 0, 0, 0); srv.scale.setScalar(1.15); setServerPower(srv, 1, C.warm); const page = srv.userData.tome.userData.screen;
    const p1 = add(g, ghost(), -1.5, 0, 1.6), p2 = add(g, person('#2f5fc4'), 0, 0, 1.6);
    p1.position.set(-1.5, 0, 1.6); p2.position.set(0, 0, 1.6);
    const T0 = 4 * D, rr = rng(31);
    // where each service is when it lets go, and where it ends up on the floor
    const from = FAIL.map((tf, i) => orbitAt(T0 + tf, i));
    const drift = FAIL.map((_, i) => V((rr() - .5) * 2.2 + (from[i].x > 0 ? .5 : -.5), 0, 1.2 + rr() * 1.2));
    const spin = FAIL.map((_, i) => V(1.4 + rr(), (rr() - .5) * 3, (rr() - .5) * 2.4));
    const bolts = Object.keys(STRIKE).map(i => add(g, lightningBolt(), from[i].x, from[i].y - .2, from[i].z));
    const fires = BURN.map(i => { const f = fireOn(relics[i], 0, .1, 0, 1.0); f.scale.setScalar(.001); return f; });
    relics.forEach((o, i) => smoke(o, 0, .3, 0, 4, { color: '#8f867c', start: FAIL[i] - .6, size: 1.6, opacity: .5, rise: 2.4 }));
    smoke(srv, 0, 1.7, 0, 5, { color: '#8f867c', start: 3.7, size: 1.2, opacity: .45, rise: 2.2 });
    const sparks = Array.from({ length: 30 }, (_, n) => ({ m: add(g, sph(.05, glow('#ffd27a', 3)), 0, -40, 0), i: n % 5, v: V((rr() - .5) * 3, 1.5 + rr() * 2.2, (rr() - .5) * 3) }));
    return (t, u) => {
      Object.entries(STRIKE).forEach(([i, ts], b) => setLightning(bolts[b], t, ts, .35));
      // the book's pages flicker red as things go wrong, and go dark once the last service is gone
      const alive = 1 - ease(seg(t, 3.8, 4.3));
      setServerPower(srv, t < .35 ? 1 : (.6 + .4 * Math.sin(t * 30)) * alive, t > .35 ? C.red : C.warm); if (t > .35) { page.material.color.set(C.red); page.material.emissive.set(C.red); }
      srv.position.x = Math.sin(t * 70) * .03 * (pulse(t, .4, .7) + pulse(t, 1.8, 2.1) + pulse(t, 3.1, 3.4));
      const T = T0 + t;
      relics.forEach((o, i) => {
        const tf = FAIL[i], k = t - tf, lead = seg(t, tf - 1.0, tf);
        if (k < 0) {
          o.position.copy(orbitAt(T, i)); o.rotation.set(0, Math.sin(T + i) * .3, lead * .5 * Math.sin(t * 20 + i)); o.scale.setScalar(.6325 * (1 + .12 * lead * Math.sin(t * 25 + i)));
          animateIcon(o, T); dim[i](1 - .8 * lead);
        } else {
          const f = from[i], d = drift[i], y = Math.max(f.y + 1.0 * k - 5.2 * k * k, .2), m = Math.min(k, .9);
          o.position.set(f.x + d.x * m, y, f.z + d.z * m);
          const roll = Math.min(k * 1.4, 1); o.rotation.set(spin[i].x * roll * .5 + roll * .9, spin[i].y * roll, spin[i].z * roll * .5); o.scale.setScalar(.6325);
          dim[i](.1 + .12 * Math.max(0, Math.sin(t * 9 + i) * Math.exp(-k * 1.5)));
        }
      });
      fires.forEach((fire, n) => { const i = BURN[n], on = ease(seg(t, FAIL[i] - 1.0, FAIL[i] - .3)); fire.scale.setScalar(Math.max(.001, on * (1 - .3 * seg(t, FAIL[i] + 1, FAIL[i] + 1.8)))); });
      sparks.forEach(o => { const k = t - FAIL[o.i]; o.m.visible = k > 0 && k < 1.1; if (o.m.visible) { const l = V(from[o.i].x + drift[o.i].x * .6, .3, from[o.i].z + drift[o.i].z * .6); o.m.position.set(l.x + o.v.x * k * .6, l.y + o.v.y * k - 4.4 * k * k, l.z + o.v.z * k * .6); } });
      const back = ease(seg(t, .3, 1.6)), lean = ease(seg(t, 1.6, 3.4)), out = ease(seg(t, .2, 1.2));
      p1.position.set(lerp(-1.5, -4.5, out) - .5 * lean, 0, lerp(1.6, 2.6, out)); p2.position.set(lerp(0, 4.5, out) + .5 * lean, 0, lerp(1.6, 2.6, out));
      p1.rotation.y = lerp(Math.PI + .5, Math.PI * .85, out); p2.rotation.y = lerp(.35, -1.33, out);
      feelAt(p2, t, [[0, 'proud'], [.5, 'surprised'], [1.3, 'alarm'], [2.6, 'cower'], [4.0, 'sad']]);
      p1.userData.head.rotation.z = .3 * back; p2.userData.head.rotation.z = -.3 * back; p1.userData.head.rotation.x = -.25 * lean; p2.userData.head.rotation.x = -.25 * lean;
    };
  }
};
export default [shot1, shot2, shot3, shotEasy, shot4];
