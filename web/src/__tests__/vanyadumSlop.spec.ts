import { describe, expect, it } from 'vitest';
import {
  SLOP_ANGLES,
  SLOP_FINGERS,
  SLOP_SPRITE_SIZE,
  createSlopFacings,
  generateSlopSprite,
  generateSlopSprites,
  slopFacing,
  slopSpriteAngle,
  slopSpriteIndex,
} from '../lib/vanyadumSlop';

/**
 * The нейрослоп's client half: the picture, and the two angles that choose which
 * picture.
 *
 * WHY THERE IS ANYTHING TO TEST HERE AT ALL. The sprite is generated as a pure
 * `(angle, seed) → bytes` rather than drawn into a 2D canvas, and that is the
 * whole reason this file can exist: a canvas routine is untestable in jsdom and
 * unassertable anywhere, so a game that ships no art (ADR-051) would be shipping
 * pictures nobody could check. What is asserted below is what the renderer
 * actually depends on — that the same building draws the same creature, that the
 * silhouette is the hitbox rather than a matter of taste, and that a слоп walking
 * a circle round you turns rather than flipping between two drawings.
 *
 * The same reasoning is why the ONE FRAME OF MEMORY a derived facing needs lives
 * in `lib/` rather than beside the sprites it chooses: a memory keyed by an id
 * that outlives its creature is exactly the kind of thing that is wrong for a
 * single frame and invisible in a screenshot, so it is put somewhere node can
 * drive it a frame at a time.
 */

/** The catalogue's own proportion: `player.radius / player.body_height`. */
const TORSO = 0.35 / 1.8;

/**
 * A stable hash of a byte array, for the determinism claims.
 *
 * FNV-1a, and it lives in the test rather than in `lib/` because nothing in the
 * game hashes a sprite — this is the assertion's own machinery, and production
 * code carrying a helper only a test calls is exactly what this project forbids.
 */
function hash(bytes: Uint8Array): number {
  let h = 2166136261;
  for (let i = 0; i < bytes.length; i++) {
    h ^= bytes[i];
    h = Math.imul(h, 16777619);
  }
  return h >>> 0;
}

/** The sprite as generated for one of the rotations, at the default size. */
function sprite(angle: number, seed = 7): Uint8Array {
  return generateSlopSprite({ angle, torsoHalf: TORSO, seed });
}

/** Whether a pixel is part of the creature at all. */
function opaque(bytes: Uint8Array, px: number, py: number, size = SLOP_SPRITE_SIZE): boolean {
  return bytes[(py * size + px) * 4 + 3] > 0;
}

/** How dark a pixel is, 0..255, ignoring anything transparent. */
function darkness(bytes: Uint8Array, px: number, py: number, size = SLOP_SPRITE_SIZE): number {
  const i = (py * size + px) * 4;
  if (bytes[i + 3] === 0) return 0;
  return 255 - Math.max(bytes[i], bytes[i + 1], bytes[i + 2]);
}

describe('slopSpriteIndex', () => {
  it('draws the face at somebody standing where the creature is looking', () => {
    // The definition of rotation zero, and the one every other case is measured
    // against: a слоп walks at the nearest man, so the man it is walking at is
    // the one who sees its front.
    expect(slopSpriteIndex(0, 0, 8)).toBe(0);
    expect(slopSpriteIndex(1.3, 1.3, 8)).toBe(0);
    expect(slopSpriteIndex(-2.9, -2.9, 8)).toBe(0);
  });

  it('draws the back of its head at somebody it is walking away from', () => {
    expect(slopSpriteIndex(0, Math.PI, 8)).toBe(4);
    expect(slopSpriteIndex(Math.PI, 0, 8)).toBe(4);
  });

  it('never indexes off the front of the array, whichever way it turned', () => {
    // `%` keeps the sign of the dividend in JavaScript, so a viewer standing
    // just clockwise of the creature's nose is a NEGATIVE relative angle — and
    // an index of −1 is an undefined material and a black sprite.
    for (let n = -40; n <= 40; n++) {
      const index = slopSpriteIndex(0, (n * Math.PI) / 7, SLOP_ANGLES);
      expect(index).toBeGreaterThanOrEqual(0);
      expect(index).toBeLessThan(SLOP_ANGLES);
    }
  });

  it('rounds to the nearest rotation rather than to the one it has passed', () => {
    // Flooring would hold every sprite up to a whole step too long, which reads
    // as a creature that turns late and then snaps — the exact defect eight
    // rotations exist to avoid.
    const step = (2 * Math.PI) / 8;
    expect(slopSpriteIndex(0, step * 0.49, 8)).toBe(0);
    expect(slopSpriteIndex(0, step * 0.51, 8)).toBe(1);
  });

  it('agrees with the angle each rotation was generated for', () => {
    // The two halves of one convention, and they are in the same module for
    // exactly this reason: disagreeing would draw a creature walking towards you
    // with the back of its head on, which is the kind of thing nobody notices
    // for a month.
    for (let i = 0; i < SLOP_ANGLES; i++) {
      expect(slopSpriteIndex(0, slopSpriteAngle(i), SLOP_ANGLES)).toBe(i);
    }
  });
});

