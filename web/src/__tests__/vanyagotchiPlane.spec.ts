import { describe, expect, it } from 'vitest';
import {
  BAND_PROPERTY,
  DEPTH_PROPERTY,
  DEPTH_SCALES,
  LABEL_MAX,
  NO_HEADS,
  PEER_BASE_PX,
  PROP_LIFE_MS,
  SAY_BELOW_PROPERTY,
  SAY_FLIP_Y,
  SAY_MAX,
  SEARCH_WALK_MS,
  UNKNOWN_ART,
  UNKNOWN_SPOT,
  UNNAMED_PLACE,
  X_PROPERTY,
  Y_PROPERTY,
  applyFrame,
  applyPosition,
  avatarEndpoint,
  bandFor,
  beside,
  capLabel,
  capSay,
  hereLabel,
  hotspotsFor,
  hueFor,
  huntRestarted,
  isRenderablePosition,
  outOfReach,
  shortOf,
  peersIn,
  propScale,
  readAppearances,
  readHere,
  readStore,
  sameHere,
  sameStore,
  searchVerb,
  spotAriaLabel,
  storeLabel,
  readHunt,
  resolveArt,
  sameAppearance,
  sayBelow,
  tapToPosition,
  travelPlaces,
  type PeerAppearance,
} from '../lib/vanyagotchiPlane';
import type { VanyagotchiAction, VanyagotchiConfig, VanyagotchiSkin } from '../api/types';

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
  it('never draws an entity below the legibility floor', () => {
    // THE constraint, and the reason the far band is 1 rather than something
    // smaller: depth can only make an entity BIGGER, so the floor the mobile
    // suite measures is the unscaled CSS size itself. Scaling the far band down
    // instead would make this a product of two numbers that have to be re-checked
    // together every time either one moves.
    //
    // IT USED TO SAY 44 AND TO CALL IT A TAP TARGET, and both halves of that were
    // wrong in a way worth recording rather than quietly editing. A dot is
    // `pointer-events: none` and the plane takes every tap, so nothing here has
    // ever been tappable; 44 was how small a face may be drawn and still be read.
    // That made it a DRAWING judgement, which is what allowed the world scale to
    // come down to 32 when the owner reported the yard's contents as too big. The
    // invariant this test is really about — depth never shrinks anybody, so the
    // smallest drawn size is the CSS size — is untouched by that, which is why
    // the assertion is written against the constant rather than against a number.
    expect(Math.min(...DEPTH_SCALES)).toBe(1);
    expect(PEER_BASE_PX * Math.min(...DEPTH_SCALES)).toBeGreaterThanOrEqual(PEER_BASE_PX);
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

  it.each([['happy'], ['sad']])('keeps the %s a claim left behind', (pose) => {
    // The two MOODS, and they must survive this pass rather than falling back to
    // fine: winning and losing a key move no stat at all, so the pose is the
    // entire outcome of the race. Read as fine, a lost key would be indis-
    // tinguishable from never having pressed the button.
    expect(readAppearances([peer({ pose })])[0]?.pose).toBe(pose);
  });

  it('reads a line somebody said', () => {
    expect(readAppearances([peer({ say: 'устал' })])[0]?.say).toBe('устал');
  });

  it('reads the instant a thing on the ground stops existing', () => {
    // The ONE field that says «this is not a person» without naming a kind, and
    // therefore the only thing the half-size, no-circle, shrinking drawing is
    // allowed to key off. Kept as the absolute unix second the server sent, not
    // converted to a countdown here: an absolute instant is constant for the
    // thing's whole life, which is what keeps it out of `sameAppearance`'s way.
    expect(readAppearances([peer({ expires: 1_800_000_000 })])[0]?.expires).toBe(1_800_000_000);
  });

  it.each([
    ['omitted', undefined],
    ['zero, as an older server would send it', 0],
    ['null', null],
    ['negative', -5],
    ['not a number', '1800000000'],
    ['NaN', Number.NaN],
    ['infinite', Number.POSITIVE_INFINITY],
  ])('leaves the expiry absent when it is %s', (_name, expires) => {
    // All of these mean the same thing — this entity is not going anywhere — and
    // the failure mode of telling them apart is severe rather than cosmetic: an
    // entity wrongly given an expiry is drawn at half size and shrinking, and one
    // given an expiry in the past is drawn at nothing at all. That is a player
    // who has become invisible, so every malformed shape has to land on "absent".
    const [look] = readAppearances([peer({ expires })]);
    expect(look && 'expires' in look).toBe(false);
  });

  it('leaves the expiry absent for an ordinary person', () => {
    // The common case, stated on its own because it is what every assertion
    // about the yard's people silently depends on.
    const [look] = readAppearances([peer()]);
    expect(look && 'expires' in look).toBe(false);
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

// The head counts. They are on the wire rather than derived here for one reason:
// `peers` carries the NPCs, the sleepers and the litter too, so counting the list
// would mean this client learning to tell a person from a character — content
// knowledge the entity frame exists to keep out of the browser.

describe('readHere', () => {
  it('reads how many people the server counted in each place', () => {
    const heads = readHere({ yard: 3, les: 1 });
    expect(heads.get('yard')).toBe(3);
    expect(heads.get('les')).toBe(1);
  });

  it('reads a place nobody is in as nobody, because the zeroes are omitted', () => {
    // The server sends only the places that have somebody in them, so an absent
    // key is an empty place rather than an unknown one — which is what lets the
    // travel sheet print a count for every location off a two-entry object.
    const heads = readHere({ yard: 3 });
    expect(heads.get('kusty')).toBeUndefined();
    expect(heads.size).toBe(1);
  });

  it('is empty for a frame that counts nothing, rather than guessing', () => {
    // THERE IS NO FALLBACK TO THE ENTITY COUNT ANY MORE, and its removal is the
    // point rather than an omission. That fallback used to be right because a
    // server old enough to omit `here` had no NPCs either; the number it would
    // fall back to now is the entities in ONE place including its characters, its
    // sleepers and everything lying on its ground, which is a head count of
    // nothing at all.
    expect(readHere(undefined)).toBe(NO_HEADS);
    expect(readHere(null)).toBe(NO_HEADS);
    expect(readHere({})).toBe(NO_HEADS);
  });

  it.each([
    ['a bare number, which is what the previous wire carried', 3],
    ['an array', [1, 2]],
  ])('reads %s as no counts at all', (_name, raw) => {
    // The first of these is the shape this field USED to have, and it is here as
    // a guard rather than as tidiness: the obvious way to "fix" a count that has
    // gone missing is to put the old fallback back, and a client that did would
    // report the лес's crowd in the двор. The second is what a `[]` on the wire
    // would silently become — an object with numeric keys — if the guard only
    // tested `typeof`.
    expect(readHere(raw).size).toBe(0);
  });

  it.each([
    ['not a number', { yard: '3' }],
    ['a fraction', { yard: 1.5 }],
    ['negative', { yard: -1 }],
    ['NaN', { yard: Number.NaN }],
    ['Infinity', { yard: Number.POSITIVE_INFINITY }],
  ])('drops a count that is %s rather than showing it', (_name, raw) => {
    expect(readHere(raw).has('yard')).toBe(false);
  });

  it('keeps the readable counts beside an unreadable one', () => {
    // Losing the whole tally to one broken entry would empty the travel sheet's
    // counts for a fault in a place nobody is looking at.
    const heads = readHere({ yard: 2, les: 'many' });
    expect(heads.get('yard')).toBe(2);
    expect(heads.has('les')).toBe(false);
  });
});

describe('sameHere', () => {
  it('is true for two frames counting the world the same way', () => {
    // What every frame carries: five times a second, the same two numbers.
    expect(sameHere(new Map([['yard', 3]]), new Map([['yard', 3]]))).toBe(true);
  });

  it('is true for two uncounted worlds, so an empty tally never re-renders', () => {
    expect(sameHere(NO_HEADS, NO_HEADS)).toBe(true);
    expect(sameHere(new Map(), NO_HEADS)).toBe(true);
  });

  it('notices somebody arriving where you are standing', () => {
    expect(sameHere(new Map([['yard', 3]]), new Map([['yard', 4]]))).toBe(false);
  });

  it('notices somebody arriving somewhere else entirely', () => {
    // The cost of a tally rather than a number, and it is deliberate: the travel
    // sheet is only worth opening because the counts beside its places are live.
    expect(sameHere(new Map([['yard', 3]]), new Map([['yard', 3], ['les', 1]]))).toBe(false);
  });

  it('notices the last person leaving a place', () => {
    expect(sameHere(new Map([['yard', 3], ['les', 1]]), new Map([['yard', 3]]))).toBe(false);
  });

  it('ignores the order the server happened to serialise its map in', () => {
    const a = new Map([['yard', 3], ['les', 1]]);
    const b = new Map([['les', 1], ['yard', 3]]);
    expect(sameHere(a, b)).toBe(true);
  });
});

// ---------------------------------------------------------------------------
// One room, several places. The frame carries the whole world and the browser
// draws one place of it — see the section header in the source for why that is
// the trade rather than a room per location.
// ---------------------------------------------------------------------------

describe('peersIn', () => {
  const yard = { id: 'a', x: 0.1, y: 0.1 };
  const les = { id: 'b', x: 0.2, y: 0.2, loc: 'les' };
  const said = { id: 'c', x: 0.3, y: 0.3, loc: 'yard' };

  it('draws the place he is standing in and nothing else', () => {
    expect(peersIn([yard, les, said], 'yard', 'yard')).toEqual([yard, said]);
  });

  it('reads an entity that named no place as being in the default one', () => {
    // The common case on the wire: most of the world is in the first place most
    // of the time, so it sends no field at all rather than repeating one key per
    // entity five times a second.
    expect(peersIn([yard], 'yard', 'yard')).toEqual([yard]);
    expect(peersIn([yard], 'les', 'yard')).toEqual([]);
  });

  it('does not let the unlabelled follow the player around', () => {
    // THE bug the two arguments exist to prevent. Read as "absent means wherever
    // the viewer is", every entity that named no place would be drawn in all four
    // of them at once — and the лес would be full of people standing in the yard.
    expect(peersIn([yard, les], 'les', 'yard')).toEqual([les]);
  });

  it('leaves an empty place empty rather than falling back to the whole world', () => {
    expect(peersIn([yard, les], 'kusty', 'yard')).toEqual([]);
  });

  it('draws what named no place at all before the catalogue has arrived', () => {
    // The first second of every visit: the plane runs on the socket and the
    // config comes over HTTP, so neither the viewer's place nor the default is
    // known. Drawing the entities that named none is the default place, which is
    // where a fresh Ваня stands; drawing everybody would put four locations on
    // top of each other.
    expect(peersIn([yard, les], '', '')).toEqual([yard]);
  });

  it('passes a malformed entry through rather than throwing on it', () => {
    // NOT this function's job to reject it. There is exactly one renderability
    // guard in this module and it runs downstream (`isRenderablePosition`), so a
    // second one here would be a second place to disagree about who is on the
    // plane. What it must not do is throw while reading a field off `null` — and
    // an entry that says nothing about where it is reads as the default place,
    // like every other entry that says nothing.
    expect(peersIn([null, 7, yard], 'yard', 'yard')).toEqual([null, 7, yard]);
    expect(readAppearances(peersIn([null, 7, yard], 'yard', 'yard'))).toHaveLength(1);
    // And it is not smuggled into another place either.
    expect(peersIn([null, 7], 'les', 'yard')).toEqual([]);
  });

  it('ignores a location that is not a usable string', () => {
    // Each of these means «this entity did not say», which is the default place.
    const odd = [
      { id: 'a', loc: '' },
      { id: 'b', loc: 7 },
      { id: 'c', loc: null },
    ];
    expect(peersIn(odd, 'yard', 'yard')).toEqual(odd);
    expect(peersIn(odd, 'les', 'yard')).toEqual([]);
  });

  it('handles a world with nobody in it', () => {
    expect(peersIn([], 'yard', 'yard')).toEqual([]);
  });
});

// The key hunt. The frame carries WHICH hunt is running and never that one has
// just started, so the announcement is a difference between two frames and lives
// entirely on this side of the wire — which is exactly why it is a pure function
// with a table of cases rather than something inside the component.

describe('readHunt', () => {
  it('reads the id of the hunt the server says is running', () => {
    expect(readHunt('7f3a91')).toBe('7f3a91');
  });

  it('reads a frame with no hunt on it as no hunt', () => {
    // Both shapes mean the same thing and neither is exceptional: a server too
    // old to send the field, and a frame published in the instant between a key
    // being won and its replacement being lost.
    expect(readHunt(undefined)).toBe('');
    expect(readHunt('')).toBe('');
  });

  it.each([
    ['a number', 7],
    ['null', null],
    ['an object', { id: 'a' }],
    ['an array', ['a']],
  ])('reads %s as no hunt rather than passing it on', (_name, raw) => {
    // Whatever this is, it is not an id, and treating it as one would put a
    // value the comparison below cannot reason about into `seenHunt`.
    expect(readHunt(raw)).toBe('');
  });
});

describe('huntRestarted', () => {
  it('announces a hunt that replaced the one we were watching', () => {
    // The only case that is news: we saw the keys lying there, somebody took
    // them, and a fresh pair has been lost.
    expect(huntRestarted('a', 'b')).toBe(true);
  });

  it('says nothing on the first sight of a hunt', () => {
    // THE case this function exists for. A player who arrives after the keys
    // were found is standing in a world where a hunt is already running, and
    // telling him it has just started would announce an event from before he was
    // here. A late joiner takes part in silence.
    expect(huntRestarted('', 'a')).toBe(false);
  });

  it('says nothing while the same hunt goes on', () => {
    // Five frames a second carry the same id, and every one of them is the same
    // world. An announcement per frame would be the whole screen.
    expect(huntRestarted('a', 'a')).toBe(false);
  });

  it('says nothing when a hunt ends to nothing', () => {
    // It looks like a change and is not one: the winning claim exhausts the key
    // and inserts its replacement in one statement, so an empty value is a gap
    // between two frames. Announcing it would put «ключи снова потерялись» up at
    // the moment they were FOUND.
    expect(huntRestarted('a', '')).toBe(false);
  });

  it('says nothing about a world with no hunt in it at all', () => {
    expect(huntRestarted('', '')).toBe(false);
  });
});

// How a thing on the ground is drawn as it runs out. The server sends the
// instant it stops existing and never a size, so everything between "just
// dropped" and "gone" is worked out here — the same division of labour the stat
// bars use, where a rate crosses the wire and the browser interpolates.

describe('propScale', () => {
  /** A round instant to do arithmetic against, in the milliseconds `now` uses. */
  const NOW = 1_800_000_000_000;
  /** The same instant as the unix SECONDS an expiry is expressed in. */
  const NOW_S = NOW / 1_000;

  it('leaves everybody who is not going anywhere at full size', () => {
    // The case that runs for almost every entity in the yard, which is why the
    // function is safe to apply to all of them without first asking what
    // anything is. There is no branch in the view that knows which entities are
    // things, and this is what pays for that.
    expect(propScale(undefined, NOW)).toBe(1);
  });

  it('draws a thing with its whole life ahead of it at full size', () => {
    expect(propScale(NOW_S + PROP_LIFE_MS / 1_000, NOW)).toBe(1);
  });

  it('draws a thing halfway through its life at half size', () => {
    // The interpolation itself. Linear on purpose: the alternative is an easing
    // curve, which would make a deposit hold its size and then disappear in a
    // hurry — the opposite of the "it is on its way out" reading this is for.
    expect(propScale(NOW_S + PROP_LIFE_MS / 2_000, NOW)).toBeCloseTo(0.5, 6);
  });

  it('draws a thing that has run out at nothing', () => {
    // Nothing, not something small. The server drops it from the roster on its
    // own tick, so this is what covers the gap between the two clocks — and the
    // gap has to be covered by it VANISHING rather than by it sitting at a
    // visible minimum, which would leave a permanent speck wherever a deposit
    // once was until the next frame arrived.
    expect(propScale(NOW_S, NOW)).toBe(0);
    expect(propScale(NOW_S - 60, NOW)).toBe(0);
  });

  it('never draws a thing bigger than full size, however long it has left', () => {
    // A server whose lifetime has been retuned upward hands us more remaining
    // life than this client draws ageing for. That is a drawing decision to
    // degrade gracefully from — the thing simply sits at full size for a while
    // before it starts shrinking — and not a licence to draw a deposit larger
    // than a Ваня.
    expect(propScale(NOW_S + 10 * PROP_LIFE_MS, NOW)).toBe(1);
  });

  it('shrinks monotonically as the clock advances', () => {
    // The property a player actually observes, asserted over the whole life
    // rather than at the three points above: it only ever gets smaller.
    const expires = NOW_S + PROP_LIFE_MS / 1_000;
    let previous = Number.POSITIVE_INFINITY;
    for (let ms = 0; ms <= PROP_LIFE_MS; ms += PROP_LIFE_MS / 60) {
      const scale = propScale(expires, NOW + ms);
      expect(scale).toBeLessThanOrEqual(previous);
      expect(scale).toBeGreaterThanOrEqual(0);
      previous = scale;
    }
    expect(previous).toBe(0);
  });

  it('holds a thing at full size rather than vanishing it when the clock is unusable', () => {
    // `now` is the server's clock as the view tracks it, and before the first
    // state response lands there may not be one. Drawing at nothing would be the
    // worst possible reading of "we do not know what time it is" — the yard's
    // litter would be invisible until a pet response arrived — so an unusable
    // clock leaves everything at full size.
    expect(propScale(NOW_S + 60, Number.NaN)).toBe(1);
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

  it('notices a thing on the ground arriving, and costs nothing thereafter', () => {
    // Both halves matter and they pull opposite ways. The guard must SEE the
    // expiry, or a deposit would arrive drawn as a full-size person and stay that
    // way until something else about the yard happened to change. And it must
    // then stop seeing it, which is free only because the field is an ABSOLUTE
    // instant: it is identical on every one of the three thousand frames that
    // follow, where a "seconds left" countdown would differ on all of them and
    // re-render the entire yard five times a second.
    expect(sameAppearance([look()], [look({ expires: 1_800_000_000 })])).toBe(false);
    expect(sameAppearance([look({ expires: 1_800_000_000 })], [look()])).toBe(false);
    expect(
      sameAppearance([look({ expires: 1_800_000_000 })], [look({ expires: 1_800_000_000 })]),
    ).toBe(true);
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

// ---------------------------------------------------------------------------
// The beer store: the one rule in this game about where somebody is standing.
//
// Every test below pins a DERIVATION or a guard rather than a number. The
// threshold itself is catalogue content (`arrive_within`) and is passed in, so
// retuning it in internal/gamevanyagotchi/content.go keeps all of this green —
// which is the whole point of the client being told the number rather than
// holding one.
// ---------------------------------------------------------------------------

describe('readStore', () => {
  it('reads a well-formed block', () => {
    expect(readStore({ x: 0.82, y: 0.22, left: 6 })).toEqual({ x: 0.82, y: 0.22, left: 6 });
  });

  it('is undefined for a yard with no crate', () => {
    expect(readStore(undefined)).toBeUndefined();
    expect(readStore(null)).toBeUndefined();
  });

  it('is undefined for anything that is not a place', () => {
    // The place is what the whole block is FOR — a count with nowhere to walk
    // to could grey a button and never explain it.
    expect(readStore('yes')).toBeUndefined();
    expect(readStore({ left: 6 })).toBeUndefined();
    expect(readStore({ x: 0.5, left: 6 })).toBeUndefined();
    expect(readStore({ x: Number.NaN, y: 0.2, left: 6 })).toBeUndefined();
    expect(readStore({ x: 0.5, y: Number.POSITIVE_INFINITY, left: 6 })).toBeUndefined();
  });

  it('reads an unusable count as empty rather than as no crate at all', () => {
    // Deliberately the other way round from the place. Both grey the button, so
    // the difference is only what the player is told, and «ящик пуст» beside a
    // crate he can see is truer than pretending the yard has none.
    expect(readStore({ x: 0.8, y: 0.2 })?.left).toBe(0);
    expect(readStore({ x: 0.8, y: 0.2, left: Number.NaN })?.left).toBe(0);
    expect(readStore({ x: 0.8, y: 0.2, left: '6' })?.left).toBe(0);
    expect(readStore({ x: 0.8, y: 0.2, left: -3 })?.left).toBe(0);
  });

  it('truncates a fractional count, because half a serving is not a thing', () => {
    expect(readStore({ x: 0.8, y: 0.2, left: 2.9 })?.left).toBe(2);
  });
});

describe('sameStore', () => {
  it('is true for two yards with no crate, so an empty yard never re-renders', () => {
    expect(sameStore(undefined, undefined)).toBe(true);
  });

  it('is false when one of them has a crate', () => {
    expect(sameStore(undefined, { x: 0.8, y: 0.2, left: 6 })).toBe(false);
    expect(sameStore({ x: 0.8, y: 0.2, left: 6 }, undefined)).toBe(false);
  });

  it('is true for the same crate, which is what every frame carries', () => {
    expect(sameStore({ x: 0.8, y: 0.2, left: 6 }, { x: 0.8, y: 0.2, left: 6 })).toBe(true);
  });

  it('notices the count falling, which is the thing that actually changes', () => {
    expect(sameStore({ x: 0.8, y: 0.2, left: 6 }, { x: 0.8, y: 0.2, left: 5 })).toBe(false);
  });

  it('notices the crate being stood up somewhere else', () => {
    expect(sameStore({ x: 0.8, y: 0.2, left: 6 }, { x: 0.3, y: 0.2, left: 6 })).toBe(false);
    expect(sameStore({ x: 0.8, y: 0.2, left: 6 }, { x: 0.8, y: 0.9, left: 6 })).toBe(false);
  });
});

describe('beside', () => {
  const crate = { x: 0.8, y: 0.2 };

  it('is true standing on it', () => {
    expect(beside({ x: 0.8, y: 0.2 }, crate, 0.12)).toBe(true);
  });

  it('is true just inside the threshold and false just outside it', () => {
    expect(beside({ x: 0.8 - 0.11, y: 0.2 }, crate, 0.12)).toBe(true);
    expect(beside({ x: 0.8 - 0.13, y: 0.2 }, crate, 0.12)).toBe(false);
  });

  it('is true exactly ON the threshold — the same `<=` the server compares with', () => {
    // If this ever flips to `<`, the button and the server disagree for exactly
    // one position, which is the hardest kind of bug to be told about.
    expect(beside({ x: 0.8 - 0.12, y: 0.2 }, crate, 0.12)).toBe(true);
  });

  it('measures both axes rather than only the one that differs', () => {
    // 0.09 on each axis is 0.127 apart, which is OUTSIDE 0.12 — a version that
    // compared axes independently would call this near.
    expect(beside({ x: 0.8 - 0.09, y: 0.2 - 0.09 }, crate, 0.12)).toBe(false);
  });

  it('is false when it cannot know where he is', () => {
    // A client whose hello has not been answered yet does not know which entity
    // it is, and cannot be beside anything.
    expect(beside(undefined, crate, 0.12)).toBe(false);
  });

  it('is false when the yard has nothing to stand beside', () => {
    expect(beside({ x: 0.8, y: 0.2 }, undefined, 0.12)).toBe(false);
  });

  it('is false rather than guessing when the threshold is unusable', () => {
    // Guessing would grey at a distance nothing else in the system agrees with.
    expect(beside({ x: 0.8, y: 0.2 }, crate, undefined)).toBe(false);
    expect(beside({ x: 0.8, y: 0.2 }, crate, Number.NaN)).toBe(false);
    expect(beside({ x: 0.8, y: 0.2 }, crate, -1)).toBe(false);
  });
});

describe('shortOf', () => {
  const gated = { needs_stat: 'bladder', needs_at_least: 15 };
  const values = (v: number) => new Map([['bladder', v]]);

  it('never blocks a verb that is not gated on a stat', () => {
    expect(shortOf({}, values(0))).toBe(false);
    expect(shortOf(undefined, values(0))).toBe(false);
    expect(shortOf(null, values(0))).toBe(false);
  });

  it('blocks it below the threshold and allows it exactly on one', () => {
    expect(shortOf(gated, values(14.9))).toBe(true);
    // Exactly on it is ALLOWED, which is the server's own comparison: `apply`
    // refuses on `row.Value < action.NeedsAtLeast`, so a client that greyed at
    // equality would grey a press the server would have taken.
    expect(shortOf(gated, values(15))).toBe(false);
    expect(shortOf(gated, values(100))).toBe(false);
  });

  it('fails OPEN when the value has not arrived yet', () => {
    // A pet whose state is still in flight, not a pet with an empty bladder.
    // Greying here would make the row flicker on every load, and letting it
    // through costs at worst one «рано ещё» from the server.
    expect(shortOf(gated, new Map())).toBe(false);
    expect(shortOf(gated, new Map([['bladder', Number.NaN]]))).toBe(false);
  });

  it('fails OPEN when the served threshold is unusable', () => {
    expect(shortOf({ needs_stat: 'bladder' }, values(0))).toBe(false);
    expect(shortOf({ needs_stat: 'bladder', needs_at_least: Number.NaN }, values(0))).toBe(false);
  });

  it('looks only at WHETHER the verb names a stat, never at which one', () => {
    // The same property outOfReach has: the client holds no content keys, so a
    // verb gated on a stat this browser has never heard of behaves identically.
    const exotic = { needs_stat: 'терпение', needs_at_least: 40 };
    expect(shortOf(exotic, new Map([['терпение', 39]]))).toBe(true);
    expect(shortOf(exotic, new Map([['терпение', 40]]))).toBe(false);
  });

  it('ignores fail_chance entirely — the shy roll is never drawn as a grey button', () => {
    // `fail_chance` reaches the cheatsheet and must never reach the action row:
    // a control that greyed itself at random would read as broken.
    expect(shortOf({ fail_chance: 1 } as never, values(0))).toBe(false);
  });
});

describe('outOfReach', () => {
  const crate = { x: 0.8, y: 0.2, left: 6 };
  const gated = { needs_near: 'beer_crate' };

  it('never blocks a verb that is not gated on a place, wherever he is', () => {
    expect(outOfReach({}, undefined, false)).toBe(false);
    expect(outOfReach({}, crate, false)).toBe(false);
    expect(outOfReach(undefined, undefined, false)).toBe(false);
    expect(outOfReach(null, crate, true)).toBe(false);
  });

  it('blocks it when the yard has no crate to stand at', () => {
    expect(outOfReach(gated, undefined, true)).toBe(true);
  });

  it('blocks it when there is nothing left in the crate, even standing on it', () => {
    expect(outOfReach(gated, { ...crate, left: 0 }, true)).toBe(true);
  });

  it('blocks it from across the yard', () => {
    expect(outOfReach(gated, crate, false)).toBe(true);
  });

  it('allows it standing at a crate with something in it', () => {
    expect(outOfReach(gated, crate, true)).toBe(false);
  });

  it('looks only at WHETHER the verb names a place, never at which one', () => {
    // The load-bearing property: the client holds no content keys, so a verb
    // gated on a kind this browser has never heard of behaves identically. A
    // version that compared `needs_near` to 'beer_crate' would pass every test
    // above and fail this one.
    expect(outOfReach({ needs_near: 'something_invented_next_year' }, crate, true)).toBe(false);
    expect(outOfReach({ needs_near: 'something_invented_next_year' }, crate, false)).toBe(true);
  });
});

describe('storeLabel', () => {
  it('says nothing when the yard has no crate', () => {
    expect(storeLabel(undefined, false)).toBeNull();
  });

  it('says the crate is empty, which means wait rather than walk', () => {
    expect(storeLabel({ x: 0.8, y: 0.2, left: 0 }, true)).toBe('🍺 ящик пуст');
  });

  it('tells him to walk over, and how much is worth walking for', () => {
    expect(storeLabel({ x: 0.8, y: 0.2, left: 6 }, false)).toBe('🍺 ящик: 6 — дойди');
  });

  it('drops the instruction once he has arrived', () => {
    expect(storeLabel({ x: 0.8, y: 0.2, left: 6 }, true)).toBe('🍺 ящик: 6');
  });

  it('stays short enough for a third item in the status row at 320px', () => {
    // The row must not wrap and must not overflow — the width that has broken
    // this screen before. Pinned as a bound rather than as an exact string so
    // the copy can be reworded without a test edit.
    for (const line of [
      storeLabel({ x: 0.8, y: 0.2, left: 0 }, false),
      storeLabel({ x: 0.8, y: 0.2, left: 24 }, false),
      storeLabel({ x: 0.8, y: 0.2, left: 24 }, true),
    ]) {
      expect([...(line ?? '')].length).toBeLessThanOrEqual(20);
    }
  });
});

// ---------------------------------------------------------------------------
// The key hunt. The key itself is nowhere in these tests and cannot be: it is
// hidden server-side and never published, so the whole of what the browser is
// given is the list of places it MIGHT be under and a way of naming one.
// ---------------------------------------------------------------------------

/** A hiding place, shaped as the catalogue serves one. */
const spot = (over: Record<string, unknown> = {}) => ({
  key: 'bush',
  label: 'куст',
  emoji: '🌳',
  at: { x: 0.28, y: 0.34 },
  ...over,
});

/**
 * A catalogue with the given locations in it.
 *
 * Cast rather than built in full, deliberately: what these tests read is
 * `locations`, and spelling out five other lists to satisfy the compiler would
 * make it look as though one of them mattered.
 */
const catalogue = (over: Record<string, unknown>): VanyagotchiConfig =>
  ({ locations: [], default_location: 'yard', ...over }) as unknown as VanyagotchiConfig;

/** A location, shaped as the catalogue serves one. */
const place = (key: string, hotspots?: unknown[]) => ({
  key,
  label: key,
  entry: { x: 0.5, y: 0.5 },
  ...(hotspots ? { hotspots } : {}),
});

describe('hotspotsFor', () => {
  it('reads the hiding places of the location it was asked about', () => {
    const spots = hotspotsFor(catalogue({ locations: [place('yard', [spot()])] }), 'yard');
    expect(spots).toEqual([{ key: 'bush', label: 'куст', emoji: '🌳', at: { x: 0.28, y: 0.34 } }]);
  });

  it('never lends one location its neighbour’s hiding places', () => {
    // The assertion that is not about today. There is one location in the game
    // as it stands, so a version that flattened every location's hotspots into
    // one list would behave identically — right up to the лес arriving, at which
    // point every yard would be showing every other yard's bushes.
    const cfg = catalogue({
      locations: [place('yard', [spot()]), place('lift', [spot({ key: 'panel', label: 'панель' })])],
    });
    expect(hotspotsFor(cfg, 'yard').map((s) => s.key)).toEqual(['bush']);
    expect(hotspotsFor(cfg, 'lift').map((s) => s.key)).toEqual(['panel']);
  });

  it('is empty for a location this catalogue does not describe', () => {
    expect(hotspotsFor(catalogue({ locations: [place('yard', [spot()])] }), 'zabroshka')).toEqual(
      [],
    );
  });

  it('is empty for a location with nothing to search in', () => {
    // A real state rather than a defensive one: a location need not have a hunt.
    expect(hotspotsFor(catalogue({ locations: [place('yard')] }), 'yard')).toEqual([]);
  });

  it('is empty before the catalogue has arrived, and when nobody said where', () => {
    // The plane runs on the socket and the catalogue comes over HTTP, so a yard
    // with no config yet is the ordinary first second of every visit.
    expect(hotspotsFor(null, 'yard')).toEqual([]);
    expect(hotspotsFor(undefined, 'yard')).toEqual([]);
    expect(hotspotsFor(catalogue({ locations: [place('yard', [spot()])] }), undefined)).toEqual([]);
    expect(hotspotsFor(catalogue({ locations: [place('yard', [spot()])] }), '')).toEqual([]);
  });

  it('survives a catalogue whose locations are not a list at all', () => {
    expect(hotspotsFor(catalogue({ locations: undefined }), 'yard')).toEqual([]);
    expect(hotspotsFor(catalogue({ locations: 'yard' }), 'yard')).toEqual([]);
  });

  it('drops a hiding place with nothing to name in a claim', () => {
    // The key is what the claim carries, so a hotspot without one could only
    // ever produce a frame the server refuses — a tap that does nothing.
    const spots = hotspotsFor(
      catalogue({ locations: [place('yard', [spot({ key: '' }), spot({ key: undefined })])] }),
      'yard',
    );
    expect(spots).toEqual([]);
  });

  it('drops a hiding place with nowhere to walk to', () => {
    // The other entry requirement: arrival is measured against this point, so a
    // place without a usable one is a search that can never be completed.
    const spots = hotspotsFor(
      catalogue({
        locations: [
          place('yard', [
            spot({ key: 'a', at: undefined }),
            spot({ key: 'b', at: { x: 0.5 } }),
            spot({ key: 'c', at: { x: Number.NaN, y: 0.5 } }),
            spot({ key: 'd' }),
          ]),
        ],
      }),
      'yard',
    );
    expect(spots.map((s) => s.key)).toEqual(['d']);
  });

  it('draws a hiding place the catalogue gave no picture, rather than hiding it', () => {
    // The same choice `resolveArt` makes for a person: the place is real and the
    // key may be under it, and a client older than its server must still be able
    // to win a hunt. An untappable bush is a losing one.
    const spots = hotspotsFor(
      catalogue({ locations: [place('yard', [spot({ emoji: '' }), spot({ key: 'b', emoji: 7 })])] }),
      'yard',
    );
    expect(spots.map((s) => s.emoji)).toEqual([UNKNOWN_SPOT, UNKNOWN_SPOT]);
  });

  it('keeps a nameless hiding place tappable, with no name rather than a wire key', () => {
    const spots = hotspotsFor(
      catalogue({ locations: [place('yard', [spot({ label: undefined })])] }),
      'yard',
    );
    expect(spots).toHaveLength(1);
    expect(spots[0]?.label).toBe('');
  });

  it('freezes what it returns', () => {
    // Several things read this list in one render; none of them may sort it.
    const spots = hotspotsFor(catalogue({ locations: [place('yard', [spot()])] }), 'yard');
    expect(Object.isFrozen(spots)).toBe(true);
  });
});

describe('searchVerb', () => {
  /** A verb, shaped as the catalogue serves one. */
  const verb = (over: Partial<VanyagotchiAction> = {}): VanyagotchiAction => ({
    key: 'claim',
    label: 'искать ключи',
    emoji: '🔑',
    effects: [],
    done: 'нашёл ключи',
    revives_fatal: false,
    starts_over: false,
    ...over,
  });

  const claim = verb({ contests: 'key', needs_spot: true });
  const drink = verb({ key: 'drink', contests: 'beer_crate', needs_near: 'beer_crate' });
  const relieve = verb({ key: 'relieve' });

  it('picks the verb that races for something with no place to walk to', () => {
    expect(searchVerb([drink, relieve, claim])?.key).toBe('claim');
  });

  it('is not fooled by the verb that races for something you CAN walk to', () => {
    // «выпить пива» contests the crate as hard as the claim contests the key.
    // What tells them apart is that one of them says where to stand, because
    // the crate is visible and the key is not.
    expect(searchVerb([drink, relieve])).toBeUndefined();
  });

  it('never looks at the verb’s key, so a renamed verb is still found', () => {
    // The load-bearing property. A browser holding the string «claim» would be
    // a browser that has to be redeployed the day the verb is renamed, which is
    // exactly the coupling the wire is shaped to avoid (ADR-028).
    expect(searchVerb([verb({ key: 'rummage', contests: 'key', needs_spot: true })])?.key).toBe('rummage');
  });

  it('is nothing at all when no verb searches for anything', () => {
    expect(searchVerb([relieve, drink])).toBeUndefined();
    expect(searchVerb([])).toBeUndefined();
  });

  it('is nothing at all when TWO verbs fit, rather than guessing between them', () => {
    // The case worth knowing about when a second contested verb is added: the
    // predicate has stopped identifying one thing, and sending the wrong verb
    // with somebody else's spot on it is worse than sending none. The yard
    // simply stops offering hiding places, and the player is told nothing false.
    expect(searchVerb([claim, verb({ key: 'dig', contests: 'treasure', needs_spot: true })])).toBeUndefined();
  });

  it('tolerates a catalogue that has not arrived, or one with a broken entry', () => {
    expect(searchVerb(undefined)).toBeUndefined();
    expect(
      searchVerb([{ key: '', contests: 'key', needs_spot: true } as VanyagotchiAction, claim])?.key,
    ).toBe('claim');
  });
});

describe('spotAriaLabel', () => {
  it('says what searching this place would be', () => {
    expect(spotAriaLabel({ label: 'куст' })).toBe('искать: куст');
  });

  it('never shows a trailing colon over nothing, and never a wire key', () => {
    // A hotspot the catalogue described without a label is still drawable, so
    // this is a live path rather than a defensive one — and «искать: » or
    // «искать: bush» are both worse than saying only what is certain.
    expect(spotAriaLabel({})).toBe('искать здесь');
    expect(spotAriaLabel({ label: '' })).toBe('искать здесь');
    expect(spotAriaLabel({ label: '   ' })).toBe('искать здесь');
  });

  it('trims, because a padded name is a stranger-looking name', () => {
    expect(spotAriaLabel({ label: ' куст ' })).toBe('искать: куст');
  });
});

describe('SEARCH_WALK_MS', () => {
  it('outlasts the longest walk the plane allows', () => {
    // The backstop must never expire a search that is still genuinely walking,
    // or a far hiding place would be unsearchable for a reason nothing on screen
    // explains. The plane is a unit square in normalised coordinates, so the
    // longest journey is its diagonal — √2 — and the prose puts the walking
    // speed at about a fifth of the yard a second, which is a little over seven
    // seconds. Derived here rather than pinned so that a slower Ваня fails this
    // rather than silently losing his searches.
    const longestWalkMs = (Math.SQRT2 / 0.2) * 1_000;
    expect(SEARCH_WALK_MS).toBeGreaterThan(longestWalkMs * 1.5);
  });

  it('is short enough to be a backstop rather than a memory', () => {
    // The failure it exists to prevent is a claim firing minutes after the tap
    // that armed it, so a value long enough to feel like "forever" would be no
    // guard at all.
    expect(SEARCH_WALK_MS).toBeLessThanOrEqual(60_000);
  });
});

// ---------------------------------------------------------------------------
// The places: which one you are in, which ones you can go to, and what each of
// them is painted with. Nothing below ever names a location — every one of these
// functions is handed a key it has never seen and does arithmetic on it, which
// is the property that keeps a fifth place a backend-only change (ADR-028).
// ---------------------------------------------------------------------------

describe('hueFor', () => {
  it('gives the same string the same hue every time', () => {
    // The whole basis of it: nobody sends a colour, and every browser agrees.
    expect(hueFor('yard')).toBe(hueFor('yard'));
    expect(hueFor('les')).toBe(hueFor('les'));
  });

  it('gives different places different hues', () => {
    // The four the game actually ships. Asserted as a SET rather than as five
    // magic numbers, because what matters is that no two places are painted the
    // same, not which colour any of them happens to get.
    const keys = ['yard', 'les', 'lift', 'kusty', 'zabroshka'];
    expect(new Set(keys.map(hueFor)).size).toBe(keys.length);
  });

  it('is always a hue CSS can use', () => {
    for (const key of ['yard', 'les', '', 'x', 'очень-длинный-ключ-места', '🌳']) {
      const hue = hueFor(key);
      expect(Number.isInteger(hue)).toBe(true);
      expect(hue).toBeGreaterThanOrEqual(0);
      expect(hue).toBeLessThan(360);
    }
  });

  it('does not overflow into nonsense on a long key', () => {
    // The hash is `*31 + charCode` on a 32-bit unsigned, so a long string wraps
    // rather than reaching Infinity and modulo-ing to NaN — which would paint the
    // plane with `hsl(NaN ...)`, i.e. nothing at all.
    expect(Number.isFinite(hueFor('x'.repeat(4096)))).toBe(true);
  });
});

describe('travelPlaces', () => {
  const heads = new Map([
    ['yard', 3],
    ['les', 1],
  ]);
  const location = (key: string, label: string) => ({
    key,
    label,
    entry: { x: 0.5, y: 0.5 },
  });
  const world = (...locations: ReturnType<typeof location>[]) =>
    catalogue({ locations }) as VanyagotchiConfig;

  it('lists every place the catalogue serves, in its order', () => {
    const places = travelPlaces(world(location('yard', 'двор'), location('les', 'лес')), heads, 'yard');
    expect(places.map((p) => p.key)).toEqual(['yard', 'les']);
    expect(places.map((p) => p.label)).toEqual(['двор', 'лес']);
  });

  it('carries the head count the frame gave each place', () => {
    const places = travelPlaces(world(location('yard', 'двор'), location('les', 'лес')), heads, 'yard');
    expect(places.map((p) => p.count)).toEqual([3, 1]);
  });

  it('reads a place the tally does not mention as empty', () => {
    // The zeroes are omitted on the wire, so this is the ordinary case in a world
    // of four places and two players — not a fault.
    const places = travelPlaces(world(location('kusty', 'кусты')), heads, 'yard');
    expect(places[0]?.count).toBe(0);
  });

  it('marks the one he is standing in rather than dropping it', () => {
    // It is the row that says where he is now, and pressing it is the way out of
    // the sheet that is a control rather than a gesture.
    const places = travelPlaces(world(location('yard', 'двор'), location('les', 'лес')), heads, 'les');
    expect(places.map((p) => p.here)).toEqual([false, true]);
  });

  it('marks nothing when he is somewhere the catalogue does not describe', () => {
    const places = travelPlaces(world(location('yard', 'двор')), heads, 'atlantis');
    expect(places.some((p) => p.here)).toBe(false);
  });

  it('keeps a nameless place travellable, under a generic noun', () => {
    // The opposite of what a nameless hiding place gets, and deliberately: an
    // aria label can say less, but a row with nothing written on it is a place
    // nobody can go to.
    const places = travelPlaces(world(location('les', '   ')), heads, 'yard');
    expect(places[0]?.label).toBe(UNNAMED_PLACE);
  });

  it('never shows the player a wire key', () => {
    // The one fallback that is not allowed: «les» is what the claim carries, not
    // what the game calls the place.
    const places = travelPlaces(world(location('les', '')), heads, 'yard');
    expect(places[0]?.label).not.toBe('les');
  });

  it('trims, because a padded name is a wider row', () => {
    expect(travelPlaces(world(location('les', ' лес ')), heads, 'yard')[0]?.label).toBe('лес');
  });

  it('drops a place with nothing to name in a journey', () => {
    // The key is what `vanyagotchi_goto` carries, so a location without one could
    // only ever produce a frame the server refuses.
    const places = travelPlaces(
      catalogue({
        locations: [
          { key: '', label: 'нигде', entry: { x: 0, y: 0 } },
          { label: 'тоже нигде', entry: { x: 0, y: 0 } },
          null,
          location('yard', 'двор'),
        ],
      }),
      heads,
      'yard',
    );
    expect(places.map((p) => p.key)).toEqual(['yard']);
  });

  it('is empty before the catalogue has arrived, or when it carries no places', () => {
    expect(travelPlaces(null, heads, 'yard')).toEqual([]);
    expect(travelPlaces(undefined, heads, 'yard')).toEqual([]);
    expect(travelPlaces(catalogue({ locations: undefined }), heads, 'yard')).toEqual([]);
    expect(travelPlaces(catalogue({ locations: 'yard' }), heads, 'yard')).toEqual([]);
  });

  it('freezes what it returns', () => {
    // The sheet and the plane's own caption both read it in one render; neither
    // may sort it.
    expect(Object.isFrozen(travelPlaces(world(location('yard', 'двор')), heads, 'yard'))).toBe(true);
  });
});

describe('hereLabel', () => {
  it('names the place and counts the people in it', () => {
    expect(hereLabel('двор', 3)).toBe('двор: 3');
  });

  it('leaves the name exactly as the catalogue spells it', () => {
    // NOT INFLECTED, and that is the decision rather than laziness: «во дворе»,
    // «в лесу», «в кустах» and «на заброшке» are four different inflections of
    // the sentence this replaced, and a client cannot derive any of them from a
    // nominative label — so it prints the label and lets the colon do the work.
    expect(hereLabel('лес', 1)).toBe('лес: 1');
    expect(hereLabel('заброшка', 0)).toBe('заброшка: 0');
  });

  it('says the count alone before anybody has named the place', () => {
    // The pre-catalogue second. A placeholder noun would be inventing a name for
    // somewhere that has one.
    expect(hereLabel('', 2)).toBe('2');
    expect(hereLabel('   ', 0)).toBe('0');
  });

  it('stays short enough to be a caption on a phone-sized plane', () => {
    // It is drawn over the world rather than beside it, so a long one would reach
    // across the yard. Pinned as a bound rather than as a string so the copy can
    // be reworded without a test edit.
    expect([...hereLabel('заброшка', 24)].length).toBeLessThanOrEqual(16);
  });
});

describe('readStore / sameStore — the shop belongs to a place', () => {
  // A COORDINATE MEANS NOTHING WITHOUT A PLACE. (0.82, 0.22) is the crate in двор
  // and an empty patch of лес, so a store block read without its location would
  // put a shop in front of somebody four places away from it, at a spot where
  // there is nothing. It was harmless while the yard was the only place there
  // was; five places made it a bug.
  it('reads which place the shop is in', () => {
    expect(readStore({ x: 0.82, y: 0.22, left: 6, loc: 'les' })?.loc).toBe('les');
  });

  it('reads an absent place as absent, which the caller reads as the default', () => {
    expect(readStore({ x: 0.82, y: 0.22, left: 6 })?.loc).toBeUndefined();
    expect(readStore({ x: 0.82, y: 0.22, left: 6, loc: '' })?.loc).toBeUndefined();
    expect(readStore({ x: 0.82, y: 0.22, left: 6, loc: 7 })?.loc).toBeUndefined();
  });

  it('notices the shop standing somewhere else', () => {
    // Without this the guard never re-evaluates: the crate is at one fixed pitch,
    // so a move between locations changes nothing else about the block.
    expect(
      sameStore({ x: 0.82, y: 0.22, left: 6 }, { x: 0.82, y: 0.22, left: 6, loc: 'les' }),
    ).toBe(false);
    expect(
      sameStore({ x: 0.82, y: 0.22, left: 6, loc: 'les' }, { x: 0.82, y: 0.22, left: 6, loc: 'les' }),
    ).toBe(true);
  });
});
