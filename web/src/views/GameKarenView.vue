<template>
  <div class="karen-root">
    <!-- SPLASH — the rules cheatsheet, generated from the served catalogue. -->
    <section v-if="phase === 'splash'" class="karen-splash" data-testid="karen-splash">
      <h1 class="karen-title">{{ config?.title || 'СИМУЛЯТОР КАРЕНА' }}</h1>
      <p class="karen-lore">{{ KAREN_LORE }}</p>

      <div v-if="loading" class="karen-loading">
        <v-progress-circular indeterminate size="28" />
      </div>

      <template v-else>
        <div class="karen-rules" data-testid="karen-rules">
          <section
            v-for="block in rules"
            :key="block.title"
            class="karen-rule-block"
            data-testid="karen-rule-block"
          >
            <h2 class="karen-rule-title">{{ block.title }}</h2>
            <dl class="karen-rule-list">
              <template v-for="line in block.lines" :key="line.label">
                <dt>{{ line.label }}</dt>
                <dd>{{ line.text }}</dd>
              </template>
            </dl>
          </section>
        </div>

        <div v-if="myShifts.length" class="karen-list" data-testid="karen-runs">
          <h2 class="karen-rule-title">Твои смены</h2>
          <ul>
            <li v-for="(shift, i) in myShifts" :key="i">
              {{ causeIcon(shift.cause) }} {{ money(shift.salary) }} · {{ seconds(shift.seconds) }} с
            </li>
          </ul>
        </div>

        <div v-if="topShifts.length" class="karen-list" data-testid="karen-top">
          <h2 class="karen-rule-title">Кто больше не работал</h2>
          <ol>
            <li v-for="(shift, i) in topShifts" :key="i">
              {{ shift.name }} — {{ money(shift.salary) }}
            </li>
          </ol>
        </div>

        <v-btn
          class="karen-start"
          color="warning"
          size="large"
          data-testid="karen-start"
          :loading="starting"
          @click="start"
        >
          НАЧАТЬ СМЕНУ
        </v-btn>
      </template>

      <p v-if="error" class="karen-error" data-testid="karen-error">{{ error }}</p>
    </section>

    <!-- PLAYING — a fixed-height column: readouts, the office, the controls.
         Nothing here scrolls, and nothing stands where a control takes the tap. -->
    <section v-else-if="phase === 'playing'" class="karen-play" data-testid="karen-play">
      <div class="karen-hud">
        <span class="karen-hud-cell" data-testid="karen-hud-money">
          <span class="karen-hud-label">ЗАРПЛАТА</span>
          <span class="karen-hud-value">{{ money(pay) }}</span>
        </span>
        <span class="karen-hud-cell karen-hud-mult" data-testid="karen-hud-mult">
          {{ formatMultiplier(mult) }}
        </span>
        <span class="karen-hud-cell karen-hud-dash">
          {{ dashMs > 0 ? `РЫВОК ${formatSeconds(dashMs)}` : 'РЫВОК ГОТОВ' }}
        </span>
        <button class="karen-quit" type="button" data-testid="karen-quit" @click="quit">
          УЙТИ
        </button>
      </div>

      <div class="karen-streak" data-testid="karen-hud-streak">
        <span class="karen-streak-fill" :style="{ '--fill': String(ramp) }" />
      </div>

      <p v-if="link !== 'open'" class="karen-link" data-testid="karen-link">
        {{ link === 'connecting' ? 'связь…' : 'связь потеряна, ждём…' }}
      </p>

      <div class="karen-stage" :style="{ '--ratio': String(ratio) }">
        <div class="karen-plane" data-testid="karen-plane">
          <span
            v-for="(desk, i) in desks"
            :key="i"
            class="karen-desk"
            data-testid="karen-desk"
            :style="deskStyle(desk)"
          />
          <span ref="bossEl" class="karen-boss" data-testid="karen-boss" aria-hidden="true">
            <span class="karen-fig-body" />
            <span class="karen-fig-head">
              <span class="karen-fig-shine" />
              <span class="karen-boss-grin" />
            </span>
          </span>
          <span ref="meEl" class="karen-me" data-testid="karen-me" aria-hidden="true">
            <span class="karen-fig-body" />
            <span class="karen-fig-head"><span class="karen-fig-hair" /></span>
          </span>
        </div>
      </div>

      <div class="karen-controls">
        <div
          ref="stickEl"
          class="karen-stick"
          data-testid="karen-stick"
          role="application"
          aria-label="Идти"
          @pointerdown="onStickDown"
          @pointermove="onStickMove"
          @pointerup="onStickUp"
          @pointercancel="onStickUp"
        >
          <span class="karen-stick-knob" :style="knobStyle" />
        </div>

        <button
          class="karen-dash"
          type="button"
          data-testid="karen-dash"
          :disabled="dashMs > 0"
          @pointerdown.prevent="onDash"
          @click.prevent
        >
          РЫВОК
        </button>
      </div>
    </section>

    <!-- OVER — the ending the CATALOGUE names, the money, and the way back in. -->
    <section v-else class="karen-splash" data-testid="karen-over">
      <h1 class="karen-title" data-testid="karen-over-title">{{ overTitle }}</h1>
      <p class="karen-lore">{{ overSub }}</p>
      <p class="karen-over-salary" data-testid="karen-over-salary">{{ money(over?.pay ?? 0) }}</p>
      <p class="karen-over-secs">за {{ seconds(over?.secs ?? 0) }} с</p>
      <v-btn color="warning" size="large" data-testid="karen-retry" :loading="starting" @click="start">
        ЕЩЁ РАЗ
      </v-btn>
      <v-btn variant="text" size="large" class="karen-back" @click="backToSplash">НАЗАД</v-btn>
      <p v-if="error" class="karen-error">{{ error }}</p>
    </section>
  </div>
