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
        <!-- THE SERVER'S SHELL COUNT, never the prediction's. The browser
             predicts the gun so that a muzzle flash can be drawn with no round
             trip, but what is WRITTEN DOWN is what the snapshot says — so a
             client that predicted a shot the server refused is corrected within
             one frame rather than showing a number it made up. -->
        <span class="dum-hud-cell" data-testid="vanyadum-shells">
          🔫 {{ reloading ? 'жду' : `${loaded}/${barrels}` }}
        </span>
        <span
          v-for="p in config?.pickups ?? []"
          :key="p.key"
          class="dum-hud-cell"
          :data-testid="`vanyadum-count-${p.key}`"
        >
          {{ p.icon }} {{ bag[p.grants] ?? 0 }}
        </span>
        <!-- Not «осталось»: nothing is being counted down any more. What is on
             the floor goes up as well as down. -->
        <span class="dum-hud-cell dum-hud-right" data-testid="vanyadum-floor">
          на полу {{ onFloor.length }}
        </span>
      </div>

      <!-- THE STANDINGS. Real DOM, never inside the canvas (ADR-047) — it is a
           readout somebody reads, and nothing painted into a canvas can be
           asserted on without pixel comparison.

           It is of the BUILDING rather than of the view: it lists everybody in
           the заброшка, including the people two rooms away who are deliberately
           missing from the snapshot. That makes it the only honest source for
           how many are in here — the peer array is filtered now, so counting it
           would report how many are visible and would tick up and down as
           somebody walked through a doorway.

           Absent rather than empty before the first board frame lands, which is
           a fraction of a second after walking in: a roster change publishes one
           on the tick it happened. Absent again for as long as the link is down
           — a directory nothing is refreshing stops being true, and the hello
           that follows a reconnect brings a fresh one (`onStatus`). -->
      <div v-if="board.length" class="dum-board" data-testid="vanyadum-board">
        <div class="dum-board-head">👥 {{ board.length }} на заброшке</div>
        <ol class="dum-board-rows">
          <li
            v-for="row in board"
            :key="row.slot"
            class="dum-board-row"
            :class="{ 'is-me': row.slot === mySlot }"
            data-testid="vanyadum-board-row"
          >
            <!-- The arrow is in the text rather than in a ::before, so it is
                 real content a test can read — and so the row is told apart by
                 something other than being a slightly brighter grey. -->
            <span class="dum-board-name">{{ row.slot === mySlot ? '▸ ' : '' }}{{ row.name }}</span>
            <span class="dum-board-time">{{ clock(row.seconds) }}</span>
            <span
              v-for="p in config?.pickups ?? []"
              :key="p.key"
              class="dum-board-bag"
              :data-testid="`vanyadum-board-${p.key}`"
            >
              {{ p.icon }}{{ row.bag[p.grants] ?? 0 }}
            </span>
          </li>
        </ol>
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

      <!-- HELD RATHER THAN TAPPED. The обрез has a cadence, so a held trigger
           is refused between shots by the same rule on both ends — which makes
           holding the natural control and tapping unnecessary. `pointerup` and
           `pointercancel` both release it: a pointer the browser takes away
           mid-gesture must not leave the trigger stuck down.

           The gesture is stopped from reaching the pad underneath, or the same
           thumb would also be turning the player round. -->
      <button
        class="dum-fire"
        type="button"
        data-testid="vanyadum-fire"
        aria-label="Стрелять"
        @pointerdown.stop="pullTrigger"
        @pointerup.stop="releaseTrigger"
        @pointercancel.stop="releaseTrigger"
        @pointerleave="releaseTrigger"
        @click.stop
      >
        🔫
      </button>

      <!-- The mute, within reach of the thumb that is firing and clear of both
           the stick's half of the screen and the standings. A game whose only
           sound is a shotgun needs somewhere to turn it off that is not a
           settings screen nobody will open. -->
      <button
        class="dum-mute"
        type="button"
        data-testid="vanyadum-mute"
        :aria-pressed="soundOff"
        :aria-label="soundOff ? 'Включить звук' : 'Выключить звук'"
        @pointerdown.stop
        @click.stop="toggleSound"
      >
        {{ soundOff ? '🔇' : '🔊' }}
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
 * WHAT YOU SEE AND WHAT IS TRUE ARE TWO DIFFERENT FRAMES. A snapshot is cut to
 * the room you are standing in and the rooms through its doorways, so somebody
 * further off is simply absent from it rather than announced as gone; the
 * standings frame, once a second, is of the whole building and names everybody
 * including you. Peers are addressed by a SLOT — a place, not a person, reused
 * after its holder leaves — and the standings are the only thing that says whose
 * slot it currently is. See vanyadumRoster for both, and for the one ordering
 * hazard that arrangement has.
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
 *
 * THE TRIGGER IS PREDICTED AND THE SHELL COUNT IS NOT, and holding those two
 * apart is the whole shape of the gun's arrival here. The browser runs the
 * server's own trigger rule, so it knows within the same frame whether a pull
 * became a shot — that is what the muzzle flash, the kick and the bang are
 * driven from, and it is why they land in zero milliseconds instead of a round
 * trip. What is WRITTEN on the HUD is the snapshot's number, because a shell
 * count is something a player reads and a guess that is taken back a frame later
 * is worse than a number that is fifty milliseconds old.
 *
 * SOMEBODY ELSE'S SHOT IS THE ONE THING HERE THAT NEEDS TELLING. Your own is
 * derived — a predicted barrel count falling by one IS the shot — but a peer
 * carries no barrel count, so his gun going off rides the snapshot as a marker
 * on the tick it happened, and the заброшка's other four see a flash at him. It
 * arrives as a LEVEL lasting a tick and is drawn as an EVENT lasting three
 * frames; the conversion is vanyadumFlash's, and the drawing is the scene's.
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
import { createGunAudio, type GunAudio } from '../lib/vanyadumSound';
import { createInterpolator, type Interpolator, type PeerState } from '../lib/vanyadumInterp';
import { changedHands, clock, decodeBoard, decodePeers, type BoardRow } from '../lib/vanyadumRoster';
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
/**
 * The gun, as the newest snapshot describes it.
 *
 * SEPARATE FROM THE PREDICTION ON PURPOSE, and the split is the whole shape of
 * this iteration. The prediction is what decides whether to draw a muzzle flash
 * this instant; these are what the HUD is allowed to WRITE DOWN, because a shell
 * count is a number a player reads and a number he reads must not be a guess
 * that is corrected a frame later.
 */
