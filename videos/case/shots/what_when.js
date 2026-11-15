// Chapter C, "what happens when": ONE default scene, used for every scenario.
//
// The hall is split in two by the carpet. On the left is the usual way: one lone machine (an altar with its open book and a service floating above it) and a worried
// acolyte. On the right is Adaetum's way: three machines and an acolyte who is soon relieved. The same event hits the same machine on both sides; what differs is what
// follows. On the left it ends badly (the machine goes dark, the service falls, a red cross). On the right it is put right (a green ring, a service hops to a
// neighbour, a green check). A scenario is only data: which event (HIT), which fix (FIX) and one line of narration. Add a scenario by adding a row to SCENARIOS.
import * as THREE from '../../vendor/three.module.js';
import { C, add, box, cyl, sph, mat, glow, brass, V, arc, ease, seg, lerp, pulse, clamp, rng, room, person, server, setServerPower, lightningBolt, setLightning, feelAt, animateIcon, dimOf, ICONS } from '../lib.js';

const T_HIT = 1.3, T_FIX = 2.4, RO = 2.2, SECS = 6;            // seconds: the event lands, then the fix (or the failure) plays out
const LEFT = V(-6.5, 0, -.5), RIGHT = [V(3.8, 0, -2.2), V(9.4, 0, -2.2), V(6.6, 0, 1.3)];
const WARM_DEAD = '#ff5a5f';

