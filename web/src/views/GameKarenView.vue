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

      <!-- Last on the screen and outside the `v-else`, so it is there while the
           catalogue is still loading and there is nothing else to read. -->
      <p class="karen-disclaimer" data-testid="karen-disclaimer">{{ KAREN_DISCLAIMER }}</p>
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
          <!-- The bottle. Placed from the catalogue, drawn only while it is
               there — `bt` is omitted while it stands, so an absent field means
               present and the common case costs nothing to say. -->
          <span
            v-if="bottleAt && bottleMs === 0"
            class="karen-bottle"
            data-testid="karen-bottle"
            :style="bottleStyle"
            aria-hidden="true"
          />
          <!-- WHERE SOMETHING JUST HAPPENED. One short ring, keyed so that a
               second event re-mounts it and restarts the animation rather than
               being swallowed by the first. -->
          <span
            v-if="pop"
            :key="pop.key"
            class="karen-pop"
            data-testid="karen-pop"
            :data-kind="pop.kind"
            :style="{ '--x': String(pop.u), '--y': String(pop.v) }"
            aria-hidden="true"
          />
          <span ref="bossEl" class="karen-boss" data-testid="karen-boss" aria-hidden="true">
            <span v-if="bossSays" class="karen-say" data-testid="karen-boss-say">{{ bossSays }}</span>
            <span class="karen-fig-body" />
            <span class="karen-fig-head">
              <span class="karen-fig-shine" />
              <span class="karen-boss-grin" />
            </span>
          </span>
          <span
            v-for="peer in peers"
            :key="peer.id"
            :ref="(el) => setPeerEl(peer.id, el as Element | null)"
            class="karen-peer"
            data-testid="karen-peer"
            :data-peer="peer.id"
            :style="{ '--body': peerColour(peer.id) }"
            aria-hidden="true"
          >
            <span v-if="sayFor(karenLines, peer.line)" class="karen-say" data-testid="karen-peer-say">{{
              sayFor(karenLines, peer.line)
            }}</span>
            <span class="karen-fig-body" />
            <span class="karen-fig-head"><span class="karen-fig-hair" /></span>
            <!-- WHO is standing there. The shirt colour tells two colleagues
                 apart at a glance; the face says which of your friends it is.
                 Absent until the fetch answers, and absent for good if he has no
                 picture — a 404 is the ordinary reply and means "draw a plain
                 figure", not "something went wrong". -->
            <img
              v-if="peer.avatar"
              class="karen-peer-badge"
              data-testid="karen-peer-avatar"
              :src="peer.avatar"
              alt=""
              referrerpolicy="no-referrer"
              @error="onPeerFaceError(peer.id)"
            />
          </span>
          <span ref="meEl" class="karen-me" data-testid="karen-me" aria-hidden="true">
            <span v-if="meSays" class="karen-say" data-testid="karen-me-say">{{ meSays }}</span>
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

        <!-- ONE CONTROL PER COLLEAGUE, and none at all when you are alone.
             The design said "tap the colleague on the plane"; a figure is a
             ~40 px cut-out that moves, is drawn behind the man chasing it, and
             can be under your own thumb — a sub-44 px moving target, which the
             mobile rules forbid outright. A fixed control is stationary, sized,
             and can show a cooldown. It also wears his face, which is how you
             tell two colleagues apart.
             Rendered from the same reactive roster as the figures, so it
             appears and disappears with them and never needs its own guard. -->
        <div v-if="config?.redirect" class="karen-verbs">
          <button
            v-for="peer in peers"
            :key="peer.id"
            class="karen-verb"
            type="button"
            data-testid="karen-redirect"
            :data-peer="peer.id"
            :style="{ '--body': peerColour(peer.id) }"
            :disabled="redirectMs > 0"
            :aria-label="config.redirect.label"
            @pointerdown.prevent="onRedirect(peer.id)"
            @click.prevent
          >
            <img v-if="peer.avatar" class="karen-verb-face" :src="peer.avatar" alt="" referrerpolicy="no-referrer" />
            <span class="karen-verb-label">{{
              redirectMs > 0 ? formatSeconds(redirectMs) : config.redirect.label
            }}</span>
          </button>
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
import { KAREN_DISCLAIMER, KAREN_LORE, buildRules, endingFor } from '../lib/karenRules';
import {
  applyBoss,
  applyFigure,
  karenAvatarEndpoint,
  movingFast,
  peerColour,
  sameRoster,
  type PeerLook,
  decimal,
  deskBox,
  sayFor,
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
// WHICH LINE, not the line itself: the wire sends an index and the catalogue —
// fetched once — holds the words (ADR-037). Reactive because they are text and
// change at snapshot rate, which is exactly what reactivity is for; the two
// POSITIONS beside them are the opposite and never come near it.
const meLine = ref(0);
const bossLine = ref(0);
const link = ref<'connecting' | 'open' | 'lost'>('connecting');
const over = ref<{ cause: string; pay: number; secs: number } | null>(null);

/**
 * The other people in the office.
 *
 * MEMBERSHIP IS REACTIVE AND POSITIONS ARE NOT, which is the yard's rule and it
 * matters more here, not less. This list changes when somebody joins, leaves or
 * starts saying something else — a `v-for` and a text node, a few times a shift.
 * Where they are STANDING changes ten times a second and never touches Vue: it
 * goes to CSS custom properties on the element, written from the draw loop, so a
 * plane full of figures costs the compositor rather than the scheduler.
 */
const peers = ref<PeerShown[]>([]);

/**
 * Handles whose face this browser has given up on — a 404 (he has no picture) or
 * an image that would not load.
 *
 * Remembered so the `img` is not re-created every roster change to fail again.
 * It is per shift rather than per session, because the office is: a handle is
 * minted per process and means nothing after a restart.
 */
const brokenFaces = ref(new Set<string>());

/** The redirect verb's cooldown, milliseconds, straight off the snapshot. */
const redirectMs = ref(0);

/** How long until another bottle stands in the office. Zero means one is there. */
const bottleMs = ref(0);

/** Which of the catalogue's spots it is on. Absent means the first, like every index here. */
const bottleSpot = ref(0);

/**
 * Where the bottle stands, in plane coordinates.
 *
 * From the catalogue by INDEX, never from a coordinate on the frame: it moves
 * every ten seconds, and two positions ten times a second per viewer would be
 * twenty bytes forever to say something that changes once in a hundred frames.
 */
const bottleAt = computed(() => {
  const spots = config.value?.bottle?.spots;
  if (!spots || !spots.length || !constants) return null;
  const at = spots[Math.min(bottleSpot.value, spots.length - 1)];
  return at ? toPlane(at.x, at.y, constants.officeW, constants.officeH) : null;
});

const bottleStyle = computed(() => {
  const at = bottleAt.value;
  return at ? { '--x': String(at.u), '--y': String(at.v) } : {};
});

/** A peer as the template needs him: identity, speech, and a face if he has one. */
interface PeerShown extends PeerLook {
  avatar?: string;
}

/**
 * Whether two rosters draw the same faces.
 *
 * Beside sameRoster rather than folded into it: that one is about the OFFICE —
 * who is here and what they are saying, which is what the server tells us — and
 * this is about what this browser has managed to load, which is local. Both have
 * to be false before a re-render is worth doing.
 */
function sameFaces(a: readonly PeerShown[], b: readonly PeerShown[]): boolean {
  if (a.length !== b.length) return false;
  return a.every((p, i) => p.avatar === b[i].avatar);
}

/**
 * Points the bald man at a colleague.
 *
 * A VERB TRAVELS OVER THE SOCKET AND IS ANSWERED WITH STATE, never with a
 * response body — the same rule the yard settled (ADR-043). The office judges
 * it, and the caller learns the outcome from the next snapshot exactly as
 * everybody else does, which is what stops this client believing a verb the
 * server refused.
 *
 * The cooldown is checked here only to keep the button honest; the office checks
 * it too, and the office is the one that decides.
 */
function onRedirect(peerID: string): void {
  if (!shift || redirectMs.value > 0) return;
  realtimeClient(shift.room).send({ t: 'karen_do', v: 'redirect', tg: peerID });
}

/**
 * A one-off mark on the plane saying that something just happened, and where.
 *
 * THE RULE THIS IMPLEMENTS (see CLAUDE.md → «A verb announces itself on the
 * plane»): anything that is not ordinary movement gets a brief visual
 * acknowledgement at the place it happened. Standing still, walking and dashing
 * do not — you can already see those. Drinking, and pointing the bald man at a
 * colleague, are invisible without this: the office simply behaves differently a
 * moment later, and a player who did not know he had pressed anything reads that
 * as the game misbehaving.
 *
 * DELIBERATELY SMALL: one ring, under half a second, no colour flash and no
 * screen shake. It is an acknowledgement, not a celebration, and it must never
 * be the thing you are looking at when the man arrives.
 *
 * IT COSTS NOTHING ON THE WIRE. Every one of these is derived from a field the
 * snapshot already carries crossing from zero to non-zero — the bottle's return
 * timer starting, his drunk timer starting, your own verb cooldown starting. An
 * event field would be bytes ten times a second to say "nothing happened".
 */
/**
 * Where the bald man is, in plane coordinates — the anchor for anything that
 * happens TO him.
 *
 * A PLAIN FUNCTION AND NOT A `computed`, which is the whole of a bug worth
 * remembering. `bossAt` is deliberately not reactive (positions never enter
 * reactivity on this plane), so a computed over it caches its first evaluation
 * forever — and its first evaluation happens before the first snapshot has set
 * `bossAt` at all, so it cached `null` and every mark on him silently did
 * nothing. A computed's cache is only correct when its inputs are reactive.
 */
/** Where you are, in plane coordinates. A function, not a computed — see bossPlace. */
function mePlace(): { u: number; v: number } | null {
  if (!predictor || !constants) return null;
  const v = predictor.view();
  return toPlane(v.x, v.y, constants.officeW, constants.officeH);
}

function bossPlace(): { u: number; v: number } | null {
  if (!bossAt || !constants) return null;
  return toPlane(bossAt.x, bossAt.y, constants.officeW, constants.officeH);
}

/**
 * Which line index is «ЭТО НУЖНО УТОЧНИТЬ У ДРУГОГО».
 *
 * FOUND BY MATCHING THE STRING THE CATALOGUE ALREADY PUBLISHES, rather than by
 * serving an index or hardcoding one. `redirect.say` and `karen_lines` are both
 * already on the config this client fetched once, so the join costs nothing on
 * the wire and nothing at runtime — and the server can still rearrange its pools
 * without a client deploy, which is the property that made the layout server-side
 * in the first place.
 */
const redirectLine = computed(() => {
  const say = config.value?.redirect?.say;
  const lines = config.value?.karen_lines;
  if (!say || !lines) return -1;
  return lines.indexOf(say);
});

const pop = ref<{ key: number; kind: string; u: number; v: number } | null>(null);
let popKey = 0;
let popTimer: number | undefined;

function markAt(kind: string, at: { u: number; v: number } | null): void {
  if (!at) return;
  popKey += 1;
  pop.value = { key: popKey, kind, u: at.u, v: at.v };
  if (popTimer !== undefined) window.clearTimeout(popTimer);
  // Cleared on a timer rather than on `animationend`, which never fires under
  // prefers-reduced-motion — where the animation is switched off entirely.
  popTimer = window.setTimeout(() => {
    pop.value = null;
  }, 600);
}

function onPeerFaceError(id: string): void {
  const next = new Set(brokenFaces.value);
  next.add(id);
  brokenFaces.value = next;
}
const karenLines = computed(() => config.value?.karen_lines);

const meSays = computed(() => sayFor(config.value?.karen_lines, meLine.value));
const bossSays = computed(() => sayFor(config.value?.boss_lines, bossLine.value));

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
/**
 * The peers' elements and their interpolators, both plain Maps and neither
 * reactive — this is the imperative tier, and nothing here is ever read during a
 * render.
 *
 * One interpolator EACH rather than one for all of them: createInterpolator is a
 * factory over closure state, a peer is an independent stream of samples, and a
 * shared buffer would make one person's stall smear across everybody.
 */
const peerEls = new Map<string, HTMLElement>();
const peerInterp = new Map<string, Interpolator>();
/** Each colleague's last balloon index, so an announcement can be seen ARRIVING. */
const peerLines = new Map<string, number>();
/** Each colleague's last drawn position, which is how his speed — and so his dash — is known. */
const peerSeen = new Map<string, { x: number; y: number; at: number }>();
/** The grin state currently written on the boss, so the class is not rewritten. */
let bossDrunk = false;
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
  peers.value = [];
  if (popTimer !== undefined) window.clearTimeout(popTimer);
  popTimer = undefined;
  pop.value = null;
  brokenFaces.value = new Set();
  redirectMs.value = 0;
  bottleMs.value = 0;
  bottleSpot.value = 0;
  peerEls.clear();
  peerInterp.clear();
  peerLines.clear();
  peerSeen.clear();
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
    placePeers(now);
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

/**
 * Collects a peer's element as Vue creates it, and drops it when Vue removes it.
 *
 * The `=== node` guard is load-bearing and the yard shipped the bug once: Vue
 * invokes a function ref on EVERY patch, not only when the element changes, so
 * without it the first-placement branch below would run on every re-render and
 * fight the draw loop for the position.
 */
function setPeerEl(id: string, el: Element | null): void {
  if (!el) {
    peerEls.delete(id);
    return;
  }
  const node = el as HTMLElement;
  if (peerEls.get(id) === node) return;
  peerEls.set(id, node);
  // Place it immediately if a sample has already arrived, so a figure cannot be
  // painted for one frame at the plane's default corner and then jump. Through
  // the same writer the loop uses, so it cannot arrive holding a position
  // without the depth that belongs with it.
  const at = peerInterp.get(id)?.at(performance.now());
  if (at && constants) {
    applyFigure(node, toPlane(at.x, at.y, constants.officeW, constants.officeH));
  }
}

/**
 * Draws everybody else, from the same interpolation buffer the лысый uses and at
 * the same served delay.
 *
 * A peer is INTERPOLATED and never predicted: his intent is not ours to guess.
 * That is the whole difference between this and placeMe, and it is why the two
 * are separate functions rather than a loop over "figures".
 */
function placePeers(now: number): void {
  if (!constants) return;
  for (const [id, el] of peerEls) {
    const at = peerInterp.get(id)?.at(now);
    // Nothing has arrived for this one yet — draw nothing rather than guess a
    // position and then snap it.
    if (!at) continue;
    applyFigure(el, toPlane(at.x, at.y, constants.officeW, constants.officeH));

    // A COLLEAGUE'S DASH, DERIVED FROM HOW FAST HE IS MOVING and from nothing
    // else. It is a buff — brief, but a state rather than an event — and until
    // now it was visible only to the man doing it, which the visibility rule
    // calls unfinished.
    //
    // Two consecutive drawn positions and the walk speed the catalogue already
    // publishes are enough: nothing on this plane exceeds a walk except a dash,
    // so "faster than a walk" IS "dashing". That costs no byte on a frame that
    // repeats ten times a second, and it works for every peer without the server
    // saying anything about any of them.
    const was = peerSeen.get(id);
    peerSeen.set(id, { x: at.x, y: at.y, at: now });
    if (!was) continue;
    const fast = movingFast(was, at, (now - was.at) / 1000, constants.walkSpeed);
    if (fast === (el.dataset.fast === '1')) continue;
    if (fast) el.dataset.fast = '1';
    else delete el.dataset.fast;
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
  // Same rule for the balloons: absent is index 0, which is the default line.
  // Read as "unchanged" a figure would stick on the last interesting thing it
  // said for the rest of the shift.
  const wasMeLine = meLine.value;
  meLine.value = num(frame.p);
  // Omitted when ready, like `dc` — absent means zero, never "unchanged".
  // EDGES, NOT LEVELS: each of these is "it just started", which is the only
  // moment worth marking — a cooldown merely still running would flash the plane
  // ten times a second for eight seconds. Both are read and compared BEFORE
  // either ref is written, because an assignment earlier in this function would
  // destroy the very edge being looked for. (It did, once.)
  const bt = num(frame.bt);
  if (bt > 0 && bottleMs.value === 0) markAt('bottle', bottleAt.value);
  redirectMs.value = num(frame.rc);
  bottleMs.value = bt;

  // WHOEVER JUST SAID IT, WHEREVER THEY ARE STANDING. The redirect used to be
  // marked off your own cooldown starting, which meant only the person who
  // pressed it ever saw anything — a colleague pointing the bald man at YOU was
  // silent on your screen, which is the one time it matters most.
  //
  // The announcement is already on the wire for everybody: your own line is `p`
  // and a colleague's is `pr[].p`, both indexes into a pool this client fetched
  // once. So every screen sees every redirect, derived locally, with no extra
  // byte on a frame that repeats ten times a second.
  if (redirectLine.value >= 0 && meLine.value === redirectLine.value && wasMeLine !== redirectLine.value) {
    markAt('redirect', mePlace());
  }
  bottleSpot.value = num(frame.bs);

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
    bossLine.value = num(b.p);
    const grin = num(b.g) / 255;
    // GREEN IS A STATE OF THE FIGURE, not a box around it. It goes on a data
    // attribute that flips `--skin` and `--body`, exactly as the grin steps do —
    // a `background` on `.karen-boss` paints the positioning rectangle and reads
    // as a broken sprite (§17.5 of the build plan, learned the hard way).
    const drunk = num(b.d) > 0;
    if (drunk && !bossDrunk) markAt('drunk', bossPlace());
    if (el && drunk !== bossDrunk) {
      bossDrunk = drunk;
      if (drunk) el.dataset.drunk = '1';
      else delete el.dataset.drunk;
    }
    bossAt = { x: num(b.x) / 100, y: num(b.y) / 100 };
    // Buffered rather than drawn. The render loop reads him back out a beat
    // later, between two samples it already holds, which is what makes jitter
    // and a dropped frame cost nothing.
    bossInterp?.push({ x: bossAt.x, y: bossAt.y, grin }, performance.now());
  }

  applyPeers(frame.pr);
}

/**
 * Folds the office's other occupants into the two tiers.
 *
 * `pr` is OMITTED when you are alone, which is the common case and the reason it
 * costs nothing on the wire — so absent means "nobody here", never "unchanged".
 * Read the other way a colleague who walked out would stand in the office for
 * the rest of the shift.
 */
function applyPeers(raw: unknown): void {
  const list = Array.isArray(raw) ? raw : [];
  const now = performance.now();
  const roster: PeerShown[] = [];
  const live = new Set<string>();

  for (const entry of list) {
    if (!entry || typeof entry !== 'object') continue;
    const p = entry as Record<string, unknown>;
    const id = typeof p.i === 'string' ? p.i : '';
    if (!id) continue;
    live.add(id);
    const line = num(p.p);
    // A COLLEAGUE'S redirect, marked where HE is standing. Same edge rule as
    // yours: the line becoming the announcement, not merely being it.
    if (redirectLine.value >= 0 && line === redirectLine.value && peerLines.get(id) !== line && constants) {
      markAt('redirect', toPlane(num(p.x) / 100, num(p.y) / 100, constants.officeW, constants.officeH));
    }
    peerLines.set(id, line);
    roster.push({
      id,
      line,
      // Derived from the handle, never sent with it (ADR-037). The browser
      // caches the answer, so this is one request per colleague per shift even
      // though the string is recomputed on every roster change.
      avatar: brokenFaces.value.has(id) ? undefined : karenAvatarEndpoint(id),
    });
    let interp = peerInterp.get(id);
    if (!interp) {
      // Same period as the лысый's, from the same served rate, so everybody who
      // is not predicted is drawn at the same instant in the past.
      interp = createInterpolator(1000 / (config.value?.sim.snapshot_hz || 10));
      peerInterp.set(id, interp);
    }
    // Buffered, never drawn here — see placePeers.
    interp.push({ x: num(p.x) / 100, y: num(p.y) / 100 }, now);
  }

  // Somebody who is no longer on the frame has been promoted or walked out. The
  // element goes with the roster below; the buffer has to be dropped explicitly
  // or a handle that came back would be interpolated from where it died.
  for (const id of peerInterp.keys()) {
    if (!live.has(id)) peerInterp.delete(id);
  }
  for (const id of peerLines.keys()) {
    if (!live.has(id)) peerLines.delete(id);
  }
  for (const id of peerSeen.keys()) {
    if (!live.has(id)) peerSeen.delete(id);
  }

  // THE GUARD IS THE POINT. This runs ten times a second and almost every call
  // describes the same people saying the same things; assigning a fresh array
  // each time would be a scheduler pass and a patch per peer per frame to
  // produce identical markup.
  if (!sameRoster(peers.value, roster) || !sameFaces(peers.value, roster)) peers.value = roster;
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

/* Quiet, and at the very bottom. It has to be legible — that is the whole point
   of it — so it is dimmed rather than shrunk past reading. */
.karen-disclaimer {
  margin: 4px 0 0;
  max-width: 34rem;
  font-size: 0.72rem;
  line-height: 1.35;
  opacity: 0.55;
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

/* THE OFFICE IS THE SCREEN, AND EVERYTHING ELSE FLOATS ON IT.
   This used to be a flex column — readouts, then the office, then a band of
   controls — which meant the plane only got whatever height the other two left
   it. The office is a portrait 16 × 22 room, so a plane bounded by height is a
   plane narrower than the phone: measured on a 412 px phone, 470 px of an
   available 591 with dead space above and below it. The controls' band alone
   was 200 px of that.
   So the stage is absolute and fills this box, and the HUD, the streak, the
   link and the controls are drawn ON TOP of it — which is «ВАНЯДУМ»'s
   arrangement exactly (`.dum-play` / `.dum-canvas` / `.dum-hud`), and the
   yard's rule that a control belongs on the plane rather than under it.
   Nothing here is a column any more, so nothing can push the office. */
.karen-play {
  position: absolute;
  inset: 0;
  overflow: hidden;
  background: #16181d;
  color: rgba(255, 255, 255, 0.94);
}

/* IN FLOW, BUT OVER THE OFFICE. `.karen-play` is no longer a column, so normal
   flow starts at its top edge and stacks these two exactly where they were —
   while `z-index` puts them above the stage behind them. Keeping them in flow
   rather than absolutely positioning each is what lets the streak sit under the
   HUD without anybody having to know how tall the HUD is.
   `pointer-events: none` because they are readouts standing on the floor now: a
   tap that lands on the word ЗАРПЛАТА has to reach the office, not be eaten by
   a label. The one real control in here turns them back on for itself. */
.karen-hud {
  position: relative;
  z-index: 2;
  display: flex;
  align-items: center;
  gap: 8px;
  padding: 6px 10px;
  font-variant-numeric: tabular-nums;
  min-width: 0;
  pointer-events: none;
  text-shadow: 0 1px 3px rgba(0, 0, 0, 0.9);
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
  /* The exception to the strip above: this one IS a control. */
  pointer-events: auto;
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
  position: relative;
  z-index: 2;
  pointer-events: none;
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
  position: absolute;
  inset: 0;
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
.karen-boss,
.karen-peer {
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

/* ANOTHER КАРЕН. Same build as you — a head with hair, a body — because he is
   the same kind of thing; the shirt is the only difference, and it is written
   INLINE from his handle (peerColour) because a colour changes when somebody
   joins, which is a membership event and not something for the frame loop.

   `--body` rather than `background`, for the reason stated above the figure box:
   a background on the positioning rectangle paints the coordinate instead of the
   man, which is the defect the лысый shipped with once. The skin stays
   everybody's, so the office reads as colleagues in different shirts rather than
   as different species. */
.karen-peer {
  --skin: #f0d9bd;
  --body: #4a5a6a;
  /* Slightly recessed, so a glance tells you which one you are steering — but
     not so faint that a colleague is easy to lose, since he is the thing you are
     negotiating with. */
  opacity: 0.88;
}

/* HIS FACE, beside the head rather than on it: a colleague is a cut-out figure,
   and painting a photograph into the head would make him a token seen from
   above, which is the one thing this plane is not. Sized off `--unit` like every
   other length here, so it rides the depth ramp with the man. */
.karen-peer-badge {
  position: absolute;
  left: 78%;
  top: -6%;
  width: 62%;
  aspect-ratio: 1;
  border-radius: 50%;
  object-fit: cover;
  border: 2px solid rgba(0, 0, 0, 0.4);
  background: rgba(0, 0, 0, 0.25);
  /* Decoration on somebody else's figure: it must never take a tap meant for
     the office underneath. */
  pointer-events: none;
}

/* A COLLEAGUE MID-DASH. A buff, so it is shown for as long as it lasts rather
   than flashed once, and it is a property of the figure rather than something
   orbiting him — the same shape the bald man's green follows. Small on purpose:
   it says "he is moving fast", not "look over here". */
.karen-peer[data-fast] {
  filter: drop-shadow(0 0 6px rgba(255, 255, 255, 0.55));
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
/* The bottle. A small thing on the floor you have to walk to — deliberately
   drawn at ground level and NOT feet-anchored like a figure, because it is not
   standing, it is lying about. */
.karen-bottle {
  position: absolute;
  left: 0;
  top: 0;
  width: calc(var(--unit) * 0.28);
  height: calc(var(--unit) * 0.62);
  transform: translate3d(calc(var(--x, 0.5) * 100cqw - 50%), calc(var(--y, 0.5) * 100cqh - 50%), 0);
  border-radius: 40% 40% 22% 22%;
  background: linear-gradient(180deg, #cfe3d0 0%, #7fa886 55%, #4c6b52 100%);
  box-shadow: 0 0 0 1px rgba(0, 0, 0, 0.4);
  pointer-events: none;
  z-index: 1;
}

/* Drunk: green, and it is the SKIN and the BODY that go green rather than the
   positioning box — the same rule the grin steps follow, and for the same
   reason. He is still coming; he is just not coming in a straight line. */
.karen-boss[data-drunk] {
  --skin: linear-gradient(180deg, #b9d8a4 0%, #7fae6a 100%);
  --body: #4f7a46;
}

.karen-boss[data-grin='closing'] {
  --skin: linear-gradient(180deg, #f3c98a 0%, #d99a52 100%);
  --body: #9a5f2a;
}

.karen-boss[data-grin='here'] {
  --skin: linear-gradient(180deg, #ff9f7a 0%, #e0644a 100%);
  --body: #a83b1f;
}

/* THE BALLOON, and it hangs off the figure rather than being placed beside it.
   `.karen-me` / `.karen-boss` are already positioned every animation frame by a
   transform, so a child anchored to the top of that box is carried along by the
   compositor for nothing — no second write, no chance of the words being a frame
   behind the man saying them.
   `bottom: 100%` puts it above the head; the figure's box is feet-anchored, so
   that is above him rather than over him. It does not scale with `--depth`,
   deliberately: a far-away line would become unreadable exactly when the man
   saying it is hardest to see. */
.karen-say {
  position: absolute;
  left: 50%;
  bottom: 100%;
  transform: translateX(-50%);
  margin-bottom: 4px;
  padding: 2px 6px;
  border-radius: 8px;
  background: rgba(12, 14, 18, 0.82);
  color: rgba(255, 255, 255, 0.92);
  font-size: 0.54rem;
  font-weight: 700;
  letter-spacing: 0.02em;
  line-height: 1.2;
  /* TWO lines, and the two numbers below are one measurement rather than two
     tastes. A balloon used to be a single `nowrap` row, which is what forced
     `TestNobodySaysMoreThanFitsOnAPhone` down to 32 runes — and 32 runes is
     under a short Russian sentence, so the pools stayed blunt and the co-op
     lines that are coming did not fit at all.

     The arithmetic, at the bold uppercase Cyrillic these pools are written in
     with `letter-spacing: 0.02em`: a rune costs about 0.6 × the font size, so
     0.54rem ≈ 8.64px gives ~5.2px a rune and `max-width: 160px` holds ~30 of
     them on a row. Two rows is ~61, and the pools are bounded at 48 to leave
     room for a bad word split — the wrap is by word, so a line that would break
     awkwardly still fits rather than being clipped. `line-clamp` is the
     backstop, not the plan: a sentence past the bound is a red Go test, and the
     ellipsis only exists so that a mistake degrades instead of covering the
     office it is standing in.

     Three things that must NOT change with it: this box still does not scale
     with `--depth` (see above), it stays `pointer-events: none`, and two rows
     at this size is ~25px, which is well clear of the readouts standing over
     the top edge of the same plane. */
  white-space: normal;
  /* `width: max-content` IS LOAD-BEARING, and it is the thing `nowrap` was
     hiding. This balloon is absolutely positioned inside the figure, so its
     containing block is a ~40px positioning box — and an absolutely positioned
     element shrink-to-fits against THAT, not against the screen. While it was
     one `nowrap` row nothing could wrap, so the narrow containing block never
     showed; the moment wrapping was allowed, every line broke against 40px and
     a 14-rune balloon became three rows. `max-content` asks for the unwrapped
     width and `max-width` is what actually bounds it. */
  width: max-content;
  max-width: 160px;
  text-align: center;
  /* Both spellings, and they are two browsers rather than one habit: the
     unprefixed `line-clamp` is what current Chrome uses (it computes `display`
     to `flow-root` itself, which is why the `-webkit-box` above does not survive
     in devtools), and the `-webkit-` trio is the only clamp Safari understood
     until 18. Neither is redundant while the audience is «whatever phone a
     friend has». */
  display: -webkit-box;
  -webkit-box-orient: vertical;
  -webkit-line-clamp: 2;
  line-clamp: 2;
  overflow: hidden;
  pointer-events: none;
  text-shadow: 0 1px 2px rgba(0, 0, 0, 0.9);
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

/* ON the office rather than under it, both thumbs where they already were.
   The wrapper is a layout box and NOTHING ELSE: `pointer-events: none` so the
   gap between the two thumbs is still office — a tap there has to reach the
   floor, because the whole width of the screen is the room now. Each control
   turns them back on for itself.
   `align-items: flex-end` rather than `center`, because the two are different
   sizes and it is their BOTTOMS that must line up against the edge of the
   screen. And the bottom padding respects the home indicator: this band used to
   be reserved space, and now it is the room. */
.karen-controls {
  position: absolute;
  left: 0;
  right: 0;
  bottom: 0;
  z-index: 3;
  display: flex;
  align-items: flex-end;
  justify-content: space-between;
  /* The left padding is DELIBERATELY bigger than the right, and it is the stick
     that earns the asymmetry. At 16px a side, a 104px stick starts 16px from the
     glass — so the half of it a thumb reaches first sits in the phone's own
     edge-swipe strip, where a drag that begins there is arguing with the system
     gesture rather than steering, and on a curved screen it is partly on the
     bezel. 30px clears that strip and centres the stick ~82px in, which on a
     360px phone is a comfortable quarter of the width.
     The dash keeps its 16px: it is a TAP, so its edge costs nothing, and
     `justify-content: space-between` holds it against the right without being
     told. Widening the stick instead was the other option and is the wrong one —
     104px is already the size a thumb wants, and a bigger circle would eat the
     office rather than move off its edge. */
  padding: 10px 16px calc(18px + env(safe-area-inset-bottom, 0px)) 30px;
  gap: 12px;
  pointer-events: none;
}

.karen-stick {
  position: relative;
  pointer-events: auto;
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

/* The colleague buttons, stacked above the dash on the right — the thumb that
   reaches РЫВОК is the thumb that reaches these, and they are used in the same
   breath as a dodge. Column-reverse so the first colleague sits nearest the
   thumb rather than furthest from it. */
.karen-verbs {
  pointer-events: none;
  display: flex;
  flex-direction: column-reverse;
  align-items: flex-end;
  gap: 8px;
}

.karen-verb {
  pointer-events: auto;
  display: flex;
  align-items: center;
  gap: 6px;
  /* ≥ 44 px, like every control on this plane. */
  min-height: 44px;
  padding: 4px 10px 4px 4px;
  border-radius: 22px;
  border: 2px solid var(--body, #4a5a6a);
  background: rgba(12, 14, 18, 0.72);
  color: rgba(255, 255, 255, 0.92);
  font-size: 0.62rem;
  font-weight: 700;
  letter-spacing: 0.02em;
  touch-action: manipulation;
}

.karen-verb:disabled {
  opacity: 0.45;
}

/* His face ON the button, which is what makes two colleagues distinguishable
   without a name — the same picture the plane draws beside his head. */
.karen-verb-face {
  width: 34px;
  height: 34px;
  border-radius: 50%;
  object-fit: cover;
  background: rgba(255, 255, 255, 0.12);
}

.karen-verb-label {
  white-space: nowrap;
}

.karen-dash {
  pointer-events: auto;
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

/* THE ACKNOWLEDGEMENT. One ring, expanding and fading, under half a second.
   Deliberately restrained: it says "that landed", not "well done" — a bigger
   effect would be the thing you are watching when the bald man arrives, which is
   the one moment this game cannot afford to take your eye off.
   Sized off `--unit` like everything else here, so it scales with the plane. */
.karen-pop {
  position: absolute;
  left: 0;
  top: 0;
  width: calc(var(--unit) * 0.9);
  height: calc(var(--unit) * 0.9);
  margin: calc(var(--unit) * -0.45) 0 0 calc(var(--unit) * -0.45);
  transform: translate3d(calc(var(--x, 0.5) * 100cqw), calc(var(--y, 0.5) * 100cqh), 0);
  border-radius: 50%;
  border: 2px solid var(--pop, rgba(255, 255, 255, 0.85));
  pointer-events: none;
  z-index: 4;
  animation: karen-pop 420ms ease-out forwards;
}

/* A colour per kind, so two things happening in the same second are still two
   things. No text: the balloons already say what happened. */
.karen-pop[data-kind='bottle'] {
  --pop: rgba(150, 220, 150, 0.9);
}
.karen-pop[data-kind='drunk'] {
  --pop: rgba(120, 210, 110, 0.95);
}
.karen-pop[data-kind='redirect'] {
  --pop: rgba(240, 180, 41, 0.95);
}

@keyframes karen-pop {
  from {
    opacity: 0.9;
    scale: 0.35;
  }
  to {
    opacity: 0;
    scale: 1.6;
  }
}

@media (prefers-reduced-motion: reduce) {
  .karen-streak-fill {
    transition: none;
  }

  /* A dash is a state rather than motion, so its aura stays under reduced
     motion — there is nothing moving about it to reduce. */

  /* Still SHOWN, just not animated — somebody who has asked for less motion
     still needs to know their verb landed. It is cleared on a timer rather than
     on `animationend` precisely so that this branch works. */
  .karen-pop {
    animation: none;
    opacity: 0.55;
  }
}
</style>