const loaded = ref(0);
const reloading = ref(false);
/** Barrels a full gun holds, from the catalogue — never a 2 typed out here. */
const barrels = computed(() => config.value?.gun.barrels ?? 0);
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
/**
 * The standings: everybody in the building, in slot order, as the newest board
 * frame described them.
 *
 * FULL STATE, replaced rather than merged — a person who has gone is simply not
 * in the next one, exactly as a peer who has walked out of view is simply not in
 * the next snapshot. It is also the slot directory: the map from the small
 * integer a snapshot addresses a peer by to whoever is currently standing in
 * that place.
 *
 * EMPTIED WHENEVER THE LINK IS NOT OPEN, because a directory is only true while
 * something is keeping it true — see `onStatus`.
 */
const board = ref<BoardRow[]>([]);
/**
 * Our own place in the building, or −1 while we do not know it: before the first
 * ready frame, and again from the moment the socket goes until the next one.
 *
 * NOTHING ELSE EVER TELLS US. A snapshot names everybody except its own reader,
 * so without this the standings could be read in full without knowing which row
 * was ours. −1 rather than 0, because slot 0 is a real place and the first one
 * handed out — defaulting to it would put the arrow on somebody else.
 */
const mySlot = ref(-1);
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

// --- the trigger -----------------------------------------------------------

