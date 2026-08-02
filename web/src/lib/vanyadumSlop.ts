/**
 * «ВАНЯДУМ» — the нейрослоп: the sprite it is drawn as, the two angles that
 * decide which sprite, and the one frame of memory one of those angles is
 * derived from.
 *
 * WHY A SPRITE AND NOT A MESH, WHICH IS THE ONE DESIGN DECISION IN THIS FILE.
 * Everything else in this building is built out of boxes and capsules, and a
 * слоп could have been too. It is a billboard because of what it has to SAY: a
 * нейрослоп is supposed to look like something a bad model generated — smooth,
 * over-rendered, lit from nowhere, with too many fingers. That is a sentence a
 * deliberately bad texture can speak and a low-poly mesh cannot, and it is the
 * same reason Doom drew its monsters this way rather than the one Doom actually
 * had.
 *
 * AND IT IS GENERATED INTO A TYPED ARRAY, NOT DRAWN INTO A CANVAS — the rule
 * vanyadumTexture states for walls, and it binds harder here. A
 * `(angle, seed) → Uint8Array` is a pure function node can run and a test can
 * hash; a 2D canvas routine is untestable in jsdom and unassertable anywhere.
 * This game ships no art at all (ADR-051), so a sprite that cannot be asserted
 * on is a picture nobody can check.
 *
 * SEVERAL ANGLES APIECE, and they are ROTATIONS OF A MODEL rather than eight
 * unrelated drawings. Every part of the creature is placed in its own body space
 * — sideways and forwards — and projected for the angle being drawn, so an arm
 * held out in front really does swing across the body as the thing turns, and a
 * face is only drawn while it is pointing at you. Eight of them, which is what
 * Doom used, and enough that a слоп walking a circle round you never reads as a
 * cardboard cut-out.
 *
 * Pure, and node-testable, like everything else in `lib/`. The renderer uploads
 * what this produces and knows nothing about how it was made.
 */

import { parseHex, rng } from './vanyadumTexture';

/**
 * Every sprite is square and this many pixels a side.
 *
 * Sixty-four, against the walls' 128: it is drawn at most two metres tall on a
 * phone, it is filtered NEAREST like everything else here, and a crunchy sprite
 * is the look rather than a compromise.
 */
export const SLOP_SPRITE_SIZE = 64;

/**
 * How many rotations are generated. Doom's number, and the reason is the same:
 * fewer and a слоп crossing in front of you snaps between poses, more and each
 * one is a texture nobody can tell from its neighbour.
 */
export const SLOP_ANGLES = 8;

/**
 * How many fingers a нейрослоп has on each hand.
 *
 * SEVEN, AND THAT IS THE JOKE RATHER THAN A DETAIL. A generated hand with too
 * many fingers is the single most recognisable tell of a bad model, and it is
 * the whole reason this creature is a drawn sprite instead of a capsule: there
 * is nowhere to put it on a mesh this game would plausibly build.
 */
export const SLOP_FINGERS = 7;

/**
 * The colours it is drawn in.
 *
 * NOT FROM THE CATALOGUE, and deliberately unlike a pickup's `tint`: the server
 * publishes a слоп's rules — what it costs, how fast it is, what kills it — and
 * no appearance at all, because how a client draws one is a rendering decision.
 * The palette is chosen to say "rendered by something that did not understand
 * the brief": clay skin, a cyan rim light coming from no light in this building,
 * and a specular hotspot on a creature made of meat.
 */
const SKIN = parseHex('#c8a58f');
const SHADOW = parseHex('#4b3a3f');
const RIM = parseHex('#8fe3ff');
const SHEEN = parseHex('#fff6e2');
const EYE = parseHex('#120c14');

/**
 * How far a слоп must have moved between two positions before that movement is
 * believed to be its facing, in square metres.
 *
 * One millimetre squared. It is a FLOATING-POINT FLOOR rather than a judgement
 * about walking: the two positions are interpolated, so a слоп standing against
 * a wall — or one being held because the buffer ran dry — differs from itself by
 * rounding rather than by nothing at all. Anything above this at any frame rate
 * a phone can manage is real movement: the creature covers a couple of
 * centimetres per drawn frame at 144 Hz and sixteen at the tick rate.
 */
const FACING_FLOOR = 1e-6;

/** The world angle sprite `index` is drawn for; zero is nose to nose. */
export function slopSpriteAngle(index: number, count = SLOP_ANGLES): number {
  return (index * 2 * Math.PI) / count;
}

