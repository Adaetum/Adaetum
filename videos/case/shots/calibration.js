// A calibration shot for testing how closely a video model follows the proxy. It is not part of the film.
// Everything is countable, colour-coded and timed, so each thing the model keeps or drops can be checked:
//   shrines: 3 in a row, halos red / green / blue, with 1 / 2 / 3 relics above them
//   walker: a blue acolyte walks left to right the whole shot, passing the red shrine as the lightning strikes; an orange acolyte stands at the far right
//   coin: one gold coin arcs from the chest at far left to the blue shrine between 1.0 s and 2.4 s
//   lightning: strikes the green shrine at 2.6 s; fire starts on it at 2.8 s; smoke from 3.2 s
//   camera: slow dolly in with a pan to the right
import * as THREE from '../../vendor/three.module.js';
import { C, D, add, V, arc, ease, seg, lerp, pulse, room, person, server, setServerPower, chest, coin, smoke, lightningBolt, setLightning, fireOn, animateIcon, ICONS } from '../lib.js';

export const calibration = {
  bg: C.bg,
  cam: u => { const k = ease(u); return [V(lerp(-3.5, 2.8, k), lerp(3.8, 3.2, k), lerp(10.5, 8.2, k)), V(lerp(0, 1.2, k), 1.8, 0)]; },
  build(g) {
    g.add(room(18, 12));
    const X = [-3.4, 0, 3.4], cols = [C.red, C.green, C.blue];
    const srv = X.map((x, i) => { const s = add(g, server(cols[i]), x, 0, 0); s.scale.setScalar(1.15); setServerPower(s, 1, cols[i]); return s; });
    const relics = []; [1, 2, 3].forEach((n, i) => { for (let k = 0; k < n; k++) { const o = ICONS[(i + k) % 5](); o.scale.setScalar(.42); g.add(o); relics.push({ o, i, k, n }); } });
    const ch = add(g, chest(), -6.4, 0, 1.5); ch.rotation.y = .6;
    const gold = add(g, coin(.32), 0, -50, 0);
    const walker = add(g, person('#2f5fc4'), -6, 0, 3.0); walker.rotation.y = Math.PI / 2;
    const still = add(g, person('#c4672a'), 6.2, 0, 2.6); still.rotation.y = -.6;
    const bolt = add(g, lightningBolt(), 0, 1.0, 0), fire = fireOn(g, 0, 1.95, 0, 1.0); fire.scale.setScalar(.001);
    smoke(g, 0, 2.3, 0, 7, { color: '#8f867c', start: 3.2, size: 1.8, opacity: .5, rise: 3.6 });
    return (t) => {
      relics.forEach(r => { r.o.position.set(X[r.i] + (r.k - (r.n - 1) / 2) * .8, 3.3 + .08 * Math.sin(t * 2 + r.k), 0); animateIcon(r.o, t); });
      walker.position.x = lerp(-8.5, 3.5, t / D); walker.position.y = Math.abs(Math.sin(t * 6)) * .05;
      still.userData.head.rotation.y = .5 * ease(seg(t, 2.6, 3.2));
      const f = seg(t, 1.0, 2.4); gold.visible = f > 0 && f < 1; gold.position.copy(arc(V(-6.4, 1.2, 1.5), V(3.4, 3.0, 0), f, 2.5)); gold.rotation.y = t * 6;
      setLightning(bolt, t, 2.6, .4); setServerPower(srv[1], t < 2.6 ? 1 : .25, cols[1]);
      fire.scale.setScalar(Math.max(.001, ease(seg(t, 2.8, 3.6))));
    };
  }
};
