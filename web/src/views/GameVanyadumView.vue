<template>
  <div class="dum-root" data-testid="vanyadum-root">
    <!-- SPLASH — the rules cheatsheet, generated from the served catalogue. -->
    <section v-if="phase === 'splash'" class="dum-splash" data-testid="vanyadum-splash">
      <h1 class="dum-title">ВАНЯДУМ</h1>
      <p class="dum-lore">
        Нейрослопы сожрали весь рэп. Ключи где-то на заброшке, пиво там же.
        Только Ваня, последний оплот традиционных ценностей русского репа, ещё
        может это разгрести.
      </p>

      <div v-if="loading" class="dum-loading">
        <v-progress-circular indeterminate size="28" />
      </div>

      <div v-else-if="!webgl" class="dum-nogl" data-testid="vanyadum-nogl">
        <p class="mb-2">Твой браузер не умеет 3D (WebGL выключен или не тянет).</p>
        <p class="text-caption">Остальные разделы работают — заходи в «Ванягоччи».</p>
      </div>

      <template v-else>
        <!-- The заброшка would not let us in. A refusal is never a queue, so
             this says so and the door below is the retry. -->
        <p v-if="full" class="dum-full" data-testid="vanyadum-full">
          ЗАБРОШКА ПОЛНА — внутри уже {{ config?.world.max_occupants ?? 0 }}. Подожди, пока кто-нибудь
          выйдет, и попробуй ещё раз.
        </p>

        <div class="dum-rules" data-testid="vanyadum-rules">
          <section v-for="block in rules" :key="block.title" class="dum-rule-block">
            <h2 class="dum-rule-title">{{ block.title }}</h2>
            <dl class="dum-rule-list">
              <template v-for="line in block.lines" :key="line.label">
                <dt>{{ line.label }}</dt>
                <dd>{{ line.text }}</dd>
              </template>
            </dl>
          </section>
        </div>

        <div v-if="visits.length" class="dum-visits" data-testid="vanyadum-visits">
          <h2 class="dum-rule-title">Ты уже заходил</h2>
          <ul>
            <li v-for="(visit, i) in visits" :key="i">🕒 {{ visit.seconds }} с · 🍺 {{ visit.beer }}</li>
          </ul>
        </div>

        <v-btn
          class="dum-enter"
          color="error"
          size="large"
          data-testid="vanyadum-enter"
          :loading="entering"
          @click="enterPlay"
        >
          НА ЗАБРОШКУ
        </v-btn>
      </template>

      <p v-if="error" class="dum-error" data-testid="vanyadum-error">{{ error }}</p>
    </section>

    <!-- PLAYING — the world is the canvas; everything else is real DOM. There
         are only two phases now: you are on the splash or you are in the
         building. Nothing ends, so there is no third screen. -->
    <section v-else class="dum-play" data-testid="vanyadum-play">
      <canvas ref="canvasEl" class="dum-canvas" data-testid="vanyadum-canvas" />

      <div class="dum-hud" data-testid="vanyadum-hud">
        <span class="dum-hud-cell">♥ {{ health }}</span>
        <span
          v-for="p in config?.pickups ?? []"
          :key="p.key"
          class="dum-hud-cell"
          :data-testid="`vanyadum-count-${p.key}`"
        >
          {{ p.icon }} {{ bag[p.grants] ?? 0 }}
        </span>
        <!-- Not «осталось»: nothing is being counted down any more. What is on
             the floor goes up as well as down, and how many people are in here
             is the whole point of there being one building. -->
        <span class="dum-hud-cell dum-hud-right" data-testid="vanyadum-floor">
          на полу {{ onFloor.length }}
        </span>
        <span class="dum-hud-cell dum-hud-who" data-testid="vanyadum-occupants">
          👥 {{ occupants }}
        </span>
      </div>

      <p v-if="link !== 'open'" class="dum-link" data-testid="vanyadum-link">
        {{ link === 'connecting' ? 'связь…' : 'связь потеряна, ждём…' }}
      </p>

      <p v-if="sceneFailed" class="dum-blind" data-testid="vanyadum-blind">
        3D не запустилось — ты на заброшке, но смотреть не на что. Уйди со страницы.
      </p>

      <!-- The whole play surface is one touch target: the left half moves, the
           right half looks. Buttons sit on top of it and stop propagation.
           On a mouse it is also what you click to capture the pointer. -->
      <div
        ref="padEl"
        class="dum-pad"
        data-testid="vanyadum-pad"
        @pointerdown="onPointerDown"
        @pointermove="onPointerMove"
        @pointerup="onPointerUp"
        @pointercancel="onPointerUp"
        @click="grabMouse"
      />

      <!-- Desktop only, and only while the mouse is free. Real DOM like every
           other control, so the suite can see whether it is offered. -->
      <button
        v-if="canPointerLock && !pointerLocked"
        class="dum-lock"
        type="button"
        data-testid="vanyadum-lock"
        @click.stop="grabMouse"
      >
        клик — захватить мышь<br />
        <span class="dum-lock-sub">Esc — отпустить</span>
      </button>

      <div
        v-if="stick.active"
        class="dum-stick"
        :style="{ left: `${stick.originX}px`, top: `${stick.originY}px` }"
        aria-hidden="true"
      >
        <span class="dum-stick-knob" :style="stickKnobStyle" />
      </div>

      <button
        class="dum-fire"
        type="button"
        data-testid="vanyadum-fire"
        aria-label="Стрелять"
        @pointerdown.stop
        @click.stop
      >
        🔫
      </button>
    </section>
  </div>
