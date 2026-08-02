import { describe, expect, it } from 'vitest';
import {
  SHADE,
  TEXTURE_METRES,
  buildLevelMeshes,
  hexToRgb,
  levelBounds,
  pickupsOnFloor,
  sectorAt,
  triangleCount,
  type VanyadumLevel,
} from '../lib/vanyadumLevel';

/**
 * The level geometry, tested without a GPU.
 *
 * This is the whole point of the mesh builder being a pure function: on a canvas
 * there is no `elementFromPoint`, no bounding box and no computed style, so the
 * only assertions available about the world are the ones made BEFORE it reaches
 * WebGL. Everything below is one of those.
 */

/** Two rooms side by side, sharing x = 10 with a doorway through the middle. */
function twoRooms(rightFloor = 0): VanyadumLevel {
  return {
    seed: 1,
    sectors: [
      {
        id: 0, x0: 0, y0: 0, x1: 10, y1: 10,
        fz: 0, cz: 3, w: 'concrete', f: 'floor', c: 'ceiling', l: 1,
      },
      {
        id: 1, x0: 10, y0: 0, x1: 20, y1: 10,
        fz: rightFloor, cz: rightFloor + 3, w: 'brick', f: 'floor', c: 'ceiling', l: 0.5,
      },
    ],
    portals: [{ a: 0, b: 1, v: true, at: 10, lo: 4, hi: 6 }],
    walls: [
      { v: true, a: 0, lo: 0, hi: 10, s: 0 },
      { v: false, a: 0, lo: 0, hi: 10, s: 0 },
      { v: true, a: 20, lo: 0, hi: 10, s: 1 },
    ],
    pickups: [{ id: 0, k: 'beer', s: 1, p: { x: 15, y: 5 } }],
    spawn: { x: 5, y: 5 },
    spawn_sector: 0,
    spawn_yaw: 0,
  };
}

describe('buildLevelMeshes', () => {
  it('groups triangles by the surface that textures them', () => {
    const meshes = buildLevelMeshes(twoRooms());
    // Two floors and two ceilings share their keys; the two rooms' walls do not.
    expect(Object.keys(meshes).sort()).toEqual(['brick', 'ceiling', 'concrete', 'floor']);
    // Two floors + two ceilings + three walls = seven quads = fourteen triangles.
    expect(triangleCount(meshes)).toBe(14);
  });

  it('maps world +Y onto three.js −Z, which is where a camera looks', () => {
    // Getting this wrong does not look like a broken transform; it looks like
    // broken controls, so it is pinned rather than trusted.
    const meshes = buildLevelMeshes(twoRooms());
    const floor = meshes.floor.positions;
    const zs: number[] = [];
    for (let i = 2; i < floor.length; i += 3) zs.push(floor[i]);
    expect(Math.min(...zs)).toBe(-10);
    // toBeCloseTo rather than toBe, because negating a zero world coordinate
    // yields -0 and Object.is(-0, 0) is false. A signed zero is meaningless to a
    // GPU and normalising it in the builder would be code that exists only to
    // satisfy a matcher.
    expect(Math.max(...zs)).toBeCloseTo(0, 10);
  });

  it('puts a floor at its sector floor height and a ceiling at its ceiling', () => {
    const meshes = buildLevelMeshes(twoRooms(1.5));
    const heights = (arr: number[]) => {
      const out = new Set<number>();
      for (let i = 1; i < arr.length; i += 3) out.add(arr[i]);
      return [...out].sort((a, b) => a - b);
    };
    expect(heights(meshes.floor.positions)).toEqual([0, 1.5]);
    expect(heights(meshes.ceiling.positions)).toEqual([3, 4.5]);
  });

  it('draws the riser and the lintel where two rooms differ in height', () => {
    // Without these a step is a hole you can see through: a wall exists only
    // where two rooms do NOT connect, so a doorway with a step has floor on one
    // side and nothing under it on the other.
    const flat = triangleCount(buildLevelMeshes(twoRooms(0)));
    const stepped = triangleCount(buildLevelMeshes(twoRooms(0.6)));
    expect(stepped - flat).toBe(4); // two quads: one riser, one lintel
  });

  it('scales uvs in world metres so a texture is the same size everywhere', () => {
    const meshes = buildLevelMeshes(twoRooms());
    const us: number[] = [];
    for (let i = 0; i < meshes.floor.uvs.length; i += 2) us.push(meshes.floor.uvs[i]);
    // The left room spans 0..10 metres, so 0..5 tiles at two metres each.
    expect(Math.max(...us)).toBe(20 / TEXTURE_METRES);
  });

  it('bakes the sector light and the face shade into vertex colours', () => {
    const meshes = buildLevelMeshes(twoRooms(), () => [1, 1, 1]);
    // The first room is fully lit, so its floor carries the floor shade exactly
    // and its ceiling the ceiling shade — which is the whole lighting model.
    expect(meshes.floor.colors[0]).toBeCloseTo(SHADE.floor, 6);
    expect(meshes.ceiling.colors[0]).toBeCloseTo(SHADE.ceiling, 6);
  });

  it('darkens a dim room without touching a bright one', () => {
    const meshes = buildLevelMeshes(twoRooms());
    // Room 1 has light 0.5 and a brick wall of its own; room 0 has concrete.
    expect(meshes.brick.colors[0]).toBeCloseTo(SHADE.wallX * 0.5 * 1, 6);
    expect(meshes.concrete.colors[0]).toBeCloseTo(SHADE.wallX * 1 * 1, 6);
  });

  it('tints by surface, so an unknown key still renders', () => {
    const meshes = buildLevelMeshes(twoRooms(), (key) => (key === 'floor' ? [1, 0, 0] : [1, 1, 1]));
    expect(meshes.floor.colors[0]).toBeCloseTo(SHADE.floor, 6);
    expect(meshes.floor.colors[1]).toBe(0);
  });

  it('produces the same arrays for positions, uvs and colours', () => {
    const meshes = buildLevelMeshes(twoRooms());
    for (const m of Object.values(meshes)) {
      const verts = m.positions.length / 3;
      expect(m.uvs.length / 2).toBe(verts);
      expect(m.colors.length / 3).toBe(verts);
      expect(verts % 3).toBe(0); // whole triangles
    }
  });

  it('skips a wall whose sector is missing rather than throwing', () => {
    // Defensive because the level is server data: a wall naming a sector that is
    // not there should cost one missing quad, not a black screen.
    const level = twoRooms();
    level.walls.push({ v: true, a: 5, lo: 0, hi: 1, s: 99 });
    expect(() => buildLevelMeshes(level)).not.toThrow();
    expect(triangleCount(buildLevelMeshes(level))).toBe(14);
  });
});