/** The обрез's voice. Built once; it wakes no audio hardware until armed. */
const audio: GunAudio = createGunAudio();
const soundOff = ref(false);
/** The key that fires, on a machine that has one. */
const FIRE_KEY = 'Space';
/**
 * Whether the trigger is being held, by a thumb, a key or a mouse button.
 *
 * ONE FLAG FOR THREE INPUTS rather than three paths to one outcome. A player on
 * a laptop with a touchscreen can use any of them, and none of them means
 * anything different by the time it reaches the emitter.
 */
let triggerHeld = false;
/**
 * How many barrels the PREDICTION had last frame, and whether it was reloading.
 *
 * The flash and the sound are per-frame comparisons over these, which is this
 * project's rule for exactly this situation: a barrel count falling by one IS
 * the shot, so nothing has to be published to say one happened, and a reload
 * timer crossing from zero to non-zero is the reload starting. Both are values
 * the frame already carries. Compared BEFORE anything overwrites them, and
 * seeded from the prediction the moment it is built so that entering the
 * building is not itself read as two shots.
 */
let drawnLoaded = 0;
let drawnReloading = false;

/**
 * Takes hold of the trigger, from whichever input asked.
 *
 * IT ALSO WAKES THE AUDIO, and this is the only place it can. A browser refuses
 * to start an audio context that no user gesture asked for, and the shot is
 * played from the render loop a frame or two later — by which time the gesture
 * is over. Arming here, inside the handler that saw the tap, is what stops the
 * first shot of a visit being the silent one.
 */
function pullTrigger(): void {
  triggerHeld = true;
  audio.arm();
}

function releaseTrigger(): void {
  triggerHeld = false;
}

function toggleSound(): void {
  soundOff.value = audio.toggleMuted();
}

/**
 * Whether this frame's command should carry a trigger pull.
 *
 * HELD IS NOT THE SAME AS ASKED, and the difference is bytes. A held trigger
 * with no filter would put `"f":true` on all forty commands a second, uplink, on
 * mobile data — where a gun that fires as fast as it can manages three shots in
 * that second. So the request is suppressed while THIS CLIENT'S OWN prediction
 * says the gun is busy, which it can do honestly because it is running the same
 * refusal rule the server would: sending a pull it already knows will be refused
 * is spending the worse half of the connection to be told no.
 *
 * The cost of being wrong is bounded and small. The client's timers come from
 * the snapshot twenty times a second, so the two ends can only disagree by the
 * millisecond the wire quantises to — and if the client is the pessimistic one,
 * the trigger is still held, so the pull goes out on the next command a
 * fortieth of a second later.
 *
 * A DRY GUN IS NOT BUSY, deliberately: pulling with nothing to load reaches the
 * server, because whether there is a bottle in your pockets is the server's to
 * say and a pickup may have landed since the last frame.
 */
function triggerWanted(): boolean {
  if (!triggerHeld || !predictor) return false;
  const gun = predictor.raw();
  return gun.cooldown <= 0 && gun.reload <= 0;
}

/**
 * Turns this frame's predicted gun into a flash, a kick and a bang.
 *
 * A PER-FRAME COMPARISON RATHER THAN AN EVENT, which is this project's rule for
 * exactly this: a barrel count falling by one IS the shot and a reload timer
 * leaving zero IS the reload starting, so neither has to be published to be
 * seen — on the wire the server makes the identical argument for not sending an
 * "I fired" field.
 *
 * IT READS THE PREDICTION AND NOT THE SNAPSHOT, which is the whole of why the
 * effect lands the instant a thumb does. The browser has already run the same
 * trigger rule the server is about to run, so what is marked is a shot that was
 * GRANTED — a pull refused inside the cadence changes nothing here and draws
 * nothing, which matters because holding the trigger down is mostly refusals.
 *
 * A shot is the count going DOWN. The server permits one command to finish a
 * reload and fire in the same breath, which would arrive here as the count going
 * up; this client cannot produce that command, because `triggerWanted` withholds
 * the pull for as long as its own prediction is still reloading. Anything that
 * changes there has to come back and look at this.
 */
