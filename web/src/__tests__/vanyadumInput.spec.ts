import { describe, expect, it } from 'vitest';
import {
  LOOK_SENSITIVITY,
  MAX_MOUSE_DELTA,
  MAX_PULL_LATCH_MS,
  MOUSE_SENSITIVITY,
  STICK_DEADZONE,
  applyLook,
  axesFromKeys,
  buildInputFrame,
  createEmitter,
  createPullLatch,
  mouseLook,
  pullLatchMs,
  stickVector,
  wrapAngle,
  type VanyadumCommand,
} from '../lib/vanyadumInput';

describe('stickVector', () => {
  const origin = { x: 100, y: 100 };

  it('walks forward when the thumb goes up the screen', () => {
    // Screen +y is downwards, so forward is a negative dy. Getting this
    // backwards is the classic inverted-controls bug and it is not obvious from
    // reading the code, so it is pinned.
    const v = stickVector(origin, { x: 100, y: 50 }, 50);
    expect(v.my).toBeCloseTo(1, 6);
    expect(v.mx).toBeCloseTo(0, 6);
  });

  it('strafes right when the thumb goes right', () => {
    const v = stickVector(origin, { x: 150, y: 100 }, 50);
    expect(v.mx).toBeCloseTo(1, 6);
    expect(v.my).toBeCloseTo(0, 6);
  });

  it('ignores a thumb resting inside the dead zone', () => {
    // Without this the player drifts while standing still, which reads as the
    // game being possessed rather than as a sensitivity problem.
    const nudge = STICK_DEADZONE * 0.5 * 50;
    expect(stickVector(origin, { x: 100 + nudge, y: 100 }, 50)).toEqual({ mx: 0, my: 0 });
  });

  it('clamps rather than normalises, so half a push is half speed', () => {
    const half = stickVector(origin, { x: 100, y: 75 }, 50);
    expect(half.my).toBeCloseTo(0.5, 6);

    const beyond = stickVector(origin, { x: 100, y: -400 }, 50);
    expect(beyond.my).toBeCloseTo(1, 6);
  });

  it('answers zero for a zero radius instead of dividing by it', () => {
    expect(stickVector(origin, { x: 200, y: 200 }, 0)).toEqual({ mx: 0, my: 0 });
  });
});

describe('applyLook', () => {
  it('turns right when you drag right', () => {
    // The server's convention read back: yaw zero faces world +Y and increasing
    // yaw swings towards +X, which is a right turn seen from above.
    const next = applyLook({ yaw: 0, pitch: 0 }, 100, 0, 1.5);
    expect(next.yaw).toBeCloseTo(100 * LOOK_SENSITIVITY, 6);
  });

  it('looks down when you drag down', () => {
    const next = applyLook({ yaw: 0, pitch: 0 }, 0, 100, 1.5);
    expect(next.pitch).toBeLessThan(0);
  });

  it('clamps pitch, because beyond straight up the horizon rolls', () => {
    expect(applyLook({ yaw: 0, pitch: 0 }, 0, -100000, 1.5).pitch).toBe(1.5);
    expect(applyLook({ yaw: 0, pitch: 0 }, 0, 100000, 1.5).pitch).toBe(-1.5);
  });

  it('wraps yaw, so a long run of turning never grows it without bound', () => {
    let state = { yaw: 0, pitch: 0 };
    for (let i = 0; i < 500; i++) state = applyLook(state, 100, 0, 1.5);
    expect(Math.abs(state.yaw)).toBeLessThanOrEqual(Math.PI + 1e-9);
  });
});