/**
 * Which of the generated rotations to draw, given where the creature is looking
 * and where the viewer is standing.
 *
 * Both angles are in the SERVER'S floor plane and both come from this client, so
 * nothing here has to agree with the wire: the слоп's facing is derived from two
 * of its own positions and the bearing is measured from the camera to it. What
 * matters is only the difference — zero means the viewer is standing where the
 * creature is looking, which is the sprite drawn with its face on.
 */
export function slopSpriteIndex(
  facing: number,
  bearing: number,
  count = SLOP_ANGLES,
): number {
  const step = (2 * Math.PI) / count;
  const turned = bearing - facing;
  // `%` keeps the sign of the dividend in JavaScript, so a negative angle would
  // index backwards off the front of the array without the second modulo.
  return ((Math.round(turned / step) % count) + count) % count;
}

/**
 * Which way a слоп is facing, from where it was and where it is now.
 *
 * THE WIRE DOES NOT CARRY IT, and that is a measured decision on the server
 * rather than an omission: a слоп walks at the man it is chasing and does
 * nothing else, so the way it is pointing IS the way it is travelling, and two
 * consecutive positions give it for nothing. Twelve bytes a creature a tick,
 * derived — which at the population and the snapshot rate is most of what a
 * third нейрослоп would have cost.
 *
 * `fallback` is what to keep believing when the two positions are the same
 * point: one pressed against a wall, or a frame drawn while the interpolation
 * buffer was holding. Facing the way it last moved is right in both cases, and
 * far better than snapping to zero — which would point every stuck слоп east.
 */
export function slopFacing(
  fromX: number,
  fromY: number,
  toX: number,
  toY: number,
  fallback: number,
): number {
  const dx = toX - fromX;
  const dy = toY - fromY;
  if (dx * dx + dy * dy < FACING_FLOOR) return fallback;
  return Math.atan2(dy, dx);
}

/** A нейрослоп as the frame being drawn places it, for facing purposes only. */
export interface FacedSlop {
  id: number;
  x: number;
  y: number;
}

/** Where the eye is standing this frame, in the server's own floor plane. */
export interface SlopViewer {
  x: number;
  y: number;
}

/** The two angles that choose one creature's sprite in the frame being drawn. */
export interface SlopView {
  /** Which way it is walking, derived from where it was one frame ago. */
  facing: number;
  /** The bearing from the creature to the eye watching it. */
  bearing: number;
}

/**
 * The per-id memory `slopFacing` needs, and the one rule about forgetting it.
 *
 * WHY THE REMEMBERING IS NOT LEFT TO THE RENDERER. A facing derived from two
 * positions needs somewhere to keep the first one, and an id is a PLACE in the
 * building rather than a creature: it is handed to the next нейрослоп once this
 * one is shot, and it also drops out of the snapshot whenever the creature walks
 * into a room this player cannot see into. So a remembered position outlives the
 * thing it was measured on, and a facing derived across the hand-over is a
 * bearing over the whole заброшка that nothing ever walked — one drawn frame of
 * a creature aimed at nowhere, and it is the first frame it is ever seen in.
 *
 * THE FORGETTING IS THEREFORE STRUCTURAL RATHER THAN A GUARD: what a frame did
 * not carry is dropped, so an id that comes back is seeded exactly as one seen
 * for the first time — and there is no branch anybody can forget to write. It is
 * the same discipline, for the same reason, that keeps `createSlopMarks`
 * honest, and it is NOT covered by the interpolation buffer's argument for
 * having no `forget` for слопы: that argument is about the frames that buffer
 * holds, and this map is neither in it nor bounded by it.
 *
 * A CREATURE SEEN FOR THE FIRST TIME FACES THE VIEWER. It is the honest guess —
 * walking at the nearest man is the entire creature — and it costs at most the
 * one frame before it has moved and said so itself.
 *
 * Pure and node-testable, like everything else in `lib/`, which is the point:
 * the renderer holds the GPU and this holds the decision, so the decision can be
 * asserted on without one.
 */
