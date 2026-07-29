import { describe, expect, it } from 'vitest';
import {
  BAND_PROPERTY,
  DEPTH_PROPERTY,
  DEPTH_SCALES,
  depthScaleFor,
  GRIN_PROPERTY,
  X_PROPERTY,
  Y_PROPERTY,
  applyBoss,
  applyFigure,
  bandFor,
  decimal,
  deskBox,
  formatMoney,
  formatMultiplier,
  formatSeconds,
  grinState,
  sayFor,
  rampFraction,
  toPlane,
  type StyleTarget,
} from '../lib/karenPlane';

/**
 * Placing the office, and reading the numbers on the wire.
 *
 * Everything here is pure by construction, which is what lets the interesting
 * half of the game's drawing be checked without a browser: the view writes CSS
 * custom properties and never works out a coordinate or builds a sentence.
 */

/** A style target with no DOM behind it — see StyleTarget for why that is enough. */
function target(): StyleTarget & { props: Map<string, string> } {
  const props = new Map<string, string>();
  return { props, style: { setProperty: (name, value) => void props.set(name, value) } };
}

describe('metres to the plane', () => {
  it('maps the office onto 0..1 on each axis', () => {
    expect(toPlane(8, 11, 16, 22)).toEqual({ u: 0.5, v: 0.5 });
    expect(toPlane(0, 0, 16, 22)).toEqual({ u: 0, v: 0 });
    expect(toPlane(16, 22, 16, 22)).toEqual({ u: 1, v: 1 });
  });

  it('does not flip an axis, because both origins are top-left', () => {
    // The office is +Y down and so is the plane, which is why there is no axis
    // flip anywhere in this client. Getting it wrong would draw a chase running
    // the wrong way and look like a control bug rather than a transform one.
    expect(toPlane(8, 2, 16, 22).v).toBeLessThan(toPlane(8, 20, 16, 22).v);
  });

  it('clamps rather than letting a stray coordinate off the plane', () => {
    expect(toPlane(-5, 99, 16, 22)).toEqual({ u: 0, v: 1 });
  });

  it('answers the middle for an office that has not arrived yet', () => {
    // A NaN written into a custom property resolves to nothing in CSS and leaves
    // a figure stuck at its last position with no error anywhere.
    expect(toPlane(8, 11, 0, 0)).toEqual({ u: 0.5, v: 0.5 });
    expect(toPlane(Number.NaN, Number.NaN, 16, 22)).toEqual({ u: 0, v: 0 });
  });
});

describe('depth', () => {
  it('never draws anybody smaller than their own CSS size', () => {
    // The legibility floor is the stylesheet's size rather than a product of two
    // numbers that could drift apart — which holds only while the far band is 1.
    expect(DEPTH_SCALES[0]).toBe(1);
    for (const s of DEPTH_SCALES) expect(s).toBeGreaterThanOrEqual(1);
  });

  it('grows monotonically towards the front of the office', () => {
    for (let i = 1; i < DEPTH_SCALES.length; i++) {
      expect(DEPTH_SCALES[i]).toBeGreaterThan(DEPTH_SCALES[i - 1]);
    }
  });

  it('puts a figure in the band its feet are standing in', () => {
    expect(bandFor(0)).toBe(0);
    expect(bandFor(0.99)).toBe(DEPTH_SCALES.length - 1);
    expect(bandFor(1)).toBe(DEPTH_SCALES.length - 1);
  });

  it('answers the back band for anything it cannot read', () => {
    expect(bandFor(Number.NaN)).toBe(0);
    expect(bandFor(-3)).toBe(0);
  });
});

