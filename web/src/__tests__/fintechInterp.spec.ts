import { describe, expect, it } from 'vitest';
import { createInterpolator } from '../lib/fintechInterp';

/**
 * Entity interpolation — the third Gambetta rung, and the one this game shipped
 * without. Every test here is a way the CSS transition it replaces went wrong,
 * or a way the arrival-keyed timeline that replaced THAT went wrong.
 */

// The served `sim.render_delay_ms` at today's rates: 1.5 snapshot periods of the
// 20 Hz the office publishes at. It is a SERVED number rather than one this module
// computes — the office rewinds by exactly it to resolve a catch — so it is what
// the interpolator is BUILT with rather than something it derives.
const DELAY = 75;
// One simulation tick, which is what the timeline is measured in.
const TICK = 50;

const at = (x: number) => ({ x, y: 0, grin: 0 });
const build = () => createInterpolator(DELAY, TICK);

describe('the interpolator', () => {
  it('draws nothing at all before anything has arrived', () => {
    expect(build().at(1000)).toBeNull();
  });

  it('draws between the two samples bracketing the delayed instant', () => {
    const i = build();
    // Ticks 20 and 22 — two ticks, 100 ms of simulated time — arriving on time.
    i.push(at(0), 20, 1000);
    i.push(at(10), 22, 1100);
    // Drawn at now−DELAY. At now = 1100+DELAY that is 1100 — the newer sample.
    expect(i.at(1100 + DELAY)!.x).toBeCloseTo(10, 6);
    // Half way between them in TIME is half way between them in space.
    expect(i.at(1050 + DELAY)!.x).toBeCloseTo(5, 6);
    expect(i.at(1025 + DELAY)!.x).toBeCloseTo(2.5, 6);
  });

  it('draws exactly as far behind as it was told to, whatever it was told', () => {
    // THE DELAY IS SERVED AND NEVER DECIDED HERE. The office rewinds by exactly
    // this number to resolve a catch against the world the victim was looking at,
    // so a client that quietly used its own would be choosing how far behind the
    // office believes it to be. It is what made the move from 10 Hz to 20 Hz
    // snapshots cost nothing on this side: 150 ms of staleness became 75 ms
    // because the office said so.
    for (const delay of [150, 75, 40]) {
      const i = createInterpolator(delay, TICK);
      i.push(at(0), 20, 1000);
      i.push(at(10), 22, 1100);
      expect(i.at(1100 + delay)!.x).toBeCloseTo(10, 6);
      expect(i.at(1050 + delay)!.x).toBeCloseTo(5, 6);
    }
  });

  it('measures time in the office ticks, so jitter is not mistaken for speed', () => {
    // THE DEFECT THE TICK TIMELINE EXISTS TO REMOVE, and it is not subtle.
    // Frames leave two ticks apart — 100 ms of simulated time each, at a constant
    // walking speed — and arrive 40 ms, 160 ms and 100 ms apart. Keyed on ARRIVAL
    // those gaps ARE the timeline, so the лысый appears to cover the same ground
    // in 40 ms that he covered in 160: two and a half times his speed, then five
    // eighths of it, on the exact stretch where the dodge is being judged.
    //
    // Keyed on the tick, every one of those gaps is 100 ms and the drawn speed is
    // flat. Sampled across the middle of the span, the step between consecutive
    // samples must never vary by more than a hair.
    const i = build();
    i.push(at(0), 100, 1000);
    i.push(at(10), 102, 1040);
    i.push(at(20), 104, 1200);
    i.push(at(30), 106, 1300);

    // Measured over the INTERIOR of the span — the ends are where the buffer
    // holds rather than interpolates, and a hold is a step of zero that has
    // nothing to say about jitter. Where the timeline actually starts is not
    // knowable from here on purpose: the clock offset is estimated, and nothing
    // drawn depends on its absolute value.
    const steps: number[] = [];
    let prev: number | null = null;
    for (let t = 1000; t <= 1400; t += 10) {
      const x = i.at(t)!.x;
      if (prev !== null && prev > 0.5 && x < 29.5) steps.push(x - prev);
      prev = x;
    }
    expect(steps.length).toBeGreaterThan(4);
    expect(Math.max(...steps) - Math.min(...steps)).toBeLessThan(1e-6);
  });

  it('never goes backwards and never stalls mid-glide', () => {
    // The other half of the same claim, and the one the CSS transition failed:
    // arrive late and it glides for its fixed 100 ms and then stands still.
    const i = build();
    i.push(at(0), 100, 1000);
    i.push(at(10), 102, 1080);
    i.push(at(20), 104, 1210);
    i.push(at(30), 106, 1300);
    let prev = -Infinity;
    let stalls = 0;
    for (let t = 1000 + DELAY; t <= 1290 + DELAY; t += 16) {
      const x = i.at(t)!.x;
      expect(x).toBeGreaterThanOrEqual(prev - 1e-9);
      if (Math.abs(x - prev) < 1e-9) stalls++;
      prev = x;
    }
    expect(stalls).toBe(0);
  });

  it('rides straight over a dropped frame', () => {
    // The hub discards a slow client's backlog on purpose, so this happens — and
    // on a tick timeline the gap is self-describing: the missing tick is simply
    // not there, and the two samples either side are four ticks apart.
    const i = build();
    i.push(at(0), 100, 1000);
    i.push(at(20), 104, 1200); // tick 102 never arrived
    expect(i.at(1100 + DELAY)!.x).toBeCloseTo(10, 6);
  });

  it('survives a burst of frames arriving at once after a stall', () => {
    // A phone coming out of a tunnel is handed everything the hub still had, in
    // order, within a few milliseconds. On an ARRIVAL timeline that is a world
    // where six ticks happened in five milliseconds and the buffer's whole span
    // collapses to nothing; on the office's timeline they carry their own
    // spacing, so the figure plays them out at his real speed.
    const i = build();
    i.push(at(0), 200, 1000);
    for (let n = 1; n <= 6; n++) i.push(at(n * 10), 200 + n * 2, 1500 + n);

    // The span really is six ticks — 300 ms — wide rather than five milliseconds:
    // asked for an instant inside it, he is somewhere in the middle rather than
    // pinned at an end.
    const mid = i.at(1506 - 150 + DELAY)!.x;
    expect(mid).toBeGreaterThan(0);
    expect(mid).toBeLessThan(60);
    // And once the drawn instant is past everything held he holds at the newest,
    // which is the documented answer to a sender that has gone quiet.
    expect(i.at(3000)!.x).toBeCloseTo(60, 6);
  });

  it('starts a fresh timeline when the ticks restart under it', () => {
    // A restarted process is a new office whose clock begins again at nothing.
    // Treated as late frames those would be dropped for ever and the figure would
    // freeze for the rest of the shift.
    const i = build();
    i.push(at(0), 900, 1000);
    i.push(at(10), 902, 1100);
    i.push(at(50), 3, 1200);
    i.push(at(99), 5, 1300);
    expect(i.at(1300 + DELAY)!.x).toBeCloseTo(99, 6);
  });

  it('holds rather than extrapolating when the sender goes quiet', () => {
    // A boss who keeps walking on a guess and is then corrected is worse than
    // one who pauses: the guess is the thing being dodged.
    const i = build();
    i.push(at(0), 20, 1000);
    i.push(at(10), 22, 1100);
    expect(i.at(5000)!.x).toBeCloseTo(10, 6);
  });

  it('interpolates the grin too, because it is a continuous quantity', () => {
    const i = build();
    i.push({ x: 0, y: 0, grin: 0 }, 20, 1000);
    i.push({ x: 0, y: 0, grin: 1 }, 22, 1100);
    expect(i.at(1050 + DELAY)!.grin).toBeCloseTo(0.5, 6);
  });

  it('keeps a bounded buffer however long the shift runs', () => {
    const i = build();
    for (let n = 0; n < 500; n++) i.push(at(n), n * 2, 1000 + n * 100);
    expect(i.size()).toBeLessThanOrEqual(8);
    // And still draws correctly from what it kept.
    expect(i.at(1000 + 499 * 100 + DELAY)!.x).toBeCloseTo(499, 6);
  });

  it('drops a duplicate or a late arrival rather than sorting it in', () => {
    const i = build();
    i.push(at(0), 20, 1000);
    i.push(at(10), 22, 1100);
    i.push(at(99), 21, 1050); // late, and a tick already passed
    expect(i.at(1100 + DELAY)!.x).toBeCloseTo(10, 6);
  });

  it('resets between shifts, clock estimate and all', () => {
    const i = build();
    i.push(at(5), 900, 1000);
    i.reset();
    expect(i.at(2000)).toBeNull();
    // A new shift's ticks start again, and must not be placed against the old
    // office's clock — which would put every sample minutes in the past.
    i.push(at(1), 2, 9000);
    i.push(at(2), 4, 9100);
    expect(i.at(9100 + DELAY)!.x).toBeCloseTo(2, 6);
  });
});