</template>

<script setup lang="ts">
/**
 * «СИМУЛЯТОР КАРЕНА» — the fourth game, and the second the server simulates.
 *
 * WHAT IS DRAWN, AND HOW. The office is DOM and CSS: a plane with desks on it
 * and two figures placed by CSS custom properties. That is this game's own
 * rendering decision (ADR-028 lets each game make one) and it is the yard's
 * technique rather than «ВАНЯДУМ»'s — a flat plan view of a room is what a
 * stylesheet is good at, and it keeps every readout assertable, which nothing
 * inside a canvas is.
 *
 * WHERE THE TRUTH LIVES. The server simulates; this view draws. It sends the
 * axes the thumb is pushing and never a position, never a salary, and never a
 * claim that anything happened.
 *
 * TWO CLOCKS, ON PURPOSE:
 *
 *   * requestAnimationFrame DRAWS, as fast as the phone will, from the PREDICTED
 *     position — the client runs the same `step` the server does, so movement
 *     responds instantly and updates at frame rate rather than at the ten hertz
 *     snapshots arrive at.
 *   * a timer at the served `input_hz` SENDS, because the socket allows ten
 *     frames a second and that is a bound this game fits inside rather than
 *     loosens.
 *
 * AND A SAMPLE WHERE NOTHING HAPPENED IS NOT SENT. Standing perfectly still is
 * the whole point of this game, and it costs the network nothing: the emitter
 * produces no command, so no frame goes out, and the salary climbs because the
 * SERVER is advancing the shift. A client that had to keep talking in order to
 * be paid would be the exact defect «ВАНЯДУМ» shipped once.
 *
 * WHAT IS PREDICTED. Position, and the dash timers that decide how fast it
 * moves. The money is NOT predicted — the salary, the multiplier and the streak
 * are read straight off the snapshot, because they are the score, and a score
 * that flickered between a guess and the truth would be worse than one that is
 * simply ten hertz.
 */

import { computed, onBeforeUnmount, onMounted, ref } from 'vue';
import { gameKarenApi } from '../api/endpoints';
import { ApiError } from '../api/client';
import type { KarenConfig, KarenRect, KarenShift, KarenShiftRow, KarenTopRow } from '../api/types';
import { KAREN_LORE, buildRules, endingFor } from '../lib/karenRules';
import {
  applyBoss,
  applyFigure,
  decimal,
  deskBox,
  formatMoney,
  formatMultiplier,
  formatSeconds,
  grinState,
  rampFraction,
  toPlane,
} from '../lib/karenPlane';
import { MAX_STEP_SECONDS, stepConstants } from '../lib/karenStep';
import {
  REDUNDANT_COMMANDS,
  buildInputFrame,
  createEmitter,
  dashAxes,
  createPredictor,
  stickVector,
  type Emitter,
  type KarenAxes,
  type Predictor,
} from '../lib/karenPredict';
import type { StepCommand, StepConstants } from '../lib/karenStep';
import { createInterpolator, type Interpolator } from '../lib/karenInterp';
import { realtimeClient, type ConnectionStatus, type RealtimeFrame } from '../realtime/socket';

type Phase = 'splash' | 'playing' | 'over';

const phase = ref<Phase>('splash');
const loading = ref(true);
const starting = ref(false);
const error = ref('');

const config = ref<KarenConfig | null>(null);
const myShifts = ref<KarenShiftRow[]>([]);
const topShifts = ref<KarenTopRow[]>([]);
const rules = computed(() => buildRules(config.value));
const desks = computed<KarenRect[]>(() => config.value?.office.desks ?? []);
const ratio = computed(() => {
  const o = config.value?.office;
  return o && o.w > 0 && o.h > 0 ? o.w / o.h : 0.75;
});

// --- what the server last told us ------------------------------------------
// Read at ten hertz and rendered as text, which is exactly what reactivity is
// for. Positions are the opposite and never come near it — see drawFrame.
const pay = ref(0);
const mult = ref(100);
const ramp = ref(0);
const dashMs = ref(0);
const link = ref<'connecting' | 'open' | 'lost'>('connecting');
const over = ref<{ cause: string; pay: number; secs: number } | null>(null);

const overTitle = computed(() => endingFor(config.value, over.value?.cause ?? '')?.title ?? 'СМЕНА ОКОНЧЕНА');
const overSub = computed(() => endingFor(config.value, over.value?.cause ?? '')?.sub ?? '');

const meEl = ref<HTMLElement | null>(null);
const bossEl = ref<HTMLElement | null>(null);
const stickEl = ref<HTMLElement | null>(null);