</template>

<script setup lang="ts">
/**
 * «ВАНЯДУМ» — the third game, and the first in 3D.
 *
 * ONE BUILDING, RUNNING CONTINUOUSLY. There is no run, no lobby and no
 * matchmaking: the заброшка is always there, everybody who opens the game is in
 * the same one, and pickups come back a while after somebody takes them. Walking
 * in is opening the socket and saying hello; walking out is leaving the page.
 * Nothing ends, so nothing announces an ending and there is no result screen —
 * only the splash and the building.
 *
 * WHICH BUILDING, AND WHEN IT STOPS BEING THAT ONE. The level is fetched once
 * over HTTP and referenced by index for the rest of the session. It is
 * regenerated only when the last person leaves, so a client that was away long
 * enough can come back holding geometry nobody is standing in: the `world_id` on
 * the ready frame is the ONE signal that says so, and the answer is to re-fetch
 * and rebuild.
 *
 * WHAT IS ON THE CANVAS, AND WHAT IS NOT. The canvas holds the world. Every
 * readout, every control, the splash and the rules are real DOM — which is a
 * testing decision before it is a design one, because nothing inside a canvas
 * can be asserted on without pixel comparison and a test-only introspection API
 * may not ship (see ADR-046, and `src/render/vanyadumScene`).
 *
 * WHERE THE TRUTH LIVES. The server simulates; this view draws. It sends the
 * axes the player is pushing and never a position — a prediction is something
 * this file draws, never something it asserts.
 *
 * TWO SETS OF CONTROLS, AND THE DEVICE PICKS. A phone gets thumbs: a stick that
 * appears where the left one lands, a drag on the right half to look. A desktop
 * gets WASD and the mouse, which needs the pointer CAPTURED — otherwise looking
 * stops at the edge of the window. The split is decided per EVENT rather than
 * per device, so a laptop with a touchscreen keeps both.
 *
 * TWO CLOCKS, ON PURPOSE:
 *
 *   * requestAnimationFrame draws, as fast as the phone will, from the
 *     PREDICTED position — the client runs the same `Step` the server does, so
 *     movement responds instantly and updates at frame rate rather than at the
 *     twenty hertz snapshots arrive at.
 *   * a 100 ms timer sends input, because the socket allows ten frames a second
 *     and that is a bound this game fits inside rather than loosens.
 *
 * PREDICTION IS NOT AUTHORITY. Every snapshot resets the client to what the
 * server says and replays whatever is still unacknowledged on top of it; a
 * disagreement is eased out over a tenth of a second, or snapped if it is too
 * large to glide. Peers are the other way round — they cannot be predicted, so
 * they are drawn interpolated in the recent past. See ADR-052.
 */

import { computed, onBeforeUnmount, onMounted, ref, shallowRef, watch } from 'vue';
import { useTheme } from 'vuetify';
import { gameVanyadumApi } from '../api/endpoints';
import { ApiError } from '../api/client';
import type { VanyadumConfig, VanyadumVisitRow, VanyadumWorld } from '../api/types';
import { pickupsOnFloor } from '../lib/vanyadumLevel';
import { buildRules } from '../lib/vanyadumRules';
import {
  axesFromKeys,
  applyLook,
  buildInputFrame,
  createEmitter,
  mouseLook,
  stickVector,
  type Emitter,
  type VanyadumAxes,
} from '../lib/vanyadumInput';
import { createPredictor, type Predictor } from '../lib/vanyadumPredict';
import { createInterpolator, type Interpolator, type PeerState } from '../lib/vanyadumInterp';
import type { VanyadumScene } from '../render/vanyadumScene';
import { realtimeClient, type ConnectionStatus, type RealtimeFrame } from '../realtime/socket';

type Phase = 'splash' | 'playing';

const phase = ref<Phase>('splash');
const loading = ref(true);
const entering = ref(false);
const error = ref('');
/** The заброшка refused the hello because it is at MaxOccupants. */
const full = ref(false);
const webgl = ref(true);
/** The probe passed but the context could not be created — see enterPlay. */
const sceneFailed = ref(false);

const config = ref<VanyadumConfig | null>(null);
const visits = ref<VanyadumVisitRow[]>([]);
const rules = computed(() => buildRules(config.value));

// --- what the server last told us -----------------------------------------
const health = ref(0);
const bag = ref<Record<string, number>>({});
/** The pickup ids lying on the floor right now. Goes UP as well as down. */
const onFloor = ref<number[]>([]);
/**
 * The last pickup bitmask a snapshot carried, or null before the first one.
 *
 * Null is a state rather than a default: zero is a real answer — the mask with
 * every pickup collected — so "we have not been told yet" cannot be spelled `0`
 * without the first empty floor being mistaken for it.
 */
let floorMask: number | null = null;
/** How many people are in the building, including you. Derived from the frame. */
const occupants = ref(1);
const link = ref<'connecting' | 'open' | 'lost'>('connecting');

/**
 * The camera, written by snapshots and read by the render loop.
 *
 * Deliberately a plain object rather than a ref: it is written twenty times a
 * second and read sixty, and putting that through Vue's reactivity would buy a
 * scheduler pass and a vdom patch per frame to produce a number only the
 * renderer reads. Same rule the yard settled on — membership is reactive,
 * positions are not.
 */
const view = { x: 0, y: 0, z: 0, yaw: 0, pitch: 0 };

/** What the interpolator says the peers look like this frame. */
let peers: PeerState[] = [];
/** The last snapshot tick we drew, echoed so the server can derive our latency. */
let seenTick = 0;