function markGun(gun: { loaded: number; reload: number }): void {
  const reloadingNow = gun.reload > 0;
  if (gun.loaded < drawnLoaded) {
    scene.value?.fire();
    audio.shot();
  }
  if (reloadingNow && !drawnReloading) audio.reload();
  drawnLoaded = gun.loaded;
  drawnReloading = reloadingNow;
}

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
  window.addEventListener('mousedown', onMouseDown);
  window.addEventListener('mouseup', onMouseUp);
  document.addEventListener('pointerlockchange', onPointerLockChange);
});

onBeforeUnmount(() => {
  teardownPlay();
  window.removeEventListener('keydown', onKeyDown);
  window.removeEventListener('keyup', onKeyUp);
  window.removeEventListener('mousemove', onMouseMove);
  window.removeEventListener('mousedown', onMouseDown);
  window.removeEventListener('mouseup', onMouseUp);
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
  // Loaded and dry, exactly as the server's NewPlayer leaves somebody — and
  // overwritten by the first snapshot, which is a twentieth of a second away.
  loaded.value = config.value.gun.barrels;
  reloading.value = false;
  bag.value = {};
  onFloor.value = level.pickups.map((p) => p.id);
  floorMask = null;
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
      barrels: config.value.gun.barrels,
      fireCooldownSeconds: config.value.gun.fire_cooldown_seconds,
      reloadSeconds: config.value.gun.reload_seconds,
      reloadCost: config.value.gun.reload_cost,
    },
    start: {
      x: level.spawn.x,
      y: level.spawn.y,
      sector: level.spawn_sector,
      yaw: level.spawn_yaw,
    },
  });
  // Seeded from the prediction rather than from zero, or the first frame reads
  // a full gun appearing out of nothing as two shots and fires twice.
  drawnLoaded = predictor.raw().loaded;
  drawnReloading = false;
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
  // The standings describe the building that has just been thrown away. Keeping
  // them would list the people who were standing in it for as long as it took
  // the next board to arrive.
  board.value = [];
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
  // The place we were holding is given back the moment the socket goes, so it
  // is no longer ours to point an arrow at.
  mySlot.value = -1;
  // Handing the mouse back is not optional: leaving a page with the pointer
  // still captured strands the cursor on a screen that no longer uses it.
  if (pointerLocked.value) document.exitPointerLock?.();
  stickPointer = null;
  lookPointer = null;
  stick.value = { active: false, originX: 0, originY: 0, x: 0, y: 0 };
  heldKeys.clear();
  triggerHeld = false;
  // The audio hardware goes back with everything else. A context left open holds
  // a device for a game nobody is playing, and the mute survives it in `soundOff`
  // — the next context is built muted or not exactly as this one was left.
  audio.close();
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
    for (const cmd of emitter.due(now, axes, triggerWanted())) {
      // Applied locally the instant it exists, and queued for sending
      // unchanged. Predicting one thing and sending another is the one mistake
      // this whole arrangement cannot survive.
      outbox.push(predictor.apply(cmd));
    }
    // AFTER the commands and BEFORE anything else touches the prediction, which
    // is the ordering the comparison depends on: what is being compared is this
    // frame's gun against last frame's, and a value overwritten first is a shot
    // nobody hears.
    markGun(predictor.raw());
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
    case 'vanyadum_board':
      applyStandings(frame);
      break;
    case 'vanyadum_ready':
      // Our own place, and the only frame that ever says it. Read before the
      // building is checked, because a reconnect into the SAME building is
      // answered with a ready frame and nothing else — `adoptBuilding` then
      // correctly does nothing, and the slot would never be picked up.
      mySlot.value = typeof frame.slot === 'number' ? frame.slot : -1;
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
  // What the player is carrying, read here because two things need it: the HUD
  // draws the whole bag, and the predictor needs the one counter the trigger
  // spends. `c` is omitted for somebody carrying nothing, which is everybody for
  // their first minute.
  const carried = (frame.c as Record<string, number> | undefined) ?? {};

  // The authoritative position AND the authoritative gun, folded in rather than
  // assigned: the predictor drops what this acknowledges, rewinds to it, and
  // replays whatever is still pending on top.
  //
  // THE GUN'S TIMERS COME FROM HERE ON EVERY FRAME AND NOT ONLY ON THE FIRST,
  // which is ADR-058's sharp edge and the reason this game's own version of it
  // is worse than the office's. A predicted timer only advances when a command
  // is emitted, and a player who has just fired and is standing perfectly still
  // emits nothing at all — so a locally held cadence would stop dead exactly
  // when somebody stops walking to aim. The server keeps it running (world.go's
  // idle fill); this is where the client is told.
  predictor?.reconcile({
    x: num(frame.x) / 100,
    y: num(frame.y) / 100,
    sector: num(frame.s),
    ack: num(frame.ack),
    loaded: num(frame.b),
    // Milliseconds on the wire, seconds in the simulation. Both are absent at
    // rest, which is nearly always, and absent means zero.
    cooldown: num(frame.d) / 1000,
    reload: num(frame.r) / 1000,
    ammo: config.value ? num(carried[config.value.gun.ammo]) : 0,
  });

  // Peers go into the interpolation buffer stamped with the SERVER'S TICK, and
  // are read back a fixed delay later. The tick rather than `performance.now()`
  // because the tick is a perfect fixed-rate timeline where an arrival time is
  // the network's jitter wearing a clock's clothes — and because the server's lag
  // compensation rewinds to exactly `serverTick − delay`, so keying on anything
  // else has the two ends disagreeing about which instant was on screen. See
  // vanyadumInterp.
  //
  // A peer is addressed by a SLOT and its height is derived from the room the
  // frame named — see vanyadumRoster for both, and for why the height is
  // resolved here at ingest rather than when the figure is drawn.
  //
  // A slot the standings have not named yet is pushed exactly like any other.
  // The figure is drawn with nothing attached to it, which is what it means to
  // be safe about the one ordering hazard this arrangement has: the server
  // publishes the board ahead of the snapshot that first carries a new holder,
  // so this only happens if that board frame was dropped, and it heals within a
  // second. Dropping the peer instead would make somebody invisible; guessing a
  // name would put the wrong one on him.
  if (interp && world && config.value) {
    interp.push(
      decodePeers(frame.p, world.level, config.value.player.eye_height),
      tick,
      performance.now(),
    );
  }

  health.value = num(frame.hp);
  // The readouts, which are the SERVER'S and never the prediction's — a shell
  // count is a number somebody reads, and a number somebody reads must not be a
  // guess that is taken back a frame later. `b` is always sent, because a
  // resting gun is full rather than empty and an absent field would have to mean
  // the worst case.
  loaded.value = num(frame.b);
  reloading.value = num(frame.r) > 0;

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
  for (const [k, v] of Object.entries(carried)) {
    if (bag.value[k] !== v) bag.value = { ...bag.value, [k]: v };
  }
}