// ---- the events: a small symbolic prop over the target, shown between about 0.4 s and the moment of impact ----
const EVENTS = {
  strike: g => { const b = lightningBolt(); b.position.y = 2.2; g.add(b); return { fx: t => setLightning(b, t, T_HIT - .2, .4) }; },
  crack: g => { const bars = [0, 1, 2, 3].map(i => { const m = add(g, box(.08, .5 + .15 * i, .04, glow(C.red, 1.6)), (i - 1.5) * .35, .55 + .1 * i, .9); m.rotation.z = (i - 1.5) * .4; return m; }); return { fx: t => bars.forEach((m, i) => { m.scale.setScalar(Math.max(.001, ease(seg(t, .6 + i * .15, 1.1 + i * .15)))); }) }; },
  crash: g => { const h = add(g, sph(.8, new THREE.MeshBasicMaterial({ color: C.red, transparent: true, opacity: 0 })), 0, 3.1, 0); return { fx: t => { h.material.opacity = .45 * seg(t, .5, 1.0) * (1 - seg(t, 2.3, 2.7)) * (.6 + .4 * Math.sin(t * 20)); } }; },
  commit: g => { const c = add(g, box(.7, .7, .7, glow(C.red, 1.2)), 0, 7, 0); return { fx: t => { const k = ease(seg(t, .4, 1.3)); c.position.y = lerp(7, 2.0, k); c.rotation.y = t * 3; c.visible = t < 2.6; } }; },
  drift: g => { const w = new THREE.Group(); g.add(w); add(w, new THREE.Mesh(new THREE.TorusGeometry(.22, .07, 8, 16, Math.PI * 1.6), glow('#ffb347', 1.2)), 0, .35, 0); add(w, cyl(.05, .9, glow('#ffb347', 1), 8), 0, -.1, 0); return { fx: t => { w.position.set(0, 3.3, 0); w.rotation.z = Math.sin(t * 6) * .5; w.scale.setScalar(Math.max(.001, ease(seg(t, .4, .9)) * (1 - ease(seg(t, 2.3, 2.7))))); } }; },
  expire: g => { const k = new THREE.Group(); g.add(k); add(k, new THREE.Mesh(new THREE.TorusGeometry(.55, .06, 8, 28), brass()), 0, 0, 0); const hand = add(k, box(.05, .45, .03, mat('#222')), 0, .2, .04); hand.geometry.translate(0, .2, 0); return { fx: t => { k.position.set(0, 3.5, 0); k.scale.setScalar(Math.max(.001, ease(seg(t, .4, .9)) * (1 - ease(seg(t, 2.3, 2.7))))); hand.rotation.z = -t * 5; } }; },
  leak: g => { const key = new THREE.Group(); g.add(key); add(key, new THREE.Mesh(new THREE.TorusGeometry(.16, .04, 6, 16), glow(C.gold, 1.4)), 0, .4, 0); add(key, cyl(.04, .7, glow(C.gold, 1.2), 6), 0, 0, 0); const r = rng(5); const drips = Array.from({ length: 6 }, () => ({ m: add(g, sph(.05, glow(C.gold, 2)), 0, -40, 0), v: V((r() - .5) * 2, 0, (r() - .5) * 2), t0: .8 + r() * .8 })); return { fx: t => { key.position.set(0, 3.5, 0); key.scale.setScalar(Math.max(.001, ease(seg(t, .4, .9)) * (1 - ease(seg(t, 2.3, 2.7))))); drips.forEach(d => { const k = t - d.t0; d.m.visible = k > 0 && k < 1.2; if (d.m.visible) d.m.position.set(d.v.x * k, 3.3 - 3 * k * k, d.v.z * k); }); } }; },
  update: g => { const gear = add(g, new THREE.Mesh(new THREE.TorusGeometry(.5, .09, 8, 6), glow('#ffb347', 1.6)), 0, 3.4, 0); gear.rotation.x = Math.PI / 2; return { fx: t => { gear.rotation.z = t * 5; gear.scale.setScalar(Math.max(.001, ease(seg(t, .4, .9)) * (1 - ease(seg(t, 2.3, 2.7))))); } }; },
  lock: g => { const k = new THREE.Group(); g.add(k); add(k, box(.6, .5, .25, brass()), 0, 0, 0); add(k, new THREE.Mesh(new THREE.TorusGeometry(.2, .05, 6, 14, Math.PI), brass()), 0, .25, 0); return { fx: t => { k.position.set(0, 3.5, 0); k.rotation.z = .08 * Math.sin(t * 25) * pulse(t, 1.0, 2.2); k.scale.setScalar(Math.max(.001, ease(seg(t, .4, .9)) * (1 - ease(seg(t, 2.3, 2.7))))); } }; },
  outage: g => { const c = ICONS[0](); c.scale.setScalar(.55); g.add(c); const x = [1, -1].map(s => { const b = add(g, box(1.0, .12, .05, glow(C.red, 2)), 0, 0, .3); b.rotation.z = s * .78; return b; }); return { fx: t => { const k = ease(seg(t, .4, .9)) * (1 - ease(seg(t, 2.3, 2.7))); c.position.set(0, 3.7, 0); animateIcon(c, t); c.scale.setScalar(Math.max(.001, .55 * k)); x.forEach(b => { b.position.set(0, 3.7, .3); b.scale.setScalar(Math.max(.001, k * (t > 1.1 ? 1 : 0))); }); } }; },
  load: g => { const r = rng(9), orbs = Array.from({ length: 14 }, (_, i) => ({ m: add(g, sph(.1, glow('#ffffff', 1.4)), 0, -40, 0), a: i / 14 * Math.PI * 2, d: 5 + r() * 3 })); return { fx: t => orbs.forEach(o => { const k = seg(t, .3 + (o.a % 1) * .8, 1.7), p = 1 - ease(k); o.m.visible = k > 0 && k < 1; o.m.position.set(Math.cos(o.a) * o.d * p, 1.2 + 1.2 * p, Math.sin(o.a) * o.d * p); }) }; },
  none: () => ({ fx: () => {} }),
  dark: () => ({ fx: () => {} }),
  attack: () => ({ fx: () => {} }),
};
// a check or a cross that pops up over a result
const mark = (g, ok, x, z) => { const m = add(g, new THREE.Group(), x, 5.4, z); const col = ok ? C.green : C.red; if (ok) { add(m, box(.18, .7, .1, glow(col, 2.2)), -.22, -.1, 0).rotation.z = .8; add(m, box(.18, 1.3, .1, glow(col, 2.2)), .2, .15, 0).rotation.z = -.7; } else { add(m, box(1.4, .2, .1, glow(col, 2.2)), 0, 0, 0).rotation.z = .78; add(m, box(1.4, .2, .1, glow(col, 2.2)), 0, 0, 0).rotation.z = -.78; } m.scale.setScalar(.001); return m; };
const ring = (g, x, z, col, r = 1.8) => { const m = add(g, new THREE.Mesh(new THREE.TorusGeometry(r, .06, 8, 48), glow(col, 1.4)), x, .06, z); m.rotation.x = Math.PI / 2; return m; };
const tintAltar = (s, hex) => { const c = new THREE.Color(hex); s.children[0].traverse(n => { if (n.isMesh && n.material && !(n.material.emissive && n.material.emissive.getHex() !== 0)) { n.material = n.material.clone(); n.material.color.lerp(c, .55); } }); };

