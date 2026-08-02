import { describe, expect, it } from 'vitest';
import { BUFFER_FRAMES, createInterpolator, shortestTurn } from '../lib/vanyadumInterp';
import { PEER_DOWN, PEER_FIRED } from '../lib/vanyadumRoster';

/**
 * Entity interpolation — how everything that is not you is drawn.
 *
 * Two halves. The first is the TIMELINE: the buffer is keyed on the server's own
 * tick rather than on when a frame turned up here, and the jitter transcript
 * below is the reason — on an arrival timeline the network's jitter is rendered
 * as velocity, and in this game it also puts the client's draw and the server's
 * lag-compensation rewind at different instants. The second is the three
 * degradations, which decide whether a bad connection looks like a pause or like
 * a peer teleporting through a wall.
 */

/** The served `sim.interp_delay_ms`. Never a number this side picks. */
const DELAY = 120;
/** One simulation step, `1000 / sim.hz`. The unit the timeline is counted in. */
const TICK = 50;

/**
 * One peer, addressed by the SLOT the wire names him by — a place in the
 * building rather than a person. What the standings say about who is holding
 * that place is vanyadumRoster's business, and is tested there.
 */
function peer(slot: number, x: number, yaw = 0) {
  return { slot, x, y: 0, z: 1.65, yaw };
}

const build = () => createInterpolator(DELAY, TICK);

// --- the jitter transcript --------------------------------------------------

/** Where tick zero sits on the local clock in the transcripts below. */
const ORIGIN = 1000;
/** The first tick a transcript publishes. The server sends one per tick. */
const FIRST_TICK = 200;
/** Metres per tick. A constant walk, so the true velocity is known exactly. */
const PER_TICK = 0.25;
/** The true rendered velocity, in metres per millisecond — five metres a second. */
const TRUE_V = PER_TICK / TICK;

/** Where a constant walker is at a tick. */
const walkX = (tick: number) => (tick - FIRST_TICK) * PER_TICK;

/** When the server published a tick, on the local clock it is being compared to. */
const emitted = (tick: number) => ORIGIN + tick * TICK;

/**
 * Recovers the interpolator's own estimate of where tick zero sits on this
 * clock, by inverting a drawn position.
 *
 * It works because the transcripts walk at a constant speed, so a drawn `x` names
 * the tick it was drawn at exactly: `now − delay` is that tick's local time, and
 * the difference between the two is the estimate. It is the only honest way to
 * see the estimator from outside — the offset is deliberately not exported, since
 * nothing drawn depends on its absolute value.
 *
 * Only meaningful while the drawn instant is INTERIOR to the buffer; at either
 * end the buffer holds rather than interpolates, and a held position names no
 * particular tick.
 */
function impliedOffset(i: ReturnType<typeof build>, nowMs: number): number {
  const x = i.sample(nowMs).peers[0].x;
  const tick = FIRST_TICK + x / PER_TICK;
  return nowMs - DELAY - tick * TICK;
}

