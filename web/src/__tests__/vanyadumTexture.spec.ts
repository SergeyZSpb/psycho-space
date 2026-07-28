import { describe, expect, it } from 'vitest';
import {
  TEXTURE_SIZE,
  generateTexture,
  parseHex,
  rng,
  surfaceTint,
  type VanyadumSurface,
} from '../lib/vanyadumTexture';

/**
 * The textures, tested as bytes.
 *
 * This file is the payoff for generating into a typed array rather than drawing
 * into a canvas: none of these assertions is possible against a 2D context in
 * jsdom, and none of them would be possible at all once the pixels are on the
 * GPU. Whether the result looks like concrete is a human's job; whether it is
 * reproducible, opaque, in range and actually patterned is this file's.
 */

const concrete: VanyadumSurface = {
  key: 'concrete',
  base: '#5b5f5e',
  accent: '#3f4443',
  noise: 0.55,
  roughness: 0.35,
  pattern: 'concrete',
};

const brick: VanyadumSurface = { ...concrete, key: 'brick', pattern: 'brick' };

describe('rng', () => {
  it('is deterministic for a seed', () => {
    const a = rng(42);
    const b = rng(42);
    for (let i = 0; i < 20; i++) expect(a()).toBe(b());
  });

  it('produces different streams for different seeds', () => {
    expect(rng(1)()).not.toBe(rng(2)());
  });

  it('stays inside 0..1', () => {
    const r = rng(7);
    for (let i = 0; i < 2000; i++) {
      const v = r();
      expect(v).toBeGreaterThanOrEqual(0);
      expect(v).toBeLessThan(1);
    }
  });
});

describe('parseHex', () => {
  it('reads a colour with or without the hash', () => {
    expect(parseHex('#ff8000')).toEqual([255, 128, 0]);
    expect(parseHex('ff8000')).toEqual([255, 128, 0]);
  });

  it('falls back to grey rather than producing NaN components', () => {
    // A NaN would reach a GPU as an undefined byte, which is a texture that
    // looks different on different phones for no reason anybody can find.
    expect(parseHex('')).toEqual([128, 128, 128]);
    expect(parseHex('#12345')).toEqual([128, 128, 128]);
    expect(parseHex('rebeccapurple')).toEqual([128, 128, 128]);
  });
});

describe('generateTexture', () => {
  it('is the right length for an RGBA image', () => {
    expect(generateTexture(concrete, 32, 1)).toHaveLength(32 * 32 * 4);
    expect(generateTexture(concrete).length).toBe(TEXTURE_SIZE * TEXTURE_SIZE * 4);
  });

  it('is reproducible from its seed', () => {
    // A reload mid-run must not repaint the world in different concrete.
    const a = generateTexture(concrete, 32, 5);
    const b = generateTexture(concrete, 32, 5);
    expect(Array.from(a)).toEqual(Array.from(b));
  });

  it('differs between seeds, so two surfaces are not the same wall', () => {
    const a = generateTexture(concrete, 32, 1);
    const b = generateTexture(concrete, 32, 2);
    expect(Array.from(a)).not.toEqual(Array.from(b));
  });

  it('is fully opaque, because nothing in this game is see-through', () => {
    const bytes = generateTexture(concrete, 16, 3);
    for (let i = 3; i < bytes.length; i += 4) expect(bytes[i]).toBe(255);
  });

  it('keeps every channel inside a byte', () => {
    const bytes = generateTexture({ ...concrete, noise: 1 }, 32, 9);
    for (const v of bytes) {
      expect(v).toBeGreaterThanOrEqual(0);
      expect(v).toBeLessThanOrEqual(255);
    }
  });

  it('actually varies — a flat colour would mean the noise never ran', () => {
    const bytes = generateTexture(concrete, 32, 4);
    const reds = new Set<number>();
    for (let i = 0; i < bytes.length; i += 4) reds.add(bytes[i]);
    expect(reds.size).toBeGreaterThan(8);
  });

  it('puts mortar in a brick pattern where a plain one has none', () => {
    // The brick generator's whole job is the mortar lines; without them it is
    // the same speckled wall as concrete under a different name.
    const bricked = generateTexture(brick, 64, 1);
    const plain = generateTexture({ ...brick, pattern: 'concrete' }, 64, 1);
    expect(Array.from(bricked)).not.toEqual(Array.from(plain));

    // The top rows of a course are mortar, so they should sit closer to the
    // accent colour than the middle of a brick does.
    const rowAvg = (bytes: Uint8Array, y: number) => {
      let sum = 0;
      for (let x = 0; x < 64; x++) sum += bytes[(y * 64 + x) * 4];
      return sum / 64;
    };
    expect(rowAvg(bricked, 0)).toBeLessThan(rowAvg(bricked, 4));
  });

  it('treats an unknown pattern as plain rather than throwing', () => {
    // Patterns are catalogue data, so a server that grows one this build has
    // never heard of must still produce a wall.
    expect(() => generateTexture({ ...concrete, pattern: 'кирпич-3000' }, 16, 1)).not.toThrow();
  });
});

describe('surfaceTint', () => {
  it('returns the base colour in 0..1', () => {
    expect(surfaceTint({ ...concrete, base: '#ff8000' })).toEqual([1, 128 / 255, 0]);
  });
});
