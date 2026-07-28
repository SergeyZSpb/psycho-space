/**
 * «ВАНЯДУМ» — the level as the server describes it, and the geometry built from
 * it.
 *
 * WHY THIS FILE IS PURE. Everything here turns one plain object into arrays of
 * numbers. It touches no WebGL context, imports nothing from three.js, and knows
 * nothing about a renderer — which is what makes the geometry of this game
 * unit-testable in node, where a `<canvas>` is not. The renderer's job is
 * reduced to uploading what this returns.
 *
 * That split is the answer to the one real cost of moving a game onto a canvas:
 * the yard's several hundred layout assertions have no equivalent here, so as
 * much as possible is moved OUT of the canvas — into the DOM for anything a
 * person reads, and into pure functions like this one for anything a test does.
 *
 * COORDINATES. The server thinks in a floor plane (x, y) with a separate height
 * per sector; three.js has Y up. The mapping is fixed here and nowhere else:
 *
 *     three.x =  world.x
 *     three.y =  world height (a sector's floor / ceiling)
 *     three.z = -world.y
 *
 * so world +Y — the direction yaw zero faces, per the server's sim.go — is
 * three's −Z, which is exactly where an unrotated camera looks. Getting this
 * wrong does not look like a broken transform; it looks like broken controls.
 */

/** One rectangular room. Field names are the server's short JSON keys. */
export interface VanyadumSector {
  id: number;
  x0: number;
  y0: number;
  x1: number;
  y1: number;
  /** Floor and ceiling height, in metres. */
  fz: number;
  cz: number;
  /** Surface keys into the served catalogue. */
  w: string;
  f: string;
  c: string;
  /** Light level, 0..1. */
  l: number;
}

/** An opening in the wall two sectors share. */
export interface VanyadumPortal {
  a: number;
  b: number;
  v: boolean;
  at: number;
  lo: number;
  hi: number;
}

/** A solid segment. `s` is the sector whose edge it is — see the server's Wall. */
export interface VanyadumWall {
  v: boolean;
  a: number;
  lo: number;
  hi: number;
  s: number;
}

/** Something lying on the floor. */
export interface VanyadumPickup {
  id: number;
  k: string;
  s: number;
  p: { x: number; y: number };
}

export interface VanyadumLevel {
  seed: number;
  sectors: VanyadumSector[];
  portals: VanyadumPortal[];
  walls: VanyadumWall[];
  pickups: VanyadumPickup[];
  spawn: { x: number; y: number };
  spawn_sector: number;
  spawn_yaw: number;
}

/**
 * Triangles for one surface key, ready to become a BufferGeometry.
 *
 * Non-indexed on purpose: a quad is six vertices rather than four plus indices.
 * At this level size the extra vertices are nothing, and the arrays stay
 * trivially inspectable by a test — which matters far more here, where no test
 * can look at the picture.
 */
export interface SurfaceMesh {
  positions: number[];
  uvs: number[];
  colors: number[];
}

export type LevelMeshes = Record<string, SurfaceMesh>;

/**
 * How many metres of world one tile of texture covers. Kept in world units
 * rather than per-face so a texture is the same physical size on a wall three
 * metres long and on one thirty metres long.
 */
export const TEXTURE_METRES = 2;

/**
 * Fake directional shading, baked into vertex colours.
 *
 * There is no light in this scene — no lamps, no normals, no shading pass. Every
 * surface is drawn with a plain unlit material and the whole sense of depth
 * comes from these four multipliers, exactly as Doom's did: floors bright,
 * ceilings dark, and the two wall orientations slightly different from each
 * other so a corner reads as a corner. It costs nothing, it cannot flicker, and
 * it looks more like the game this is a joke about than real lighting would.
 */
export const SHADE = {
  floor: 1,
  ceiling: 0.5,
  /** Walls facing along the world's X axis. */
  wallX: 0.78,
  /** Walls facing along Y — deliberately different, so corners are legible. */
  wallY: 0.94,
  /** The riser and lintel of a step between rooms at different heights. */
  step: 0.68,
} as const;