// ---- the scene itself ----
const make = (hit, fix) => ({
  bg: '#0d0a14',
  // six seconds: rest on the usual way on the left, glide across the carpet, then rest on Adaetum's way on the right (which plays RO seconds later)
  cam: u => { const x = lerp(-4.6, 5.2, ease(seg(u * SECS, 2.0, 3.5))); return [V(x, 4.7, 11.8), V(x * .92, 2.4, 0)]; },
  build(g) {
    g.add(room(26, 14));
    const lm = add(g, server(C.blue, 0, false), LEFT.x, 0, LEFT.z); lm.scale.setScalar(1.2); tintAltar(lm, '#7d8699');
    const rm = RIGHT.map(p => { const s = add(g, server(C.blue, 0, false), p.x, 0, p.z); setServerPower(s, 1, C.blue); return s; });
    // each machine's service floats above it as its own object (so a scenario can drop it, hop it or copy it)
    const mk = (k, p, sc) => { const o = ICONS[k](); o.scale.setScalar(.55 * sc); g.add(o); o.position.set(p.x, 3.06 * sc, p.z); return { o, home: o.position.clone(), dim: dimOf(o) }; };
    const lr = mk(1, LEFT, 1.2), rr = RIGHT.map((p, i) => mk(i + 2, p, 1));
    const extra = add(g, server(C.blue, 0), 6.6, 0, -5.2); extra.scale.setScalar(.001);   // the machine an "add a machine" scenario pops in
    const lRing = ring(g, LEFT.x, LEFT.z, '#7d8699', 2.2), rRing = RIGHT.map(p => ring(g, p.x, p.z, C.green, 1.7));
    const pl = add(g, person('#4a5878'), -3.3, 0, 3.0); pl.rotation.y = -.7;
    const pr = add(g, person('#2f5fc4'), 2.0, 0, 3.0); pr.rotation.y = .7;
    // the event, once on each side, over the machine it hits
    const Lev = (EVENTS[hit] || EVENTS.none)(add(g, new THREE.Group(), LEFT.x, 0, LEFT.z)), Rev = (EVENTS[hit] || EVENTS.none)(add(g, new THREE.Group(), RIGHT[0].x, 0, RIGHT[0].z));
    const lx = mark(g, false, LEFT.x, LEFT.z), rv = mark(g, true, RIGHT[0].x + 2, RIGHT[0].z);
    // attackers (left reach their target; right are stopped at a shield)
    const dark = () => [0, 1, 2].map(i => add(g, person('#14101a'), 0, 0, -9)); const Latk = hit === 'attack' ? dark() : [], Ratk = hit === 'attack' ? dark() : [];
    const shield = add(g, new THREE.Mesh(new THREE.TorusGeometry(4.2, .07, 8, 60), glow(C.green, 1.8)), 6.5, .08, -.3); shield.rotation.x = Math.PI / 2; shield.scale.setScalar(.001);
    // load: extra service copies on the right
    const copies = fix === 'scale' ? [0, 1].map(i => { const o = ICONS[2 + i](); o.scale.setScalar(.001); g.add(o); return o; }) : [];
    return (t) => {
      Lev.fx(t);
      // ---- left: the usual way. Everything ends badly. ----
      const bad = hit === 'none' && fix === 'pop' ? .5 : ease(seg(t, T_HIT, T_HIT + .6));
      setServerPower(lm, 1 - bad, bad > .4 ? WARM_DEAD : C.blue);
      [lr, ...rr].forEach((r, i) => animateIcon(r.o, t + i));
      const fall = ease(seg(t, T_HIT + .3, T_HIT + 1.3)); lr.o.position.y = lr.home.y - 3.2 * fall * fall; lr.o.rotation.z = fall * 1.2; lr.dim(1 - .88 * bad);
      lRing.material.color.set(bad > .4 ? C.red : '#7d8699'); lRing.material.emissive.set(bad > .4 ? C.red : '#7d8699');
      Latk.forEach((a, i) => { const k = ease(seg(t, .5 + i * .2, 2.0 + i * .2)); a.position.set(LEFT.x + (i - 1) * 1.6, 0, lerp(-9, LEFT.z - 1.6, k)); a.rotation.y = 0; });
      lx.scale.setScalar(Math.max(.001, ease(seg(t, 3.0, 3.6))));
      // ---- right: Adaetum's way. The same event, put right, RO seconds after the left. ----
      const tr = t - RO; Rev.fx(tr);
      const R0 = rm[0], R1 = rm[1], hitK = ease(seg(tr, T_HIT, T_HIT + .4)), fixK = ease(seg(tr, T_FIX, T_FIX + 1.0));
      const down = fix === 'hop' || fix === 'rebuild';
      if (fix === 'rebuild') { rm.forEach((s, i) => { const up = ease(seg(tr, T_FIX + i * .5, T_FIX + .8 + i * .5)), lvl = tr < T_HIT ? 1 : up; setServerPower(s, lvl, up > .1 || tr < T_HIT ? C.blue : WARM_DEAD); rRing[i].material.emissiveIntensity = 1.4 * lvl; rr[i].dim(.12 + .88 * lvl); }); }
      else { setServerPower(R0, down ? 1 - hitK * (1 - fixK) : 1 - .45 * hitK * (1 - fixK), hitK > 0 && fixK < .5 ? '#ffb347' : C.blue); rRing[0].material.color.set(hitK > 0 && fixK < .5 ? '#ffb347' : C.green); rRing[0].material.emissive.set(hitK > 0 && fixK < .5 ? '#ffb347' : C.green); rRing[0].scale.setScalar(1 + .1 * pulse(tr, T_FIX, T_FIX + 1)); rr[0].dim(1 - (down ? .85 : .5) * hitK * (1 - fixK)); }
      if (fix === 'hop') { const k = ease(seg(tr, T_HIT + .5, T_FIX + 1.0)); rr[0].o.position.copy(arc(rr[0].home, rr[1].home.clone().add(V(.9, .9, 0)), k, 1.4)); }
      if (fix === 'pop') { const k = ease(seg(tr, T_FIX, T_FIX + .9)); extra.scale.setScalar(Math.max(.001, 1.0 * k * (1 + .15 * Math.sin(Math.PI * k)))); setServerPower(extra, seg(tr, T_FIX + .5, T_FIX + 1.3), C.blue); }
      if (fix === 'drain') { const R2 = rm[2], k = ease(seg(tr, T_HIT, T_FIX + .6)), gone = ease(seg(tr, T_FIX + .6, T_FIX + 1.5)); rr[2].o.position.copy(arc(rr[2].home, rr[0].home.clone().add(V(.9, .9, 0)), k, 1.2)); R2.scale.setScalar(Math.max(.001, 1 - gone)); rRing[2].visible = gone < .9; }
      if (fix === 'scale') { copies.forEach((o, i) => { const k = ease(seg(tr, T_FIX + i * .3, T_FIX + .9 + i * .3)); o.position.set(RIGHT[1 - i * 0].x + (i ? 1.3 : -1.3), 5.0 + .1 * Math.sin(tr * 2 + i), RIGHT[1].z); o.scale.setScalar(Math.max(.001, .5 * k)); animateIcon(o, tr); }); }
      if (fix === 'block') { const k = ease(seg(tr, T_HIT - .2, T_HIT + .4)); shield.scale.setScalar(Math.max(.001, k)); Ratk.forEach((a, i) => { const go = seg(tr, .5 + i * .2, 1.8 + i * .2), bounce = ease(seg(tr, T_HIT + .4, T_FIX + .4)); a.position.set(RIGHT[2].x + (i - 1) * 2.4, 0, lerp(-9, -1.6, go) - 3.0 * bounce); a.rotation.y = 0; a.visible = tr < T_FIX + 1.4; }); }
      rv.scale.setScalar(Math.max(.001, ease(seg(tr, 3.1, 3.7))));
      // the acolytes
      feelAt(pl, t, [[0, 'neutral'], [T_HIT, 'alarm'], [T_FIX + .4, 'sad']]);
      feelAt(pr, t, [[0, 'neutral'], [RO + T_HIT, 'surprised'], [RO + T_FIX + .6, 'relief'], [RO + 3.4, 'joy']]);
    };
  },
});

