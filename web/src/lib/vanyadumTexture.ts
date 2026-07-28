/**
 * «ВАНЯДУМ» — every texture in the game, generated from a few numbers.
 *
 * THIS GAME SHIPS NO ART. Not a sprite, not a tile, not a byte in the blob store
 * — which is a deliberate departure from «Смолтолк в Химках», whose pictures
 * live in Postgres, and it is what makes a заброшка cost nothing on the wire:
 * the whole of a level's appearance is five short catalogue entries and a
 * pattern name.
 *
 * AND IT IS GENERATED INTO A TYPED ARRAY, NOT DRAWN INTO A CANVAS. That is the
 * load-bearing choice in this file, and it is made for testability rather than
 * for speed. A `(params, size, seed) → Uint8Array` is an ordinary pure function
 * that node can run and a test can assert on — the same seed must give the same
 * bytes, a brick pattern must actually contain mortar — where the obvious
 * alternative, drawing with a 2D canvas context, is untestable in jsdom and
 * unassertable anywhere. The renderer wraps the result in a DataTexture; nothing
 * else in the game knows how a texture was made.
 */

/** A surface as the catalogue describes it. Mirrors the server's Surface. */
export interface VanyadumSurface {
  key: string;
  base: string;
  accent: string;
  noise: number;
  roughness: number;
  pattern: string;
}

/** Every texture is square and this size. 128 is plenty for a заброшка. */
export const TEXTURE_SIZE = 128;

/**
 * mulberry32 — a small, fast, well-distributed PRNG.
 *
 * Seeded explicitly rather than using Math.random because a texture has to be
 * REPRODUCIBLE: the same level generated twice must look the same, or a reload
 * mid-run repaints the world in different concrete.
 */
export function rng(seed: number): () => number {
  let a = seed >>> 0;
  return () => {
    a = (a + 0x6d2b79f5) >>> 0;
    let t = a;
    t = Math.imul(t ^ (t >>> 15), t | 1);
    t ^= t + Math.imul(t ^ (t >>> 7), t | 61);
    return ((t ^ (t >>> 14)) >>> 0) / 4294967296;
  };
}

/** Parses `#rrggbb` into 0..255 components; anything unparseable is mid grey. */
export function parseHex(hex: string): [number, number, number] {
  const m = /^#?([0-9a-f]{6})$/i.exec(hex.trim());
  if (!m) return [128, 128, 128];
  const n = parseInt(m[1], 16);
  return [(n >> 16) & 255, (n >> 8) & 255, n & 255];
}

function mix(a: number, b: number, t: number): number {
  return a + (b - a) * t;
}

function clamp255(v: number): number {
  return v < 0 ? 0 : v > 255 ? 255 : v | 0;
}

/**
 * Value noise, smoothed by averaging a coarse lattice.
 *
 * Deliberately not Perlin or simplex: this is grime on a wall in a joke game,
 * and a cell average with bilinear interpolation is a dozen lines that look
 * identical at this scale. Complexity needs a requirement (CLAUDE.md), and
 * "prettier noise nobody will look at" is not one.
 */
function valueNoise(size: number, cells: number, r: () => number): Float32Array {
  const lattice = new Float32Array((cells + 1) * (cells + 1));
  for (let i = 0; i < lattice.length; i++) lattice[i] = r();

  const out = new Float32Array(size * size);
  const step = size / cells;
  for (let y = 0; y < size; y++) {
    for (let x = 0; x < size; x++) {
      const gx = x / step;
      const gy = y / step;
      const x0 = Math.floor(gx);
      const y0 = Math.floor(gy);
      const fx = gx - x0;
      const fy = gy - y0;
      // Smoothstep, so the lattice does not show as a grid of diamonds.
      const sx = fx * fx * (3 - 2 * fx);
      const sy = fy * fy * (3 - 2 * fy);
      const at = (cx: number, cy: number) =>
        lattice[Math.min(cy, cells) * (cells + 1) + Math.min(cx, cells)];
      const top = mix(at(x0, y0), at(x0 + 1, y0), sx);
      const bottom = mix(at(x0, y0 + 1), at(x0 + 1, y0 + 1), sx);
      out[y * size + x] = mix(top, bottom, sy);
    }
  }
  return out;
}

/**
 * Generates one texture as RGBA bytes.
 *
 * The result is `size * size * 4` long and is what a DataTexture takes
 * directly. Alpha is always 255: nothing in this game is translucent, and a
 * texture that could be would be a way to see through a wall.
 */
export function generateTexture(
  surface: VanyadumSurface,
  size = TEXTURE_SIZE,
  seed = 1,
): Uint8Array<ArrayBuffer> {
  // Backed by an explicit ArrayBuffer rather than the shorthand, so the type is
  // the non-shared one a GPU upload accepts. `new Uint8Array(n)` widens to
  // ArrayBufferLike, which includes SharedArrayBuffer and is refused by every
  // texture API for a reason.
  const out = new Uint8Array(new ArrayBuffer(size * size * 4));
  const base = parseHex(surface.base);
  const accent = parseHex(surface.accent);
  const r = rng(seed);

  // Coarser roughness means larger features. The catalogue gives it in metres;
  // here it only has to decide how many lattice cells fit across the tile.
  const cells = Math.max(2, Math.round(size / Math.max(4, surface.roughness * 64)));
  const noise = valueNoise(size, cells, r);
  const grain = rng(seed ^ 0x9e3779b9);

  for (let y = 0; y < size; y++) {
    for (let x = 0; x < size; x++) {
      const i = (y * size + x) * 4;
      let t = noise[y * size + x] * surface.noise;

      // The pattern decides where the accent colour goes; the noise above is
      // the same grime in every case.
      switch (surface.pattern) {
        case 'brick':
          t = Math.max(t, brickMortar(x, y, size));
          break;
        case 'boards':
          t = Math.max(t, boardGap(x, size));
          break;
        default:
          break;
      }

      // A little per-pixel speckle on top, so a flat wall is never a flat
      // colour — which is what makes an untextured-looking surface look cheap.
      const speck = (grain() - 0.5) * 24 * surface.noise;
      for (let c = 0; c < 3; c++) {
        out[i + c] = clamp255(mix(base[c], accent[c], t) + speck);
      }
      out[i + 3] = 255;
    }
  }
  return out;
}

/** 1 where a brick's mortar line runs, 0 in the middle of a brick. */
function brickMortar(x: number, y: number, size: number): number {
  const rows = 8;
  const h = size / rows;
  const row = Math.floor(y / h);
  // Every other course is offset by half a brick — which is the entire
  // difference between "bricks" and "a grid".
  const offset = (row % 2) * (size / 8);
  const bw = size / 4;
  const withinRow = y - row * h;
  const col = (x + offset) % bw;
  const mortar = 3;
  return withinRow < mortar || col < mortar ? 1 : 0;
}

/** 1 in the gap between two boards. */
function boardGap(x: number, size: number): number {
  const w = size / 6;
  return x % w < 2 ? 1 : 0;
}

/**
 * The colour a surface reads as before its texture exists, and the tint its
 * vertex colours are multiplied by. Exposed so the mesh builder and the renderer
 * agree without either importing the other's idea of a palette.
 */
export function surfaceTint(surface: VanyadumSurface): [number, number, number] {
  const [r, g, b] = parseHex(surface.base);
  return [r / 255, g / 255, b / 255];
}