/** Parses a `#rrggbb` string into 0..1 components. Anything else is mid grey. */
export function hexToRgb(hex: string): [number, number, number] {
  const m = /^#?([0-9a-f]{6})$/i.exec(hex.trim());
  if (!m) return [0.5, 0.5, 0.5];
  const n = parseInt(m[1], 16);
  return [((n >> 16) & 255) / 255, ((n >> 8) & 255) / 255, (n & 255) / 255];
}

function emptySurface(): SurfaceMesh {
  return { positions: [], uvs: [], colors: [] };
}

function surfaceFor(out: LevelMeshes, key: string): SurfaceMesh {
  const existing = out[key];
  if (existing) return existing;
  const fresh = emptySurface();
  out[key] = fresh;
  return fresh;
}

/**
 * Pushes one quad as two triangles.
 *
 * Corners are given in order around the quad. Winding is deliberately not
 * fussed over: every material in this scene is double-sided, because the player
 * cannot leave the level and a back face nobody can reach is not worth an entire
 * class of bug where a wall is invisible from one side only.
 */
function quad(
  m: SurfaceMesh,
  corners: [number, number, number][],
  uv: [number, number][],
  shade: number,
  light: number,
  rgb: [number, number, number],
): void {
  const order = [0, 1, 2, 0, 2, 3];
  const level = Math.max(0, Math.min(1, light)) * shade;
  for (const i of order) {
    const c = corners[i];
    m.positions.push(c[0], c[1], c[2]);
    m.uvs.push(uv[i][0], uv[i][1]);
    m.colors.push(rgb[0] * level, rgb[1] * level, rgb[2] * level);
  }
}

/**
 * Builds every triangle in a level, grouped by the surface key that textures it.
 *
 * Four kinds of face, and the last is the one that is easy to forget: floors,
 * ceilings, walls, and the RISER between two rooms at different heights. Without
 * that last one a step is a hole you can see through, because a wall exists only
 * where the two rooms do not connect and a doorway with a step has floor on one
 * side and nothing under it on the other.
 *
 * `tint` maps a surface key to its base colour, so a level renders sensibly
 * before any texture has been generated — and keeps rendering sensibly for a
 * surface key the catalogue gained and this build has never heard of.
 */