let shift: KarenShift | null = null;
let constants: StepConstants | null = null;
let predictor: Predictor | null = null;
let emitter: Emitter | null = null;
/** Commands applied locally and not yet sent. */
let outbox: StepCommand[] = [];
let release: (() => void) | null = null;
let sendTimer: number | undefined;
let frameHandle = 0;
let lastFrameMs = 0;
/** The last snapshot tick we drew, echoed so the server can derive our latency. */
let seenTick = 0;
/** Wall clock at clock-in, so walking out can say how long the shift was. */
let startedAtMs = 0;
/** A dash asked for and not yet carried by a command. Cleared when one takes it. */
let dashPending = false;
// The last way the thumb actually went, and where the лысый was on the last
// snapshot. Both exist only to give a dash a direction when the stick is
// neutral — which, in this game, is most of the time. See dashAxes.
let lastDir: { mx: number; my: number } | null = null;
let bossAt: { x: number; y: number } | null = null;
// The лысый is INTERPOLATED, not predicted: his intent is not ours to guess, so
// he is drawn in the recent past between two samples that have both already
// arrived. See karenInterp.
let bossInterp: Interpolator | null = null;
/** The grin state currently written on the boss, so the class is not rewritten. */
let bossGrin = '';

// --- the stick --------------------------------------------------------------
// Fixed rather than floating: it is the only control on the left of the screen,
// it is always in the same place, and a thumb that has found it once never has
// to look again. «ВАНЯДУМ» puts its stick wherever the thumb lands because the
// whole screen is the world there; here the world is a plane above the controls,
// so there is a proper place to put one.
const knob = ref({ dx: 0, dy: 0 });
const STICK_RADIUS = 52;
let stickPointer: number | null = null;
let stickOrigin = { x: 0, y: 0 };
let axes: KarenAxes = { mx: 0, my: 0 };

const knobStyle = computed(() => ({
  transform: `translate3d(${knob.value.dx}px, ${knob.value.dy}px, 0)`,
}));

const money = (v: number) => formatMoney(v);
/**
 * A shift's length, whole.
 *
 * The API serves `seconds` as a `double precision` column — a shift really did
 * last 4.7168 seconds — and printing that verbatim would put the simulation's
 * arithmetic on the splash screen. Nobody is racing to the millisecond.
 */
const seconds = (v: number) => decimal(v, 0);

/** How a finished shift is marked in the list. Presentation, not a rule. */
function causeIcon(cause: string): string {
  return cause === 'promoted' ? '🎉' : '🚪';
}

function deskStyle(d: KarenRect): Record<string, string> {
  const o = config.value?.office;
  const box = deskBox(d, o?.w ?? 0, o?.h ?? 0);
  return {
    left: `${box.left * 100}%`,
    top: `${box.top * 100}%`,
    width: `${box.width * 100}%`,
    height: `${box.height * 100}%`,
  };
}

// --- lifecycle -------------------------------------------------------------

onMounted(async () => {
  // The catalogue is fetched here because the SPLASH is built from it. Nothing
  // else happens until the button: POST /shifts puts you in the office, and
  // connecting on mount would clock you in while you were still reading.
  try {
    config.value = await gameKarenApi.config();
  } catch (e) {
    error.value = e instanceof ApiError ? `не открылось (${e.code})` : 'не открылось';
  }
  await loadLists();

  // A shift may already be going — a reload, or the game open on another tab.
  // The office outlives a dropped socket by design, so pick it back up rather
  // than stranding the player behind a button that answers 409.
  try {
    shift = await gameKarenApi.current();
    enterPlay();
  } catch {
    // 404 is the ordinary answer. Nothing to resume.
  }
  loading.value = false;
});

onBeforeUnmount(teardownPlay);

async function loadLists(): Promise<void> {
  const [mine, top] = await Promise.allSettled([gameKarenApi.myShifts(), gameKarenApi.topShifts()]);
  myShifts.value = mine.status === 'fulfilled' ? mine.value.shifts : [];
  topShifts.value = top.status === 'fulfilled' ? top.value.shifts : [];
}

async function start(): Promise<void> {
  if (starting.value) return;
  starting.value = true;
  error.value = '';
  try {
    shift = await gameKarenApi.start();
    enterPlay();
  } catch (e) {
    if (e instanceof ApiError && e.code === 'shift_in_progress') {
      // Somebody's other tab. Resuming is the right answer, not a second shift.
      try {
        shift = await gameKarenApi.current();
        enterPlay();
      } catch {
        error.value = 'смена уже идёт на другой вкладке';
      }
    } else if (e instanceof ApiError && e.code === 'office_full') {
      error.value = 'в офисе мест нет, подожди';
    } else {
      error.value = e instanceof ApiError ? `не вышло (${e.code})` : 'не вышло';
    }
  } finally {
    starting.value = false;
  }
}

function backToSplash(): void {
  over.value = null;
  phase.value = 'splash';
  void loadLists();
}

/**
 * Walks out.
 *
 * The ending is shown from what this client already knows — it is the one that
 * did the walking, so the CAUSE is not in doubt — while the TITLE and the SUB
 * still come from the catalogue via `endingFor`. The salary is the last
 * snapshot's, so it can be up to a tenth of a second stale; the row Postgres
 * gets is the server's own number, and that is the one the list will show.
 */
async function quit(): Promise<void> {
  const secs = startedAtMs ? Math.max(0, Math.round((Date.now() - startedAtMs) / 1000)) : 0;
  const salary = pay.value;
  try {
    await gameKarenApi.leave();
  } catch {
    // Best effort: an occupant with no connection is ended and written after the
    // server's own grace period anyway, so a failed DELETE costs a minute of an
    // idle simulation and nothing else.
  }
  finish({ cause: 'left', pay: salary, secs });
}