// ---- the scenarios: a row each. line = what the narrator says (the usual way, then Adaetum's way) ----
export const SCENARIOS = [
  { id: 'machine', hit: 'strike', fix: 'hop', title: 'A machine dies', line: "Machine dies: traditionally a 3 a.m. page. Adaetum moves apps.", shows: 'Lightning strikes a machine on both sides. On the left it goes dark and its service falls; on the right its service hops to a neighbour and a green check appears.' },
  { id: 'disk', hit: 'crack', fix: 'hop', title: 'A disk fails', line: "A failed disk means a backup restore, if one exists. Adaetum's Longhorn keeps copies.", shows: 'Red cracks race across a machine on both sides; the left one goes dark, the right one is covered by a copy on its neighbour.' },
  { id: 'app', hit: 'crash', fix: 'heal', title: 'An app crashes', line: "An app crashes. Often unnoticed. Adaetum restarts it and alerts you.", shows: 'A service flickers red on both sides; on the left nobody notices and it dies, on the right it restarts and glows green.' },
  { id: 'change', hit: 'commit', fix: 'heal', title: 'A bad change goes out', line: "A bad change goes out. Typically no way back. Adaetum reverts the commit.", shows: 'A red block drops onto the machine on both sides; the left breaks, the right reverts and recovers.' },
  { id: 'hand', hit: 'drift', fix: 'heal', title: 'Someone edits by hand', line: "Hand edits cause drift. Adaetum prevents it and restores known states.", shows: 'A wrench appears over a machine; on the left it drifts and breaks, on the right it is put back to plan.' },
  { id: 'cert', hit: 'expire', fix: 'heal', title: 'A certificate expires', line: "An expired certificate causes an outage. Adaetum renews it before it expires.", shows: 'A clock races over a machine; the left runs out and fails, the right renews in time.' },
  { id: 'pass', hit: 'leak', fix: 'heal', title: 'A password leaks', line: "Leaked passwords are hard to trace and stop. Adaetum rotates them from one vault.", shows: 'A golden key spills drips over a machine; the left is compromised, the right rotates it and recovers.' },
  { id: 'update', hit: 'update', fix: 'heal', title: 'Updates are due', line: "Updates due. Conventionally, downtime. Adaetum patches one at a time.", shows: 'An amber gear turns over a machine; the left goes down for patching, the right patches quietly and stays up.' },
  { id: 'vault', hit: 'lock', fix: 'heal', title: 'The vault restarts', line: "The vault restarts. By default, sealed. Adaetum unseals itself.", shows: 'A padlock shakes over a machine; the left stays sealed and fails, the right unseals itself.' },
  { id: 'add', hit: 'none', fix: 'pop', title: 'You add a machine', line: "Adding a machine traditionally means a weekend of wiring. Adaetum joins it itself.", shows: 'On the right a new machine pops up and links to the others by itself; on the left the lone machine sits half-lit.' },
  { id: 'remove', hit: 'none', fix: 'drain', title: 'You remove a machine', line: "Remove a machine: drain it. Adaetum relocates its apps.", shows: 'On the right a machine hands its service to another and fades away; on the left pulling the plug darkens it.' },
  { id: 'attack', hit: 'attack', fix: 'block', title: 'An attacker comes knocking', line: "An attacker knocks. Commonly, open ports. Adaetum: none, one sign-in, all logged.", shows: 'Three dark figures approach on both sides; on the left they reach the machine, on the right they bounce off a green shield.' },
  { id: 'lost', hit: 'dark', fix: 'rebuild', title: 'The cluster is lost', line: "A server is lost. Classically, rebuilt from memory. Adaetum rebuilds from the plan.", shows: 'Every light goes out; the left stays dark, the right rebuilds one machine at a time from the plan.' },
  { id: 'outage', hit: 'outage', fix: 'heal', title: 'A provider has an outage', line: "A service provider causes an outage. Adaetum keeps running locally.", shows: 'A cloud with a red cross hangs over a machine; the left depends on it and fails, the right keeps running.' },
  { id: 'busy', hit: 'load', fix: 'scale', title: 'An app gets busy', line: "When an app gets busy, you buy a bigger box. Adaetum adds copies, then removes them.", shows: 'A crowd rushes a machine; the left is overwhelmed, the right adds copies of the service.' },
];
export const whatWhen = SCENARIOS.map(s => make(s.hit, s.fix));