describe('slopFacing', () => {
  it('takes the way it is walking as the way it is looking', () => {
    // THE FIELD THE WIRE DOES NOT CARRY. A слоп walks at whoever it is chasing
    // and does nothing else, so its facing is derivable from two of its own
    // positions — twelve bytes a creature a tick, and at the population and the
    // snapshot rate that is most of what a third one would have cost.
    expect(slopFacing(0, 0, 1, 0, 99)).toBeCloseTo(0, 9);
    expect(slopFacing(0, 0, 0, 1, 99)).toBeCloseTo(Math.PI / 2, 9);
    expect(slopFacing(3, 3, 2, 3, 99)).toBeCloseTo(Math.PI, 9);
  });

  it('keeps facing the way it last moved when it has not moved at all', () => {
    // A creature pressed against a wall, or a frame drawn while the
    // interpolation buffer was holding its newest. Snapping to zero would point
    // every stuck слоп in the building east.
    expect(slopFacing(4, 9, 4, 9, 1.234)).toBe(1.234);
  });

  it('believes a step small enough to be one drawn frame of a walk', () => {
    // The floor is a floating-point one, not a judgement about walking: at 144
    // frames a second an interpolated слоп moves about two centimetres between
    // two draws, and that has to count.
    expect(slopFacing(0, 0, 0.02, 0, 99)).toBeCloseTo(0, 9);
    // And a difference of a tenth of a millimetre does not, because that is
    // rounding rather than movement.
    expect(slopFacing(0, 0, 0.0001, 0, 1.5)).toBe(1.5);
  });
});