/**
 * Where the player is AIMING, which is the client's own state rather than the
 * server's.
 *
 * Aim is an input: the server clamps it and echoes it, but it never overrides
 * it, because a view that snapped back to a snapshot's angle would fight the
 * thumb that is turning it. Position is the opposite — entirely the server's.
 */
const aim = { yaw: 0, pitch: 0 };

const canvasEl = ref<HTMLCanvasElement | null>(null);
const padEl = ref<HTMLElement | null>(null);
const scene = shallowRef<VanyadumScene | null>(null);
const theme = useTheme();

/** The building we fetched and built meshes for, or null on the splash. */
let world: VanyadumWorld | null = null;
/**
 * Guards the re-fetch a stale `world_id` triggers.
 *
 * Two ready frames can arrive before the first re-fetch has landed — a reconnect
 * delivers one per open — and two concurrent rebuilds would race each other for
 * the same scene object.
 */
let adopting = false;
let emitter: Emitter | null = null;
let predictor: Predictor | null = null;
let interp: Interpolator | null = null;
/** Commands applied locally and not yet sent. */
let outbox: ReturnType<Predictor['apply']>[] = [];
let release: (() => void) | null = null;
let sendTimer: number | undefined;
let frameHandle = 0;
let lastFrameMs = 0;
let resizeObserver: ResizeObserver | null = null;
const heldKeys = new Set<string>();

// --- the movement stick ----------------------------------------------------
const stick = ref({ active: false, originX: 0, originY: 0, x: 0, y: 0 });
/** Radius in pixels of a full push. Small enough for a thumb, big enough to aim. */
const STICK_RADIUS = 56;
let stickPointer: number | null = null;
let lookPointer: number | null = null;
let lookLast = { x: 0, y: 0 };

// --- the mouse, on a desktop ------------------------------------------------

/**
 * Whether capturing the mouse is worth offering at all.
 *
 * Two conditions, and both are needed. `requestPointerLock` may simply not
 * exist; and `(pointer: fine)` is what separates a mouse from a thumb — asking a
 * phone to capture a pointer it does not have would put a dead prompt in the
 * middle of the screen. Resolved once, on mount, because neither answer changes
 * while somebody is playing.
 */
const canPointerLock = ref(false);
/** Whether the pointer is captured right now. Driven only by the browser. */
const pointerLocked = ref(false);

const stickKnobStyle = computed(() => {
  const dx = stick.value.x - stick.value.originX;
  const dy = stick.value.y - stick.value.originY;
  const mag = Math.hypot(dx, dy);
  const scale = mag > STICK_RADIUS ? STICK_RADIUS / mag : 1;
  return { transform: `translate(${dx * scale}px, ${dy * scale}px)` };
});

/** The axes being pushed right now, from whichever input is in use. */
function currentAxes(): VanyadumAxes {
  const keys = axesFromKeys(heldKeys);
  let mx = keys.mx;
  let my = keys.my;
  if (stick.value.active) {
    const v = stickVector(
      { x: stick.value.originX, y: stick.value.originY },
      { x: stick.value.x, y: stick.value.y },
      STICK_RADIUS,
    );
    mx = v.mx;
    my = v.my;
  }
  return { mx, my, yaw: aim.yaw, pitch: aim.pitch };
}

const moving = () => {
  const a = currentAxes();
  return a.mx !== 0 || a.my !== 0;
};

// --- lifecycle -------------------------------------------------------------

onMounted(async () => {
  // Probed once, before anything is built: a browser with WebGL turned off gets
  // a sentence rather than a black rectangle, and the rest of the app still
  // works. A real path, not a test hook — see webglAvailable.
  const { webglAvailable } = await import('../render/vanyadumScene');
  webgl.value = webglAvailable();

  canPointerLock.value =
    typeof Element !== 'undefined' &&
    'requestPointerLock' in Element.prototype &&
    (window.matchMedia?.('(pointer: fine)').matches ?? false);

  try {
    config.value = await gameVanyadumApi.config();
  } catch (e) {
    error.value = e instanceof ApiError ? `не открылось (${e.code})` : 'не открылось';
  }
  await loadVisits();

  // NOTHING IS RESUMED AND NOTHING IS ENTERED HERE, which is a deliberate change
  // of shape rather than a simplification. A hello creates an occupant and
  // therefore, eventually, a recorded visit — so it may only be sent when the
  // player has actually said he wants to be in the building. Walking somebody in
  // because they opened the page would write a visit for reading the rules.
  loading.value = false;

  window.addEventListener('keydown', onKeyDown);
  window.addEventListener('keyup', onKeyUp);
  // On WINDOW rather than on the pad: once the pointer is locked the cursor no
  // longer exists, so which element a move "is over" stops being a meaningful
  // question and the browser is free to deliver it to the document.
  window.addEventListener('mousemove', onMouseMove);
  document.addEventListener('pointerlockchange', onPointerLockChange);
});

onBeforeUnmount(() => {
  teardownPlay();
  window.removeEventListener('keydown', onKeyDown);
  window.removeEventListener('keyup', onKeyUp);
  window.removeEventListener('mousemove', onMouseMove);
  document.removeEventListener('pointerlockchange', onPointerLockChange);
});

async function loadVisits(): Promise<void> {
  try {
    visits.value = (await gameVanyadumApi.myVisits()).visits;
  } catch {
    visits.value = [];
  }
}

