import { describe, expect, it } from 'vitest';
import { createInterpolator } from '../lib/vanyadumInterp';
import type { VanyadumLevel } from '../lib/vanyadumLevel';
import {
  PEER_DOWN,
  PEER_FIRED,
  PEER_HIT,
  PEER_PROTECTED,
  changedHands,
  clock,
  decodeBoard,
  decodePeers,
  decodeSlops,
} from '../lib/vanyadumRoster';

/**
 * The slot directory — the half of «ВАНЯДУМ»'s wire that says WHO, as opposed
 * to the half that says where.
 *
 * A peer is addressed by a small integer naming a place in the building, and the
 * standings frame publishes who is holding each place. That split is what took a
 * peer entry from 71 bytes to 49, and it brings exactly one hazard with it: a
 * slot is REUSED, so the same number can mean two people a second apart.
 * Everything below is about decoding it and about being safe when the two frames
 * arrive in the wrong order.
 */

/** Two rooms at different heights, which is what makes the derived eye interesting. */
const LEVEL: VanyadumLevel = {
  seed: 1,
  sectors: [
    { id: 0, x0: 0, y0: 0, x1: 10, y1: 10, fz: 0, cz: 3, w: 'concrete', f: 'floor', c: 'ceiling', l: 0.8 },
    { id: 1, x0: 10, y0: 0, x1: 20, y1: 10, fz: 0.4, cz: 3.4, w: 'concrete', f: 'floor', c: 'ceiling', l: 0.6 },
  ],
  portals: [{ a: 0, b: 1, v: true, at: 10, lo: 4, hi: 6 }],
  walls: [],
  pickups: [],
  spawn: { x: 5, y: 5 },
  spawn_sector: 0,
  spawn_yaw: 0,
};

const EYE = 1.65;

describe('decodePeers', () => {
  it('reads a peer by its slot, in the quantisation the wire uses', () => {
    // Centimetres and thousandths of a radian, because this frame repeats twenty
    // times a second forever.
    const [p] = decodePeers([{ n: 2, x: -1234, y: 560, s: 0, yaw: -6283 }], LEVEL, EYE);
    expect(p.slot).toBe(2);
    expect(p.x).toBeCloseTo(-12.34, 9);
    expect(p.y).toBeCloseTo(5.6, 9);
    expect(p.yaw).toBeCloseTo(-6.283, 9);
  });

  it('derives the eye from the room the wire named, not from a height it sent', () => {
    // The height stopped being on the wire: the sector is three characters where
    // an eye height was six, AND it is the more useful of the two, because this
    // client holds the level and can look a floor up in it.
    const peers = decodePeers(
      [
        { n: 0, x: 500, y: 500, s: 0, yaw: 0 },
        { n: 1, x: 1500, y: 500, s: 1, yaw: 0 },
      ],
      LEVEL,
      EYE,
    );
    expect(peers[0].z).toBeCloseTo(EYE, 9);
    // The raised room's floor plus the same eye height — which is exactly what
    // the server's own EyeZ computes for the player standing in it.
    expect(peers[1].z).toBeCloseTo(0.4 + EYE, 9);
  });

  it('falls back to the bare eye height for a room the level has not got', () => {
    // The server answers the same way to its own bounds check. A peer drawn at
    // the wrong height is a bug; a peer not drawn at all is a worse one.
    const [p] = decodePeers([{ n: 0, x: 0, y: 0, s: 97, yaw: 0 }], LEVEL, EYE);
    expect(p.z).toBeCloseTo(EYE, 9);
  });

  it('reads slot zero and position zero as the real values they are', () => {
    // Nothing on a peer entry is `omitempty`, deliberately: slot 0 is a real
    // place and the first one handed out, and x and y are genuinely zero
    // somewhere in every building. A decode that treated either as "unset" would
    // put the first person to walk in at the origin.
    const [p] = decodePeers([{ n: 0, x: 0, y: 0, s: 0, yaw: 0 }], LEVEL, EYE);
    expect(p.slot).toBe(0);
    expect(p.x).toBe(0);
    expect(p.y).toBe(0);
  });

  it('reads the peer state, and reads its absence as nothing happening', () => {
    // The one field on a peer that is not geometry. Your own gun needs no such
    // marker — a barrel count falling by one IS the shot — but another man's gun
    // has nothing on the wire to fall, and being SHOT moves nobody at all, so
    // this is the only thing that says either happened two metres away. Omitted
    // on every tick nothing did, which is almost every tick of almost every peer.
    const peers = decodePeers(
      [
        { n: 0, x: 0, y: 0, s: 0, yaw: 0, st: PEER_FIRED },
        { n: 1, x: 0, y: 0, s: 0, yaw: 0, st: PEER_HIT },
        { n: 2, x: 0, y: 0, s: 0, yaw: 0, st: PEER_DOWN },
        { n: 3, x: 0, y: 0, s: 0, yaw: 0, st: PEER_PROTECTED },
        { n: 4, x: 0, y: 0, s: 0, yaw: 0 },
      ],
      LEVEL,
      EYE,
    );
    expect(peers.map((p) => p.st)).toEqual([PEER_FIRED, PEER_HIT, PEER_DOWN, PEER_PROTECTED, 0]);
  });

  it('reads a state it has never heard of as nothing rather than as something', () => {
    // Either end may learn a value without a coordinated deploy, so a client one
    // deploy behind must not draw a fifth state as one of the four it knows.
    // Zero is the resting value, so an unknown one being drawn as "nothing
    // happened" is the safe direction: a missing mark, never an invented one.
    const [p] = decodePeers([{ n: 0, x: 0, y: 0, s: 0, yaw: 0, st: 'нет' }], LEVEL, EYE);
    expect(p.st).toBe(0);
  });

  it('answers with nothing when the array is absent, which is the common case', () => {
    // Omitted entirely when there is nobody else in view — and that is most
    // frames, in a building this size with the peers cut to the rooms you can
    // see into.
    expect(decodePeers(undefined, LEVEL, EYE)).toEqual([]);
    expect(decodePeers([], LEVEL, EYE)).toEqual([]);
    expect(decodePeers('nonsense', LEVEL, EYE)).toEqual([]);
  });

  it('skips an entry that is not an object rather than drawing a peer at nowhere', () => {
    const peers = decodePeers([null, 7, { n: 3, x: 100, y: 100, s: 0, yaw: 0 }], LEVEL, EYE);
    expect(peers).toHaveLength(1);
    expect(peers[0].slot).toBe(3);
  });
});