describe('mouseLook', () => {
  const level = { yaw: 0, pitch: 0 };

  it('turns the same way a drag does, only finer', () => {
    // Same convention as the thumb — otherwise the two inputs would disagree
    // about which way right is — at the mouse's own, lower sensitivity.
    const m = mouseLook(level, 100, 0, 1.5);
    expect(m.yaw).toBeCloseTo(100 * MOUSE_SENSITIVITY, 9);
    expect(m.yaw).toBeGreaterThan(0);
    expect(Math.abs(m.yaw)).toBeLessThan(Math.abs(applyLook(level, 100, 0, 1.5).yaw));
  });

  it('looks down when the mouse goes down', () => {
    expect(mouseLook(level, 0, 100, 1.5).pitch).toBeLessThan(0);
  });

  it('clamps the enormous first delta a lock hands over', () => {
    // NOT a taste decision. Chromium reports the first movement after a capture
    // as the delta from wherever the cursor was on the desktop, which on a wide
    // monitor is thousands of pixels — so without the clamp, clicking to grab
    // the mouse spins the player round before they have moved it.
    const spike = mouseLook(level, 4000, 0, 1.5);
    const capped = mouseLook(level, MAX_MOUSE_DELTA, 0, 1.5);
    expect(spike.yaw).toBeCloseTo(capped.yaw, 12);
    expect(mouseLook(level, -4000, 0, 1.5).yaw).toBeCloseTo(-capped.yaw, 12);
  });

  it('leaves anything below the cap exactly alone', () => {
    // The clamp must be a ceiling, not a scale — an ordinary movement has to
    // arrive unchanged or the whole feel is wrong.
    const d = MAX_MOUSE_DELTA - 1;
    expect(mouseLook(level, d, 0, 1.5).yaw).toBeCloseTo(d * MOUSE_SENSITIVITY, 9);
  });

  it('refuses a non-finite delta instead of poisoning the view', () => {
    // A MouseEvent built without the movement fields reports them as undefined,
    // which arrives as NaN. That would wreck yaw permanently AND make every
    // input frame afterwards malformed, because JSON turns a NaN into null and
    // the server drops the frame — a run that keeps going but cannot move, with
    // nothing on screen saying why.
    for (const bad of [Number.NaN, Infinity, -Infinity, undefined as unknown as number]) {
      const m = mouseLook({ yaw: 0.3, pitch: -0.2 }, bad, bad, 1.5);
      expect(Number.isFinite(m.yaw)).toBe(true);
      expect(Number.isFinite(m.pitch)).toBe(true);
      expect(m.yaw).toBeCloseTo(0.3, 12);
      expect(m.pitch).toBeCloseTo(-0.2, 12);
    }
  });

  it('still clamps pitch, however hard the mouse is thrown', () => {
    let state = { yaw: 0, pitch: 0 };
    for (let i = 0; i < 200; i++) state = mouseLook(state, 0, -MAX_MOUSE_DELTA, 1.5);
    expect(state.pitch).toBe(1.5);
  });
});

describe('wrapAngle', () => {
  it('brings any angle into −π..π', () => {
    expect(wrapAngle(0)).toBe(0);
    expect(wrapAngle(Math.PI * 3)).toBeCloseTo(Math.PI, 6);
    expect(wrapAngle(-Math.PI * 3)).toBeCloseTo(-Math.PI, 6);
    expect(wrapAngle(Math.PI * 100.5)).toBeGreaterThanOrEqual(-Math.PI);
  });
});

describe('axesFromKeys', () => {
  it('maps WASD and the arrows the same way', () => {
    expect(axesFromKeys(new Set(['KeyW']))).toEqual({ mx: 0, my: 1 });
    expect(axesFromKeys(new Set(['ArrowUp']))).toEqual({ mx: 0, my: 1 });
    expect(axesFromKeys(new Set(['KeyA']))).toEqual({ mx: -1, my: 0 });
    expect(axesFromKeys(new Set(['KeyS', 'KeyD']))).not.toEqual({ mx: 1, my: -1 });
  });

  it('cancels opposite keys instead of preferring one', () => {
    expect(axesFromKeys(new Set(['KeyW', 'KeyS']))).toEqual({ mx: 0, my: 0 });
  });

  it('does not make a diagonal faster than a straight line', () => {
    const d = axesFromKeys(new Set(['KeyW', 'KeyD']));
    expect(Math.hypot(d.mx, d.my)).toBeCloseTo(1, 6);
  });

  it('ignores keys it does not know', () => {
    expect(axesFromKeys(new Set(['Space', 'ShiftLeft']))).toEqual({ mx: 0, my: 0 });
  });
});

