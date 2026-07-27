import { describe, expect, it } from 'vitest';
import {
  BAND_PROPERTY,
  DEPTH_PROPERTY,
  DEPTH_SCALES,
  LABEL_MAX,
  PEER_BASE_PX,
  PROP_LIFE_MS,
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
  beside,
  capLabel,
  capSay,
  huntRestarted,
  isRenderablePosition,
  outOfReach,
  propScale,
  readAppearances,
  readHere,
  readStore,
  sameStore,
  storeLabel,
  readHunt,
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
