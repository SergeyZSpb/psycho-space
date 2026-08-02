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

import { createFlash, createPeerFlashes } from '../lib/vanyadumFlash';
import type { LevelMeshes, VanyadumLevel } from '../lib/vanyadumLevel';
import { buildLevelMeshes, levelBounds } from '../lib/vanyadumLevel';
import { PEER_DOWN, PEER_PROTECTED } from '../lib/vanyadumRoster';
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
  /**
   * The figure a peer is drawn as, straight from the catalogue.
   *
   * IT IS THE HITBOX, which is why none of it is a number typed in here. The
   * server stands a cylinder of `radius`, `bodyHeight` tall, on the floor of the
   * room a man is in and shoots at THAT — so a client drawing him shorter or
   * thinner would be drawing a target that does not match the one being hit, and
   * every near miss would look like a bug in the game rather than in the aim.
   * `eyeHeight` is here because the wire sends a peer's EYE and a body hangs
   * below it.
   */
  player: { radius: number; bodyHeight: number; eyeHeight: number };
  /**
   * Honoured by damping the view bob and the recoil; never by hiding anything
   * informative. EVERY MARK STAYS — both muzzle flashes, the blow on whoever was
   * hit, the grey of a man on the floor and the blue of one who cannot be
   * touched — because each of them is what says something happened, and somebody
   * who asked for less movement still has to be told. None of them is animated
   * in any case: a mark is in a frame or it is not and is cleared by a count of
   * frames rather than by an animation ending, and a state is a colour. That is
   * the shape this project requires of an acknowledgement precisely so that
   * turning motion off cannot silence it.
   */
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
  /** The gun's resting pose. The recoil is measured from here, never accumulated. */
  const GUN_REST = { x: 0.17, y: -0.17, z: -0.32, pitch: 0.06 };
  const muzzle = new THREE.Mesh(
    // A disc rather than a sphere: it is seen end-on and nothing else, so the
    // hemisphere behind it would be pixels nobody can look at. Seven sides, for
    // the same reason the renderer does not antialias — this is a Doom joke.
    new THREE.CircleGeometry(0.16, 7),
    new THREE.MeshBasicMaterial({ color: 0xffd9a0, fog: false }),
  );
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
    // Just past the muzzle end of the barrels, and hidden until something fires.
    muzzle.position.set(0, 0, -0.63);
    muzzle.visible = false;
    gun.add(left, right, stock, muzzle);
    // Low and to the right, tipped up a little — the pose every game in this
    // lineage has used, because it reads as "held" rather than "floating".
    gun.position.set(GUN_REST.x, GUN_REST.y, GUN_REST.z);
    gun.rotation.set(GUN_REST.pitch, 0.04, 0.02);
    camera.add(gun);
  }
  scene.add(camera);

  // --- the loop's state ----------------------------------------------------
  let bobPhase = 0;
  let disposed = false;

  /**
   * The muzzle flash, COUNTED IN DRAWN FRAMES rather than in seconds.
   *
   * The seconds it used to be counted in could expire inside the frame that was
   * about to draw them, which at twenty frames a second — the rate the view
   * clamps `dt` at, and one a cheap phone really reaches — meant the flash was
   * never drawn at all. On exactly that phone it is also the only zero-latency
   * mark left, since the kick is damped away under `prefers-reduced-motion` and
   * the sound is gone the moment somebody presses the mute. See vanyadumFlash
   * for the arithmetic, and for what a frame count costs.
   *
   * A flash long enough to LOOK at is the opposite mistake — it takes the eye
   * off the thing walking towards you, which is the one thing this game will
   * ever ask anybody to watch. So the count is a floor rather than a target:
   * enough frames to be caught, never enough to be studied, with the shell
   * count on the HUD carrying whatever a glance missed.
   */
  const flash = createFlash();
  /** How far the gun is thrown back by a shot, in metres, and how fast it returns. */
  const RECOIL_METRES = 0.09;
  const RECOIL_SECONDS = 0.075;
  /** How much of the recoil is still unspent, 0..1. */
  let recoil = 0;

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

    // THE FLASH IS SPENT BY BEING DRAWN AND THE RECOIL DECAYS ON A CLOCK, and
    // the two answer to different rules. The flash is what SAYS a shot happened,
    // so it survives `prefers-reduced-motion` intact — somebody who asked for
    // less movement still has to be told he fired — and it is counted in frames
    // so that a phone drawing few of them still gets one. The kick is
    // decoration, so under that setting it is simply not applied, and a decay
    // measured in seconds is right for it: nothing is lost by a slow phone
    // skipping most of a spring.
    muzzle.visible = flash.frame();
    if (recoil > 0) {
      // Exponential, so it settles rather than stopping dead, and cut off where
      // a millimetre of a metre stops being visible on a phone.
      recoil *= Math.exp(-dtSeconds / RECOIL_SECONDS);
      if (recoil < 0.01) recoil = 0;
      const kick = opts.reducedMotion ? 0 : recoil;
      gun.position.z = GUN_REST.z + kick * RECOIL_METRES;
      gun.position.y = GUN_REST.y - kick * RECOIL_METRES * 0.3;
      gun.rotation.x = GUN_REST.pitch + kick * 0.22;
    }

    for (const [, group] of pickupMeshes) {
      // The spin every item in this lineage has had. Cosmetic, driven by the
      // frame clock rather than by the server, and worth nothing on the wire.
      group.rotation.y += dtSeconds * 1.6;
    }

    renderer.render(scene, camera);
  }

  // --- peers ---------------------------------------------------------------
  // One figure per SLOT — a place in the building rather than a person — created
  // on first sight and reused after. Built here rather than in the level pass
  // because peers come and go with the interpolation buffer, and rebuilding the
  // world when somebody walks in would be absurd.
  //
  // Keying on the slot also BOUNDS this map, where keying on a pseudonym did not:
  // there are only ever MaxOccupants places, so a tab left open through a hundred
  // people arriving and leaving holds four capsules rather than a hundred. A slot
  // changing hands reuses the same capsule, which is right — nothing about the
  // figure is per-person.
  /** A body, the flash at the end of his gun, and the blow that landed on him. */
  interface PeerFigure {
    body: InstanceType<typeof THREE.Mesh>;
    /**
     * The muzzle flash, parented to the body so it follows his position and his
     * facing for free — and so hiding him hides it, which is what a peer walking
     * out of the snapshot has to do to an unfinished mark.
     */
    muzzle: InstanceType<typeof THREE.Mesh>;
    /** The blow: a red halo for the frames after a shot landed on him. */
    blow: InstanceType<typeof THREE.Mesh>;
    /**
     * The `st` this figure was last drawn in.
     *
     * The pose and the tint are rewritten only when it changes, because they are
     * STATES: a man is down for three seconds and protected for two, which is
     * sixty and forty frames of setting a colour to the colour it already is.
     * −1 rather than 0, so the first frame always writes.
     */
    drawnState: number;
  }
  const peerFigures = new Map<number, PeerFigure>();
  // The catalogue's own dimensions, so the figure is exactly the cylinder the
  // server shoots at: a capsule of `radius` whose ends are hemispheres of the
  // same, which comes to `bodyHeight` overall.
  const peerRadius = opts.player.radius;
  const peerGeometry = new THREE.CapsuleGeometry(
    peerRadius,
    Math.max(0, opts.player.bodyHeight - 2 * peerRadius),
    4,
    8,
  );
  /**
   * A blob rather than the disc the player's own muzzle uses.
   *
   * His own is seen end-on and nothing else, so a disc is the whole of what
   * could be looked at; somebody else's is seen from wherever you happen to be
   * standing, and a disc edge-on is invisible from exactly the angle a shot
   * across a room arrives at. Six by four segments — a few dozen triangles, and
   * it reads as a blob of light at any distance a peer is ever drawn at.
   */
  const peerMuzzleGeometry = new THREE.SphereGeometry(0.13, 6, 4);
  /**
   * The blow, and it is a HALO ROUND THE MAN rather than a spot on him.
   *
   * A small blob would be the obvious shape and it cannot work: it would sit
   * INSIDE a capsule of `peerRadius` and be hidden by it from every angle. So the
   * mark is wider than the body and translucent, which is also the right thing to
   * look at — it says WHERE somebody was hit and nothing else, it is
   * unmistakably not the small warm blob at a muzzle, and it is over in three
   * frames.
   */
  const peerBlowGeometry = new THREE.SphereGeometry(peerRadius + 0.14, 8, 6);
  /**
   * The three colours a figure is drawn in, and each is a RULE rather than a
   * preference.
   *
   * Somebody who cannot be hurt has to LOOK like somebody who cannot be hurt, or
   * a player will empty both barrels into him and conclude the game is broken —
   * so protection is a property of the figure for the whole two seconds it lasts,
   * exactly as this project requires of a buff. A man on the floor goes grey and
   * stays grey for as long as he is lying there, for the same reason.
   */
  const PEER_TINT_ALIVE = 0xd05a4a;
  const PEER_TINT_DOWN = 0x4c4f52;
  const PEER_TINT_PROTECTED = 0x6ec6ff;
  /**
   * WHICH PEERS ARE MARKED IN THE FRAME BEING DRAWN — his gun going off, and a
   * shot landing on him.
   *
   * The wire marks the tick each happened on, which is a LEVEL lasting several
   * drawn frames; this converts them into events by marking the transition, per
   * slot and per kind. It also counts each mark in frames rather than seconds,
   * for the same reason the player's own does — see vanyadumFlash. The other two
   * states are not in there at all: they last, so they are drawn straight off
   * `st` below.
   */
  const peerFlashes = createPeerFlashes();

  /**
   * Places every peer the interpolator produced, and hides the ones it did not.
   *
   * CALLED EXACTLY ONCE PER DRAWN FRAME, which is not merely how the view
   * happens to call it: the muzzle marks below are counted in frames, so a
   * second call inside one frame would spend a mark that was never rendered.
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
  function setPeers(
    peers: { slot: number; x: number; y: number; z: number; yaw: number; st?: number }[],
  ): void {
    if (disposed) return;
    // Before the loop, so every peer's marks are decided against the same frame.
    const marks = peerFlashes.frame(peers);
    for (const figure of peerFigures.values()) figure.body.visible = false;
    for (const p of peers) {
      let figure = peerFigures.get(p.slot);
      if (!figure) {
        const body = new THREE.Mesh(
          peerGeometry,
          new THREE.MeshBasicMaterial({ color: PEER_TINT_ALIVE, fog: true }),
        );
        const muzzle = new THREE.Mesh(
          peerMuzzleGeometry,
          // NOT FOGGED, unlike the man holding it, and that is the one place
          // this mark is allowed to break the scene's own rules. The murk starts
          // at three metres and is total by the far wall, so a fogged flash
          // would be dimmest at exactly the distance a player most needs telling
          // that somebody over there is shooting. The murk is mood; this is not.
          new THREE.MeshBasicMaterial({ color: 0xffd9a0, fog: false }),
        );
        // Half a metre in front of him, a little to one side, at about the
        // height a двустволка is held: local −Z is the way the capsule faces,
        // and the body's own centre is half its height above the floor.
        muzzle.position.set(0.12, 0.4, -0.5);
        muzzle.visible = false;
        const blow = new THREE.Mesh(
          peerBlowGeometry,
          // Unfogged for the reason the muzzle is, and translucent because it is
          // WIDER than the man it marks: opaque, it would replace him for three
          // frames at exactly the moment a player is watching what happens to
          // him. `depthWrite` off so it cannot sort itself in front of the
          // things it is drawn over.
          new THREE.MeshBasicMaterial({
            color: 0xff3b30,
            fog: false,
            transparent: true,
            opacity: 0.55,
            depthWrite: false,
          }),
        );
        blow.visible = false;
        body.add(muzzle, blow);
        scene.add(body);
        figure = { body, muzzle, blow, drawnState: -1 };
        peerFigures.set(p.slot, figure);
      }

      const st = p.st ?? 0;
      const down = st === PEER_DOWN;
      // `z` is an EYE height — derived by the client from the sector the wire
      // named, since the wire stopped carrying it — so the floor he is standing
      // on is that much below it, and the body sits on the floor.
      const floorZ = p.z - opts.player.eyeHeight;
      figure.body.position.set(
        p.x,
        floorZ + (down ? peerRadius : opts.player.bodyHeight / 2),
        -p.y,
      );
      figure.body.rotation.y = -p.yaw;
      if (st !== figure.drawnState) {
        figure.drawnState = st;
        // Tipped over onto the floor. WHICH WAY HE FELL IS NOT MODELLED — the X
        // rotation is applied after the yaw, so every corpse lies along the same
        // world axis. It reads as a body on the ground from any angle, which is
        // the whole of what it has to say.
        figure.body.rotation.x = down ? Math.PI / 2 : 0;
        const material = figure.body.material as InstanceType<typeof THREE.MeshBasicMaterial>;
        material.color.setHex(
          down
            ? PEER_TINT_DOWN
            : st === PEER_PROTECTED
              ? PEER_TINT_PROTECTED
              : PEER_TINT_ALIVE,
        );
      }
      figure.body.visible = true;
      figure.muzzle.visible = marks.fired.has(p.slot);
      figure.blow.visible = marks.hit.has(p.slot);
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

  /**
   * The обрез going off: the barrels light and the gun is thrown back.
   *
   * CALLED FOR A SHOT THE PREDICTION GRANTED, never for a tap. The browser runs
   * the same trigger rule the server is about to run, so it already knows
   * whether a pull became a shot — and a flash drawn on the tap instead would
   * fire for every refusal inside the cadence, which is most of them when
   * somebody holds the trigger down. What it buys by being here rather than
   * waiting for the snapshot is the whole point: zero milliseconds.
   *
   * Restarting rather than adding, so the second barrel one cadence later is the
   * same kick and not a bigger one.
   */
  function fire(): void {
    if (disposed) return;
    // The mark is only started here; `render` is the sole writer of whether the
    // muzzle is in a frame. Lighting it from both places is how a flash ends up
    // cleared by the draw that was supposed to show it.
    flash.fire();
    recoil = 1;
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

  return { render, resize, setOnFloor, setPeers, fire, dispose };
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