export function createSlopFacings() {
  /** Where each нейрослоп was on the PREVIOUS drawn frame, and where it went. */
  let last = new Map<number, { x: number; y: number; facing: number }>();

  return {
    /**
     * The angles for the frame now being drawn, one per creature and in the
     * order they arrived.
     *
     * EACH ANSWER CARRIES ITS OWN СЛОП rather than being keyed by id, so the
     * caller draws what it is given instead of looking every creature back up —
     * and there is no absent case for it to invent a facing for. Whatever else
     * the caller hangs on a слоп rides through untouched, which is how the
     * renderer keeps the height it needs and this file keeps knowing nothing
     * about it.
     *
     * ONE CALL PER DRAWN FRAME, because "the previous frame" is what the whole
     * thing is measured against: a second call inside one frame would compare a
     * creature against itself and hold every facing still.
     */
    frame<T extends FacedSlop>(slops: readonly T[], viewer: SlopViewer): (T & SlopView)[] {
      const out: (T & SlopView)[] = [];
      // Built alongside rather than written into `last`, so nothing this frame
      // reads has already been overwritten — and so the ids this frame did not
      // carry are gone by construction when the two are swapped below.
      const now = new Map<number, { x: number; y: number; facing: number }>();
      for (const s of slops) {
        const bearing = Math.atan2(viewer.y - s.y, viewer.x - s.x);
        const was = last.get(s.id);
        const facing = was ? slopFacing(was.x, was.y, s.x, s.y, was.facing) : bearing;
        out.push({ ...s, facing, bearing });
        now.set(s.id, { x: s.x, y: s.y, facing });
      }
      last = now;
      return out;
    },
  };
}

/** What a sprite is generated from. */
export interface SlopSpriteOptions {
  /** Where the viewer is standing relative to its face; zero is nose to nose. */
  angle: number;
  /**
   * Half the torso's width, as a fraction of the sprite's own side.
   *
   * IT IS THE HITBOX, exactly as a peer's capsule is. The server walks a слоп
   * with the player's own collision resolver and shoots at it with the player's
   * own body model — one disc of `player.radius`, `player.body_height` tall — so
   * this is `radius / body_height` and never a number chosen because it looked
   * right. The limbs reach outside it, which is honest: an arm is not a target
   * and a torso is.
   */
  torsoHalf: number;
  /** Which building this is, so two заброшки do not grow identical creatures. */
  seed: number;
  size?: number;
}

/** One ellipse of the figure, already projected into the sprite's plane. */
interface Blob {
  h: number;
  v: number;
  a: number;
  b: number;
}

/** One limb, likewise. */
interface Limb {
  h0: number;
  v0: number;
  h1: number;
  v1: number;
  r: number;
}

/**
 * The creature as this angle sees it.
 *
 * Built ONCE PER SPRITE rather than per pixel — a couple of dozen shapes against
 * four thousand pixels, so projecting inside the raster loop would be four
 * thousand copies of the same trigonometry.
 */
interface Figure {
  blobs: Blob[];
  limbs: Limb[];
  /** The eyes and the mouth: colour rather than coverage. */
  face: Blob[];
  /** How much of the face is pointing at the viewer, 0..1. */
  facing: number;
}

/**
 * Places every part for one viewing angle.
 *
 * THE PROJECTION IS THE WHOLE OF THE ROTATION. Each part is placed in body space
 * — `bx` across the creature, `bz` in front of it — and the viewer stands at
 * `(sin θ, cos θ)` in that space, so the screen's own rightwards axis is
 * `(cos θ, −sin θ)` and a part's horizontal place is one dot product. That makes
 * a hand held out in front swing across the body as the thing turns, and it
 * makes the face vanish behind the head at exactly the angle it should, both
 * without a second drawing.
 *
 * Vertical places are not projected at all: nothing in this game looks at a слоп
 * from above, and a billboard has no pitch to project onto.
 *
 * Every length here is a fraction of the sprite's own side, which is the
 * creature's height — so the numbers read as proportions of a man and stay
 * meaningful whatever `body_height` the catalogue serves.
 */