/**
 * Re-reads the list every time the splash comes back, because a list read once
 * on mount is stale every time after the first.
 *
 * The splash is not a screen you see once: a refusal returns to it, so does a
 * failed rebuild, and so does an error walking in. Read on the TRANSITION rather
 * than on a timer — this is a screen appearing, not something that needs
 * watching — so it costs one request each time the player is standing in front
 * of the list, and nothing at all while he is inside.
 *
 * WHAT IT STILL CANNOT SHOW, and it is worth being exact: the visit you have
 * just finished. A row is written when the abandon grace expires, minutes after
 * the socket went away, so at the moment the splash reappears that row does not
 * exist for anybody to read. What this does pick up is every visit whose grace
 * expired while you were inside — including the previous one, which is the case
 * a player would otherwise only see by reloading the page.
 */
watch(phase, (now) => {
  if (now === 'splash') void loadVisits();
});

/**
 * Walks into the building: fetches it, builds it, opens the socket.
 *
 * This is also the retry after a refusal, which is why it clears `full` on the
 * way in — there is nothing else to press, because a second button that also
 * said "try again" would be a second path to one outcome.
 */
async function enterPlay(): Promise<void> {
  if (entering.value || !webgl.value || !config.value) return;
  entering.value = true;
  error.value = '';
  full.value = false;
  try {
    const fetched = await gameVanyadumApi.world();
    world = fetched;
    phase.value = 'playing';
    await buildWorld();

    // The hello goes out from the status callback rather than here: `send` drops
    // anything written before the socket is OPEN, and subscribing only starts
    // the handshake. Sending it there also covers every reconnect, which matters
    // — the заброшка outlives a dropped socket for the length of its grace
    // period, so a returning client has to say hello again to keep its place.
    release = realtimeClient(fetched.room).subscribe({ frames: onFrame, status: onStatus });

    sendTimer = window.setInterval(sendInput, Math.round(1000 / config.value.sim.input_hz));
    lastFrameMs = performance.now();
    frameHandle = requestAnimationFrame(drawFrame);
  } catch (e) {
    // Back to the splash rather than into a building we could not finish
    // building. Half-entered is the one state with no way out of it: the door is
    // on the splash, so a player left on the play screen with no world has
    // nothing to press.
    teardownPlay();
    phase.value = 'splash';
    error.value = e instanceof ApiError ? `не вышло (${e.code})` : 'не вышло';
  } finally {
    entering.value = false;
  }
}

/**
 * Builds everything derived from the building — the scene, the predictor, the
 * interpolator, the emitter — and nothing that is derived from the socket.
 *
 * Split out because it happens twice: once on the way in, and again when a ready
 * frame names a building we are not the ones standing in (see adoptBuilding).
 * The socket, the timers and the input state deliberately survive that second
 * call: the geometry changed, the connection did not.
 */
async function buildWorld(): Promise<void> {
  if (!world || !config.value) return;
  const level = world.level;
  health.value = config.value.player.start_health;
  bag.value = {};
  onFloor.value = level.pickups.map((p) => p.id);
  floorMask = null;
  occupants.value = 1;
  aim.yaw = level.spawn_yaw;
  aim.pitch = 0;
  view.x = level.spawn.x;
  view.y = level.spawn.y;
  view.z = config.value.player.eye_height;
  view.yaw = aim.yaw;

  // The canvas only exists once the template has switched phase.
  await new Promise((resolve) => requestAnimationFrame(resolve));
  const canvas = canvasEl.value;
  if (!canvas) return;

  // Passing the WebGL probe is not the same as GETTING a context: a browser with
  // several 3D tabs open hits its context limit, a driver can be lost, and a
  // phone in a low-power mode can simply refuse. When that happens the player is
  // still in the building — the server is still simulating him — so the HUD
  // stays on screen and he is told, rather than being left staring at a black
  // rectangle.
  sceneFailed.value = false;
  try {
    const { createScene } = await import('../render/vanyadumScene');
    scene.value = await createScene({
      canvas,
      level,
      surfaces: config.value.surfaces,
      reducedMotion: window.matchMedia?.('(prefers-reduced-motion: reduce)').matches ?? false,
    });
    scene.value.setOnFloor(onFloor.value);

    resizeObserver = new ResizeObserver(() => {
      const box = canvas.getBoundingClientRect();
      scene.value?.resize(box.width, box.height);
    });
    resizeObserver.observe(canvas);
    const box = canvas.getBoundingClientRect();
    scene.value.resize(box.width, box.height);
  } catch {
    sceneFailed.value = true;
  }

  const sim = config.value.sim;
  emitter = createEmitter({
    // The send rate times the commands one frame may carry: a window then holds
    // exactly what it is allowed to, whatever the phone's frame rate is.
    hz: sim.input_hz * sim.max_commands,
    maxStepSeconds: sim.max_step_seconds,
    maxPerWake: sim.max_commands,
  });
  predictor = createPredictor({
    level,
    eyeHeight: config.value.player.eye_height,
    constants: {
      walkSpeed: config.value.player.walk_speed,
      radius: config.value.player.radius,
      maxStep: config.value.player.max_step,
      maxPitch: config.value.player.max_pitch,
      maxStepSeconds: sim.max_step_seconds,
      collisionPasses: sim.collision_passes,
    },
    start: {
      x: level.spawn.x,
      y: level.spawn.y,
      sector: level.spawn_sector,
      yaw: level.spawn_yaw,
    },
  });
  // The delay is SERVED, not chosen here: lag compensation on the server
  // rewinds by exactly this number, so a client picking its own would be
  // picking its own advantage.
  //
  // The second argument is one SIMULATION step, and it is what the interpolation
  // buffer measures its timeline in — a snapshot's `k` counts simulation ticks
  // rather than snapshots, so this stays right if the server ever publishes every
  // second or third tick instead of the one-per-step it sends today.
  interp = createInterpolator(sim.interp_delay_ms, 1000 / (sim.hz || 20));
  outbox = [];
  seenTick = 0;
}