describe('createSlopFacings', () => {
  /** The eye, standing well to the east so a bearing of zero means "at me". */
  const eye = { x: 100, y: 0 };

  it('faces a creature it has never seen at the man watching it', () => {
    // The honest guess for the one frame before it has moved and said so
    // itself: walking at the nearest man is the entire creature.
    const facings = createSlopFacings();
    const [drawn] = facings.frame([{ id: 1, x: 0, y: 0 }], eye);
    expect(drawn.facing).toBeCloseTo(0, 9);
    expect(drawn.bearing).toBeCloseTo(0, 9);
  });

  it('takes the way it walked between two frames as the way it looks', () => {
    const facings = createSlopFacings();
    facings.frame([{ id: 1, x: 0, y: 0 }], eye);
    const [north] = facings.frame([{ id: 1, x: 0, y: 1 }], eye);
    expect(north.facing).toBeCloseTo(Math.PI / 2, 9);
    // And it keeps facing that way while it is not moving — pressed against a
    // wall, or drawn while the interpolation buffer was holding.
    const [held] = facings.frame([{ id: 1, x: 0, y: 1 }], eye);
    expect(held.facing).toBeCloseTo(Math.PI / 2, 9);
  });

  it('gives a reused id no part of the dead creature it inherited', () => {
    // THE BUG THIS MAP EXISTS TO MAKE IMPOSSIBLE. An id is a PLACE in the
    // building and is handed to the next нейрослоп once its holder is shot, so
    // a remembered position outlives the creature it was measured on. Deriving
    // across the hand-over points the newcomer along a bearing over the whole
    // заброшка that nothing ever walked — one drawn frame, and it is the frame
    // where a player is most likely to be looking straight at it.
    const facings = createSlopFacings();
    facings.frame([{ id: 1, x: 0, y: 0 }], eye);
    // It dies: the array simply stops carrying it, which is the whole of dying.
    facings.frame([], eye);
    // The building puts a new one in the same place, far from where the last
    // one fell. Its first frame faces the viewer, exactly as any other first
    // sight does — NOT along the twenty metres between the two creatures.
    const [fresh] = facings.frame([{ id: 1, x: 0, y: 20 }], eye);
    const [firstSight] = createSlopFacings().frame([{ id: 1, x: 0, y: 20 }], eye);
    expect(fresh.facing).toBeCloseTo(firstSight.facing, 9);
  });

  it('starts a creature fresh when it walks back into view', () => {
    // The same forgetting, for the other reason a слоп leaves the array: the
    // snapshot is cut to the rooms this player can see into, so one that walked
    // off and came back has moved a distance nobody watched. Deriving a facing
    // across that gap is the same bearing over nothing.
    const facings = createSlopFacings();
    facings.frame([{ id: 2, x: 0, y: 0 }], eye);
    facings.frame([], eye);
    facings.frame([], eye);
    const [back] = facings.frame([{ id: 2, x: 0, y: 9 }], eye);
    expect(back.facing).toBeCloseTo(Math.atan2(eye.y - 9, eye.x - 0), 9);
  });

  it('keeps two creatures apart, and hands each one its own слоп back', () => {
    // Nothing may be derived across ids: each answer carries the слоп it was
    // computed for, so the caller draws what it is given rather than looking
    // every creature back up.
    const facings = createSlopFacings();
    facings.frame(
      [
        { id: 1, x: 0, y: 0, z: 4 },
        { id: 2, x: 10, y: 0, z: 7 },
      ],
      eye,
    );
    const drawn = facings.frame(
      [
        { id: 1, x: 0, y: 1, z: 4 },
        { id: 2, x: 10, y: -1, z: 7 },
      ],
      eye,
    );
    expect(drawn.map((d) => d.id)).toEqual([1, 2]);
    expect(drawn[0].facing).toBeCloseTo(Math.PI / 2, 9);
    expect(drawn[1].facing).toBeCloseTo(-Math.PI / 2, 9);
    // And whatever else the caller hung on a слоп rides through untouched —
    // the renderer needs the floor it is standing on to place the billboard.
    expect(drawn.map((d) => d.z)).toEqual([4, 7]);
  });

  it('measures the bearing from the creature to the eye, this frame', () => {
    // The sprite is chosen against where the camera is standing NOW: it is
    // passed in rather than read off the camera because the camera is not moved
    // until the render call, and a rotation chosen against last frame's
    // viewpoint lags the world by exactly the amount a fast turn makes visible.
    const facings = createSlopFacings();
    const [north] = facings.frame([{ id: 1, x: 0, y: 0 }], { x: 0, y: 5 });
    expect(north.bearing).toBeCloseTo(Math.PI / 2, 9);
    const [west] = facings.frame([{ id: 1, x: 0, y: 0 }], { x: -5, y: 0 });
    expect(west.bearing).toBeCloseTo(Math.PI, 9);
  });
});

