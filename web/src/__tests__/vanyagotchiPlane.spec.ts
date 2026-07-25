import { describe, expect, it } from 'vitest';
import {
  LABEL_MAX,
  UNKNOWN_ART,
  X_PROPERTY,
  Y_PROPERTY,
  applyFrame,
  applyPosition,
  capLabel,
  isRenderablePosition,
  readAppearances,
  resolveArt,
  sameAppearance,
  tapToPosition,
  type PeerAppearance,
} from '../lib/vanyagotchiPlane';
import type { VanyagotchiSkin } from '../api/types';

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

// The appearance half of a roster frame. These tests are about two properties
// and not really about anything else: that this client renders a world it does
// NOT understand — an unknown skin, an unknown pose, a field a half-deployed
// server has not started sending — because the alternative is a client that must
// ship in lockstep with the server; and that five identical frames a second cost
// no renders, because that is the split the whole plane is built around.

/** The catalogue as the screen receives it, cut down to what art resolution reads. */
const VANYA: VanyagotchiSkin = {
  key: 'vanya',
  label: 'дядя Ваня',
  emoji: '🫃',
  gradient: 'linear-gradient(160deg, #6b4a2f, #2f4a6b)',
};

/** A skin somebody has since uploaded a picture for. */
const PAINTED: VanyagotchiSkin = { ...VANYA, key: 'painted', image: '/api/assets/vanya.png' };

/** One roster entry, in the shape the server sends it. */
function peer(over: Record<string, unknown> = {}): Record<string, unknown> {
  return { id: 'a', x: 0.5, y: 0.5, art: 'vanya', pose: 'fine', ...over };
}

describe('readAppearances', () => {
  it('reads what the server said each entity looks like', () => {
    expect(
      readAppearances([peer({ id: 'a', art: 'vanya', label: 'Ваня', pose: 'poorly' })]),
    ).toEqual([{ id: 'a', art: 'vanya', label: 'Ваня', pose: 'poorly' }]);
  });

  it('carries no coordinates', () => {
    // The load-bearing omission: this is the shape that goes through Vue, so a
    // stray x would drag positions back into the vdom five times a second.
    const [look] = readAppearances([peer({ x: 0.25, y: 0.75 })]);
    expect(look && 'x' in look).toBe(false);
    expect(look && 'y' in look).toBe(false);
  });

  it('agrees with the position pass about who is on the plane', () => {
    // Both halves of a frame go through the same guard, so the keyed list and
    // the head count cannot disagree about who is standing in the yard.
    const frame: unknown[] = [peer({ id: 'good' }), peer({ id: 'bad', x: Number.NaN }), null, 7];
    expect(readAppearances(frame).map((l) => l.id)).toEqual(['good']);
    expect(frame.filter(isRenderablePosition).map((p) => p.id)).toEqual(['good']);
  });

  it('draws a pose it has never heard of as fine', () => {
    // A pose the server added after this bundle shipped. Rendering nothing, or
    // passing the raw word through to an attribute the stylesheet keys on, would
    // both make a new pose a client deploy.
    expect(readAppearances([peer({ pose: 'dancing' })])[0]?.pose).toBe('fine');
    expect(readAppearances([peer({ pose: 42 })])[0]?.pose).toBe('fine');
  });

  it('assumes fine when a half-deployed server sends no pose at all', () => {
    const { pose, ...rest } = peer();
    expect(pose).toBe('fine'); // the field really was removed below
    expect(readAppearances([rest])[0]?.pose).toBe('fine');
  });

  it('keeps an art key the catalogue has never described', () => {
    // Resolution happens later and falls back then; dropping the key here would
    // lose the only thing that lets a backend-added skin appear with no deploy.
    expect(readAppearances([peer({ art: 'npc-dog' })])[0]?.art).toBe('npc-dog');
  });

  it('reads a missing art key as empty rather than as undefined', () => {
    const { art, ...rest } = peer();
    expect(art).toBe('vanya');
    expect(readAppearances([rest])[0]?.art).toBe('');
    expect(readAppearances([peer({ art: 7 })])[0]?.art).toBe('');
  });

  it.each([
    ['omitted', undefined],
    ['empty', ''],
    ['nothing but spaces', '   '],
    ['not a string', 12],
  ])('leaves the label absent when it is %s', (_name, label) => {
    // Absent rather than empty, so "no name" is one state everywhere and the
    // template can never print the string "undefined" under a dot.
    const [look] = readAppearances([peer({ label })]);
    expect(look && 'label' in look).toBe(false);
  });

  it('caps a name long enough to cover the neighbours', () => {
    const look = readAppearances([peer({ label: 'а'.repeat(40) })])[0];
    expect([...(look?.label ?? '')]).toHaveLength(LABEL_MAX);
  });

  it('freezes what it returns', () => {
    // Handed straight to a shallowRef and never mutated in place, exactly like
    // the store's peerIds.
    expect(Object.isFrozen(readAppearances([peer()]))).toBe(true);
  });
});