describe('the timeline is the server tick, not the arrival', () => {
  it('renders a constant walk at a constant speed through jitter, loss and a burst', () => {
    // THE DEFECT THIS MODULE WAS RE-KEYED TO REMOVE, as one transcript.
    //
    // The server publishes every tick, at a perfectly fixed rate, while the peer
    // walks at a constant speed. The network then does what a phone's network
    // does: consecutive frames land 9 ms apart and then 104 ms apart, one is
    // dropped outright, and two arrive together a millisecond apart.
    //
    // Keyed on ARRIVAL those gaps ARE the timeline, so the same ground covered in
    // 9 ms as in 104 makes the peer walk at several times his speed and then at a
    // fraction of it — on precisely the stretch where a dodge is being judged and
    // a shot is being led — and the pair that arrives together is a step taken in
    // a millisecond. Keyed on the TICK every one of those gaps is one step wide,
    // and the drawn velocity is flat to within the creep.
    //
    // TWO PROPERTIES OF THE TRANSCRIPT ARE DELIBERATE, because both are about a
    // claim this test is NOT making:
    //
    //   * The least-delayed frame is the FIRST. The minimum estimator re-anchors
    //     the whole timeline the first time it sees a faster frame, which is a
    //     one-off step rather than a velocity; the estimator's own tests below
    //     are what pin that.
    //   * Nothing is late by more than the served delay absorbs. A frame later
    //     than that leaves the buffer with nothing to interpolate towards, and
    //     then HOLDING is the correct answer rather than a speed — which is why a
    //     burst here is two frames, and why the long burst is a separate test
    //     about the buffer's span.
    const i = build();
    // One-way delay per tick, in arrival order; `null` is the frame the hub
    // dropped, which it does on purpose because a snapshot is idempotent full
    // state. The pair at the end arrives 1 ms apart.
    const delays: (number | null)[] = [8, 20, 74, 33, 75, 40, null, 25, 60, 15, 70, 60, 11, 45, 22];

    const arrivals: { tick: number; at: number }[] = [];
    delays.forEach((d, n) => {
      if (d === null) return;
      const tick = FIRST_TICK + n;
      arrivals.push({ tick, at: emitted(tick) + d });
    });

    // Interleaved exactly as the game runs it: frames land while the render loop
    // is already drawing, rather than all being pushed up front — so the clock
    // estimate is still moving under the samples being taken.
    let next = 0;
    const drawn: { at: number; x: number }[] = [];
    const lastArrival = arrivals[arrivals.length - 1].at;
    for (let now = arrivals[0].at; now <= lastArrival + 200; now += 16) {
      while (next < arrivals.length && arrivals[next].at <= now) {
        i.push([peer(1, walkX(arrivals[next].tick))], [], arrivals[next].tick, arrivals[next].at);
        next++;
      }
      const out = i.sample(now).peers;
      if (out.length) drawn.push({ at: now, x: out[0].x });
    }

    // Measured over the INTERIOR only: before the first frame's own instant and
    // after the last one's the buffer holds, and a hold is a velocity of zero
    // that has nothing to say about jitter.
    const maxX = walkX(FIRST_TICK + delays.length - 1);
    const speeds: number[] = [];
    for (let n = 1; n < drawn.length; n++) {
      const a = drawn[n - 1];
      const b = drawn[n];
      if (a.x <= 0 || b.x >= maxX) continue;
      speeds.push((b.x - a.x) / (b.at - a.at));
    }

    // Enough of the walk to be a claim about the walk rather than about one gap.
    expect(speeds.length).toBeGreaterThan(20);
    for (const v of speeds) {
      // Within a few per cent, throughout. Measured against the arrival-keyed
      // version of this module, the same transcript runs from 0.48× to 3.76× the
      // true speed — which is what the player was watching before this change.
      expect(v).toBeGreaterThan(TRUE_V * 0.95);
      expect(v).toBeLessThan(TRUE_V * 1.05);
    }
  });

  it('rides straight over a dropped frame', () => {
    // On a tick timeline a gap is self-describing: the missing tick is simply not
    // there, and the two frames either side are two steps apart.
    const i = build();
    i.push([peer(1, 0)], [], 100, 1000);
    i.push([peer(1, 20)], [], 102, 1100); // tick 101 never arrived
    // Half way between them in time is half way between them in space.
    expect(i.sample(1050 + DELAY).peers[0].x).toBeCloseTo(10, 6);
  });

  it('keeps a burst of buffered frames spaced as the server sent them', () => {
    // A phone coming out of a tunnel is handed everything the hub still had, in
    // order, within a few milliseconds. On an ARRIVAL timeline that is a world
    // where six ticks happened in six milliseconds and the buffer's whole span
    // collapses to nothing; on the server's timeline they carry their own
    // spacing, however long they spent queued.
    const i = build();
    i.push([peer(1, 0)], [], 200, 1000);
    for (let n = 1; n <= 6; n++) i.push([peer(1, n * 10)], [], 200 + n, 1500 + n);
    /** When the last of the burst landed — the six arrive a millisecond apart. */
    const settled = 1506;

    // WHERE THE TIMELINE ENDS UP ANCHORED, because the instants below are named
    // from it. Each burst frame lands a millisecond after the one before while
    // describing a tick fifty milliseconds later, so each looks less delayed than
    // the last and the final one is the least-delayed frame the estimator has
    // ever seen — its own arrival is therefore where that tick sits on this
    // clock. (The frame from before the stall was better evidence still, but by
    // half a second beyond the interpolation budget, so the ceiling let it go
    // rather than hold every peer frozen against it; see `maxExcess`.)
    //
    // A hundred milliseconds of the server's timeline is then two ticks of
    // walking, whether the two frames describing them arrived a hundred
    // milliseconds apart or one.
    const early = i.sample(settled + DELAY - 200).peers[0].x;
    const later = i.sample(settled + DELAY - 100).peers[0].x;
    expect(later - early).toBeCloseTo(20, 6);
    // And past everything held it holds at the newest, which is the documented
    // answer to a sender that has gone quiet.
    expect(i.sample(4000).peers[0].x).toBeCloseTo(60, 6);
  });
});