/**
 * Throws away everything built from a building, keeping the socket.
 *
 * The counterpart of buildWorld, and the reason the two are a pair: a
 * regenerated заброшка invalidates every mesh, the predictor's collision data
 * and the interpolation buffer's contents, all of which describe walls that are
 * no longer there.
 */
function disposeWorld(): void {
  resizeObserver?.disconnect();
  resizeObserver = null;
  scene.value?.dispose();
  scene.value = null;
  emitter?.reset();
  emitter = null;
  interp?.reset();
  interp = null;
  predictor = null;
  outbox = [];
  peers = [];
  floorMask = null;
}

/**
 * Leaves the building. Closing the socket IS leaving, so this is also what an
 * unmount does — there is no goodbye to send and nothing to abandon.
 */
function teardownPlay(): void {
  if (sendTimer !== undefined) window.clearInterval(sendTimer);
  sendTimer = undefined;
  if (frameHandle) cancelAnimationFrame(frameHandle);
  frameHandle = 0;
  release?.();
  release = null;
  disposeWorld();
  world = null;
  adopting = false;
  // Handing the mouse back is not optional: leaving a page with the pointer
  // still captured strands the cursor on a screen that no longer uses it.
  if (pointerLocked.value) document.exitPointerLock?.();
  stickPointer = null;
  lookPointer = null;
  stick.value = { active: false, originX: 0, originY: 0, x: 0, y: 0 };
  heldKeys.clear();
}

// --- the two clocks --------------------------------------------------------

/**
 * The draw clock. Emits and PREDICTS input, decays any correction, and renders.
 *
 * The whole difference between this and what shipped in iteration 1: the camera
 * comes from the predictor rather than from the last snapshot, so it moves at
 * frame rate and responds to a thumb with no round trip in between.
 */
function drawFrame(now: number): void {
  const dt = Math.min(0.1, (now - lastFrameMs) / 1000);
  lastFrameMs = now;

  if (predictor && emitter) {
    predictor.look(aim.yaw, aim.pitch);
    const axes = currentAxes();
    for (const cmd of emitter.due(now, axes)) {
      // Applied locally the instant it exists, and queued for sending
      // unchanged. Predicting one thing and sending another is the one mistake
      // this whole arrangement cannot survive.
      outbox.push(predictor.apply(cmd));
    }
    predictor.tick(dt);
    // Drawn over the carry rather than at the last command's endpoint: commands
    // exist forty times a second and this runs sixty to a hundred and
    // forty-four, so without it the eye is redrawn identically for one to three
    // frames and then jumps a sub-step. Read AFTER `due`, which has just taken
    // whatever it could out of the leftover — and given the SAME axes `due` was
    // given, so the frame drawn ahead and the command it anticipates cannot
    // disagree.
    //
    // NOTHING PINS THIS ARGUMENT. A zero here type-checks and leaves every suite
    // green while the judder comes straight back, because the camera is inside
    // the canvas, which ADR-047 accepts is opaque to both Playwright suites.
    // Only a person looking at the game can see it — so change this line on
    // purpose or not at all. The reasoning is on `view` in vanyadumPredict.ts.
    const v = predictor.view(emitter.residualSeconds(), axes);
    view.x = v.x;
    view.y = v.y;
    view.z = v.z;
    view.yaw = v.yaw;
    view.pitch = v.pitch;
  }

  // Peers are drawn in the recent past, because their intent cannot be
  // predicted the way our own is. Empty until multiplayer fills it.
  peers = interp ? interp.sample(now) : [];
  scene.value?.setPeers(peers);

  scene.value?.render(view, moving(), dt);
  frameHandle = requestAnimationFrame(drawFrame);
}

/** The send clock. One frame per window, carrying everything sampled in it. */
function sendInput(): void {
  if (!world || !predictor || !config.value) return;
  const fresh = outbox;
  outbox = [];
  // An empty frame still spends one of the socket's ten a second, so silence is
  // the right answer when nothing happened.
  if (fresh.length === 0) return;

  // Redundancy: the tail of everything still unacknowledged rides along, so one
  // lost packet costs no input at all. The server drops any sequence it has
  // already ACCEPTED — queued as well as stepped — which is what makes a
  // duplicate free; dropping only what it had stepped would have let a resend
  // back into a queue that was still holding the original. The pending list
  // this reads from has to exist for reconciliation anyway.
  //
  // The frame's shape is a pure function (buildInputFrame), tested on its own —
  // `seenTick` is the last snapshot tick we drew, from which the server derives
  // our round trip, because deriving beats trusting a number a client chooses.
  const frame = buildInputFrame(
    seenTick,
    fresh,
    predictor.unacknowledged(config.value.sim.redundant + fresh.length),
  );
  realtimeClient(world.room).send({ ...frame });
}

// --- the socket ------------------------------------------------------------

function onFrame(frame: RealtimeFrame): void {
  switch (frame.t) {
    case 'vanyadum_snap':
      applySnapshot(frame);
      break;
    case 'vanyadum_ready':
      void adoptBuilding(typeof frame.world_id === 'string' ? frame.world_id : '');
      break;
    case 'vanyadum_full':
      refused();
      break;
    default:
      // Unknown `t` is ignored, which is what lets either end learn a message
      // type without a coordinated deploy.
      break;
  }
}