describe('capLabel', () => {
  it('passes a name that fits through untouched', () => {
    expect(capLabel('Ваня')).toBe('Ваня');
  });

  it('trims, because a padded name is a wider name', () => {
    expect(capLabel('  Ваня  ')).toBe('Ваня');
  });

  it('caps a long name with an ellipsis', () => {
    const capped = capLabel('Владимир Владимирович Маяковский');
    expect(capped).toBe('Владимир Владим…');
    expect([...(capped ?? '')]).toHaveLength(LABEL_MAX);
  });

  it('does not cut an astral character in half', () => {
    // A name is exactly the field somebody fills with emoji, and slicing UTF-16
    // through a surrogate pair leaves a lone half that renders as a tofu box.
    const capped = capLabel('🍺'.repeat(20)) ?? '';
    expect([...capped]).toHaveLength(LABEL_MAX);
    expect(capped).toBe(`${'🍺'.repeat(LABEL_MAX - 1)}…`);
    expect(capped).not.toContain('�');
    expect(capped.split('🍺').join('')).toBe('…');
  });

  it.each([
    ['nothing at all', undefined],
    ['an empty string', ''],
    ['whitespace', ' \n '],
    ['a number', 7],
    ['null', null],
  ])('reports %s as no name', (_name, raw) => {
    expect(capLabel(raw)).toBeUndefined();
  });
});

describe('resolveArt', () => {
  it('draws the catalogue emoji for a key it knows', () => {
    expect(resolveArt([VANYA], 'vanya')).toEqual({ emoji: '🫃' });
  });

  it('prefers a picture when the catalogue has one', () => {
    expect(resolveArt([VANYA, PAINTED], 'painted')).toEqual({
      emoji: '🫃',
      image: '/api/assets/vanya.png',
    });
  });

  it('renders a key the catalogue has never described', () => {
    // THE property this iteration buys: the backend adds a skin — or a whole
    // NPC — and every client already running draws it as something rather than
    // as a hole in the yard.
    expect(resolveArt([VANYA], 'npc-dog')).toEqual({ emoji: UNKNOWN_ART });
  });

  it('renders before the catalogue has even arrived', () => {
    // The plane runs on the socket and the catalogue comes over HTTP, so a
    // populated yard with no catalogue yet is the ordinary first second.
    expect(resolveArt(undefined, 'vanya')).toEqual({ emoji: UNKNOWN_ART });
    expect(resolveArt([], 'vanya')).toEqual({ emoji: UNKNOWN_ART });
  });

  it('falls back for a skin that describes no look at all', () => {
    expect(resolveArt([{ ...VANYA, emoji: '' }], 'vanya')).toEqual({ emoji: UNKNOWN_ART });
  });
});

describe('sameAppearance', () => {
  const look = (over: Partial<PeerAppearance> = {}): PeerAppearance => ({
    id: 'a',
    art: 'vanya',
    pose: 'fine',
    ...over,
  });

  it('is true for two frames describing the same yard', () => {
    expect(sameAppearance([look()], [look()])).toBe(true);
  });

  it('ignores the order the server happened to iterate its map in', () => {
    // THE property. The roster is built by ranging a Go map, so an identical
    // world arrives shuffled; comparing in order would report a change five
    // times a second and re-render the whole yard at 5 Hz.
    const a = [look({ id: 'a' }), look({ id: 'b' }), look({ id: 'c' })];
    const b = [look({ id: 'c' }), look({ id: 'a' }), look({ id: 'b' })];
    expect(sameAppearance(a, b)).toBe(true);
  });

  it('notices a pose changing', () => {
    expect(sameAppearance([look()], [look({ pose: 'dead' })])).toBe(false);
  });

  it('notices a Ваня being given a name', () => {
    expect(sameAppearance([look()], [look({ label: 'Ваня' })])).toBe(false);
    expect(sameAppearance([look({ label: 'Ваня' })], [look()])).toBe(false);
  });

  it('notices a skin changing', () => {
    expect(sameAppearance([look()], [look({ art: 'npc-dog' })])).toBe(false);
  });

  it('notices somebody arriving or leaving', () => {
    expect(sameAppearance([look({ id: 'a' })], [look({ id: 'a' }), look({ id: 'b' })])).toBe(false);
    expect(sameAppearance([look({ id: 'a' })], [look({ id: 'b' })])).toBe(false);
    expect(sameAppearance([], [look()])).toBe(false);
  });

  it('is true for two empty yards', () => {
    expect(sameAppearance([], [])).toBe(true);
  });
});
