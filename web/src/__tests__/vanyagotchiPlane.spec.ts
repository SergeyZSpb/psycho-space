import { describe, expect, it } from 'vitest';
import {
  BAND_PROPERTY,
  DEPTH_PROPERTY,
  DEPTH_SCALES,
  LABEL_MAX,
  PEER_BASE_PX,
  SAY_BELOW_PROPERTY,
  SAY_FLIP_Y,
  SAY_MAX,
  UNKNOWN_ART,
  X_PROPERTY,
  Y_PROPERTY,
  applyFrame,
  applyPosition,
  avatarEndpoint,
  bandFor,
  capLabel,
  capSay,
  isRenderablePosition,
  readAppearances,
  readHere,
  resolveArt,
  sameAppearance,
  sayBelow,
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
  it('writes custom properties and nothing else', () => {
    // Not `transform`, and not `left`/`top`: the mapping from 0..1 to pixels
    // lives in the stylesheet so there is no measured box to invalidate when
    // mobile chrome slides and the plane resizes.
    const el = fakeEl();
    applyPosition(el, 0.25, 0.75);
    expect(el.props.get(X_PROPERTY)).toBe('0.25');
    expect(el.props.get(Y_PROPERTY)).toBe('0.75');
    expect([...el.props.keys()].every((name) => name.startsWith('--'))).toBe(true);
  });

  it('writes the depth band derived from y, in the same call', () => {
    // Derived HERE rather than anywhere else on purpose: the band is a pure
    // function of the coordinate, and computing it at a second site is how it
    // ends up a frame behind — an entity drawn at the size and stacking order of
    // where it used to be standing.
    const el = fakeEl();
    applyPosition(el, 0.5, 0.9);
    expect(el.props.get(BAND_PROPERTY)).toBe(String(DEPTH_SCALES.length - 1));
    expect(el.props.get(DEPTH_PROPERTY)).toBe(String(DEPTH_SCALES[DEPTH_SCALES.length - 1]));

    applyPosition(el, 0.5, 0.05);
    expect(el.props.get(BAND_PROPERTY)).toBe('0');
    expect(el.props.get(DEPTH_PROPERTY)).toBe(String(DEPTH_SCALES[0]));
  });

  it('says which side of its entity a balloon has to hang on', () => {
    const el = fakeEl();
    applyPosition(el, 0.5, 0.02);
    expect(el.props.get(SAY_BELOW_PROPERTY)).toBe('1');
    applyPosition(el, 0.5, 0.5);
    expect(el.props.get(SAY_BELOW_PROPERTY)).toBe('0');
  });
});

// Depth. Discrete bands rather than a continuous function of y, because the two
// halves of "nearer" are drawn by different machinery — a transform, which
// interpolates over the whole 220 ms of a move, and a z-index, which jumps — and
// only a discrete band confines their disagreement to the instant of a crossing.

describe('bandFor', () => {
  it('puts the top of the plane at the back and the bottom at the front', () => {
    expect(bandFor(0)).toBe(0);
    expect(bandFor(1)).toBe(DEPTH_SCALES.length - 1);
  });

  it('never invents a band past the end of the table', () => {
    // THE clamp that matters: y = 1 is exactly `floor(1 * 4) === 4`, one past
    // the last scale, so without this the entity standing on the bottom edge —
    // the nearest one there is — would be drawn with `undefined` for a scale.
    expect(bandFor(1)).toBe(3);
    expect(DEPTH_SCALES[bandFor(1)]).toBeDefined();
    expect(bandFor(2)).toBe(DEPTH_SCALES.length - 1);
    expect(bandFor(-1)).toBe(0);
  });

  it('changes only at a boundary, so a walk crosses bands a few times', () => {
    expect(bandFor(0.24)).toBe(0);
    expect(bandFor(0.25)).toBe(1);
    expect(bandFor(0.49)).toBe(1);
    expect(bandFor(0.5)).toBe(2);
    expect(bandFor(0.74)).toBe(2);
    expect(bandFor(0.75)).toBe(3);
  });

  it('is monotonic — nothing further down is ever further away', () => {
    let previous = -1;
    for (let y = 0; y <= 1.0001; y += 0.01) {
      const band = bandFor(y);
      expect(band).toBeGreaterThanOrEqual(previous);
      previous = band;
    }
  });

  it('falls to the back band rather than to NaN', () => {
    // Guarded even though isRenderablePosition has already rejected these: a NaN
    // written into a custom property resolves to nothing, and a dot with no
    // scale is a dot that has silently vanished.
    expect(bandFor(Number.NaN)).toBe(0);
    expect(bandFor(Number.POSITIVE_INFINITY)).toBe(0);
  });
});