describe('generateSlopSprite', () => {
  it('gives the same bytes for the same building, every time', () => {
    // A reload mid-visit must not repaint the creature, exactly as it must not
    // repaint the walls. The hash is over the whole RGBA buffer, so it pins the
    // shading as well as the shape — change either on purpose or not at all.
    expect(hash(sprite(0))).toBe(hash(sprite(0)));
    expect(hash(sprite(0))).toBe(2418336167);
  });

  it('changes the skin with the building and never the silhouette', () => {
    // The seed drives the chromatic drift and the grain and NOTHING else, which
    // is deliberate: the shape is the catalogue's, and a слоп whose outline
    // varied by building would be a hitbox that varied by building.
    const a = sprite(0, 1);
    const b = sprite(0, 900);
    expect(hash(a)).not.toBe(hash(b));
    for (let i = 3; i < a.length; i += 4) expect(a[i]).toBe(b[i]);
  });

  it('is either fully there or not there at all, never half', () => {
    // A soft edge would need sorting against everything else transparent in the
    // scene, and it would let a player see the wall through the creature's own
    // outline — which is a way of seeing through a wall.
    const bytes = sprite(0);
    for (let i = 3; i < bytes.length; i += 4) {
      expect(bytes[i] === 0 || bytes[i] === 255).toBe(true);
    }
  });

  it('leaves the corners empty and puts a creature in the middle', () => {
    const bytes = sprite(0);
    const last = SLOP_SPRITE_SIZE - 1;
    expect(opaque(bytes, 0, 0)).toBe(false);
    expect(opaque(bytes, last, 0)).toBe(false);
    expect(opaque(bytes, 0, last)).toBe(false);
    expect(opaque(bytes, last, last)).toBe(false);
    // The chest, half way up and in the middle of the sprite.
    expect(opaque(bytes, SLOP_SPRITE_SIZE / 2, Math.round(SLOP_SPRITE_SIZE * 0.5))).toBe(true);
  });

  it('draws a different picture for every rotation', () => {
    // Eight drawings that were the same drawing would be eight times the memory
    // for a cardboard cut-out.
    const hashes = new Set<number>();
    for (let i = 0; i < SLOP_ANGLES; i++) hashes.add(hash(sprite(slopSpriteAngle(i))));
    expect(hashes.size).toBe(SLOP_ANGLES);
  });

  it('shows the face only while it is pointing somewhere near you', () => {
    // A front-facing feature is drawn by how much of it a viewer gets, which
    // reaches zero as the head turns side-on and stays there behind it. Without
    // that, a слоп walking away from you would be watching you over its own
    // skull.
    const band = (bytes: Uint8Array) => {
      // The head, which is the top sixth of the sprite.
      let ink = 0;
      for (let py = 0; py < SLOP_SPRITE_SIZE * 0.22; py++) {
        for (let px = 0; px < SLOP_SPRITE_SIZE; px++) {
          if (darkness(bytes, px, py) > 150) ink++;
        }
      }
      return ink;
    };
    expect(band(sprite(0))).toBeGreaterThan(0);
    expect(band(sprite(Math.PI))).toBe(0);
    // And it fades rather than switching off at one angle.
    expect(band(sprite(Math.PI / 4))).toBeGreaterThan(0);
    expect(band(sprite(Math.PI / 4))).toBeLessThan(band(sprite(0)));
  });

  it('has too many fingers, which is the whole reason it is a sprite', () => {
    // A generated hand with too many fingers is the single most recognisable
    // tell of a bad model, and there is nowhere to put one on a capsule.
    expect(SLOP_FINGERS).toBeGreaterThan(5);
  });

  it('reaches wider than the body the server actually shoots at', () => {
    // THE TORSO IS THE HITBOX and the limbs are not, which is what the arms
    // reaching outside it says on screen. The server stands one disc of
    // `player.radius` where a слоп is; a sprite drawn no wider than that would
    // be a creature with its arms inside its own collision cylinder.
    const bytes = sprite(0);
    // The hands' height, a little under half way up.
    const py = Math.round((1 - 0.41) * SLOP_SPRITE_SIZE);
    let widest = 0;
    for (let px = 0; px < SLOP_SPRITE_SIZE; px++) {
      if (!opaque(bytes, px, py)) continue;
      widest = Math.max(widest, Math.abs((px + 0.5) / SLOP_SPRITE_SIZE - 0.5));
    }
    expect(widest).toBeGreaterThan(TORSO);
  });

  it('is proportioned by the catalogue rather than by taste', () => {
    // A wider torso is a wider creature, because the number it is drawn from is
    // the one the server shoots at.
    expect(hash(generateSlopSprite({ angle: 0, torsoHalf: TORSO, seed: 3 }))).not.toBe(
      hash(generateSlopSprite({ angle: 0, torsoHalf: TORSO * 1.4, seed: 3 })),
    );
  });
});

describe('generateSlopSprites', () => {
  it('answers with one picture per rotation, in the order they are indexed', () => {
    const all = generateSlopSprites(TORSO, 7);
    expect(all).toHaveLength(SLOP_ANGLES);
    expect(hash(all[0])).toBe(hash(sprite(0)));
    expect(hash(all[3])).toBe(hash(sprite(slopSpriteAngle(3))));
  });

  it('produces buffers a texture upload will take', () => {
    const [front] = generateSlopSprites(TORSO, 7, 16, 4);
    expect(front).toHaveLength(16 * 16 * 4);
  });
});
