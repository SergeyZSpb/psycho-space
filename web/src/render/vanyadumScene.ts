/**
 * «ВАНЯДУМ» — the WebGL renderer, and the ONLY module in the SPA that imports
 * three.js.
 *
 * WHY THIS DIRECTORY EXISTS. `src/lib/` is pure, node-testable logic; this is
 * not that. It holds a GPU context, a scene graph and a render loop, and it
 * cannot be asserted on without a browser. Keeping it apart from `lib/` is what
 * makes the boundary visible: everything ABOVE it — the level geometry, the
 * textures, the input maths, the rules — is pure and tested, and this file is
 * deliberately the thin layer that uploads what those produced.
 *
 * WHY IT IS LOADED LAZILY. three.js is comparable in size to this entire
 * application's entry bundle, and nobody who never opens «ВАНЯДУМ» should pay a
 * byte of it. The view imports this module with a dynamic `import()`, so it
 * lands in its own chunk behind its own route.
 *
 * WHY ONLY THE WORLD IS ON THE CANVAS. Every readout, every control and every
 * word of text in this game is real DOM, and that is a testing decision before
 * it is a design one: nothing inside a canvas can be asserted on without pixel
 * comparison, and a test-only introspection API may not ship. So the canvas
 * holds the world and nothing else. See ADR-046 — this is the shape that record
 * names as its own escape hatch, rather than a reversal of it.
 */

import type { LevelMeshes, VanyadumLevel } from '../lib/vanyadumLevel';
import { buildLevelMeshes, levelBounds } from '../lib/vanyadumLevel';
import type { VanyadumSurface } from '../lib/vanyadumTexture';
import { TEXTURE_SIZE, generateTexture, surfaceTint } from '../lib/vanyadumTexture';

/** Where the camera is and what it is looking at, in world units. */
export interface CameraState {
  /** Metres, in the server's floor-plane coordinates. */
  x: number;
  y: number;
  /** Eye height above the level's zero, as the server computed it. */
  z: number;
  yaw: number;
  pitch: number;
}

export interface SceneOptions {
  canvas: HTMLCanvasElement;
  level: VanyadumLevel;
  surfaces: VanyadumSurface[];
  /** Honoured by damping the view bob; never by hiding anything informative. */
  reducedMotion: boolean;
}

/**
 * Reports whether this browser can run the game at all.
 *
 * A REAL production path, not a test hook: WebGL can be disabled by policy, by a
 * blocklisted driver, or by a phone in a low-power mode, and a player on one of
 * those deserves a sentence rather than a black rectangle. That it also lets a
 * CI run assert on the HUD with no GPU is a happy consequence, not the reason —
 * shipping test-only code into a production path is forbidden here.
 */
export function webglAvailable(): boolean {
  try {
    const probe = document.createElement('canvas');
    return Boolean(
      probe.getContext('webgl2') ??
        probe.getContext('webgl') ??
        probe.getContext('experimental-webgl'),
    );
  } catch {
    return false;
  }
}

export type VanyadumScene = Awaited<ReturnType<typeof createScene>>;

/**
 * Builds the whole scene: the level, the things lying in it, and the shotgun.
 *
 * Async because three.js itself is imported here, inside the function, so that
 * merely importing this module does not pull the engine in.
 */