/**
 * Checks the building we are drawing against the one we were let into, and
 * rebuilds if they disagree.
 *
 * A disagreement means the заброшка emptied while we were away and was
 * regenerated, which is the only thing that changes its id — so every mesh, the
 * predictor's walls and the interpolation buffer describe a building nobody is
 * standing in. Re-fetching converges: the level this pulls is the one the server
 * has now, so the NEXT ready frame agrees and this does nothing.
 *
 * The socket is left alone throughout. It is the connection that is still good;
 * it is the geometry that went stale.
 */
async function adoptBuilding(id: string): Promise<void> {
  if (!id || adopting || id === world?.world_id) return;
  adopting = true;
  try {
    disposeWorld();
    world = await gameVanyadumApi.world();
    await buildWorld();
  } catch (e) {
    // Out to the splash, because staying here is a dead end: a ready frame only
    // arrives in answer to a hello, and a hello only goes out while we are
    // holding a building. Nothing would ever ask again.
    teardownPlay();
    phase.value = 'splash';
    error.value = e instanceof ApiError ? `заброшку перестроили (${e.code})` : 'заброшку перестроили';
  } finally {
    adopting = false;
  }
}

/**
 * The заброшка is at capacity and would not let us in.
 *
 * Back to the splash, which is where the retry lives: the entry button is the
 * only way in, so it is also the only way to try again. Nothing is queued and
 * nothing is scheduled — there is no moment at which a place is KNOWN to come
 * free, because nothing in this game ends.
 */
function refused(): void {
  teardownPlay();
  phase.value = 'splash';
  full.value = true;
}

function applySnapshot(frame: RealtimeFrame): void {
  // Positions arrive as centimetres and angles as thousandths of a radian —
  // integers, because this frame repeats twenty times a second forever.
  const tick = num(frame.k);
  seenTick = tick;

  // The authoritative position, folded in rather than assigned: the predictor
  // drops what this acknowledges, resets to it, and replays whatever is still
  // pending on top. Assigning it directly is what iteration 1 did, and it is
  // exactly the twenty-hertz camera this change exists to remove.
  predictor?.reconcile({
    x: num(frame.x) / 100,
    y: num(frame.y) / 100,
    sector: num(frame.s),
    ack: num(frame.ack),
  });

  // Peers go into the interpolation buffer stamped with the SERVER'S TICK, and
  // are read back a fixed delay later. The tick rather than `performance.now()`
  // because the tick is a perfect fixed-rate timeline where an arrival time is
  // the network's jitter wearing a clock's clothes — and because the server's lag
  // compensation rewinds to exactly `serverTick − delay`, so keying on anything
  // else has the two ends disagreeing about which instant was on screen. See
  // vanyadumInterp.
  if (interp) {
    const raw = Array.isArray(frame.p) ? (frame.p as Record<string, number | string>[]) : [];
    interp.push(
      raw.map((p) => ({
        id: String(p.i ?? ''),
        x: num(p.x) / 100,
        y: num(p.y) / 100,
        z: num(p.z) / 100,
        yaw: num(p.yaw) / 1000,
        state: num(p.s),
      })),
      tick,
      performance.now(),
    );
  }

  health.value = num(frame.hp);
  // HOW MANY PEOPLE ARE IN HERE, derived rather than sent: the peer array is
  // everybody but you, so its length plus one is the building's population. A
  // dedicated field would be bytes on a repeating payload to restate something
  // the frame already carries.
  const heads = (Array.isArray(frame.p) ? frame.p.length : 0) + 1;
  if (heads !== occupants.value) occupants.value = heads;

  // WHAT IS LYING ON THE FLOOR, AS A BITMASK over the index into the level's own
  // list: bit i set means the i-th pickup is there to be walked over. One number
  // rather than a list on a frame that repeats twenty times a second forever,
  // and thirty-two bits rather than sixty-four because a JSON number is an
  // IEEE754 double.
  //
  // IT MOVES IN BOTH DIRECTIONS. A bottle taken clears its bit and a bottle that
  // has come back sets it again, and this is the only thing on the wire that
  // says so — a respawn has no event, because the mask is idempotent full state
  // and an "it came back" field would be bytes spent to say nothing at all
  // almost every time it was sent.
  const mask = num(frame.pk) >>> 0;
  // Only touch reactivity when the set actually changed: at twenty frames a
  // second an unconditional assignment is a re-render per frame for nothing. The
  // mask compares EXACTLY, where a list comparison could only afford to compare
  // lengths — so one pickup swapped for another registers, and so does the
  // moment a bottle returns to a floor that had the same number of things on it.
  if (mask !== floorMask) {
    floorMask = mask;
    onFloor.value = pickupsOnFloor(world?.level.pickups ?? [], mask);
    scene.value?.setOnFloor(onFloor.value);
  }
  const c = frame.c as Record<string, number> | undefined;
  if (c) {
    for (const [k, v] of Object.entries(c)) {
      if (bag.value[k] !== v) bag.value = { ...bag.value, [k]: v };
    }
  }
}

function onStatus(status: ConnectionStatus): void {
  link.value = status === 'open' ? 'open' : status === 'connecting' ? 'connecting' : 'lost';
  // Every open, not just the first — see enterPlay. A second hello is a
  // reconnect rather than a second person: it refreshes the place we already
  // have instead of taking another one.
  if (status === 'open' && world) realtimeClient(world.room).send({ t: 'vanyadum_hello' });
}