function buildFigure(angle: number, torsoHalf: number): Figure {
  const cos = Math.cos(angle);
  const sin = Math.sin(angle);
  /** A body-space point's place across the sprite. */
  const at = (bx: number, bz: number) => bx * cos - bz * sin;
  const R = torsoHalf;

  const blobs: Blob[] = [
    // The gut, wider than the chest and lower — the silhouette of something
    // that was generated rather than designed.
    { h: at(0, 0), v: 0.44, a: R * 1.18, b: 0.13 },
    { h: at(0, 0), v: 0.54, a: R * 1.02, b: 0.16 },
    // The head, sat slightly forward of the shoulders.
    { h: at(0, 0.03), v: 0.845, a: 0.082, b: 0.092 },
  ];
  const limbs: Limb[] = [
    // A neck, because a head floating over a torso reads as a bug rather than
    // as a bad model. Short and thick: a long one reads as a giraffe, which is
    // a different joke.
    { h0: at(0, 0.02), v0: 0.69, h1: at(0, 0.03), v1: 0.76, r: 0.05 },
  ];

  // Legs, one forward and one back, so the thing reads as mid-stride from every
  // angle it can be seen from — including the two where the stride is the only
  // thing distinguishing the pose from a standing one.
  for (const side of [-1, 1]) {
    const stride = side * 0.18 * R;
    limbs.push({
      h0: at(side * 0.5 * R, 0),
      v0: 0.35,
      h1: at(side * 0.58 * R, stride),
      v1: 0.03,
      r: 0.047,
    });
  }

  // Arms, held well clear of the body and reaching forward, and hands at the end
  // of them. The width is what makes the fingers legible — an arm hugging the
  // torso merges into one silhouette and takes the joke with it — and the
  // forward reach is what makes the rotation visible: at the front the hands sit
  // outside the body, in profile one crosses it and the other swings out.
  for (const side of [-1, 1]) {
    const shoulder: [number, number] = [side * R * 1.05, 0];
    const elbow: [number, number] = [side * R * 1.75, 0.1];
    const hand: [number, number] = [side * R * 1.6, 0.34];
    limbs.push({
      h0: at(...shoulder),
      v0: 0.655,
      h1: at(...elbow),
      v1: 0.55,
      r: 0.038,
    });
    limbs.push({ h0: at(...elbow), v0: 0.55, h1: at(...hand), v1: 0.39, r: 0.032 });
    const palmH = at(...hand);
    blobs.push({ h: palmH, v: 0.39, a: 0.05, b: 0.05 });
    // AND THEN TOO MANY FINGERS. They are fanned in the sprite's own plane
    // rather than in body space, deliberately: a splayed hand reads as splayed
    // from wherever it is seen, and fingers projected properly would collapse
    // into one blob at exactly the angles the joke has to survive.
    for (let f = 0; f < SLOP_FINGERS; f++) {
      const spread = SLOP_FINGERS > 1 ? f / (SLOP_FINGERS - 1) : 0.5;
      const phi = -0.35 - spread * 2.45;
      limbs.push({
        h0: palmH,
        v0: 0.39,
        h1: palmH + Math.cos(phi) * 0.085 * side,
        v1: 0.39 + Math.sin(phi) * 0.085,
        r: 0.014,
      });
    }
  }

  // The face, and it is drawn only while it is pointing somewhere near you. A
  // front-facing feature's normal is straight ahead, so how much of it a viewer
  // gets is the cosine of where he is standing — which reaches zero exactly as
  // the head turns side-on and stays there behind it.
  const facing = Math.max(0, cos);
  const face: Blob[] = [
    { h: at(-0.036, 0.085), v: 0.862, a: 0.019, b: 0.021 },
    { h: at(0.03, 0.085), v: 0.858, a: 0.017, b: 0.019 },
    // A third one, lower and smaller. Nothing has three eyes; that is the point.
    { h: at(0.064, 0.075), v: 0.822, a: 0.012, b: 0.013 },
    // A mouth, smeared sideways rather than drawn.
    { h: at(0.004, 0.082), v: 0.783, a: 0.038, b: 0.011 },
  ];

  return { blobs, limbs, face, facing };
}

/** How far inside an ellipse a point is: 1 at the centre, 0 on the edge. */
function inEllipse(h: number, v: number, e: Blob): number {
  return 1 - Math.hypot((h - e.h) / e.a, (v - e.v) / e.b);
}

/** The same for a capsule — a segment with a thickness. */
function inCapsule(h: number, v: number, c: Limb): number {
  const dh = c.h1 - c.h0;
  const dv = c.v1 - c.v0;
  const len = dh * dh + dv * dv;
  let t = len > 0 ? ((h - c.h0) * dh + (v - c.v0) * dv) / len : 0;
  t = t < 0 ? 0 : t > 1 ? 1 : t;
  return 1 - Math.hypot(h - (c.h0 + dh * t), v - (c.v0 + dv * t)) / c.r;
}

function clamp255(value: number): number {
  return value < 0 ? 0 : value > 255 ? 255 : value | 0;
}

function mix(a: number, b: number, t: number): number {
  return a + (b - a) * t;
}

/**
 * Generates one нейрослоп sprite as RGBA bytes.
 *
 * The result is `size * size * 4` long and is what a DataTexture takes directly.
 *
 * ALPHA IS BINARY — 0 or 255, never between. A soft edge would need the sprite
 * sorted against every other transparent thing in the scene to look right, and
 * it would let a player see the wall through the creature's outline, which is a
 * way of seeing through a wall. A hard cut is also what makes the silhouette
 * crunchy at NEAREST filtering, which is the look this whole game is a joke
 * about.
 *
 * THE SHADING IS THE CHARACTERISATION. A rim light in a colour no lamp in this
 * building emits, a specular hotspot on something made of meat, a smooth
 * volumetric falloff with no facets in it and a slow chromatic drift across the
 * skin: individually four cheap tricks, together the exact look of a render
 * nobody art-directed. None of it is lit by the level, because the creature is
 * not supposed to belong to it.
 */