/** Opens the socket and starts both clocks. The predictor waits for a snapshot. */
function enterPlay(): void {
  if (!shift || !config.value) return;
  phase.value = 'playing';
  over.value = null;
  error.value = '';
  pay.value = 0;
  mult.value = 100;
  ramp.value = 0;
  dashMs.value = 0;
  link.value = 'connecting';
  seenTick = 0;
  outbox = [];
  dashPending = false;
  bossGrin = '';
  startedAtMs = Date.now();

  constants = stepConstants(config.value);
  // NO PREDICTOR YET, AND THAT IS DELIBERATE. The catalogue does not publish
  // where a shift starts — the office is static but the spawn is the server's —
  // so there is nothing honest to seed one with. The first snapshot carries an
  // authoritative position, and the predictor is built from it there; until then
  // the figures sit in the middle of the plane and the link line says «связь…».
  predictor = null;
  emitter = createEmitter({
    // The send rate times the commands one frame may carry: a window then holds
    // exactly what it is allowed to, whatever the phone's frame rate is.
    hz: config.value.move.input_hz * config.value.move.max_commands,
    maxStepSeconds: MAX_STEP_SECONDS,
    maxPerWake: config.value.move.max_commands,
    idleThreshold: constants.idleThreshold,
  });

  // The room comes from the shift, never from a string in this file: the room
  // name lives in the game's own package on the server and travels with the
  // shift, which is what keeps `realtime` from knowing this game exists.
  const client = realtimeClient(shift.room);
  release = client.subscribe({ frames: onFrame, status: onStatus });

  bossInterp = createInterpolator(1000 / (config.value?.sim.snapshot_hz || 10));
  sendTimer = window.setInterval(sendInput, Math.round(1000 / config.value.move.input_hz));
  lastFrameMs = performance.now();
  frameHandle = requestAnimationFrame(drawFrame);
}

function teardownPlay(): void {
  if (sendTimer !== undefined) window.clearInterval(sendTimer);
  sendTimer = undefined;
  if (frameHandle) cancelAnimationFrame(frameHandle);
  frameHandle = 0;
  release?.();
  release = null;
  emitter?.reset();
  emitter = null;
  predictor = null;
  constants = null;
  outbox = [];
  stickPointer = null;
  axes = { mx: 0, my: 0 };
  knob.value = { dx: 0, dy: 0 };
  dashPending = false;
  lastDir = null;
  bossAt = null;
  bossInterp = null;
}

// --- the two clocks --------------------------------------------------------

/** The draw clock. Emits and predicts input, decays any correction, and places. */
function drawFrame(now: number): void {
  const dt = Math.min(0.1, (now - lastFrameMs) / 1000);
  lastFrameMs = now;

  if (predictor && emitter) {
    const idle = constants ? constants.idleThreshold : 0.05;
    if (Math.hypot(axes.mx, axes.my) > idle) lastDir = { mx: axes.mx, my: axes.my };
    const me = predictor.view();
    const away = bossAt ? { dx: me.x - bossAt.x, dy: me.y - bossAt.y } : null;
    // The last argument is whether a dash is STILL RUNNING, which is not the
    // same question as whether one has been asked for: a dash tapped from a
    // standstill leaves the stick neutral, so it is the only thing that keeps
    // the emitter — and therefore the prediction — alive for the rest of the
    // burst. See createEmitter.due.
    const due = emitter.due(now, axes, dashPending, dashAxes(axes, lastDir, away, idle), predictor.dashing());
    for (const cmd of due) {
      // Applied locally the instant it exists, and queued for sending unchanged.
      // Predicting one thing and sending another is the one mistake this whole
      // arrangement cannot survive.
      outbox.push(predictor.apply(cmd));
    }
    // Cleared only once a command actually carried it, so a tap during a frame
    // that produced none is not silently lost.
    if (due.some((c) => c.dash)) dashPending = false;
    predictor.tick(dt);
    placeMe();
    placeBoss(now);
  }

  frameHandle = requestAnimationFrame(drawFrame);
}

/** The send clock. One frame per window, carrying everything sampled in it. */
function sendInput(): void {
  if (!shift || !predictor) return;
  const fresh = outbox;
  outbox = [];
  // An empty frame still spends one of the socket's ten a second, so silence is
  // the right answer when nothing happened — which, in this game, is most of the
  // time and on purpose.
  if (fresh.length === 0) return;

  // Redundancy: the tail of everything still unacknowledged rides along, so one
  // lost packet costs no input at all. The server drops any sequence it has
  // already applied, which is what makes a duplicate free.
  const frame = buildInputFrame(
    seenTick,
    fresh,
    predictor.unacknowledged(REDUNDANT_COMMANDS + fresh.length),
  );
  realtimeClient(shift.room).send({ ...frame });
}

// --- placing ---------------------------------------------------------------

// The лысый, drawn from the interpolation buffer every animation frame rather
// than written once per snapshot and smoothed by CSS. That is the third
// Gambetta rung, and the reason it is worth a module is in karenInterp.
function placeBoss(now: number): void {
  const el = bossEl.value;
  if (!el || !bossInterp || !constants) return;
  const at = bossInterp.at(now);
  // Nothing has arrived yet — draw nothing rather than guess a position and
  // then snap it.
  if (!at) return;
  applyBoss(el, toPlane(at.x, at.y, constants.officeW, constants.officeH), at.grin);
  const state = grinState(at.grin);
  if (state !== bossGrin) {
    bossGrin = state;
    el.dataset.grin = state;
  }
}