describe('decodeSlops', () => {
  it('reads a нейрослоп by its id, in the quantisation the wire uses', () => {
    const [foe] = decodeSlops([{ n: 1, x: 1234, y: -567, s: 1 }], LEVEL);
    expect(foe.id).toBe(1);
    expect(foe.x).toBeCloseTo(12.34, 9);
    expect(foe.y).toBeCloseTo(-5.67, 9);
  });

  it('stands it on the floor of the room the wire named, not at an eye height', () => {
    // A слоп is drawn as a billboard standing on the ground, so what it needs
    // from the sector is the ground. Room 1 is a step up from room 0.
    expect(decodeSlops([{ n: 0, x: 0, y: 0, s: 0 }], LEVEL)[0].z).toBeCloseTo(0, 9);
    expect(decodeSlops([{ n: 0, x: 0, y: 0, s: 1 }], LEVEL)[0].z).toBeCloseTo(0.4, 9);
  });

  it('falls back to ground level for a room it has never heard of', () => {
    // The client one deploy behind a level format, or a frame that arrived
    // while the building was being rebuilt. A creature on the ground floor is a
    // safe answer; a creature at NaN is a sprite that vanishes.
    expect(decodeSlops([{ n: 0, x: 0, y: 0, s: 99 }], LEVEL)[0].z).toBe(0);
  });

  it('carries no angle and no state, because the wire has neither to give', () => {
    // A слоп walks at whoever it is chasing and does nothing else, so its facing
    // is derived from two of its own positions (vanyadumSlop) — and the four
    // things a peer's `st` exists to say are all things a слоп cannot do. Four
    // fields against a peer's six, on a payload that repeats twenty times a
    // second, is what a second creature in the building is paid for.
    expect(decodeSlops([{ n: 0, x: 100, y: 100, s: 0 }], LEVEL)[0]).toEqual({
      id: 0,
      x: 1,
      y: 1,
      z: 0,
    });
  });

  it('answers with nothing when the array is absent, which is most frames', () => {
    // Omitted entirely when there is nothing to draw — a building that has just
    // been walked into is empty of them for a whole spawn interval, and they are
    // cut to the rooms the reader can see into after that.
    expect(decodeSlops(undefined, LEVEL)).toEqual([]);
    expect(decodeSlops([], LEVEL)).toEqual([]);
    expect(decodeSlops('nonsense', LEVEL)).toEqual([]);
  });

  it('skips an entry that is not an object rather than drawing one at nowhere', () => {
    const foes = decodeSlops([null, 7, { n: 1, x: 100, y: 100, s: 0 }], LEVEL);
    expect(foes).toHaveLength(1);
    expect(foes[0].id).toBe(1);
  });
});