export function generateSlopSprite(opts: SlopSpriteOptions): Uint8Array<ArrayBuffer> {
  const size = opts.size ?? SLOP_SPRITE_SIZE;
  // Backed by an explicit ArrayBuffer, so the type is the non-shared one a GPU
  // upload accepts — the same reason vanyadumTexture spells it out.
  const out = new Uint8Array(new ArrayBuffer(size * size * 4));
  const fig = buildFigure(opts.angle, opts.torsoHalf);
  const grain = rng(opts.seed);
  // A per-building phase for the chromatic drift, which together with the grain
  // above is the whole of what the seed touches — two заброшки grow differently
  // smeared creatures of exactly the same shape. THE SEED CHANGES THE SKIN AND
  // NEVER THE SILHOUETTE: the outline comes from the catalogue's own
  // proportions, and one that varied by building would be a hitbox that varied
  // by building.
  const phase = ((opts.seed >>> 0) % 1024) / 1024 * Math.PI * 2;

  for (let py = 0; py < size; py++) {
    // Quad-side units for both axes, so every number in `buildFigure` is a
    // fraction of the creature's own height and the sprite's squareness is
    // stated once, here, rather than assumed everywhere.
    const v = 1 - (py + 0.5) / size;
    for (let px = 0; px < size; px++) {
      const h = (px + 0.5) / size - 0.5;
      const i = (py * size + px) * 4;
      const speck = (grain() - 0.5) * 11;

      let cov = -1;
      for (const b of fig.blobs) {
        const d = inEllipse(h, v, b);
        if (d > cov) cov = d;
      }
      for (const l of fig.limbs) {
        const d = inCapsule(h, v, l);
        if (d > cov) cov = d;
      }
      if (cov <= 0) {
        out[i] = 0;
        out[i + 1] = 0;
        out[i + 2] = 0;
        out[i + 3] = 0;
        continue;
      }

      // How far in from the silhouette this pixel is, saturating well before the
      // middle so the rim is a band round the edge rather than a gradient over
      // the whole creature.
      const depth = Math.pow(Math.min(1, cov * 3.2), 0.55);
      const rim = Math.pow(1 - depth, 2.2);
      const hot = Math.max(
        0,
        1 - Math.hypot((h + 0.13) / 0.17, (v - 0.72) / 0.17),
      );
      const spec = hot * hot * hot;

      const channel = [0, 0, 0];
      for (let c = 0; c < 3; c++) {
        // Two and a bit radians apart, so the drift moves through the hues
        // rather than brightening and darkening all three together.
        const drift = Math.sin(v * 23 + h * 14 + phase + c * 2.1) * 7;
        channel[c] =
          mix(SHADOW[c], SKIN[c], depth) + rim * RIM[c] * 0.42 + spec * SHEEN[c] * 0.55 + drift + speck;
      }

      // The face last, over whatever the body did — it is a marking on the skin
      // rather than a part of the silhouette, and it fades out with the angle
      // instead of disappearing at one.
      if (fig.facing > 0) {
        for (const f of fig.face) {
          const d = inEllipse(h, v, f);
          if (d <= 0) continue;
          const ink = Math.min(1, d * 2.6) * fig.facing;
          for (let c = 0; c < 3; c++) channel[c] = mix(channel[c], EYE[c], ink);
        }
      }

      for (let c = 0; c < 3; c++) out[i + c] = clamp255(channel[c]);
      out[i + 3] = 255;
    }
  }
  return out;
}

/**
 * Every rotation of one building's нейрослоп, in sprite-index order.
 *
 * Here rather than in the renderer so that the index-to-angle convention lives
 * beside `slopSpriteIndex`, which is the only other thing that has an opinion
 * about it. The two disagreeing would draw a creature walking towards you with
 * the back of its head on, which is precisely the kind of thing nobody notices
 * for a month.
 */
export function generateSlopSprites(
  torsoHalf: number,
  seed: number,
  size = SLOP_SPRITE_SIZE,
  count = SLOP_ANGLES,
): Uint8Array<ArrayBuffer>[] {
  const out: Uint8Array<ArrayBuffer>[] = [];
  for (let i = 0; i < count; i++) {
    out.push(
      generateSlopSprite({ angle: slopSpriteAngle(i, count), torsoHalf, seed, size }),
    );
  }
  return out;
}
