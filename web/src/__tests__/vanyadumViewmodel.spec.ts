import { describe, expect, it } from 'vitest';
import { syringePose } from '../lib/vanyadumViewmodel';

/**
 * The syringe animation's timing.
 *
 * WHAT THESE TESTS CAN CLAIM, AND WHY THAT IS ENOUGH. The animation itself is
 * inside the canvas and ADR-047 accepts that a canvas is opaque to both
 * Playwright suites — so nothing can assert that the syringe LOOKS right. What
 * can be asserted, and is the half that would actually be wrong, is the
 * arithmetic: that the plunger tracks the health the injection is delivering —
 * the server's straight line, re-derived here from the predicted countdown rather
 * than read off a snapshot — that the hand is up before the needle moves, and
 * that turning motion off leaves the information behind rather than the movement.
 *
 * The seconds below are deliberately not the catalogue's 2,5: the duration is
 * served, so a function with a number of its own in it would pass here and drift
 * the afternoon somebody retuned the ampoule.
 */

const SECONDS = 4;

/** The pose `left` seconds from the end of a `SECONDS`-long injection. */
function at(left: number, reduced = false) {
  const pose = syringePose(left, SECONDS, reduced);
  expect(pose).not.toBeNull();
  return pose!;
}

describe('syringePose', () => {
  it('answers null when nothing is running, which is what puts the gun back', () => {
    // One question rather than a pose plus a flag: the renderer's "there is no
    // syringe" and its "put the обрез back in his hands" are the same branch.
    expect(syringePose(0, SECONDS, false)).toBeNull();
    expect(syringePose(-1, SECONDS, false)).toBeNull();
    expect(syringePose(Number.NaN, SECONDS, false)).toBeNull();
  });

  it('drives the plunger by the delivered health and nothing else', () => {
    // THE ONE CLAIM THIS MODULE EXISTS FOR. The server hands the ampoule out as a
    // straight line over the whole countdown, so the plunger is that same line: a
    // quarter down is a quarter of the ampoule in the man. The health on the HUD
    // is the same quantity from the other authority — the server's number, up to
    // a snapshot behind — so the two agree to within a round trip rather than
    // being one value read twice, and closing that round trip is the whole reason
    // the pose is driven from the prediction. An eased plunger would be a picture
    // that lies about the only thing the player is waiting for.
    expect(at(SECONDS).action).toBeCloseTo(0, 12);
    expect(at(SECONDS * 0.75).action).toBeCloseTo(0.25, 12);
    expect(at(SECONDS / 2).action).toBeCloseTo(0.5, 12);
    expect(at(0.001).action).toBeCloseTo(1 - 0.001 / SECONDS, 12);
  });

  it('never lets the plunger leave its stroke, whatever the countdown says', () => {
    // A client whose ampoule outlived its duration — a retune landing between a
    // snapshot and the catalogue it was fetched with — would otherwise push the
    // plunger out through the far end of the barrel.
    expect(at(SECONDS * 3).action).toBe(0);
    expect(syringePose(1, 0, false)!.action).toBe(1);
  });

  it('brings the hand up first and pushes the needle in second', () => {
    // The order is the whole of what the animation says. A needle already in the
    // arm while the hand is still below the frame is not a mistimed animation, it
    // is a different event.
    const early = at(SECONDS * 0.98);
    expect(early.y).toBeLessThan(0);
    // `toBeCloseTo` rather than `toBe`, throughout this file, because an offset
    // scaled by zero is negative zero in IEEE-754 and `Object.is` tells the two
    // apart where nothing else in the game ever could.
    expect(early.x).toBeCloseTo(0, 12);

    // Up, and the needle now travelling.
    const middle = at(SECONDS * 0.78);
    expect(middle.y).toBeGreaterThan(early.y);
    expect(middle.x).toBeLessThan(0);
  });

  it('settles: once it is in, nothing but the plunger moves', () => {
    // Most of the injection is the plunger, which is most of what there is to
    // look at — so the rest of the pose has to stop rather than drift for two
    // seconds.
    const a = at(SECONDS * 0.5);
    const b = at(SECONDS * 0.2);
    expect(b.x).toBeCloseTo(a.x, 12);
    expect(b.y).toBeCloseTo(a.y, 12);
    expect(b.z).toBeCloseTo(a.z, 12);
    expect(b.pitch).toBeCloseTo(a.pitch, 12);
    expect(b.roll).toBeCloseTo(a.roll, 12);
    expect(b.action).toBeGreaterThan(a.action);
  });

  it('ends at the rest pose, so the item is where it is held', () => {
    // Every offset is measured from how the syringe sits in the hand, so a
    // settled pose is zeros on the four that describe the raise — the renderer
    // adds them to a rest transform it owns.
    const settled = at(SECONDS * 0.4);
    expect(settled.y).toBeCloseTo(0, 12);
    expect(settled.z).toBeCloseTo(0, 12);
    expect(settled.pitch).toBeCloseTo(0, 12);
  });

  it('under reduced motion it is already there, and the plunger still moves', () => {
    // THE RULE THIS PROJECT KEEPS ABOUT MOTION: what goes is the travel, never
    // the information. Somebody who asked for less movement still has to be able
    // to tell that an injection is running and roughly how far along it is — so
    // the syringe is simply in the settled pose from the first frame, and the
    // plunger goes on tracking the delivery.
    const first = at(SECONDS, true);
    const later = at(SECONDS * 0.3, true);
    expect(first.y).toBeCloseTo(0, 12);
    expect(first.z).toBeCloseTo(0, 12);
    expect(first.pitch).toBeCloseTo(0, 12);
    expect(first.x).toBe(later.x);
    expect(first.roll).toBe(later.roll);
    expect(later.action).toBeGreaterThan(first.action);
  });

  it('damped and undamped agree once the hand is up', () => {
    // The two are the same animation from the moment the needle is in — which is
    // what makes reduced motion a matter of skipping the sweep rather than a
    // second animation to keep in step by hand.
    const damped = at(SECONDS * 0.5, true);
    const full = at(SECONDS * 0.5, false);
    expect(damped.x).toBeCloseTo(full.x, 12);
    expect(damped.y).toBeCloseTo(full.y, 12);
    expect(damped.roll).toBeCloseTo(full.roll, 12);
    expect(damped.action).toBe(full.action);
  });
});