describe('decodeBoard', () => {
  it('reads a row, the bag it is carrying, and what it has done', () => {
    const [row] = decodeBoard([
      { n: 1, i: 'K3jf9sLm2QpZ', s: 137, c: { beer: 4 }, d: 2, k: 9, br: 5 },
    ]);
    expect(row).toEqual({
      slot: 1,
      name: 'K3jf9sLm2QpZ',
      seconds: 137,
      bag: { beer: 4 },
      deaths: 2,
      // THREE DIFFERENT NUMBERS, and the two on either side of the deaths are
      // the joke: a нейрослоп put down scores here, a friend put down scores
      // only on the confession below, and nothing adds one to the other.
      kills: 9,
      betrayals: 5,
    });
  });

  it('reads somebody carrying nothing as carrying nothing, not as a missing field', () => {
    // The bag is omitted rather than sent as an empty object, which is everybody
    // for their first minute — and so are all three counters, which is everybody
    // in a building where nobody has shot anything yet. Absent means none, not
    // unknown, so the readout draws a zero and the columns stay aligned.
    const [row] = decodeBoard([{ n: 0, i: 'aaaaaaaaaaaa', s: 3 }]);
    expect(row.bag).toEqual({});
    expect(row.deaths).toBe(0);
    expect(row.kills).toBe(0);
    expect(row.betrayals).toBe(0);
  });

  it('answers with nothing when the frame carried no rows', () => {
    expect(decodeBoard(undefined)).toEqual([]);
    expect(decodeBoard([])).toEqual([]);
  });
});

describe('changedHands', () => {
  const row = (slot: number, name: string) => ({
    slot,
    name,
    seconds: 0,
    bag: {},
    deaths: 0,
    kills: 0,
    betrayals: 0,
  });

  it('says nothing when the same people are standing in the same places', () => {
    const before = [row(0, 'aaa'), row(1, 'bbb')];
    const after = [row(0, 'aaa'), row(1, 'bbb')];
    expect(changedHands(before, after)).toEqual([]);
  });

  it('names a slot whose holder has been replaced', () => {
    // The whole reason this function exists: slot 1 is a PLACE, and the man in
    // it is not the man who was in it a second ago.
    expect(changedHands([row(0, 'aaa'), row(1, 'bbb')], [row(0, 'aaa'), row(1, 'ccc')])).toEqual([1]);
  });

  it('names a slot that was empty and has been taken', () => {
    // The ordinary shape of a hand-over: the previous holder left (his row went
    // with him), and somebody else has now been given the number. The buffer
    // still remembers where the place was when the last man stood in it.
    expect(changedHands([row(0, 'aaa')], [row(0, 'aaa'), row(2, 'ddd')])).toEqual([2]);
  });

  it('says nothing about a slot that has merely gone quiet', () => {
    // Nobody is standing in it, so there is nothing to draw wrongly, and the
    // stale positions age out of the buffer on their own. It is reported the
    // moment somebody takes it, which is the test above.
    expect(changedHands([row(0, 'aaa'), row(1, 'bbb')], [row(0, 'aaa')])).toEqual([]);
  });
});

describe('clock', () => {
  it('pads the seconds, so a readout never reads 1:7', () => {
    expect(clock(0)).toBe('0:00');
    expect(clock(7)).toBe('0:07');
    expect(clock(61)).toBe('1:01');
    expect(clock(600)).toBe('10:00');
  });

  it('counts in minutes all the way up rather than growing a third field', () => {
    // Longer, but it never pushes the pseudonym off a 360 px screen.
    expect(clock(3675)).toBe('61:15');
  });

  it('refuses to render a clock that went backwards', () => {
    expect(clock(-5)).toBe('0:00');
    expect(clock(Number.NaN)).toBe('0:00');
  });
});