/**
 * The standings — who is in the building, how long they have been in it, and
 * what they are carrying.
 *
 * Once a second, and again on the tick anybody joins or leaves. It is full
 * state, so it REPLACES rather than merges: a row that is not in the newest
 * frame is somebody who has gone.
 *
 * IT IS ALSO THE SLOT DIRECTORY, and that second job is what the forgetting
 * below is for. A slot is a place and not a person — it is handed back when its
 * holder leaves and given to the next arrival — so a slot whose name has changed
 * has a different man standing in it, and everything the interpolation buffer
 * remembers about where that place was a moment ago belongs to the person who
 * left. Blended, that draws one man sliding across the building into another
 * man's position. Only the changed slots are dropped; resetting the whole buffer
 * would pause every other peer every time anybody walked in.
 */
function applyStandings(frame: RealtimeFrame): void {
  const next = decodeBoard(frame.b);
  for (const slot of changedHands(board.value, next)) interp?.forget(slot);
  board.value = next;
}

function onStatus(status: ConnectionStatus): void {
  link.value = status === 'open' ? 'open' : status === 'connecting' ? 'connecting' : 'lost';
  if (status === 'open') {
    // Every open, not just the first — see enterPlay. A second hello is a
    // reconnect rather than a second person: it refreshes the place we already
    // have instead of taking another one.
    if (world) realtimeClient(world.room).send({ t: 'vanyadum_hello' });
    return;
  }

  // THE LINK GOING AWAY VOIDS THE DIRECTORY, AND EVERYTHING KEYED BY IT.
  //
  // A slot is a place and not a person: it is handed back when its holder leaves
  // and given to the next arrival. That hand-over is only ever announced by a
  // standings frame, and a socket that is down carries no frames — so an outage
  // is precisely the window in which the mapping this client is holding can stop
  // being true without it hearing a word. Come back a few seconds later and slot
  // 1 may be somebody else entirely.
  //
  // Kept, that costs two different wrong things. The readout would go on
  // asserting that a man who has left is standing in the building, and nothing
  // about a frozen list tells its reader it is frozen — a screen naming human
  // beings is the last one to leave saying something it can no longer support.
  // And the interpolation buffer would blend the newcomer's first positions out
  // of the previous holder's last ones, drawing one man sliding across the
  // заброшка into another man's place: the snapshots resume the instant the
  // socket is back, and the board frame that would have said the place changed
  // hands is not guaranteed to have arrived first.
  //
  // So all three go, and the cost of dropping them is a fraction of a second of
  // saying less: the standings are republished on every hello, so a reconnect
  // refills the board almost as soon as it is open, and peers reappear on the
  // next snapshot. `reset` rather than `forget` per slot, because after an
  // outage EVERY slot is suspect — and because a long enough one can mean the
  // building was torn down and rebuilt, whose ticks start from zero again.
  board.value = [];
  mySlot.value = -1;
  interp?.reset();
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
  // Space is the trigger on a keyboard, and it MUST be swallowed: unhandled it
  // scrolls the page, which on a shell this tall means the game moving under the
  // player every time he fires. Repeat events are ignored — the trigger is
  // already held, and the auto-repeat rate has nothing to do with the cadence.
  if (e.code === FIRE_KEY) {
    e.preventDefault();
    if (!e.repeat) pullTrigger();
    return;
  }
  if (['ArrowLeft', 'ArrowRight'].includes(e.code)) return;
  if (e.code.startsWith('Key') || e.code.startsWith('Arrow')) e.preventDefault();
}