describe('DEPTH_SCALES', () => {
  it('never draws an entity below the 44 px tap-target floor', () => {
    // THE constraint, and the reason the far band is 1 rather than something
    // smaller: depth can only make an entity BIGGER, so the floor the mobile
    // suite measures is the unscaled CSS size itself. Scaling the far band down
    // instead would make this a product of two numbers that have to be re-checked
    // together every time either one moves.
    expect(Math.min(...DEPTH_SCALES)).toBe(1);
    expect(PEER_BASE_PX * Math.min(...DEPTH_SCALES)).toBeGreaterThanOrEqual(44);
  });

  it('grows towards the viewer, and only gently', () => {
    for (let i = 1; i < DEPTH_SCALES.length; i += 1) {
      expect(DEPTH_SCALES[i]).toBeGreaterThan(DEPTH_SCALES[i - 1]);
    }
    // A band boundary is a jump, so the step has to stay small enough not to
    // read as a pop — and the whole range small enough that the far and near
    // ends of the yard are the same game.
    expect(Math.max(...DEPTH_SCALES) / Math.min(...DEPTH_SCALES)).toBeLessThanOrEqual(1.35);
  });

  it('has between three and five bands', () => {
    // Three reads as two and a half, because the middle band is most of the
    // plane; five puts the boundaries close enough that a diagonal walk pops
    // through two at once.
    expect(DEPTH_SCALES.length).toBeGreaterThanOrEqual(3);
    expect(DEPTH_SCALES.length).toBeLessThanOrEqual(5);
  });
});