function num(v: unknown): number {
  return typeof v === 'number' && Number.isFinite(v) ? v : 0;
}

// --- pointers --------------------------------------------------------------

/**
 * Left half of the screen moves, right half looks.
 *
 * The stick's origin is wherever the thumb landed rather than a painted circle,
 * because a thumb cannot find a painted circle without looking at it — and
 * looking at it means not looking at the game.
 */
function onPointerDown(e: PointerEvent): void {
  const pad = padEl.value;
  if (!pad) return;
  // A mouse on a machine that can capture the pointer uses WASD and the mouse,
  // never the on-screen stick — otherwise the click that grabs the pointer also
  // starts a step, because it lands on the walking half of the screen. Gated on
  // the EVENT's pointer type rather than on the device, so a laptop with both a
  // mouse and a touchscreen keeps both sets of controls.
  if (e.pointerType === 'mouse' && canPointerLock.value) return;
  const box = pad.getBoundingClientRect();
  const onLeft = e.clientX - box.left < box.width / 2;
  if (onLeft && stickPointer === null) {
    stickPointer = e.pointerId;
    stick.value = {
      active: true,
      originX: e.clientX - box.left,
      originY: e.clientY - box.top,
      x: e.clientX - box.left,
      y: e.clientY - box.top,
    };
  } else if (!onLeft && lookPointer === null) {
    lookPointer = e.pointerId;
    lookLast = { x: e.clientX, y: e.clientY };
  }
  pad.setPointerCapture?.(e.pointerId);
}

function onPointerMove(e: PointerEvent): void {
  const pad = padEl.value;
  if (!pad) return;
  if (e.pointerId === stickPointer) {
    const box = pad.getBoundingClientRect();
    stick.value = {
      ...stick.value,
      x: e.clientX - box.left,
      y: e.clientY - box.top,
    };
  } else if (e.pointerId === lookPointer) {
    const dx = e.clientX - lookLast.x;
    const dy = e.clientY - lookLast.y;
    lookLast = { x: e.clientX, y: e.clientY };
    const next = applyLook(aim, dx, dy, config.value?.player.max_pitch ?? 1.5);
    aim.yaw = next.yaw;
    aim.pitch = next.pitch;
  }
}

function onPointerUp(e: PointerEvent): void {
  if (e.pointerId === stickPointer) {
    stickPointer = null;
    stick.value = { active: false, originX: 0, originY: 0, x: 0, y: 0 };
  } else if (e.pointerId === lookPointer) {
    lookPointer = null;
  }
}

// --- the mouse -------------------------------------------------------------

/**
 * Captures the pointer, which is what makes this playable with a mouse.
 *
 * Without the capture a mouse can only look as far as the window is wide, and
 * then stops at the edge — which is why every first-person game on a desktop
 * does this. It has to be called from a real click: the browser refuses a
 * capture that no user gesture asked for, and rightly, since it hides the
 * cursor and swallows the mouse.
 */
function grabMouse(): void {
  if (!canPointerLock.value || pointerLocked.value) return;
  padEl.value?.requestPointerLock?.();
}

/**
 * The browser is the only writer of the locked flag.
 *
 * Escape releases the pointer without telling us, and so does switching tabs or
 * a full-screen change — so tracking our own idea of the state would be wrong
 * within seconds. Ask the document what it actually did.
 */
function onPointerLockChange(): void {
  pointerLocked.value = !!padEl.value && document.pointerLockElement === padEl.value;
}

function onMouseMove(e: MouseEvent): void {
  if (!pointerLocked.value || phase.value !== 'playing') return;
  const next = mouseLook(aim, e.movementX, e.movementY, config.value?.player.max_pitch ?? 1.5);
  aim.yaw = next.yaw;
  aim.pitch = next.pitch;
}

// --- keyboard, so the game is developable and drivable without a thumb ------

function onKeyDown(e: KeyboardEvent): void {
  if (phase.value !== 'playing') return;
  heldKeys.add(e.code);
  if (['ArrowLeft', 'ArrowRight'].includes(e.code)) return;
  if (e.code.startsWith('Key') || e.code.startsWith('Arrow')) e.preventDefault();
}

function onKeyUp(e: KeyboardEvent): void {
  heldKeys.delete(e.code);
}

// The murk the scene clears to is fixed, so the game looks the same in both
// themes. Watching the theme is only here to stop Vuetify's own background
// showing through the letterbox on an ultra-wide screen.
watch(
  () => theme.global.current.value.dark,
  () => {},
);
</script>

<style scoped>
/* Height = visible viewport minus the app bar, with the same 72px the yard uses
   — the empirical value that survived the bug where mobile chrome sliding in
   produced a permanent scrollbar. */
.dum-root {
  height: calc(100dvh - 72px);
  overflow: hidden;
  position: relative;
}