describe('the clock offset estimator', () => {
  it('converges on the least-delayed frame it has seen', () => {
    // The best evidence of the true offset is the frame that spent least time in
    // the network, so the estimate is the minimum of `arrival − tick × period`
    // and the drawn instant sits that one-way delay behind the server's.
    const i = build();
    const delays = [90, 40, 70, 25, 60, 80, 35, 55, 45, 75];
    for (let n = 0; n < delays.length; n++) {
      const tick = FIRST_TICK + n;
      i.push([peer(1, walkX(tick))], [], tick, emitted(tick) + delays[n]);
    }
    // The middle of what is buffered, so the drawn instant is interpolated rather
    // than held.
    expect(impliedOffset(i, emitted(FIRST_TICK + 5) + DELAY)).toBeCloseTo(
      ORIGIN + Math.min(...delays),
      0,
    );
  });

  it('is not pinned for ever by one unusually early frame', () => {
    // A minimum is a ratchet, and without the creep a single lucky packet would
    // hold the estimate for the whole visit — after which any drift between
    // the two machines' clocks pushes the drawn instant past the newest frame
    // held, which shows as every peer pausing more and more often.
    const i = build();
    i.push([peer(1, walkX(FIRST_TICK))], [], FIRST_TICK, emitted(FIRST_TICK));
    const settled = 100;
    for (let n = 1; n <= 400; n++) {
      const tick = FIRST_TICK + n;
      i.push([peer(1, walkX(tick))], [], tick, emitted(tick) + settled);
    }
    const off = impliedOffset(i, emitted(FIRST_TICK + 390) + DELAY);
    // It has moved a long way off the pin...
    expect(off).toBeGreaterThan(ORIGIN + 10);
    // ...towards the delay everything else actually arrived at, and never past
    // it: creeping above the real minimum would draw peers in the future.
    expect(off).toBeLessThan(ORIGIN + settled);
  });

  it('does not chase jitter that stays inside the budget', () => {
    // The creep is a defence against a ratchet, not a second estimator. One
    // frame's worth of it has to be far below the jitter it sits underneath, or
    // the offset would chase the noise and take the drawn instant with it.
    //
    // EVERY DELAY HERE IS STILL DRAWABLE, which is the distinction the estimator
    // makes and this test's whole point. The ceiling is `DELAY − TICK`, so a
    // frame up to 70 ms behind the fastest one still has a bracketing pair and
    // is left alone; the test below is what happens when one does not.
    const i = build();
    const delays = [20, 85, 60, 88, 55, 80, 62, 87];
    for (let n = 0; n < delays.length; n++) {
      const tick = FIRST_TICK + n;
      i.push([peer(1, walkX(tick))], [], tick, emitted(tick) + delays[n]);
    }
    const off = impliedOffset(i, emitted(FIRST_TICK + 4) + DELAY);
    // Eight frames of creep under sixty-odd milliseconds of jitter move the
    // estimate by well under a millisecond, and only upwards.
    expect(off).toBeGreaterThanOrEqual(ORIGIN + 20);
    expect(off).toBeLessThan(ORIGIN + 21);
  });

  it('follows a persistent step in the delay at once rather than in fifty seconds', () => {
    // A minimum is right about jitter and wrong about a SHIFT: one early packet
    // is then the only evidence the estimator has, and everything after it is
    // an excess the creep is far too slow to work off. The ceiling is what turns
    // "the minimum I saw" into "the minimum I can still draw".
    const i = build();
    const lucky = 20;
    const step = 200;
    for (let n = 0; n < 10; n++) {
      const tick = FIRST_TICK + n;
      i.push([peer(1, walkX(tick))], [], tick, emitted(tick) + (n === 0 ? lucky : step));
    }
    const off = impliedOffset(i, emitted(FIRST_TICK + 6) + DELAY);

    // Pulled up to `seen − maxExcess` by the FIRST frame that exceeded it —
    // 200 − (120 − 50) — and creeping gently on from there.
    const ceiling = ORIGIN + step - (DELAY - TICK);
    expect(off).toBeGreaterThanOrEqual(ceiling);
    expect(off).toBeLessThan(ceiling + 5);
    // Never past the delay everything actually arrives at: creeping above the
    // real minimum would draw peers in the future.
    expect(off).toBeLessThan(ORIGIN + step);
    // And the size of what the ceiling did. Creep alone moves 0.1 % of the error
    // per frame, so after these ten it would still be within two milliseconds of
    // the lucky packet, and would need something like a thousand frames — fifty
    // seconds at twenty a second — to reach where this already is.
    expect(off).toBeGreaterThan(ORIGIN + lucky + 100);
  });

  it('keeps peers moving after a lucky packet and a delay that never comes back down', () => {
    // THE MEASUREMENT THE CEILING EXISTS FOR, and the one test in this file that
    // a creep-only estimator fails. The two above look at the estimate; this one
    // looks at what a player sees, by driving the module through a render loop
    // exactly as the view does — frames landing while it is already drawing.
    //
    // One 20 ms packet, then a one-way delay of 200 ms that never improves: the
    // sort of thing a phone does on handover. Against creep only, every single
    // sample is HELD at the newest frame — the drawn instant has been left
    // 180 ms behind a 120 ms budget — so peers move only when a snapshot lands
    // and stand still between, and two of every three drawn pairs are the
    // identical position. That is a minute of juddering bought by one lucky
    // packet.
    const i = build();
    const arrivals: { tick: number; at: number }[] = [];
    for (let n = 0; n < 60; n++) {
      const tick = FIRST_TICK + n;
      arrivals.push({ tick, at: emitted(tick) + (n === 0 ? 20 : 200) });
    }

    let next = 0;
    const drawn: { x: number; newest: number }[] = [];
    for (let now = arrivals[0].at; now <= arrivals[arrivals.length - 1].at; now += 16) {
      while (next < arrivals.length && arrivals[next].at <= now) {
        i.push([peer(1, walkX(arrivals[next].tick))], [], arrivals[next].tick, arrivals[next].at);
        next++;
      }
      // The warm-up is skipped rather than measured. Until a few frames are in,
      // the drawn instant is still BEFORE the oldest one buffered, and showing
      // that oldest frame is a documented degradation rather than the defect
      // under test — it just happens to look identical from outside.
      if (next < 4) continue;
      drawn.push({ x: i.sample(now).peers[0].x, newest: walkX(arrivals[next - 1].tick) });
    }

    // Enough draws to be a claim about the connection rather than about a frame.
    expect(drawn.length).toBeGreaterThan(100);
    // Not one of them is the newest frame's own position, which is what holding
    // looks like from outside: every peer is genuinely being interpolated.
    expect(drawn.filter((d) => d.x >= d.newest)).toHaveLength(0);
    // And the walk never pauses — no two consecutive frames draw him in the same
    // place, which is the thing the player was actually complaining about.
    let frozen = 0;
    for (let n = 1; n < drawn.length; n++) if (drawn[n].x === drawn[n - 1].x) frozen++;
    expect(frozen).toBe(0);
  });

  it('starts a fresh timeline when the ticks restart under it', () => {
    // A regenerated заброшка — or a restarted process — is a different building,
    // and its clock begins again at nothing. Treated as late frames those would
    // be dropped for ever, and every peer would freeze for as long as this
    // client stayed connected.
    const i = build();
    i.push([peer(1, 0)], [], 900, 1000);
    i.push([peer(1, 10)], [], 902, 1100);
    i.push([peer(1, 50)], [], 3, 1200);
    i.push([peer(1, 99)], [], 5, 1300);
    expect(i.size()).toBe(2);
    expect(i.sample(1300 + DELAY).peers[0].x).toBeCloseTo(99, 6);
  });

  it('forgets the offset on reset, not merely the frames', () => {
    // The timeline belongs to ONE BUILDING, and reset is what a regenerated
    // заброшка does to this buffer. The building that replaces it counts its
    // ticks from zero, so an estimate made against the previous one would put
    // every frame of the new one minutes into the past.
    const i = build();
    i.push([peer(1, 5)], [], 900, 1000);
    i.reset();
    expect(i.size()).toBe(0);
    expect(i.sample(2000).peers).toEqual([]);
    i.push([peer(1, 1)], [], 2, 9000);
    i.push([peer(1, 2)], [], 4, 9100);
    expect(i.sample(9100 + DELAY).peers[0].x).toBeCloseTo(2, 6);
  });
});