describe('sayBelow', () => {
  it('hangs a balloon above its entity almost everywhere', () => {
    expect(sayBelow(0.5)).toBe(false);
    expect(sayBelow(1)).toBe(false);
    expect(sayBelow(SAY_FLIP_Y)).toBe(false);
  });

  it('flips it under the entity where the plane would eat it', () => {
    // A clipped name is still legibly somebody's name; a clipped balloon is
    // nothing at all, and the line is only on the wire for a few seconds.
    expect(sayBelow(0)).toBe(true);
    expect(sayBelow(0.05)).toBe(true);
  });

  it('leaves room for the balloon on the shortest plane we support', () => {
    // 320x568 gives a plane about 308 px tall, and the balloon needs roughly
    // 45 px above the entity's centre. Anything less than that here means a
    // balloon clipped away on the smallest phone.
    expect(SAY_FLIP_Y * 308).toBeGreaterThanOrEqual(45);
  });

  it('does not flip on a coordinate it cannot read', () => {
    expect(sayBelow(Number.NaN)).toBe(false);
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

  it('draws a Ваня whose owner has gone as asleep', () => {
    // Not a fallback to fine: a sleeper is the thing that stops a solo visit
    // being an empty field, and drawing him as an ordinary standing player would
    // make the yard look full of people ignoring you.
    expect(readAppearances([peer({ pose: 'asleep' })])[0]?.pose).toBe('asleep');
  });

  it('reads a line somebody said', () => {
    expect(readAppearances([peer({ say: 'устал' })])[0]?.say).toBe('устал');
  });

  it.each([
    ['omitted', undefined],
    ['empty', ''],
    ['nothing but spaces', '  '],
    ['not a string', 12],
  ])('leaves the line absent when it is %s', (_name, say) => {
    // Absent rather than empty, exactly like the label: `v-if="peer.say"` is the
    // only test the template makes, so there must be one falsy state and not two.
    const [look] = readAppearances([peer({ say })]);
    expect(look && 'say' in look).toBe(false);
  });

  it('caps a line long enough to cover the yard', () => {
    // The wire is trusted to be short. This is what makes that true rather than
    // hoped for — the balloon is capped by width in CSS as well, but a kilobyte
    // of it would still be a kilobyte in the DOM.
    const look = readAppearances([peer({ say: 'я'.repeat(200) })])[0];
    expect([...(look?.say ?? '')]).toHaveLength(SAY_MAX);
  });

  it('freezes what it returns', () => {
    // Handed straight to a shallowRef and never mutated in place, exactly like
    // the store's peerIds.
    expect(Object.isFrozen(readAppearances([peer()]))).toBe(true);
  });
});

describe('avatarEndpoint', () => {
  // The other half of the wire shape above. A frame says WHO is standing in the
  // yard and what kind of thing each one is; it deliberately says nothing about
  // where the picture of the person behind an entity lives, because a URL that
  // never changes has no business being re-sent five times a second and because
  // a URL out of Postgres would be the one durable thing on an otherwise
  // per-process frame. So the address is derived from the id instead, and these
  // tests are what stop that derivation being quietly altered — every entity
  // must map to its OWN face and to nobody else's.

  it('asks the game for the face of the entity it names', () => {
    expect(avatarEndpoint('c0ffee')).toBe('/api/game-vanyagotchi/avatar/c0ffee');
  });

  it('asks a different address for every entity', () => {
    // The property that matters, and the one a template bug would break first:
    // building the URL from anything shared — the viewer's own id, the first
    // peer in the frame — would draw one person's face on the whole yard.
    expect(avatarEndpoint('a')).not.toBe(avatarEndpoint('b'));
  });

  it('is a path on this origin, not an address anybody off it chose', () => {
    // Same-origin and rooted, so the browser sends the session cookie and so
    // that no value arriving on a socket frame can ever become the host a
    // picture is fetched from. The redirect to the CDN is the server's decision
    // to make and it makes it after checking; this client never has one to make.
    expect(avatarEndpoint('c0ffee').startsWith('/api/game-vanyagotchi/avatar/')).toBe(true);
  });

  it('escapes an id, because an id is a value off the wire', () => {
    // Hex today, and this test is about the day it is not. An id carrying a
    // slash would otherwise ask for a route one level down rather than for a
    // face, and a `?` would turn the rest of it into a query string — both of
    // which fail as something other than "that entity has no picture".
    expect(avatarEndpoint('a/b')).toBe('/api/game-vanyagotchi/avatar/a%2Fb');
    expect(avatarEndpoint('a?b#c')).toBe('/api/game-vanyagotchi/avatar/a%3Fb%23c');
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

describe('capSay', () => {
  it('passes a line that fits through untouched', () => {
    expect(capSay('устал')).toBe('устал');
  });

  it('allows more than a name does, because a line is a sentence', () => {
    expect(SAY_MAX).toBeGreaterThan(LABEL_MAX);
  });

  it('caps a long line with an ellipsis, without halving a character', () => {
    const capped = capSay('🍺'.repeat(60)) ?? '';
    expect([...capped]).toHaveLength(SAY_MAX);
    expect(capped).toBe(`${'🍺'.repeat(SAY_MAX - 1)}…`);
    expect(capped).not.toContain('�');
  });

  it('reports nothing at all as no line', () => {
    expect(capSay(undefined)).toBeUndefined();
    expect(capSay('   ')).toBeUndefined();
    expect(capSay(7)).toBeUndefined();
  });
});

// The head count. It is on the wire rather than derived here for one reason:
// `peers` carries the NPCs and the sleepers too, so counting the list would mean
// this client learning to tell a person from a character — content knowledge the
// entity frame exists to keep out of the browser.

describe('readHere', () => {
  it('says how many people the server counted, not how many entities it sent', () => {
    // The whole point: eight things on the plane, two of them people.
    expect(readHere(2, 8)).toBe(2);
  });

  it('counts a yard the server says is empty of people as empty', () => {
    // Zero is a real answer — a yard of nothing but NPCs and sleepers — and must
    // not be mistaken for a missing field.
    expect(readHere(0, 5)).toBe(0);
  });

  it('falls back to the entity count when the server does not say', () => {
    // A server that has not started sending `here` has no NPCs and no sleepers
    // either, so every entity in its frame really is a person.
    expect(readHere(undefined, 3)).toBe(3);
    expect(readHere(null, 3)).toBe(3);
  });

  it.each([
    ['not a number', '2'],
    ['a fraction', 1.5],
    ['negative', -1],
    ['NaN', Number.NaN],
    ['Infinity', Number.POSITIVE_INFINITY],
  ])('falls back rather than showing %s in the status row', (_name, raw) => {
    expect(readHere(raw, 4)).toBe(4);
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

  it('notices a line being said, and being finished with', () => {
    // The field this guard is most likely to swallow, and the one it can least
    // afford to: a line is on the wire for a few seconds in a yard where nothing
    // else is changing, so if it is not compared here the balloon never appears
    // at all — and, having appeared, never leaves.
    expect(sameAppearance([look()], [look({ say: 'устал' })])).toBe(false);
    expect(sameAppearance([look({ say: 'устал' })], [look()])).toBe(false);
    expect(sameAppearance([look({ say: 'устал' })], [look({ say: 'устал' })])).toBe(true);
  });

  it('notices somebody falling asleep', () => {
    expect(sameAppearance([look()], [look({ pose: 'asleep' })])).toBe(false);
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
