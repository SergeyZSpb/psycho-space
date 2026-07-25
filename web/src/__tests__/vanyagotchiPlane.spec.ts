import { describe, expect, it } from 'vitest';
import {
  X_PROPERTY,
  Y_PROPERTY,
  applyFrame,
  applyPosition,
  isRenderablePosition,
  tapToPosition,
} from '../lib/vanyagotchiPlane';

/** The smallest thing that looks like an element to this module. */
function fakeEl() {
  const props = new Map<string, string>();
  return {
    props,
    style: {
      setProperty(name: string, value: string) {
        props.set(name, value);
      },
    },
  };
}

describe('isRenderablePosition', () => {
  it('accepts a peer the server sent', () => {
    expect(isRenderablePosition({ id: 'a', x: 0.5, y: 0.25 })).toBe(true);
  });

  it.each([
    ['a missing id', { x: 0.5, y: 0.5 }],
    ['an empty id', { id: '', x: 0.5, y: 0.5 }],
    ['a missing coordinate', { id: 'a', x: 0.5 }],
    ['a string coordinate', { id: 'a', x: '0.5', y: 0.5 }],
    ['NaN', { id: 'a', x: Number.NaN, y: 0.5 }],
    ['Infinity', { id: 'a', x: Number.POSITIVE_INFINITY, y: 0.5 }],
    ['null', null],
    ['a bare number', 7],
  ])('refuses %s', (_name, peer) => {
    expect(isRenderablePosition(peer)).toBe(false);
  });
});

describe('applyPosition', () => {
  it('writes both custom properties and nothing else', () => {
    // Not `transform`, and not `left`/`top`: the mapping from 0..1 to pixels
    // lives in the stylesheet so there is no measured box to invalidate when
    // mobile chrome slides and the plane resizes.
    const el = fakeEl();
    applyPosition(el, 0.25, 0.75);
    expect(el.props.get(X_PROPERTY)).toBe('0.25');
    expect(el.props.get(Y_PROPERTY)).toBe('0.75');
    expect(el.props.size).toBe(2);
  });
});

describe('applyFrame', () => {
  it('positions every peer that has an element', () => {
    const a = fakeEl();
    const b = fakeEl();
    const applied = applyFrame(
      [
        { id: 'a', x: 0.1, y: 0.2 },
        { id: 'b', x: 0.3, y: 0.4 },
      ],
      new Map([
        ['a', a],
        ['b', b],
      ]),
    );

    expect(applied).toBe(2);
    expect(a.props.get(X_PROPERTY)).toBe('0.1');
    expect(b.props.get(Y_PROPERTY)).toBe('0.4');
  });

  it('skips a peer whose element does not exist yet', () => {
    // A newcomer's element only appears on the render after the store notices
    // the membership change. Frames are full state at 5 Hz, so the next one
    // positions them 200 ms later — nothing is lost, and nothing must throw.
    const a = fakeEl();
    const applied = applyFrame(
      [
        { id: 'a', x: 0.1, y: 0.2 },
        { id: 'newcomer', x: 0.9, y: 0.9 },
      ],
      new Map([['a', a]]),
    );
    expect(applied).toBe(1);
  });

  it('drops an unrenderable peer without touching the others', () => {
    // A NaN written into a custom property resolves to nothing, which would
    // strand the dot at its previous position with no error anywhere.
    const good = fakeEl();
    const bad = fakeEl();
    const applied = applyFrame(
      [
        { id: 'bad', x: Number.NaN, y: 0.5 },
        { id: 'good', x: 0.6, y: 0.7 },
      ],
      new Map([
        ['good', good],
        ['bad', bad],
      ]),
    );

    expect(applied).toBe(1);
    expect(bad.props.size).toBe(0);
    expect(good.props.get(X_PROPERTY)).toBe('0.6');
  });

  it('handles an empty frame', () => {
    expect(applyFrame([], new Map())).toBe(0);
  });
});

describe('tapToPosition', () => {
  const rect = { left: 20, top: 40, width: 200, height: 400 };

  it('normalises a tap against the plane box', () => {
    expect(tapToPosition(rect, 120, 240)).toEqual({ id: '', x: 0.5, y: 0.5 });
  });

  it('puts the corners at the corners', () => {
    expect(tapToPosition(rect, 20, 40)).toEqual({ id: '', x: 0, y: 0 });
    expect(tapToPosition(rect, 220, 440)).toEqual({ id: '', x: 1, y: 1 });
  });

  it('clamps a tap that lands just outside', () => {
    // A rounded getBoundingClientRect can put a border tap a fraction outside.
    // Clamping here means the server never has to reject an honest tap.
    expect(tapToPosition(rect, 5, 500)).toEqual({ id: '', x: 0, y: 1 });
  });

  it('refuses a plane with no area rather than dividing by zero', () => {
    // Happens for one frame while the flex layout is still resolving.
    expect(tapToPosition({ left: 0, top: 0, width: 0, height: 400 }, 10, 10)).toBeNull();
    expect(tapToPosition({ left: 0, top: 0, width: 200, height: 0 }, 10, 10)).toBeNull();
  });
});