function placeMe(): void {
  const el = meEl.value;
  if (!el || !predictor || !constants) return;
  // Carried over the time that has passed since the last command but has not yet
  // become one — see viewAhead. Without it the figure advances at the command
  // rate rather than the frame rate, which is visible at a walk and is most of
  // what a dash is made of.
  const v = predictor.viewAhead(emitter ? emitter.residualSeconds() : 0, axes);
  applyFigure(el, toPlane(v.x, v.y, constants.officeW, constants.officeH));
}

// --- the socket ------------------------------------------------------------

function onFrame(frame: RealtimeFrame): void {
  switch (frame.t) {
    case 'karen_ready':
      // The office has us. Until this lands the socket may be open while the
      // occupant is not yet attached, which is a different thing and is what the
      // link line is honestly reporting.
      link.value = 'open';
      break;
    case 'karen_snap':
      applySnapshot(frame);
      break;
    case 'karen_over':
      finish({
        cause: typeof frame.cause === 'string' ? frame.cause : 'left',
        pay: num(frame.pay),
        secs: num(frame.secs),
      });
      break;
    default:
      // Unknown `t` is ignored, which is what lets either end learn a message
      // type without a coordinated deploy.
      break;
  }
}

function applySnapshot(frame: RealtimeFrame): void {
  if (!constants) return;
  // Positions arrive as centimetres, money as whole roubles, the multiplier as
  // hundredths and both timers as milliseconds — integers, because this frame
  // repeats ten times a second for as long as somebody is playing.
  seenTick = num(frame.k);
  link.value = 'open';

  const x = num(frame.x) / 100;
  const y = num(frame.y) / 100;
  const ack = num(frame.ack);
  // Omitted when the dash is ready, so an absent field means zero rather than
  // "unchanged" — reading it any other way would leave the button dead forever.
  dashMs.value = num(frame.dc);

  if (!predictor) {
    // The first authoritative position is what the predictor is seeded with —
    // see enterPlay for why there is nothing better to start from.
    predictor = createPredictor({ desks: desks.value, constants, start: { x, y } });
  }
  // The cooldown is folded in beside the position, because the client SIMULATES
  // from it: `step` refuses a dash while it is running, and a client whose own
  // copy had gone stale would refuse the dash the server was about to grant.
  predictor.reconcile({ x, y, ack, dashCooldown: dashMs.value / 1000 });

  pay.value = num(frame.pay);
  mult.value = num(frame.m);
  ramp.value = rampFraction(num(frame.st), config.value?.money.ramp_seconds ?? 0);

  const b = frame.b as Record<string, number> | undefined;
  const el = bossEl.value;
  if (b && el) {
    const grin = num(b.g) / 255;
    bossAt = { x: num(b.x) / 100, y: num(b.y) / 100 };
    // Buffered rather than drawn. The render loop reads him back out a beat
    // later, between two samples it already holds, which is what makes jitter
    // and a dropped frame cost nothing.
    bossInterp?.push({ x: bossAt.x, y: bossAt.y, grin }, performance.now());
  }
}

function finish(result: { cause: string; pay: number; secs: number }): void {
  over.value = result;
  teardownPlay();
  shift = null;
  phase.value = 'over';
  void loadLists();
}

function onStatus(status: ConnectionStatus): void {
  if (status === 'open') {
    // Every open, not just the first. `send` DROPS anything written before the
    // socket is OPEN rather than queueing it, so a hello sent at subscribe time
    // is silently discarded — and an office outlives a dropped socket, so a
    // reconnecting client has to say hello again to be re-attached to the shift
    // it is already in.
    if (shift) realtimeClient(shift.room).send({ t: 'karen_hello' });
    // Still 'connecting' until karen_ready: an open socket is not yet a desk.
    if (link.value === 'lost') link.value = 'connecting';
  } else {
    link.value = status === 'connecting' ? 'connecting' : 'lost';
  }
}

function num(v: unknown): number {
  return typeof v === 'number' && Number.isFinite(v) ? v : 0;
}

// --- the controls ----------------------------------------------------------

function onStickDown(e: PointerEvent): void {
  const el = stickEl.value;
  if (!el || stickPointer !== null) return;
  stickPointer = e.pointerId;
  const box = el.getBoundingClientRect();
  stickOrigin = { x: box.left + box.width / 2, y: box.top + box.height / 2 };
  moveStick(e.clientX, e.clientY);
  // Capture LAST, and forgivingly. It is what keeps a thumb that slides off the
  // ring still steering, but it throws for a pointer the browser does not think
  // is active — which a synthetic event is — and losing the whole gesture to
  // that would be trading a real control for a nicety.
  try {
    el.setPointerCapture?.(e.pointerId);
  } catch {
    /* not capturable; the drag still works, it just ends at the ring's edge */
  }
}

function onStickMove(e: PointerEvent): void {
  if (e.pointerId !== stickPointer) return;
  moveStick(e.clientX, e.clientY);
}

function onStickUp(e: PointerEvent): void {
  if (e.pointerId !== stickPointer) return;
  stickPointer = null;
  axes = { mx: 0, my: 0 };
  knob.value = { dx: 0, dy: 0 };
}

function moveStick(clientX: number, clientY: number): void {
  axes = stickVector(stickOrigin, { x: clientX, y: clientY }, STICK_RADIUS);
  const dx = clientX - stickOrigin.x;
  const dy = clientY - stickOrigin.y;
  const mag = Math.hypot(dx, dy);
  const scale = mag > STICK_RADIUS ? STICK_RADIUS / mag : 1;
  knob.value = { dx: dx * scale, dy: dy * scale };
}