describe('createEmitter', () => {
  const opts = { hz: 40, maxStepSeconds: 0.2, maxPerWake: 4 };
  const walking = { mx: 0, my: 1, yaw: 0.5, pitch: 0 };
  const still = { mx: 0, my: 0, yaw: 0.5, pitch: 0 };

  it('produces nothing from a single reading, because dt needs two', () => {
    const e = createEmitter(opts);
    expect(e.due(1000, walking)).toEqual([]);
  });

  it('emits at its own fixed rate, not at the frame rate', () => {
    // THE reason this replaced a per-frame sampler: whatever is predicted must
    // be exactly what is sent, so the number of commands in a send window has
    // to be a property of the emitter rather than of the phone's refresh rate.
    const fast = createEmitter(opts);
    fast.due(0, walking);
    let n = 0;
    for (let t = 8; t <= 1000; t += 8) n += fast.due(t, walking).length; // 120 Hz

    const slow = createEmitter(opts);
    slow.due(0, walking);
    let m = 0;
    for (let t = 33; t <= 1000; t += 33) m += slow.due(t, walking).length; // 30 Hz

    expect(n).toBeGreaterThan(30);
    expect(Math.abs(n - m)).toBeLessThanOrEqual(2);
  });

  it('gives every command the same sub-step, so replay is exact', () => {
    const e = createEmitter(opts);
    e.due(0, walking);
    const cmds = e.due(200, walking);
    expect(cmds.length).toBeGreaterThan(0);
    for (const c of cmds) expect(c.dt).toBeCloseTo(1 / opts.hz, 9);
  });

  it('emits nothing at all while nobody is touching anything', () => {
    // A player at rest costs the network nothing. The naive version ships ten
    // frames a second of "dt of nothing" forever, to a phone on mobile data.
    const e = createEmitter(opts);
    e.due(0, still);
    let n = 0;
    for (let t = 25; t <= 2000; t += 25) n += e.due(t, still).length;
    expect(n).toBe(0);
  });

  it('but emits a turn, because aim is state the server has to be told about', () => {
    const e = createEmitter(opts);
    e.due(0, still);
    expect(e.due(30, { ...still, yaw: 0.9 }).length).toBe(1);
  });

  it('caps what one wake may produce, so a stalled tab cannot flood', () => {
    // The server's time budget would refuse the surplus anyway; not creating it
    // is what keeps the client's own prediction agreeing with that refusal.
    const e = createEmitter(opts);
    e.due(0, walking);
    expect(e.due(60_000, walking).length).toBeLessThanOrEqual(opts.maxPerWake);
  });

  it('carries fractional leftovers rather than dropping them', () => {
    // Dropping them makes the client's simulated time drift behind the
    // server's, which shows up as a correction always in the same direction.
    const e = createEmitter(opts);
    e.due(0, walking);
    let n = 0;
    for (let t = 30; t <= 3000; t += 30) n += e.due(t, walking).length;
    // ~3 s at 40 Hz is ~120 commands; a dropped remainder would give ~100.
    expect(n).toBeGreaterThan(110);
  });

  it('forgets everything on reset, so a new run starts clean', () => {
    const e = createEmitter(opts);
    e.due(0, walking);
    e.due(200, walking);
    e.reset();
    expect(e.due(5000, walking)).toEqual([]);
  });

  it('owes the renderer nothing until it has an interval to measure', () => {
    const e = createEmitter(opts);
    expect(e.residualSeconds()).toBe(0);
    // The first reading only starts the clock; there is no elapsed time in it.
    e.due(1000, walking);
    expect(e.residualSeconds()).toBe(0);
  });

  it('grows the leftover as time passes and drops it when a command claims it', () => {
    // What makes the carry a carry: the renderer is only ever offered time that
    // has genuinely elapsed and that the next command is about to take.
    const e = createEmitter(opts);
    e.due(0, walking);
    e.due(10, walking);
    expect(e.residualSeconds()).toBeCloseTo(0.01, 9);
    e.due(20, walking);
    expect(e.residualSeconds()).toBeCloseTo(0.02, 9);
    // Past the 25 ms period: one command is emitted and takes a period with it.
    expect(e.due(30, walking)).toHaveLength(1);
    expect(e.residualSeconds()).toBeCloseTo(0.005, 9);
  });

  it('emits for a trigger pull alone, from a player who is otherwise still', () => {
    // THE STATE THIS GAME'S DEFAULT ACTUALLY IS. Standing perfectly still with
    // the screen untouched emits nothing, which is right and is what keeps a
    // resting player free — but a pull is something that happened, and a pull
    // that produced no command would be a trigger the server never hears about.
    const e = createEmitter(opts);
    e.due(0, still);
    const cmds = e.due(30, still, true);
    expect(cmds).toHaveLength(1);
    expect(cmds[0].fire).toBe(true);
  });

  it('puts the pull on exactly one command of the wake', () => {
    // A pull is a moment, not a state. Four commands each claiming it would ask
    // the server to fire four times — refused by the cadence, but only after
    // being told — and would put nine bytes on all four.
    const e = createEmitter(opts);
    e.due(0, walking);
    const cmds = e.due(140, walking, true);
    expect(cmds.length).toBeGreaterThan(1);
    expect(cmds.filter((c) => c.fire).length).toBe(1);
    expect(cmds[0].fire).toBe(true);
  });

  it('leaves the trigger off every command that was not asked for one', () => {
    const e = createEmitter(opts);
    e.due(0, walking);
    for (const c of e.due(140, walking)) expect(c.fire).toBeUndefined();
  });

  it('never offers more than one period, however far behind it is', () => {
    // Beyond a period the arrears are a stall the wake budget is already
    // refusing, and drawing ahead of a refusal is extrapolation, not carry.
    const e = createEmitter(opts);
    e.due(0, walking);
    // 140 ms is five and a half periods but only four commands are allowed, so
    // 40 ms is left owing — more than the renderer may be handed.
    expect(e.due(140, walking)).toHaveLength(opts.maxPerWake);
    expect(e.residualSeconds()).toBeCloseTo(1 / opts.hz, 9);
  });
});