describe('a snapshot that arrives before the standings that explain it', () => {
  /** The served delay and one simulation step, as the catalogue publishes them. */
  const DELAY = 120;
  const TICK = 50;

  it('draws the figure with no name attached rather than dropping him', () => {
    // THE ORDERING HAZARD, AND THE SAFE ANSWER TO IT. The server publishes the
    // standings ahead of the snapshot that first carries a new holder, so this
    // only happens when that board frame was dropped for a slow consumer — but
    // when it does, the client is holding a position for a slot it has no name
    // for.
    //
    // Being invisible for a second is worse than being unlabelled for one, and
    // guessing a name is worse than both. Nothing about the decode consults the
    // board at all, which is what makes that true by construction.
    const i = createInterpolator(DELAY, TICK);
    i.push(decodePeers([{ n: 3, x: 700, y: 500, s: 0, yaw: 0 }], LEVEL, EYE), [], 20, 1000);
    i.push(decodePeers([{ n: 3, x: 900, y: 500, s: 0, yaw: 0 }], LEVEL, EYE), [], 22, 1100);

    const drawn = i.sample(1050 + DELAY).peers;
    expect(drawn.map((p) => p.slot)).toEqual([3]);
    expect(drawn[0].x).toBeCloseTo(8, 6);
    // And the standings say nothing about him, because a board that never
    // arrived names nobody. The readout is full state, so it is short by a row
    // rather than carrying an invented one.
    expect(decodeBoard(undefined)).toEqual([]);
  });

  it('throws that slot away the moment the board says who is standing in it', () => {
    // The board catching up is also the first time this client learns the slot
    // has a holder — and it cannot know that the positions it buffered belong to
    // THAT man rather than to whoever had the place before him. So they go, and
    // the next snapshot re-establishes him from scratch. One frame of history
    // lost, against a man drawn sliding out of somebody else's position.
    const i = createInterpolator(DELAY, TICK);
    i.push(decodePeers([{ n: 3, x: 700, y: 500, s: 0, yaw: 0 }], LEVEL, EYE), [], 20, 1000);
    i.push(decodePeers([{ n: 3, x: 900, y: 500, s: 0, yaw: 0 }], LEVEL, EYE), [], 22, 1100);

    const board = decodeBoard([{ n: 3, i: 'K3jf9sLm2QpZ', s: 4 }]);
    for (const slot of changedHands([], board)) i.forget(slot);
    expect(i.sample(1050 + DELAY).peers).toEqual([]);

    // ...and he is back on the very next frame, which is fifty milliseconds.
    i.push(decodePeers([{ n: 3, x: 1100, y: 500, s: 0, yaw: 0 }], LEVEL, EYE), [], 24, 1200);
    expect(i.sample(1200 + DELAY).peers.map((p) => p.slot)).toEqual([3]);
  });

  it('leaves everybody else alone when one place changes hands', () => {
    // Why this is `forget` and not `reset`: resetting would pause every other
    // peer in the building every time anybody walked in or out, and in a
    // заброшка this small that is often.
    const i = createInterpolator(DELAY, TICK);
    const at = (x3: number, x4: number) =>
      decodePeers(
        [
          { n: 3, x: x3, y: 500, s: 0, yaw: 0 },
          { n: 4, x: x4, y: 500, s: 0, yaw: 0 },
        ],
        LEVEL,
        EYE,
      );
    i.push(at(700, 100), [], 20, 1000);
    i.push(at(900, 300), [], 22, 1100);

    const before = decodeBoard([
      { n: 3, i: 'aaaaaaaaaaaa', s: 30 },
      { n: 4, i: 'bbbbbbbbbbbb', s: 30 },
    ]);
    const after = decodeBoard([
      { n: 3, i: 'cccccccccccc', s: 0 },
      { n: 4, i: 'bbbbbbbbbbbb', s: 31 },
    ]);
    for (const slot of changedHands(before, after)) i.forget(slot);

    const drawn = i.sample(1050 + DELAY).peers;
    expect(drawn.map((p) => p.slot)).toEqual([4]);
    // Still being interpolated, exactly as if nothing had happened next door.
    expect(drawn[0].x).toBeCloseTo(2, 6);
  });
});

describe('a peer who walks out of view', () => {
  const DELAY = 120;
  const TICK = 50;

  it('stops being drawn, and does not linger in the buffer for ever', () => {
    // Interest management means a peer leaves the snapshot because he walked two
    // rooms away, not only because he left the building — and nothing announces
    // either. Absence is the whole of it, so what is drawn is exactly what the
    // newest frame named.
    const i = createInterpolator(DELAY, TICK);
    const pair = decodePeers(
      [
        { n: 1, x: 100, y: 0, s: 0, yaw: 0 },
        { n: 2, x: 200, y: 0, s: 0, yaw: 0 },
      ],
      LEVEL,
      EYE,
    );
    i.push(pair, [], 20, 1000);
    i.push(decodePeers([{ n: 1, x: 300, y: 0, s: 0, yaw: 0 }], LEVEL, EYE), [], 22, 1100);
    expect(i.sample(1050 + DELAY).peers.map((p) => p.slot)).toEqual([1]);

    // And once every frame that ever mentioned him has aged out of the bounded
    // buffer, there is nothing left of him at all — a tab left open while people
    // wander in and out does not accumulate a building.
    for (let n = 1; n <= 40; n++) {
      i.push(decodePeers([{ n: 1, x: 300 + n, y: 0, s: 0, yaw: 0 }], LEVEL, EYE), [], 22 + n, 1100 + n * TICK);
    }
    expect(i.sample(1100 + 40 * TICK + DELAY).peers.map((p) => p.slot)).toEqual([1]);
  });
});