.dum-splash {
  height: 100%;
  overflow-y: auto;
  padding: 16px;
  display: flex;
  flex-direction: column;
  align-items: center;
  gap: 12px;
  text-align: center;
  background: linear-gradient(170deg, #1a0d0d, #0e1416);
  color: rgba(255, 255, 255, 0.94);
}

.dum-title {
  font-size: clamp(28px, 9vw, 44px);
  font-weight: 900;
  letter-spacing: 0.08em;
  margin: 0;
  color: #e2574c;
  text-shadow: 0 2px 0 rgba(0, 0, 0, 0.6);
}

.dum-lore {
  margin: 0;
  max-width: 34rem;
  font-size: 0.95rem;
  line-height: 1.45;
  opacity: 0.85;
}

.dum-rules {
  width: 100%;
  max-width: 34rem;
  text-align: left;
  display: flex;
  flex-direction: column;
  gap: 12px;
}

.dum-rule-title {
  font-size: 0.8rem;
  text-transform: uppercase;
  letter-spacing: 0.12em;
  opacity: 0.6;
  margin: 0 0 4px;
}

.dum-rule-list {
  margin: 0;
  display: grid;
  grid-template-columns: minmax(6.5rem, auto) 1fr;
  gap: 4px 10px;
  font-size: 0.86rem;
  line-height: 1.35;
}

.dum-rule-list dt {
  font-weight: 700;
  white-space: nowrap;
}

.dum-rule-list dd {
  margin: 0;
  opacity: 0.85;
}

.dum-visits {
  width: 100%;
  max-width: 34rem;
  text-align: left;
  font-size: 0.86rem;
}

.dum-visits ul {
  margin: 0;
  padding-left: 1.1rem;
}

/* The refusal. Loud enough to be the first thing read on a screen that is
   otherwise a wall of rules, and it wraps rather than overflowing a phone. */
.dum-full {
  width: 100%;
  max-width: 34rem;
  margin: 0;
  padding: 10px 12px;
  border: 2px solid #e2574c;
  border-radius: 10px;
  background: rgba(226, 87, 76, 0.15);
  color: #ffd0c9;
  font-weight: 700;
  font-size: 0.92rem;
  line-height: 1.35;
}

.dum-enter {
  min-height: 48px;
  font-weight: 900;
  letter-spacing: 0.06em;
  margin-top: 4px;
}

.dum-error,
.dum-nogl {
  font-size: 0.85rem;
  color: #ffb4a8;
  max-width: 30rem;
}

.dum-loading {
  padding: 24px;
}

/* --- playing ------------------------------------------------------------- */

.dum-play {
  position: absolute;
  inset: 0;
  overflow: hidden;
  background: #0d0f10;
}

.dum-canvas {
  position: absolute;
  inset: 0;
  width: 100%;
  height: 100%;
  display: block;
}

/* The pad sits over the whole canvas and swallows every gesture, so the page
   cannot scroll, pull to refresh, or zoom while somebody is turning round.
   `touch-action: none` is what makes that true on a phone. */
.dum-pad {
  position: absolute;
  inset: 0;
  touch-action: none;
  -webkit-user-select: none;
  user-select: none;
}

.dum-hud {
  position: absolute;
  top: 0;
  left: 0;
  right: 0;
  display: flex;
  gap: 12px;
  padding: 8px 12px;
  font-variant-numeric: tabular-nums;
  font-weight: 700;
  color: rgba(255, 255, 255, 0.92);
  text-shadow: 0 1px 3px rgba(0, 0, 0, 0.9);
  pointer-events: none;
}

/* Pushed to the far end, so the two readouts about the BUILDING sit together
   and away from the two about you. */
.dum-hud-right {
  margin-left: auto;
  opacity: 0.75;
  font-weight: 400;
}

.dum-hud-who {
  opacity: 0.75;
  font-weight: 400;
}

.dum-blind {
  position: absolute;
  left: 12px;
  right: 12px;
  top: 50%;
  margin: 0;
  text-align: center;
  font-size: 0.9rem;
  color: #ffb4a8;
  text-shadow: 0 1px 3px rgba(0, 0, 0, 0.9);
  pointer-events: none;
}

.dum-link {
  position: absolute;
  top: 36px;
  left: 12px;
  margin: 0;
  font-size: 0.8rem;
  color: #ffd28a;
  text-shadow: 0 1px 3px rgba(0, 0, 0, 0.9);
  pointer-events: none;
}

/* The desktop prompt. Centred rather than tucked in a corner because it is the
   one thing on screen that has to be found before the game is playable with a
   mouse — and it is a real button, so it is reachable by keyboard too. */
.dum-lock {
  position: absolute;
  left: 50%;
  top: 50%;
  transform: translate(-50%, -50%);
  min-height: 48px;
  padding: 12px 20px;
  border-radius: 12px;
  border: 2px solid rgba(255, 255, 255, 0.25);
  background: rgba(0, 0, 0, 0.55);
  color: rgba(255, 255, 255, 0.9);
  font-size: 0.95rem;
  font-weight: 700;
  line-height: 1.4;
  text-align: center;
}

.dum-lock-sub {
  font-size: 0.78rem;
  font-weight: 400;
  opacity: 0.7;
}

.dum-stick {
  position: absolute;
  width: 112px;
  height: 112px;
  margin: -56px 0 0 -56px;
  border-radius: 50%;
  border: 2px solid rgba(255, 255, 255, 0.25);
  pointer-events: none;
}

.dum-stick-knob {
  position: absolute;
  left: 50%;
  top: 50%;
  width: 44px;
  height: 44px;
  margin: -22px 0 0 -22px;
  border-radius: 50%;
  background: rgba(255, 255, 255, 0.3);
}

/* Comfortably past the 44px minimum and clear of the bottom edge, where a
   phone's own home indicator lives. */
.dum-fire {
  position: absolute;
  right: 16px;
  bottom: 24px;
  width: 72px;
  height: 72px;
  border-radius: 50%;
  border: 2px solid rgba(255, 255, 255, 0.3);
  background: rgba(226, 87, 76, 0.35);
  font-size: 28px;
  line-height: 1;
}

@media (prefers-reduced-motion: reduce) {
  .dum-stick-knob {
    transition: none;
  }
}
</style>