describe('buildInputFrame', () => {
  const cmd = (seq: number): VanyadumCommand => ({ seq, dt: 0.025, mx: 0, my: 1, yaw: 0, pitch: 0 });
  /**
   * The server's own cap — `MaxCommandsPerFrame + RedundantCommands`, both of
   * which are served on `sim`. Deliberately larger than anything the tests above
   * build, so only the tests that are ABOUT the cap ever meet it.
   */
  const CAP = 10;

  it('carries the tick this client last drew, so the server can derive the round trip', () => {
    // Derived rather than reported: lag compensation rewinds by exactly that
    // number, so a client choosing its own would be choosing an advantage.
    expect(buildInputFrame(42, [cmd(1)], [], CAP).k).toBe(42);
  });

  it('puts the unacknowledged tail first and the fresh commands last', () => {
    // The server applies them in order, so the resend has to come before the
    // new input or a replayed command would land after work that followed it.
    const frame = buildInputFrame(0, [cmd(5), cmd(6)], [cmd(3), cmd(4)], CAP);
    expect(frame.cmds.map((c) => c.q)).toEqual([3, 4, 5, 6]);
  });

  it('never sends the same sequence twice in one frame', () => {
    // The unacknowledged list still contains what was just applied, so without
    // the de-duplication every frame would carry its own fresh commands twice.
    const frame = buildInputFrame(0, [cmd(5)], [cmd(4), cmd(5)], CAP);
    expect(frame.cmds.map((c) => c.q)).toEqual([4, 5]);
  });

  it('sends intent and never a fact', () => {
    // The rule the whole design rests on: a prediction is something the client
    // draws, never something it asserts. No position, no health, no hit claim.
    const frame = buildInputFrame(7, [cmd(1)], [], CAP) as unknown as Record<string, unknown>;
    expect(Object.keys(frame).sort()).toEqual(['cmds', 'k', 't']);
    expect(Object.keys(frame.cmds as object[])).toHaveLength(1);
    for (const c of (frame.cmds as Record<string, unknown>[])) {
      expect(Object.keys(c).sort()).toEqual(['dt', 'mx', 'my', 'pitch', 'q', 'yaw']);
    }
  });

  it('is happy with nothing to resend', () => {
    expect(buildInputFrame(0, [cmd(1)], [], CAP).cmds.map((c) => c.q)).toEqual([1]);
  });

  it('carries the trigger on the command that had one', () => {
    const frame = buildInputFrame(0, [{ ...cmd(1), fire: true }], [], CAP);
    expect(frame.cmds[0].f).toBe(true);
  });

  it('omits the trigger rather than sending it false', () => {
    // Forty sub-steps a second go out for as long as somebody is walking, and
    // `"f":false` on every one of them is nine bytes forty times a second,
    // uplink, on mobile data, to say that nothing happened. The `sends intent
    // and never a fact` test above is what pins the resting key set; this says
    // why the trigger is not in it.
    const frame = buildInputFrame(0, [cmd(1), { ...cmd(2), fire: false }], [], CAP);
    for (const c of frame.cmds) expect('f' in c).toBe(false);
  });

  it('never builds a frame the server would cut', () => {
    // `parseInput` keeps the last `max_commands + redundant` and drops the rest,
    // so anything past the cap is uplink spent on commands nobody simulates. The
    // frame that produced it is ordinary: a send timer the browser ran late
    // leaves more fresh commands in the outbox than one wake may produce, and
    // the redundancy tail was being asked for on top of them.
    const fresh = [7, 8, 9, 10, 11, 12].map(cmd);
    const tail = [1, 2, 3, 4, 5, 6].map(cmd);
    const frame = buildInputFrame(0, fresh, tail, CAP);
    expect(frame.cmds).toHaveLength(CAP);
  });

  it('and cuts the stale half rather than the input the player is waiting for', () => {
    // The direction is the whole point, and it is the server's own: a resend
    // asks for no simulation and insures against a packet that was almost
    // certainly not lost, while a fresh command is something somebody just did.
    const fresh = [7, 8, 9, 10, 11, 12].map(cmd);
    const tail = [1, 2, 3, 4, 5, 6].map(cmd);
    const frame = buildInputFrame(0, fresh, tail, CAP);
    expect(frame.cmds.map((c) => c.q)).toEqual([3, 4, 5, 6, 7, 8, 9, 10, 11, 12]);
  });

  it('keeps the newest input even when the fresh commands alone overflow', () => {
    // A frame that is over-full with nothing to blame it on. Nothing here is
    // insurance, so what goes is the oldest of the real input — which is what
    // the server would have thrown away anyway, a round trip later.
    const fresh = Array.from({ length: CAP + 3 }, (_, i) => cmd(i + 1));
    const frame = buildInputFrame(0, fresh, [], CAP);
    expect(frame.cmds).toHaveLength(CAP);
    expect(frame.cmds[frame.cmds.length - 1].q).toBe(CAP + 3);
  });
});