export function buildLevelMeshes(
  level: VanyadumLevel,
  tint: (surfaceKey: string) => [number, number, number] = () => [1, 1, 1],
): LevelMeshes {
  const out: LevelMeshes = {};
  const byId = new Map<number, VanyadumSector>();
  for (const s of level.sectors) byId.set(s.id, s);

  for (const s of level.sectors) {
    const [x0, x1] = [s.x0, s.x1];
    const [y0, y1] = [s.y0, s.y1];
    const u = TEXTURE_METRES;

    // Floor. World +Y is three's −Z, so the room's far edge is the more
    // negative z.
    quad(
      surfaceFor(out, s.f),
      [
        [x0, s.fz, -y0],
        [x1, s.fz, -y0],
        [x1, s.fz, -y1],
        [x0, s.fz, -y1],
      ],
      [
        [x0 / u, y0 / u],
        [x1 / u, y0 / u],
        [x1 / u, y1 / u],
        [x0 / u, y1 / u],
      ],
      SHADE.floor,
      s.l,
      tint(s.f),
    );

    // Ceiling.
    quad(
      surfaceFor(out, s.c),
      [
        [x0, s.cz, -y0],
        [x0, s.cz, -y1],
        [x1, s.cz, -y1],
        [x1, s.cz, -y0],
      ],
      [
        [x0 / u, y0 / u],
        [x0 / u, y1 / u],
        [x1 / u, y1 / u],
        [x1 / u, y0 / u],
      ],
      SHADE.ceiling,
      s.l,
      tint(s.c),
    );
  }

  for (const w of level.walls) {
    const s = byId.get(w.s);
    if (!s) continue;
    const m = surfaceFor(out, s.w);
    const u = TEXTURE_METRES;
    const h = s.cz - s.fz;
    const span = w.hi - w.lo;
    const uv: [number, number][] = [
      [w.lo / u, s.fz / u],
      [(w.lo + span) / u, s.fz / u],
      [(w.lo + span) / u, (s.fz + h) / u],
      [w.lo / u, (s.fz + h) / u],
    ];
    const corners: [number, number, number][] = w.v
      ? [
          [w.a, s.fz, -w.lo],
          [w.a, s.fz, -w.hi],
          [w.a, s.cz, -w.hi],
          [w.a, s.cz, -w.lo],
        ]
      : [
          [w.lo, s.fz, -w.a],
          [w.hi, s.fz, -w.a],
          [w.hi, s.cz, -w.a],
          [w.lo, s.cz, -w.a],
        ];
    quad(m, corners, uv, w.v ? SHADE.wallX : SHADE.wallY, s.l, tint(s.w));
  }

  // The step between two rooms at different heights: a riser under the doorway
  // and a lintel over it. Both are drawn from the LOWER room's surface, and both
  // are skipped entirely when the floors match — which is most doorways.
  for (const p of level.portals) {
    const a = byId.get(p.a);
    const b = byId.get(p.b);
    if (!a || !b) continue;
    if (Math.abs(a.fz - b.fz) < 1e-9) continue;
    const lower = a.fz < b.fz ? a : b;
    const higher = a.fz < b.fz ? b : a;
    const m = surfaceFor(out, lower.w);
    const u = TEXTURE_METRES;
    const rgb = tint(lower.w);

    const face = (bottom: number, top: number) => {
      const uv: [number, number][] = [
        [p.lo / u, bottom / u],
        [p.hi / u, bottom / u],
        [p.hi / u, top / u],
        [p.lo / u, top / u],
      ];
      const corners: [number, number, number][] = p.v
        ? [
            [p.at, bottom, -p.lo],
            [p.at, bottom, -p.hi],
            [p.at, top, -p.hi],
            [p.at, top, -p.lo],
          ]
        : [
            [p.lo, bottom, -p.at],
            [p.hi, bottom, -p.at],
            [p.hi, top, -p.at],
            [p.lo, top, -p.at],
          ];
      quad(m, corners, uv, SHADE.step, lower.l, rgb);
    };

    face(lower.fz, higher.fz); // the riser you walk up
    face(lower.cz, higher.cz); // the lintel you walk under
  }

  return out;
}

/** How many triangles a built level contains, across every surface. */
export function triangleCount(meshes: LevelMeshes): number {
  let n = 0;
  for (const m of Object.values(meshes)) n += m.positions.length / 9;
  return n;
}

/**
 * The level's bounding box in three.js coordinates, which is what the renderer
 * needs in order to place a far plane that does not clip the far wall.
 */
export function levelBounds(level: VanyadumLevel): {
  min: [number, number, number];
  max: [number, number, number];
} {
  if (level.sectors.length === 0) {
    return { min: [0, 0, 0], max: [0, 0, 0] };
  }
  let minX = Infinity;
  let maxX = -Infinity;
  let minY = Infinity;
  let maxY = -Infinity;
  let minZ = Infinity;
  let maxZ = -Infinity;
  for (const s of level.sectors) {
    minX = Math.min(minX, s.x0);
    maxX = Math.max(maxX, s.x1);
    minY = Math.min(minY, s.fz);
    maxY = Math.max(maxY, s.cz);
    minZ = Math.min(minZ, -s.y1);
    maxZ = Math.max(maxZ, -s.y0);
  }
  return { min: [minX, minY, minZ], max: [maxX, maxY, maxZ] };
}

/** The sector containing a point, or undefined. Mirrors the server's SectorAt. */
export function sectorAt(
  level: VanyadumLevel,
  x: number,
  y: number,
): VanyadumSector | undefined {
  return level.sectors.find((s) => x >= s.x0 && x <= s.x1 && y >= s.y0 && y <= s.y1);
}