function onKeyUp(e: KeyboardEvent): void {
  heldKeys.delete(e.code);
  if (e.code === FIRE_KEY) releaseTrigger();
}

/**
 * The mouse's trigger, and it only exists while the pointer is captured.
 *
 * Before the capture a click means "grab the mouse", which is a different thing
 * and the one a player is trying to do — firing on it as well would mean every
 * desktop session opened with a shot nobody asked for. Bound on the WINDOW like
 * the movement, because a captured pointer has no cursor and so no element it is
 * meaningfully over.
 */
function onMouseDown(e: MouseEvent): void {
  if (!pointerLocked.value || phase.value !== 'playing' || e.button !== 0) return;
  pullTrigger();
}

function onMouseUp(e: MouseEvent): void {
  if (e.button === 0) releaseTrigger();
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

  /* THE TOP-OVERLAY WIDTH BUDGET, declared once because two overlays share it.
     The standings hug the right edge and the connection notice the left, at
     nearly the same height — so their widths are not two independent choices,
     they are one sum. Picked separately and by eye, 56vw and 38vw plus the two
     insets came to 358.4 px on a 360 px phone: correct, but by a pixel and a
     half, and by accident.

     So the link's cap is DERIVED from what the board reserves, and the gap
     below is what must stay clear between them. Neither can be widened into the
     other, at any viewport width, without editing this block — which is the
     point of it being a block. */
  --dum-board-x: 8px;
  --dum-board-w: 56vw;
  --dum-link-x: 12px;
  --dum-overlay-gap: 12px;
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

/* Pushed to the far end, so what is true of the BUILDING sits away from what is
   true of you. */
.dum-hud-right {
  margin-left: auto;
  opacity: 0.75;
  font-weight: 400;
}

/* The standings, under the HUD strip and against the right edge — clear of the
   stick, which appears wherever a left thumb lands, and clear of the fire
   button at the bottom.

   Sized in vw rather than in pixels, and the name column is what gives: a
   twelve-character pseudonym is ellipsised on a 360 px screen rather than
   pushing the clock and the bag off the side. `pointer-events: none` because it
   is a readout — a tap on it belongs to the game underneath.

   Its inset and its cap come from the shared overlay budget on `.dum-play`,
   because the notice on the other side is sized against what this reserves. */
.dum-board {
  position: absolute;
  top: 32px;
  right: var(--dum-board-x);
  max-width: var(--dum-board-w);
  padding: 4px 7px;
  border-radius: 8px;
  background: rgba(0, 0, 0, 0.42);
  color: rgba(255, 255, 255, 0.82);
  font-size: 0.72rem;
  line-height: 1.35;
  font-variant-numeric: tabular-nums;
  text-shadow: 0 1px 3px rgba(0, 0, 0, 0.9);
  pointer-events: none;
}

.dum-board-head {
  font-weight: 700;
  opacity: 0.7;
  font-size: 0.68rem;
  white-space: nowrap;
}

.dum-board-rows {
  margin: 0;
  padding: 0;
  list-style: none;
}

.dum-board-row {
  display: flex;
  align-items: baseline;
  gap: 6px;
}

.dum-board-row.is-me {
  color: #ffd28a;
  font-weight: 700;
}

.dum-board-name {
  flex: 1 1 auto;
  min-width: 0;
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
}

.dum-board-time,
.dum-board-bag {
  flex: 0 0 auto;
  white-space: nowrap;
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

/* Capped so it wraps rather than running under the standings on the other side
   of the screen — two short lines beat one that collides. The cap is what is
   LEFT once the board's column and both insets are taken out of the viewport,
   less the gap that must stay clear, so the two cannot meet however narrow the
   phone is. See the budget on `.dum-play`.

   They are not on screen together today: the link leaving `open` empties the
   standings, because a directory nothing is refreshing stops being true (see
   `onStatus`). The budget holds anyway — a notice that only appears when
   something has already gone wrong is the last place to want a layout that
   depends on the other overlay being absent. */
.dum-link {
  position: absolute;
  top: 36px;
  left: var(--dum-link-x);
  max-width: calc(
    100vw - var(--dum-board-x) - var(--dum-board-w) - var(--dum-link-x) - var(--dum-overlay-gap)
  );
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
  /* The one gesture on this screen that must not also scroll or select. */
  touch-action: none;
  -webkit-user-select: none;
  user-select: none;
}

/* Held reads as held. The trigger is a hold rather than a tap, so a control
   that looked identical pressed and released would leave the player unable to
   tell whether his thumb was still on it — and this is a colour change rather
   than a transform, so it costs nothing under reduced motion and is still
   there. */
.dum-fire:active {
  background: rgba(226, 87, 76, 0.65);
  border-color: rgba(255, 255, 255, 0.55);
}

/* Beside the trigger rather than under a menu: the only sound this game makes is
   a shotgun, so the person who wants it off wants it off NOW and with the thumb
   already down there. Sized to the 44 px minimum and left of the fire button,
   with a gap wide enough that neither is hit by accident. */
.dum-mute {
  position: absolute;
  right: 100px;
  bottom: 38px;
  width: 44px;
  height: 44px;
  border-radius: 50%;
  border: 2px solid rgba(255, 255, 255, 0.22);
  background: rgba(0, 0, 0, 0.45);
  color: rgba(255, 255, 255, 0.9);
  font-size: 18px;
  line-height: 1;
  touch-action: none;
}

@media (prefers-reduced-motion: reduce) {
  .dum-stick-knob {
    transition: none;
  }
}
</style>