/**
 * Asks for a dash.
 *
 * The flag is set here and consumed by the emitter, rather than a command being
 * built on the spot: a command built outside the emitter would be predicted at
 * a moment the fixed rate did not produce, and the client's simulated time would
 * drift ahead of the server's by exactly one sub-step per tap.
 */
function onDash(): void {
  if (dashMs.value > 0) return;
  dashPending = true;
}
</script>

<style scoped>
/* Height = visible viewport minus the app bar, with the same 72px the other two
   games use — the empirical value that survived the bug where mobile chrome
   sliding in produced a permanent scrollbar. */
.karen-root {
  height: calc(100dvh - 72px);
  overflow: hidden;
  position: relative;
}

/* --- splash and ending ---------------------------------------------------- */

.karen-splash {
  height: 100%;
  overflow-y: auto;
  padding: 16px;
  display: flex;
  flex-direction: column;
  align-items: center;
  gap: 12px;
  text-align: center;
  background: linear-gradient(170deg, #16181d, #1e1a12);
  color: rgba(255, 255, 255, 0.94);
}

.karen-title {
  font-size: clamp(24px, 7.4vw, 40px);
  font-weight: 900;
  letter-spacing: 0.06em;
  margin: 0;
  color: #f0b429;
  text-shadow: 0 2px 0 rgba(0, 0, 0, 0.6);
  /* The name is long and the phone is 360px. Breaking is better than overflowing,
     and both are better than a smaller title. */
  overflow-wrap: anywhere;
}

.karen-lore {
  margin: 0;
  max-width: 34rem;
  font-size: 0.95rem;
  line-height: 1.45;
  opacity: 0.85;
}

.karen-rules,
.karen-list {
  width: 100%;
  max-width: 34rem;
  text-align: left;
}

.karen-rules {
  display: flex;
  flex-direction: column;
  gap: 12px;
}

.karen-rule-title {
  font-size: 0.8rem;
  text-transform: uppercase;
  letter-spacing: 0.12em;
  opacity: 0.6;
  margin: 0 0 4px;
}

.karen-rule-list {
  margin: 0;
  display: grid;
  grid-template-columns: minmax(6.5rem, auto) 1fr;
  gap: 4px 10px;
  font-size: 0.86rem;
  line-height: 1.35;
}

.karen-rule-list dt {
  font-weight: 700;
  /* NOT `nowrap`: these labels are Russian words rather than «ВАНЯДУМ»'s two-
     character icons, and at 360px an unbreakable «👨‍🦲 скорость» pushes the grid
     past the viewport. */
  overflow-wrap: anywhere;
}

.karen-rule-list dd {
  margin: 0;
  opacity: 0.85;
  overflow-wrap: anywhere;
}

.karen-list ul,
.karen-list ol {
  margin: 0;
  padding-left: 1.2rem;
  font-size: 0.86rem;
  line-height: 1.5;
}

.karen-list li {
  overflow-wrap: anywhere;
}

.karen-start,
.karen-back {
  min-height: 48px;
  font-weight: 900;
  letter-spacing: 0.06em;
}

.karen-over-salary {
  margin: 0;
  font-size: clamp(28px, 10vw, 44px);
  font-weight: 900;
  font-variant-numeric: tabular-nums;
  color: #f0b429;
}

.karen-over-secs {
  margin: 0;
  opacity: 0.7;
}

.karen-error {
  font-size: 0.85rem;
  color: #ffb4a8;
  max-width: 30rem;
}

.karen-loading {
  padding: 24px;
}

/* --- playing --------------------------------------------------------------
   A FIXED-HEIGHT COLUMN, not a growing document. The shell must not scroll while
   somebody is standing still watching a multiplier climb — a page that scrolls
   under a thumb resting on a stick is a page that moves the stick. */

.karen-play {
  position: absolute;
  inset: 0;
  display: flex;
  flex-direction: column;
  overflow: hidden;
  background: #16181d;
  color: rgba(255, 255, 255, 0.94);
}

.karen-hud {
  flex: 0 0 auto;
  display: flex;
  align-items: center;
  gap: 8px;
  padding: 6px 10px;
  font-variant-numeric: tabular-nums;
  min-width: 0;
}

.karen-hud-cell {
  display: flex;
  flex-direction: column;
  line-height: 1.15;
  min-width: 0;
}

.karen-hud-label {
  font-size: 0.62rem;
  letter-spacing: 0.1em;
  opacity: 0.55;
}

.karen-hud-value {
  font-size: 1.05rem;
  font-weight: 800;
  color: #f0b429;
  white-space: nowrap;
}

.karen-hud-mult {
  font-size: 1.05rem;
  font-weight: 800;
  white-space: nowrap;
}

.karen-hud-dash {
  margin-left: auto;
  font-size: 0.66rem;
  letter-spacing: 0.06em;
  opacity: 0.72;
  text-align: right;
  white-space: nowrap;
}

/* The way out is a real target and sits at the top, clear of both thumbs — the
   yard shipped a control standing where another one takes the tap once. */
.karen-quit {
  flex: 0 0 auto;
  min-width: 56px;
  min-height: 44px;
  padding: 0 10px;
  border-radius: 10px;
  background: rgba(255, 255, 255, 0.08);
  color: rgba(255, 255, 255, 0.85);
  font-size: 0.74rem;
  font-weight: 700;
  letter-spacing: 0.06em;
}

/* The ramp, drawn rather than written: how close the multiplier is to its cap is
   the one number a player watches continuously, and a bar says it without being
   read. */
.karen-streak {
  flex: 0 0 auto;
  height: 6px;
  margin: 0 10px 4px;
  border-radius: 3px;
  background: rgba(255, 255, 255, 0.1);
  overflow: hidden;
}

.karen-streak-fill {
  display: block;
  height: 100%;
  width: 100%;
  border-radius: 3px;
  background: linear-gradient(90deg, #6b8f3a, #f0b429);
  transform-origin: 0 50%;
  transform: scaleX(var(--fill, 0));
  transition: transform 120ms linear;
}

.karen-link {
  position: absolute;
  top: 56px;
  left: 12px;
  margin: 0;
  font-size: 0.76rem;
  color: #ffd28a;
  pointer-events: none;
}

/* The stage is the only flexible child, and `min-height: 0` is what lets it give
   up space rather than pushing the controls off a short screen. */
.karen-stage {
  flex: 1 1 auto;
  min-height: 0;
  display: flex;
  align-items: center;
  justify-content: center;
  container-type: size;
}

/* THE OFFICE HAS A FIXED SHAPE, and it is the catalogue's shape rather than the
   screen's: coordinates are metres in a 16×22 room, so a plane that took
   whatever space was left would give a phone a tall office and a tablet a wide
   one — the same coordinates, different distances between them, and a chase that
   is a different game on each. `--ratio` is w/h, written by the template.
   `min(100cqw, 100cqh × ratio)` is the largest box of that shape that fits,
   whichever way the stage is shaped; `container-type: size` is what lets a
   figure be placed in `cqw`/`cqh`, so metres map to pixels entirely in CSS and
   there is no measured box cached in JavaScript to invalidate when mobile chrome
   slides in. */
.karen-plane {
  width: min(100cqw, calc(100cqh * var(--ratio, 0.75)));
  aspect-ratio: var(--ratio, 0.75);
  container-type: size;
  position: relative;
  overflow: hidden;
  border-radius: 10px;
  background:
    linear-gradient(180deg, rgba(255, 255, 255, 0.05), rgba(0, 0, 0, 0.25)),
    repeating-linear-gradient(0deg, #2a2e36 0 22px, #262a31 22px 44px);
  /* HOW BIG A PERSON IS, IN THIS OFFICE. Every fixed length inside the plane is
     a fraction of this one, so the world is drawn at the same apparent scale on
     every screen instead of shrinking as the plane grows. Declared here and used
     only by descendants — `.karen-plane` is itself the query container, so a
     `cqw` in a property ON this rule would resolve against `.karen-stage`. */
  --unit: clamp(26px, 11cqw, 54px);
}

/* Furniture. Static — it comes off the catalogue once and never moves — so
   unlike the figures these go through Vue exactly once. */
.karen-desk {
  position: absolute;
  border-radius: 3px;
  background: #4a3b28;
  box-shadow: inset 0 -2px 0 rgba(0, 0, 0, 0.35);
}

/* THE FIGURES. Feet-anchored: the coordinate is where somebody is STANDING, so
   the box hangs above it rather than being centred on it — which is what makes
   a figure in front of a desk look like it is in front of the desk.
   `translate3d` only and `will-change: transform`, so a position write is a
   compositor job and never a layout one. */
.karen-me,
.karen-boss {
  position: absolute;
  left: 0;
  top: 0;
  width: var(--unit);
  height: calc(var(--unit) * 1.6);
  transform: translate3d(
      calc(var(--x, 0.5) * 100cqw - 50%),
      calc(var(--y, 0.5) * 100cqh - 100%),
      0
    )
    scale(var(--depth, 1));
  transform-origin: 50% 100%;
  z-index: var(--band, 0);
  will-change: transform;
  pointer-events: none;
}

/* Placeholder shapes rather than art: iteration 1 ships with no uploaded assets
   at all, on purpose, and a missing sprite must be a shape and never a broken
   screen. */
.karen-me {
  --skin: #f0d9bd;
  --body: #2f6ea8;
  /* NO TRANSITION. This one is predicted and rewritten every animation frame, so
     a transition would be the compositor easing towards a value that has already
     been replaced — which reads as lag, which is the exact thing prediction is
     here to remove. */
}

/* A FIGURE IS A HEAD AND A BODY, and the head is deliberately far too big.
   Both people on the plane are built the same way so the scene reads as two
   people rather than as a person and a lozenge — the difference between them is
   colour and what is on the head, which is the whole joke: he is bald and
   smiling, and you are not.

   Caricature proportions, not human ones: a head at 52% of the figure would be
   grotesque on a body and is exactly right at 40 px on a phone, where anything
   subtler is a smudge. The silhouette has to be readable at a glance while you
   are judging a dodge, so the head is the thing that carries the identity and
   the expression, and the body is a shape underneath it. */
.karen-fig-body {
  position: absolute;
  left: 50%;
  bottom: 0;
  width: 74%;
  height: 56%;
  transform: translateX(-50%);
  border-radius: 44% 44% 26% 26%;
  background: var(--body, #8a6a3a);
  box-shadow: inset 0 -0.35em 0.5em rgba(0, 0, 0, 0.22);
}
.karen-fig-head {
  position: absolute;
  left: 50%;
  top: 0;
  width: 92%;
  aspect-ratio: 1;
  transform: translateX(-50%);
  border-radius: 50%;
  background: var(--skin, #e8d7b0);
  box-shadow:
    0 0 0 2px rgba(0, 0, 0, 0.35),
    inset -0.18em -0.22em 0.5em rgba(0, 0, 0, 0.18);
  overflow: hidden;
}
/* The bald shine. It is the only thing marking him as bald, and one soft
   highlight reads as a scalp far better than drawing an absence of hair. */
.karen-fig-shine {
  position: absolute;
  left: 26%;
  top: 12%;
  width: 34%;
  height: 22%;
  border-radius: 50%;
  background: rgba(255, 255, 255, 0.5);
  filter: blur(1px);
}
/* And you are not bald, which is the only reason this exists. */
.karen-fig-hair {
  position: absolute;
  inset: -6% -4% auto -4%;
  height: 46%;
  border-radius: 50% 50% 34% 34%;
  background: #2f2a26;
}

.karen-boss {
  --skin: #e8d7b0;
  --body: #7a5a86;
  /* HE IS NOT PREDICTED — his intent is not ours to guess — so his position
     arrives ten times a second and is eased across the gap between two frames.
     100ms is exactly the snapshot period: long enough to be continuous, short
     enough that he is never drawn anywhere the server did not put him. */
  /* NO TRANSITION. He is interpolated in JS now (karenInterp), so a CSS
     transition here would smooth an already-smooth position — adding a second
     lag on top of the interpolation delay and reintroducing the stop-start it
     exists to remove. */
}

/* HOW CLOSE HE IS, PAINTED ON THE MAN AND NOT ON HIS BOX.
   `--skin` and `--body` are what `.karen-fig-head` and `.karen-fig-body` draw
   themselves with, so the step lands on the two shapes that are actually him.
   These rules used to set `background` on `.karen-boss` itself — which is the
   figure's positioning box, a plain `--unit` × `--unit * 1.6` rectangle with
   the head and body painted on top of it. The result was a filled orange
   RECTANGLE appearing behind him the moment he got close, which read as a
   selection box or a broken sprite rather than as a man getting nearer. The
   colours below are the ones those gradients already carried; only the element
   they land on has moved. Nothing may set `background` on `.karen-me` or
   `.karen-boss` — a figure's box is a coordinate, not a surface. */
.karen-boss[data-grin='closing'] {
  --skin: linear-gradient(180deg, #f3c98a 0%, #d99a52 100%);
  --body: #9a5f2a;
}

.karen-boss[data-grin='here'] {
  --skin: linear-gradient(180deg, #ff9f7a 0%, #e0644a 100%);
  --body: #a83b1f;
}

/* The smile. Widens continuously with `--grin`, which is a number the compositor
   can interpolate — unlike the colour above, which steps. */
/* The smile, and it is the one thing on him that carries the whole joke: he is
   delighted to see you, and more so the closer he gets. A wide thin ARC rather
   than a filled blob — an arc reads as a mouth at 40 px and a blob reads as a
   hole. It widens and deepens together from `--grin`, which the compositor
   interpolates, and the interpolator feeds it a continuous value rather than one
   that steps ten times a second. */
.karen-boss-grin {
  position: absolute;
  left: 50%;
  top: 54%;
  width: calc(26% + var(--grin, 0) * 52%);
  height: calc(14% + var(--grin, 0) * 26%);
  transform: translateX(-50%);
  border-radius: 0 0 999px 999px;
  border: solid rgba(40, 16, 10, 0.9);
  border-width: 0 0 max(2px, 0.16em) 0;
  background: rgba(60, 20, 14, calc(0.15 + var(--grin, 0) * 0.5));
}

/* --- controls -------------------------------------------------------------
   Both thumbs, both ends, and nothing between them that can be hit by accident. */

.karen-controls {
  flex: 0 0 auto;
  display: flex;
  align-items: center;
  justify-content: space-between;
  padding: 10px 16px 18px;
  gap: 12px;
}

.karen-stick {
  position: relative;
  width: 104px;
  height: 104px;
  border-radius: 50%;
  border: 2px solid rgba(255, 255, 255, 0.22);
  background: rgba(255, 255, 255, 0.05);
  /* What makes a drag a drag rather than a page scroll on a phone. */
  touch-action: none;
  -webkit-user-select: none;
  user-select: none;
}

.karen-stick-knob {
  position: absolute;
  left: 50%;
  top: 50%;
  width: 46px;
  height: 46px;
  margin: -23px 0 0 -23px;
  border-radius: 50%;
  background: rgba(255, 255, 255, 0.32);
  pointer-events: none;
}

.karen-dash {
  width: 84px;
  height: 84px;
  border-radius: 50%;
  border: 2px solid rgba(240, 180, 41, 0.5);
  background: rgba(240, 180, 41, 0.22);
  color: rgba(255, 255, 255, 0.92);
  font-size: 0.8rem;
  font-weight: 800;
  letter-spacing: 0.06em;
  touch-action: none;
}

.karen-dash:disabled {
  opacity: 0.4;
  border-color: rgba(255, 255, 255, 0.18);
  background: rgba(255, 255, 255, 0.06);
}

@media (prefers-reduced-motion: reduce) {
  .karen-streak-fill {
    transition: none;
  }
}
</style>