describe('levelBounds', () => {
  it('covers every room in three.js coordinates', () => {
    const { min, max } = levelBounds(twoRooms(1.5));
    expect(min).toEqual([0, 0, -10]);
    expect(max[0]).toBe(20);
    expect(max[1]).toBe(4.5);
    expect(max[2]).toBeCloseTo(0, 10); // -0, see above
  });

  it('answers for an empty level instead of returning infinities', () => {
    const empty = { ...twoRooms(), sectors: [] };
    expect(levelBounds(empty)).toEqual({ min: [0, 0, 0], max: [0, 0, 0] });
  });
});

describe('sectorAt', () => {
  it('finds the room a point is in, and nothing outside', () => {
    const level = twoRooms();
    expect(sectorAt(level, 5, 5)?.id).toBe(0);
    expect(sectorAt(level, 15, 5)?.id).toBe(1);
    expect(sectorAt(level, 50, 5)).toBeUndefined();
  });
});

describe('pickupsOnFloor', () => {
  /** Ids deliberately not equal to their index, which is the whole hazard. */
  const three = [
    { id: 11, k: 'beer', s: 0, p: { x: 1, y: 1 } },
    { id: 22, k: 'beer', s: 0, p: { x: 2, y: 2 } },
    { id: 33, k: 'beer', s: 1, p: { x: 3, y: 3 } },
  ];

  it('reads the id out rather than assuming it is the index', () => {
    // The wire names an INDEX and the renderer names an ID. Today's generator
    // makes the two equal; nothing guarantees it will keep doing so, and a
    // level whose ids started at one would put every bottle in the wrong room.
    expect(pickupsOnFloor(three, 0b101)).toEqual([11, 33]);
  });

  it('says nothing is there for an empty mask, and everything for a full one', () => {
    expect(pickupsOnFloor(three, 0)).toEqual([]);
    expect(pickupsOnFloor(three, 0b111)).toEqual([11, 22, 33]);
  });

  it('PUTS A PICKUP BACK when its bit returns, not only takes one away', () => {
    // The one claim this whole helper exists for. A bottle is taken, and thirty
    // seconds later the server sets its bit again — with no event, because the
    // mask IS the statement. A renderer fed by something that could only ever
    // remove a mesh would leave that floor empty forever, and nobody would find
    // out until they stood over the spot and waited.
    const taken = pickupsOnFloor(three, 0b101);
    expect(taken).not.toContain(22);
    const back = pickupsOnFloor(three, 0b111);
    expect(back).toContain(22);
  });

  it('ignores bits above the level and the sign bit alike', () => {
    // The mask is 32 bits wide because a JSON number is an IEEE754 double, and
    // bit 31 arrives as a NEGATIVE number after a signed shift. Reading it
    // unsigned is what stops the top pickup vanishing on the levels big enough
    // to have one.
    expect(pickupsOnFloor(three, 0b1111_1000)).toEqual([]);
    expect(pickupsOnFloor([three[0]], -1)).toEqual([11]);
  });
});

describe('hexToRgb', () => {
  it('parses a colour and falls back to grey on nonsense', () => {
    expect(hexToRgb('#ff8000')).toEqual([1, 128 / 255, 0]);
    expect(hexToRgb('nope')).toEqual([0.5, 0.5, 0.5]);
  });
});