describe('what arrives out of order', () => {
  it('ignores a late frame rather than sorting it in', () => {
    const i = build();
    i.push([peer(1, 0)], [], 20, 1000);
    i.push([peer(1, 10)], [], 22, 1100);
    i.push([peer(1, 999)], [], 21, 1050); // late, and a tick already passed
    expect(i.size()).toBe(2);
    expect(i.sample(1100 + DELAY).peers[0].x).toBeCloseTo(10, 6);
    expect(i.sample(1050 + DELAY).peers[0].x).toBeCloseTo(5, 6);
  });

  it('ignores a duplicate tick, which would otherwise be a span of zero', () => {
    const i = build();
    i.push([peer(1, 0)], [], 20, 1000);
    i.push([peer(1, 10)], [], 22, 1100);
    i.push([peer(1, 999)], [], 22, 1110);
    expect(i.size()).toBe(2);
    expect(i.sample(1100 + DELAY).peers[0].x).toBeCloseTo(10, 6);
  });
});

describe('drawing the past', () => {
  it('draws nothing before anything has arrived', () => {
    // A peer never heard of has no last known position to hold.
    expect(build().sample(1000).peers).toEqual([]);
  });

  it('interpolates between the two frames bracketing the render instant', () => {
    const i = build();
    i.push([peer(1, 0)], [], 20, 1000);
    i.push([peer(1, 10)], [], 22, 1100);
    const [p] = i.sample(1050 + DELAY).peers;
    expect(p.x).toBeCloseTo(5, 6);
  });

  it('draws the past, not the present — that is the whole idea', () => {
    const i = build();
    i.push([peer(1, 0)], [], 20, 1000);
    i.push([peer(1, 10)], [], 22, 1100);
    // A whole delay after the newest frame, its world is the one on screen.
    expect(i.sample(1100 + DELAY).peers[0].x).toBeCloseTo(10, 6);
    // ...and a moment earlier we are genuinely behind it.
    expect(i.sample(1060 + DELAY).peers[0].x).toBeLessThan(10);
  });

  it('holds the newest frame rather than extrapolating when the buffer runs dry', () => {
    // Extrapolation is what makes a peer walk through a wall and snap back.
    // Holding makes them pause: a worse-looking correct answer, and correct
    // wins.
    const i = build();
    i.push([peer(1, 0)], [], 20, 1000);
    i.push([peer(1, 10)], [], 22, 1100);
    expect(i.sample(5000).peers[0].x).toBe(10);
  });

  it('shows the oldest frame when asked about a time before it', () => {
    // Stale, and saying so by being visibly behind beats guessing.
    const i = build();
    i.push([peer(1, 7)], [], 20, 1000);
    expect(i.sample(1000).peers[0].x).toBe(7);
  });

  it('shows a peer that appeared mid-gap at the position it appeared in', () => {
    const i = build();
    i.push([peer(1, 0)], [], 20, 1000);
    i.push([peer(1, 10), peer(2, 42)], [], 22, 1100);
    const out = i.sample(1050 + DELAY).peers;
    expect(out.find((p) => p.slot === 2)?.x).toBe(42);
  });

  it('drops a peer that is no longer in the newer frame', () => {
    // Which now means two things and announces neither: he left the building, or
    // he walked two rooms away and the snapshot's own filter stopped carrying
    // him. A figure kept alive because nothing said to remove it is a man
    // standing in a doorway he left a minute ago.
    const i = build();
    i.push([peer(1, 0), peer(2, 1)], [], 20, 1000);
    i.push([peer(1, 10)], [], 22, 1100);
    expect(i.sample(1050 + DELAY).peers.map((p) => p.slot)).toEqual([1]);
  });

  it('takes the short way round when a peer turns through the wrap point', () => {
    // The single most visible bug in naive angle interpolation: without this a
    // peer turning from just under π to just over −π spins a full circle on the
    // spot.
    const i = build();
    i.push([peer(1, 0, Math.PI - 0.1)], [], 20, 1000);
    i.push([peer(1, 0, -Math.PI + 0.1)], [], 22, 1100);
    const yaw = i.sample(1050 + DELAY).peers[0].yaw;
    expect(Math.abs(yaw)).toBeGreaterThan(Math.PI - 0.2);
  });

  it('carries a peer’s state from the newer frame, undiluted', () => {
    // A shot happened on one tick; an enumeration has no midpoint, so it is
    // taken from the frame the drawn instant is moving TOWARDS rather than
    // blended out of the one it has already passed. Which is also why it stays
    // set for as long as that frame is the newer of the pair, and why whoever
    // draws an INSTANT has to mark the transition rather than the value — see
    // vanyadumFlash.
    const i = build();
    i.push([peer(1, 0)], [], 20, 1000);
    i.push([{ ...peer(1, 10), st: PEER_FIRED }], [], 22, 1100);
    expect(i.sample(1050 + DELAY).peers[0].st).toBe(PEER_FIRED);
    // And once that tick is behind the drawn instant it is over — the value is
    // on one frame, never smeared forward into the next one. Spelled out as a
    // zero rather than left off, because that is what `decodePeers` produces: it
    // sets the field on every peer so the object it builds has one shape.
    i.push([{ ...peer(1, 20), st: 0 }], [], 24, 1200);
    expect(i.sample(1150 + DELAY).peers[0].st).toBe(0);
  });

  it('carries a state that LASTS on every frame of it, so nothing has to hold it', () => {
    // The other half of the same field, and the reason the two are drawn
    // differently. Being down is true of every tick it lasts, so a viewer whose
    // buffer skips a frame never loses a corpse the way he can lose a muzzle
    // flash — there is nothing to mark and nothing to remember.
    const i = build();
    i.push([{ ...peer(1, 0), st: PEER_DOWN }], [], 20, 1000);
    i.push([{ ...peer(1, 0), st: PEER_DOWN }], [], 22, 1100);
    expect(i.sample(1050 + DELAY).peers[0].st).toBe(PEER_DOWN);
  });

  it('interpolates the height too, so a doorway is a step up and not a jump', () => {
    // The height is DERIVED at ingest, from the room the wire named, precisely so
    // that it can be blended like the position. Resolved at draw time instead it
    // would be a discrete value changing on the frame the peer crossed the
    // threshold, and everybody would hop up steps.
    const i = build();
    i.push([{ ...peer(1, 0), z: 1.65 }], [], 20, 1000);
    i.push([{ ...peer(1, 10), z: 2.05 }], [], 22, 1100);
    expect(i.sample(1050 + DELAY).peers[0].z).toBeCloseTo(1.85, 6);
  });

  it('forgets one slot without pausing anybody else', () => {
    // A slot is a place, not a person: it is handed back when its holder leaves
    // and given to the next arrival, so blending across the hand-over draws one
    // man sliding into another man's position. Only the changed place goes —
    // `reset` would stall every other peer every time anybody walked in.
    const i = build();
    i.push([peer(1, 0), peer(2, 100)], [], 20, 1000);
    i.push([peer(1, 10), peer(2, 200)], [], 22, 1100);
    i.forget(1);
    const out = i.sample(1050 + DELAY).peers;
    expect(out.map((p) => p.slot)).toEqual([2]);
    expect(out[0].x).toBeCloseTo(150, 6);
    // The frames themselves are untouched — it is one peer that was dropped, not
    // the timeline.
    expect(i.size()).toBe(2);
  });

  it('bounds the buffer, so a tab left open does not accumulate a world', () => {
    const i = build();
    for (let n = 0; n < 500; n++) i.push([peer(1, n)], [], 100 + n, 1000 + n * TICK);
    expect(i.size()).toBeLessThanOrEqual(BUFFER_FRAMES);
    // And still draws correctly from what it kept.
    expect(i.sample(1000 + 499 * TICK + DELAY).peers[0].x).toBeCloseTo(499, 6);
  });
});