export async function createScene(opts: SceneOptions) {
  const THREE = await import('three');

  const renderer = new THREE.WebGLRenderer({
    canvas: opts.canvas,
    antialias: false, // crunchy on purpose — this is a Doom joke
    powerPreference: 'low-power', // it is a phone, outdoors, on battery
  });
  renderer.setPixelRatio(Math.min(window.devicePixelRatio || 1, 2));

  const scene = new THREE.Scene();
  const bounds = levelBounds(opts.level);
  const diagonal = Math.hypot(
    bounds.max[0] - bounds.min[0],
    bounds.max[2] - bounds.min[2],
  );
  // The far plane is the level's own diagonal plus a little, so a corridor never
  // fades out into nothing and nothing is drawn that could not be reached.
  const camera = new THREE.PerspectiveCamera(75, 1, 0.05, Math.max(40, diagonal * 1.4));
  // YXZ is the first-person ordering: yaw about the world's up, then pitch about
  // the camera's own right. Any other order rolls the horizon when you look up
  // while turning.
  camera.rotation.order = 'YXZ';

  // Fog does the work a lighting pass would, for nothing: it hides the far wall
  // of a long room, gives depth to a flat corridor, and makes a заброшка feel
  // like one. Its colour is also the clear colour, so the two never disagree.
  const murk = new THREE.Color(0x0d0f10);
  scene.fog = new THREE.Fog(murk, 3, Math.max(18, diagonal * 0.8));
  renderer.setClearColor(murk, 1);

  // --- textures, generated rather than loaded ------------------------------
  const byKey = new Map(opts.surfaces.map((s) => [s.key, s]));
  const textures = new Map<string, InstanceType<typeof THREE.DataTexture>>();
  for (const surface of opts.surfaces) {
    const bytes = generateTexture(surface, TEXTURE_SIZE, hashKey(surface.key));
    const tex = new THREE.DataTexture(bytes, TEXTURE_SIZE, TEXTURE_SIZE, THREE.RGBAFormat);
    tex.wrapS = THREE.RepeatWrapping;
    tex.wrapT = THREE.RepeatWrapping;
    // Nearest, deliberately: filtered texels on a 128-pixel concrete tile look
    // like a smear, and crisp ones look like 1993.
    tex.magFilter = THREE.NearestFilter;
    tex.minFilter = THREE.NearestMipmapNearestFilter;
    tex.generateMipmaps = true;
    tex.needsUpdate = true;
    textures.set(surface.key, tex);
  }

  // --- the level -----------------------------------------------------------
  const meshes: LevelMeshes = buildLevelMeshes(opts.level, (key) => {
    const s = byKey.get(key);
    return s ? surfaceTint(s) : [1, 1, 1];
  });

  const levelGroup = new THREE.Group();
  for (const [key, data] of Object.entries(meshes)) {
    if (data.positions.length === 0) continue;
    const geometry = new THREE.BufferGeometry();
    geometry.setAttribute('position', new THREE.Float32BufferAttribute(data.positions, 3));
    geometry.setAttribute('uv', new THREE.Float32BufferAttribute(data.uvs, 2));
    geometry.setAttribute('color', new THREE.Float32BufferAttribute(data.colors, 3));
    const material = new THREE.MeshBasicMaterial({
      map: textures.get(key) ?? null,
      vertexColors: true,
      // Double-sided because the player cannot leave the level, so a back face
      // is unreachable — and a one-sided wall that is invisible from the wrong
      // side is an entire class of bug bought for nothing.
      side: THREE.DoubleSide,
      fog: true,
    });
    levelGroup.add(new THREE.Mesh(geometry, material));
  }
  scene.add(levelGroup);

  // --- the things lying about ----------------------------------------------
  // A bottle: two cylinders, built in code like everything else here. It is not
  // meant to be convincing at two metres; it is meant to be unmistakably a
  // bottle at ten, which low-poly and a strong tint achieve and detail does not.
  const pickupMeshes = new Map<number, InstanceType<typeof THREE.Group>>();
  const bottleBody = new THREE.CylinderGeometry(0.11, 0.13, 0.3, 8);
  const bottleNeck = new THREE.CylinderGeometry(0.045, 0.06, 0.16, 6);
  for (const p of opts.level.pickups) {
    const group = new THREE.Group();
    const colour = new THREE.Color(0xc8892f);
    const material = new THREE.MeshBasicMaterial({ color: colour, fog: true });
    const body = new THREE.Mesh(bottleBody, material);
    const neck = new THREE.Mesh(bottleNeck, material);
    neck.position.y = 0.22;
    group.add(body, neck);
    const sector = opts.level.sectors.find((s) => s.id === p.s);
    group.position.set(p.p.x, (sector?.fz ?? 0) + 0.2, -p.p.y);
    scene.add(group);
    pickupMeshes.set(p.id, group);
  }

  // --- the двустволка ------------------------------------------------------
  // Parented to the camera, so it follows the view for free and needs no
  // per-frame transform of its own. Boxes, because a shotgun seen from the
  // shooter's end is two barrels and a bit of stock.
  const gun = new THREE.Group();
  {
    const steel = new THREE.MeshBasicMaterial({ color: 0x2a2d31, fog: false });
    const wood = new THREE.MeshBasicMaterial({ color: 0x5a3a22, fog: false });
    const barrel = new THREE.BoxGeometry(0.07, 0.055, 0.62);
    const left = new THREE.Mesh(barrel, steel);
    const right = new THREE.Mesh(barrel, steel);
    left.position.set(-0.037, 0, -0.3);
    right.position.set(0.037, 0, -0.3);
    const stock = new THREE.Mesh(new THREE.BoxGeometry(0.11, 0.1, 0.3), wood);
    stock.position.set(0, -0.03, 0.1);
    gun.add(left, right, stock);
    // Low and to the right, tipped up a little — the pose every game in this
    // lineage has used, because it reads as "held" rather than "floating".
    gun.position.set(0.17, -0.17, -0.32);
    gun.rotation.set(0.06, 0.04, 0.02);
    camera.add(gun);
  }
  scene.add(camera);

  // --- the loop's state ----------------------------------------------------
  let bobPhase = 0;
  let disposed = false;

  function resize(width: number, height: number): void {
    if (disposed || width <= 0 || height <= 0) return;
    renderer.setSize(width, height, false);
    camera.aspect = width / height;
    camera.updateProjectionMatrix();
  }

  /**
   * Places the camera and draws one frame.
   *
   * `moving` drives the view bob, which is the only thing in this scene that is
   * not a direct function of server state — and it is damped to nothing under
   * reduced motion, because a bobbing horizon is exactly what that setting
   * exists to stop.
   */
  function render(state: CameraState, moving: boolean, dtSeconds: number): void {
    if (disposed) return;
    const amplitude = opts.reducedMotion ? 0 : 0.022;
    bobPhase += moving ? dtSeconds * 9 : 0;
    const bob = moving ? Math.sin(bobPhase) * amplitude : 0;

    camera.position.set(state.x, state.z + bob, -state.y);
    camera.rotation.y = -state.yaw;
    camera.rotation.x = state.pitch;

    for (const [, group] of pickupMeshes) {
      // The spin every item in this lineage has had. Cosmetic, driven by the
      // frame clock rather than by the server, and worth nothing on the wire.
      group.rotation.y += dtSeconds * 1.6;
    }

    renderer.render(scene, camera);
  }

  // --- peers ---------------------------------------------------------------
  // One capsule per SLOT — a place in the building rather than a person — created
  // on first sight and reused after. Built here rather than in the level pass
  // because peers come and go with the interpolation buffer, and rebuilding the
  // world when somebody walks in would be absurd.
  //
  // Keying on the slot also BOUNDS this map, where keying on a pseudonym did not:
  // there are only ever MaxOccupants places, so a tab left open through a hundred
  // people arriving and leaving holds four capsules rather than a hundred. A slot
  // changing hands reuses the same capsule, which is right — nothing about the
  // figure is per-person.
  const peerMeshes = new Map<number, InstanceType<typeof THREE.Mesh>>();
  const peerGeometry = new THREE.CapsuleGeometry(0.35, 1.1, 4, 8);

  /**
   * Places every peer the interpolator produced, and hides the ones it did not.
   *
   * Hiding rather than removing IS how somebody leaves the picture, and it has
   * two causes that look identical from here: he left the building, or he walked
   * two rooms away and the server stopped sending him. Neither is announced —
   * the peer array is filtered full state — so what is drawn is exactly what the
   * newest frame named, and nothing is kept alive because nothing said to remove
   * it.
   *
   * Meshes are kept rather than disposed: the commonest reason to vanish for a
   * frame is the interpolation buffer running dry, and rebuilding geometry for
   * somebody about to come back is work done to make the world worse.
   */
  function setPeers(peers: { slot: number; x: number; y: number; z: number; yaw: number }[]): void {
    if (disposed) return;
    for (const m of peerMeshes.values()) m.visible = false;
    for (const p of peers) {
      let mesh = peerMeshes.get(p.slot);
      if (!mesh) {
        mesh = new THREE.Mesh(
          peerGeometry,
          new THREE.MeshBasicMaterial({ color: 0xd05a4a, fog: true }),
        );
        scene.add(mesh);
        peerMeshes.set(p.slot, mesh);
      }
      // `z` is an EYE height — derived by the client from the sector the wire
      // named, since the wire stopped carrying it — so the body hangs below it.
      mesh.position.set(p.x, p.z - 0.85, -p.y);
      mesh.rotation.y = -p.yaw;
      mesh.visible = true;
    }
  }

  /**
   * Shows exactly what is lying on the floor, and hides the rest.
   *
   * SYMMETRIC ON PURPOSE, and that symmetry is now load-bearing: things come
   * back (the server respawns a pickup where it was taken from), so this is
   * called with a set that GROWS as often as it shrinks. Written as "hide what
   * was collected" it would work for the whole of a bottle's first life and then
   * never put it back — a bug nobody would see until they waited on an empty
   * floor for half a minute. The server decides; this draws the answer.
   */
  function setOnFloor(ids: number[]): void {
    const there = new Set(ids);
    for (const [id, group] of pickupMeshes) group.visible = there.has(id);
  }

  function dispose(): void {
    if (disposed) return;
    disposed = true;
    scene.traverse((obj) => {
      const mesh = obj as { geometry?: { dispose(): void }; material?: { dispose(): void } };
      mesh.geometry?.dispose();
      mesh.material?.dispose();
    });
    for (const tex of textures.values()) tex.dispose();
    renderer.dispose();
  }

  return { render, resize, setOnFloor, setPeers, dispose };
}

/**
 * A stable small integer for a surface key, so the same wall is the same
 * concrete on every device and after every reload. Deliberately not a hash of
 * the level seed: two rooms with the same surface should look like the same
 * material.
 */
function hashKey(key: string): number {
  let h = 2166136261;
  for (let i = 0; i < key.length; i++) {
    h ^= key.charCodeAt(i);
    h = Math.imul(h, 16777619);
  }
  return h >>> 0;
}