describe('writing a figure onto its element', () => {
  it('writes the position and everything derived from it in one pass', () => {
    // The band travels WITH the position: derived at a second site it could lag
    // the coordinates by a frame, and a figure drawn a band behind where it is
    // standing is the artefact the discrete bands exist to avoid.
    const el = target();
    applyFigure(el, { u: 0.25, v: 0.8 });
    expect(el.props.get(X_PROPERTY)).toBe('0.25');
    expect(el.props.get(Y_PROPERTY)).toBe('0.8');
    expect(el.props.get(BAND_PROPERTY)).toBe('3');
    // The band still steps, because paint order has no half-way — but the
    // SCALE is read from the continuous ramp, so a figure at 0.8 is sized
    // between the knots rather than snapped to the top one.
    expect(el.props.get(DEPTH_PROPERTY)).toBe(String(depthScaleFor(0.8)));
    expect(Number(el.props.get(DEPTH_PROPERTY))).toBeGreaterThan(DEPTH_SCALES[2]);
    expect(Number(el.props.get(DEPTH_PROPERTY))).toBeLessThan(DEPTH_SCALES[3]);
  });

  it('gives the boss his grin along with his position', () => {
    const el = target();
    applyBoss(el, { u: 0.5, v: 0.5 }, 0.72);
    expect(el.props.get(X_PROPERTY)).toBe('0.5');
    expect(el.props.get(GRIN_PROPERTY)).toBe('0.72');
  });

  it('never writes a grin CSS cannot use', () => {
    const el = target();
    applyBoss(el, { u: 0.5, v: 0.5 }, Number.NaN);
    expect(el.props.get(GRIN_PROPERTY)).toBe('0');
    applyBoss(el, { u: 0.5, v: 0.5 }, 4);
    expect(el.props.get(GRIN_PROPERTY)).toBe('1');
  });
});

describe('how pleased he is', () => {
  it('is three states, because the colour steps where the smile glides', () => {
    expect(grinState(0)).toBe('far');
    expect(grinState(0.34)).toBe('far');
    expect(grinState(0.35)).toBe('closing');
    expect(grinState(0.69)).toBe('closing');
    expect(grinState(0.7)).toBe('here');
    expect(grinState(1)).toBe('here');
  });

  it('reads anything unparseable as far away', () => {
    // A client that could not read a frame should not be painting the screen red.
    expect(grinState(Number.NaN)).toBe('far');
  });
});

describe('desks', () => {
  it('become a fraction of the plane, ready to be a box', () => {
    expect(deskBox({ x: 2, y: 3, w: 3.4, h: 1.2 }, 16, 22)).toEqual({
      left: 0.125,
      top: 3 / 22,
      width: 3.4 / 16,
      height: 1.2 / 22,
    });
  });

  it('collapse to nothing rather than dividing by an office of size zero', () => {
    expect(deskBox({ x: 2, y: 3, w: 3.4, h: 1.2 }, 0, 0)).toEqual({
      left: 0,
      top: 0,
      width: 0,
      height: 0,
    });
  });
});

describe('reading the wire', () => {
  it('groups the salary and never breaks it across a line', () => {
    // Non-breaking throughout: a five-figure salary that wrapped between its own
    // digits read as two numbers on a 360 px phone.
    expect(formatMoney(42800)).toBe('42\u00a0800\u00a0₽');
    expect(formatMoney(0)).toBe('0\u00a0₽');
    expect(formatMoney(7)).toBe('7\u00a0₽');
    expect(formatMoney(1234567)).toBe('1\u00a0234\u00a0567\u00a0₽');
  });

  it('shows nothing rather than a negative or a NaN salary', () => {
    expect(formatMoney(-5)).toBe('0\u00a0₽');
    expect(formatMoney(Number.NaN)).toBe('0\u00a0₽');
  });

  it('turns hundredths back into the multiplier a player is protecting', () => {
    expect(formatMultiplier(275)).toBe('×2,75');
    expect(formatMultiplier(100)).toBe('×1');
    // The ceiling deserves to look like a round number when it is reached.
    expect(formatMultiplier(300)).toBe('×3');
  });

  it('reads a duration as the HUD says it', () => {
    expect(formatSeconds(1800)).toBe('1,8\u00a0с.');
    expect(formatSeconds(0)).toBe('0\u00a0с.');
    expect(formatSeconds(Number.NaN)).toBe('0\u00a0с.');
  });

  it('writes decimals the Russian way, without a trailing zero', () => {
    expect(decimal(3.2)).toBe('3,2');
    expect(decimal(3)).toBe('3');
    expect(decimal(120, 0)).toBe('120');
    expect(decimal(0.3, 2)).toBe('0,3');
  });

  it('fills the ramp bar from the streak, not from the multiplier', () => {
    // They agree today only because the ramp happens to be linear; reading the
    // streak keeps the bar right on the day it stops being.
    expect(rampFraction(0, 6)).toBe(0);
    expect(rampFraction(3000, 6)).toBe(0.5);
    expect(rampFraction(9000, 6)).toBe(1);
    expect(rampFraction(-100, 6)).toBe(0);
  });

  it('shows a full bar rather than dividing by a ramp of zero', () => {
    expect(rampFraction(1000, 0)).toBe(1);
  });
});