describe('shortestTurn', () => {
  it('answers the signed short way round', () => {
    expect(shortestTurn(0, 1)).toBeCloseTo(1, 9);
    expect(shortestTurn(0, -1)).toBeCloseTo(-1, 9);
    expect(shortestTurn(Math.PI - 0.1, -Math.PI + 0.1)).toBeCloseTo(0.2, 6);
    expect(shortestTurn(-Math.PI + 0.1, Math.PI - 0.1)).toBeCloseTo(-0.2, 6);
  });

  it('never returns more than half a turn', () => {
    for (let a = -10; a < 10; a += 0.37) {
      for (let b = -10; b < 10; b += 0.53) {
        expect(Math.abs(shortestTurn(a, b))).toBeLessThanOrEqual(Math.PI + 1e-9);
      }
    }
  });
});

describe('the нейрослопы ride the same buffer as the people', () => {
  /** One creature, addressed by the id the wire names it by. */
  const foe = (id: number, x: number, z = 0) => ({ id, x, y: 0, z });

  it('is drawn at the same instant as the man standing next to it', () => {
    // THE WHOLE REASON THEY SHARE THIS BUFFER. The server rewinds the building
    // by exactly `interp_delay_ms` to resolve a shot at either, so two buffers
    // estimating the same clock offset from the same arrivals would be two
    // answers to keep in step by hand — and the day they drifted, a слоп would
    // be a thing you have to lead differently from a person, for no reason a
    // player could ever discover.
    const i = build();
    i.push([peer(1, 0)], [foe(0, 0)], 20, 1000);
    i.push([peer(1, 10)], [foe(0, 10)], 22, 1100);
    const drawn = i.sample(1050 + DELAY);
    expect(drawn.peers[0].x).toBeCloseTo(5, 6);
    expect(drawn.slops[0].x).toBeCloseTo(5, 6);
  });

  it('interpolates a creature between the two frames that bracket the instant', () => {
    const i = build();
    i.push([], [foe(0, 0), foe(1, 100)], 20, 1000);
    i.push([], [foe(0, 20), foe(1, 200)], 22, 1100);
    const { slops } = i.sample(1050 + DELAY);
    expect(slops.map((s) => s.id)).toEqual([0, 1]);
    expect(slops[0].x).toBeCloseTo(10, 6);
    expect(slops[1].x).toBeCloseTo(150, 6);
  });

  it('glides one up a step rather than snapping it, because the height is blended', () => {
    // A слоп walks through doorways between rooms at different floor heights,
    // and the height is derived at ingest for exactly this reason.
    const i = build();
    i.push([], [foe(0, 0, 0)], 20, 1000);
    i.push([], [foe(0, 10, 0.4)], 22, 1100);
    expect(i.sample(1050 + DELAY).slops[0].z).toBeCloseTo(0.2, 6);
  });

  it('draws a creature that appeared during the gap where it actually is', () => {
    // There is nothing to interpolate from, and blending towards it from
    // anywhere would drag a слоп across the заброшка on the frame it walked
    // into view — or on the frame the building put a new one somewhere else.
    const i = build();
    i.push([], [], 20, 1000);
    i.push([], [foe(0, 42)], 22, 1100);
    expect(i.sample(1050 + DELAY).slops[0].x).toBe(42);
  });

  it('stops drawing one the newest frame does not name', () => {
    // ABSENCE IS THE WHOLE OF DYING, and of walking out of the rooms this
    // reader can see into. Either way, keeping a creature alive because nothing
    // said to remove it leaves something standing in a doorway it left a minute
    // ago — and this one would be standing there after it had been shot.
    const i = build();
    i.push([], [foe(0, 0), foe(1, 5)], 20, 1000);
    i.push([], [foe(1, 6)], 22, 1100);
    expect(i.sample(1050 + DELAY).slops.map((s) => s.id)).toEqual([1]);
  });

  it('answers with nothing at all before any frame has arrived', () => {
    expect(build().sample(1000)).toEqual({ peers: [], slops: [] });
  });

  it('holds the newest frame rather than walking a creature into a wall', () => {
    // The same degradation the peers get: extrapolation is what makes something
    // walk through a wall and snap back, and a pause is a worse-looking correct
    // answer.
    const i = build();
    i.push([], [foe(0, 0)], 20, 1000);
    i.push([], [foe(0, 10)], 22, 1100);
    expect(i.sample(9000).slops[0].x).toBe(10);
  });

  it('is emptied with everything else when the building is thrown away', () => {
    const i = build();
    i.push([peer(1, 0)], [foe(0, 0)], 20, 1000);
    i.reset();
    expect(i.sample(1050 + DELAY)).toEqual({ peers: [], slops: [] });
  });
});