describe('pullLatchMs', () => {
  it('stays under the served cadence, so a buffered pull cannot become a shot', () => {
    // The rule the bound exists for: a pull remembered for longer than the
    // cadence turns one tap during a reload into a second shot fired on the
    // player's behalf at a moment he was not asking for.
    for (const cadence of [0.35, 0.2, 0.1, 0.05]) {
      expect(pullLatchMs(cadence)).toBeLessThan(cadence * 1000);
    }
  });

  it('does not buffer a slow gun for a whole second', () => {
    // Half of a two-second cadence would be a second of memory, and a shot that
    // lands a second after the thumb left the button is not the shot anybody
    // asked for.
    expect(pullLatchMs(2)).toBe(MAX_PULL_LATCH_MS);
  });

  it('falls back to the ceiling for a gun with no cadence at all', () => {
    // Not a catalogue anybody publishes; a division that would otherwise return
    // zero and leave the latch unable to survive a single frame.
    expect(pullLatchMs(0)).toBe(MAX_PULL_LATCH_MS);
  });
});

describe('createPullLatch', () => {
  /**
   * One tap, at one frame rate, driven through the real emitter.
   *
   * THE TAP HAPPENS BETWEEN TWO FRAMES, which is the case the whole latch exists
   * for: `press` and `release` are browser events and land wherever they land,
   * while `due` is only ever called from an animation frame. Everything is
   * driven off an explicit clock, so a 144 Hz screen is a number here rather
   * than a device nobody has.
   *
   * THE CADENCE IS MODELLED, in the two lines that move `readyAt`, because
   * without it the claim would be false for a reason that has nothing to do with
   * the latch: a trigger held across two command periods legitimately asks
   * twice, and it is the GUN that refuses the second. In the view that refusal is
   * `gunBusy` over a timer both ends run; here a deadline is enough to make «one
   * tap, one shot» mean what it says. Nothing else about the gun is modelled —
   * where the ray went is the server's, and never this module's question.
   */
  const opts = { hz: 40, maxStepSeconds: 0.2, maxPerWake: 4 };
  const still = { mx: 0, my: 0, yaw: 0.5, pitch: 0 };
  /** The catalogue's own cadence, which is also what bounds the latch window. */
  const CADENCE_SECONDS = 0.35;
  /** The window every case below reads the latch with. */
  const WINDOW = pullLatchMs(CADENCE_SECONDS);

  function tap(tapMs: number, fps: number): number {
    const e = createEmitter(opts);
    const latch = createPullLatch();
    const frameMs = 1000 / fps;
    let readyAt = 0;
    // Two seconds is far longer than any of this needs; the claim is about what
    // the whole gesture produced, not about when.
    const frames = Math.ceil(2000 / frameMs);
    // Mid-way between the first and second frames, so no frame ever observes the
    // press or the release at its own instant. Both are delivered at the first
    // frame at or after they happened, which is what a browser does with an
    // event that lands between two: it is dispatched before the next frame runs,
    // and the latch is told the instant it really happened.
    const pressAt = frameMs * 1.5;
    let pressed = false;
    let released = false;
    let fired = 0;
    for (let i = 0; i < frames; i++) {
      const now = i * frameMs;
      if (!pressed && now >= pressAt) {
        latch.press(pressAt);
        pressed = true;
      }
      if (pressed && !released && now >= pressAt + tapMs) {
        latch.release();
        released = true;
      }
      const cmds = e.due(now, still, latch.wanted(now, WINDOW) && now >= readyAt);
      if (cmds.some((c) => c.fire)) {
        latch.taken();
        readyAt = now + CADENCE_SECONDS * 1000;
        fired += cmds.filter((c) => c.fire).length;
      }
    }
    return fired;
  }

  it('turns any tap at any frame rate into exactly one shot', () => {
    // THE TABLE THE BUG WAS IN. Sampling the button once a frame and discarding
    // whatever did not become a command lost a 16 ms flick 37 % of the time at
    // 144 Hz and 78 % at 90 Hz — and 8 ms taps, which every touchscreen
    // produces, far more often. Never two, either: a pull is a moment, and a
    // latch that could be claimed twice would be a gun that fires when the
    // player taps once.
    for (const tapMs of [8, 16, 25, 40]) {
      for (const fps of [144, 60, 30]) {
        expect(`${tapMs}ms@${fps}fps → ${tap(tapMs, fps)}`).toBe(`${tapMs}ms@${fps}fps → 1`);
      }
    }
  });

  it('survives the very first frame of a run, which produces no command at all', () => {
    // `due`'s first call is its clock initialiser and returns nothing whatever it
    // is handed, so a pull offered once and forgotten would be lost outright —
    // and the first frame of a visit is exactly where somebody who has been
    // waiting to get in puts his thumb.
    const e = createEmitter(opts);
    const latch = createPullLatch();
    latch.press(0);
    latch.release();
    expect(e.due(0, still, latch.wanted(0, WINDOW))).toEqual([]);
    expect(e.due(30, still, latch.wanted(30, WINDOW)).filter((c) => c.fire)).toHaveLength(1);
  });

  it('holds the pull while the button is down, however long the gun refuses', () => {
    // A level, not an edge: the window bounds a pull nobody is still asking for,
    // and a thumb that is still on the button is still asking.
    const latch = createPullLatch();
    latch.press(0);
    latch.taken();
    expect(latch.wanted(10_000, WINDOW)).toBe(true);
  });

  it('forgets a pull nobody claimed before the gun could grant it', () => {
    const latch = createPullLatch();
    latch.press(0);
    latch.release();
    expect(latch.wanted(WINDOW, WINDOW)).toBe(true);
    expect(latch.wanted(WINDOW + 1, WINDOW)).toBe(false);
  });

  it('is spent by the command that carried it, and not by the press', () => {
    // Otherwise a tap during a cooldown would be forgotten the moment it was
    // asked for, which is the fault this replaced.
    const latch = createPullLatch();
    latch.press(0);
    latch.release();
    expect(latch.wanted(10, WINDOW)).toBe(true);
    latch.taken();
    expect(latch.wanted(11, WINDOW)).toBe(false);
  });

  it('drops everything when the run ends', () => {
    const latch = createPullLatch();
    latch.press(0);
    latch.reset();
    expect(latch.wanted(0, WINDOW)).toBe(false);
  });
});