describe('the depth ramp is continuous', () => {
  // Regression: the scale used to be read off DEPTH_SCALES[bandFor(v)], so a
  // figure crossing the office changed size in three visible jerks. Paint order
  // still steps — that one genuinely has no half-way — but size must not.
  it('never jumps, however finely the plane is walked', () => {
    let prev = depthScaleFor(0);
    for (let i = 1; i <= 2000; i++) {
      const s = depthScaleFor(i / 2000);
      expect(s).toBeGreaterThanOrEqual(prev);
      // One two-thousandth of the plane may not change size by more than a
      // thousandth of the whole ramp. A banded ramp fails this at every knot.
      expect(s - prev).toBeLessThan(0.001);
      prev = s;
    }
  });

  it('keeps the ends and the knots the bands chose', () => {
    expect(depthScaleFor(0)).toBeCloseTo(DEPTH_SCALES[0], 10);
    expect(depthScaleFor(1)).toBeCloseTo(DEPTH_SCALES[DEPTH_SCALES.length - 1], 10);
    for (let i = 0; i < DEPTH_SCALES.length; i++) {
      expect(depthScaleFor(i / (DEPTH_SCALES.length - 1))).toBeCloseTo(DEPTH_SCALES[i], 10);
    }
  });

  it('survives nonsense without becoming nonsense', () => {
    expect(depthScaleFor(Number.NaN)).toBe(DEPTH_SCALES[0]);
    expect(depthScaleFor(-5)).toBeCloseTo(DEPTH_SCALES[0], 10);
    expect(depthScaleFor(5)).toBeCloseTo(DEPTH_SCALES[DEPTH_SCALES.length - 1], 10);
  });
});

describe('a balloon is an index and a catalogue, never words on the wire', () => {
  const POOL = ['Я КАРЕН', 'Я ПРОСТО ВОДЫ ПОПИТЬ', 'Я НА ВСТРЕЧУ'];

  it('reads the line the index names', () => {
    expect(sayFor(POOL, 0)).toBe('Я КАРЕН');
    expect(sayFor(POOL, 2)).toBe('Я НА ВСТРЕЧУ');
  });

  it('falls back to the default rather than to nothing', () => {
    // A figure with no balloon reads as broken; a figure saying the wrong thing
    // reads as this game. Reachable both ways across a deploy — an older client
    // against a newer catalogue and the reverse.
    expect(sayFor(POOL, 99)).toBe('Я КАРЕН');
    expect(sayFor(POOL, -1)).toBe('Я КАРЕН');
    expect(sayFor(POOL, Number.NaN)).toBe('Я КАРЕН');
    expect(sayFor(POOL, 1.7)).toBe('Я ПРОСТО ВОДЫ ПОПИТЬ');
  });

  it('says nothing at all when the catalogue has nothing to say', () => {
    expect(sayFor([], 0)).toBe('');
    expect(sayFor(undefined, 0)).toBe('');
  });
});
