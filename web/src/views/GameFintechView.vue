<template>
  <div class="fintech-root">
    <!-- SPLASH — the rules cheatsheet, generated from the served catalogue. -->
    <section v-if="phase === 'splash'" class="fintech-splash" data-testid="fintech-splash">
      <h1 class="fintech-title">{{ config?.title || 'СИМУЛЯТОР ФИНТЕХА' }}</h1>
      <p class="fintech-lore">{{ FINTECH_LORE }}</p>

      <div v-if="loading" class="fintech-loading">
        <v-progress-circular indeterminate size="28" />
      </div>

      <!-- THE ORDER IS THE OWNER'S, and it is the order somebody actually uses
           this screen in: the button first, because a returning player wants to
           play and nothing above it should stand between them and the office; then
           the boards, which are the reason to play again; then the guide, which is
           read once; then your own shifts, which only you care about and which are
           the natural bottom of the page. -->
      <template v-else>
        <v-btn
          class="fintech-start"
          color="warning"
          size="large"
          data-testid="fintech-start"
          :loading="starting"
          @click="start"
        >
          НАЧАТЬ СМЕНУ
        </v-btn>

        <!-- TWO BOARDS, BECAUSE THE GAME SCORES TWO THINGS. Money rewards
             standing still through the ramp; length rewards surviving a floor
             that speeds up every twenty seconds — and the best way to do one is
             not the best way to do the other. Every row shows both numbers, so
             the two read as one scoreboard rather than as two lists of strangers. -->
        <div v-if="hasBoards" class="fintech-boards" data-testid="fintech-top">
          <div
            v-for="board in boards"
            :key="board.key"
            class="fintech-list"
            :data-testid="`fintech-top-${board.key}`"
          >
            <h2 class="fintech-rule-title">{{ board.title }}</h2>
            <ol>
              <li v-for="(shift, i) in board.rows" :key="i">
                <span class="fintech-board-name">{{ shift.name || '—' }}</span>
                <span class="fintech-board-score">
                  {{ money(shift.salary) }} · {{ formatClock(shift.seconds) }}
                </span>
              </li>
            </ol>
          </div>
        </div>

        <div class="fintech-rules" data-testid="fintech-rules">
          <section
            v-for="block in rules"
            :key="block.title"
            class="fintech-rule-block"
            data-testid="fintech-rule-block"
          >
            <h2 class="fintech-rule-title">{{ block.title }}</h2>
            <dl class="fintech-rule-list">
              <template v-for="line in block.lines" :key="line.label">
                <dt>{{ line.label }}</dt>
                <dd>{{ line.text }}</dd>
              </template>
            </dl>
          </section>
        </div>

        <div v-if="myShifts.length" class="fintech-list" data-testid="fintech-runs">
          <h2 class="fintech-rule-title">Твои смены</h2>
          <ul>
            <li v-for="(shift, i) in myShifts" :key="i">
              {{ causeIcon(shift.cause) }} {{ money(shift.salary) }} · {{ formatClock(shift.seconds) }}
            </li>
          </ul>
        </div>
      </template>

      <p v-if="error" class="fintech-error" data-testid="fintech-error">{{ error }}</p>

      <!-- Last on the screen and outside the `v-else`, so it is there while the
           catalogue is still loading and there is nothing else to read. -->
      <p class="fintech-disclaimer" data-testid="fintech-disclaimer">{{ FINTECH_DISCLAIMER }}</p>
    </section>

    <!-- PLAYING — a fixed-height column: readouts, the office, the controls.
         Nothing here scrolls, and nothing stands where a control takes the tap. -->
    <section
      v-else-if="phase === 'playing'"
      class="fintech-play"
      data-testid="fintech-play"
      :style="{
        '--box-ratio': String(box.boxRatio),
        '--head-share': String(box.headShare),
        '--side-share': String(box.sideShare),
        '--unit-cqw': String(UNIT_CQW),
      }"
    >
      <div class="fintech-hud">
        <span class="fintech-hud-cell" data-testid="fintech-hud-money">
          <span class="fintech-hud-label">ЗАРПЛАТА</span>
          <span class="fintech-hud-value">{{ money(pay) }}</span>
        </span>
        <span class="fintech-hud-cell fintech-hud-mult" data-testid="fintech-hud-mult">
          {{ formatMultiplier(mult) }}
        </span>
        <!-- HOW LONG YOU HAVE LASTED — the second scored dimension, and the one
             thing on this strip you are trying to make bigger on purpose. Derived
             from two tick numbers, so it costs nothing on the wire. -->
        <span class="fintech-hud-cell" data-testid="fintech-hud-alive">
          <span class="fintech-hud-label">СМЕНА</span>
          <span class="fintech-hud-value">{{ formatClock(aliveSecs) }}</span>
        </span>
        <!-- AND WHAT THE OFFICE IS DOING ABOUT IT. The ramp is the only thing in
             this game a player cannot affect at all, so hiding it would just make
             the лысый feel inconsistent. `data-bump` flashes the cell for a moment
             when the level moves — an office-wide event nobody would otherwise
             notice, marked the way every verb on the plane is. -->
        <span
          class="fintech-hud-cell fintech-hud-tempo"
          data-testid="fintech-hud-tempo"
          :data-bump="tempoBump ? '1' : undefined"
        >
          <span class="fintech-hud-label">ТЕМП</span>
          <span class="fintech-hud-value">{{ tempoLabel }}</span>
        </span>
        <span class="fintech-hud-cell fintech-hud-dash" data-testid="fintech-hud-dash">
          {{ dashMs > 0 ? `РЫВОК ${formatSeconds(dashMs)}` : 'РЫВОК ГОТОВ' }}
        </span>
        <button class="fintech-quit" type="button" data-testid="fintech-quit" @click="quit">
          УЙТИ
        </button>
      </div>

      <div class="fintech-streak" data-testid="fintech-hud-streak">
        <span class="fintech-streak-fill" :style="{ '--fill': String(ramp) }" />
      </div>

      <!-- WHAT IS CURRENTLY TRUE OF YOU. A verb is marked where it happened and a
           buff is drawn on the figure carrying it — neither says how long, and a
           state with a duration whose remaining time is invisible is a state you
           cannot decide against. Absent when nothing is running, which is most of a
           shift, so the office keeps the pixels. -->
      <div v-if="buffs.length" class="fintech-buffs" data-testid="fintech-hud-buffs">
        <span
          v-for="b in buffs"
          :key="b.key"
          class="fintech-buff"
          :data-buff="b.key"
          :data-bad="b.bad ? '1' : undefined"
          >{{ b.label }} {{ b.secs }}</span
        >
      </div>

      <p v-if="link !== 'open'" class="fintech-link" data-testid="fintech-link">
        {{ link === 'connecting' ? 'связь…' : 'связь потеряна, ждём…' }}
      </p>

      <div class="fintech-stage">
        <!-- THE PLANE IS THE ROOM PLUS THE WALL OVER IT, and it is the clipper.
             The office rectangle below is the room proper: it keeps the
             catalogue's shape, it is the query container, and every coordinate is
             a fraction of IT — so nothing that maps metres to pixels knows the
             wall exists. -->
        <div class="fintech-plane" data-testid="fintech-plane">
          <div class="fintech-office" data-testid="fintech-office">
          <span
            v-for="(desk, i) in desks"
            :key="i"
            class="fintech-desk"
            data-testid="fintech-desk"
            :style="deskStyle(desk)"
          />
          <!-- THE BOTTLES — one per person on the floor, so this is a list. Each
               is placed from the catalogue by the index its bit names; a spot with
               no bit has nothing standing on it and draws nothing. -->
          <span
            v-for="b in bottlesShown"
            :key="b.i"
            class="fintech-bottle"
            data-testid="fintech-bottle"
            :style="propStyle(b)"
            aria-hidden="true"
          />
          <!-- The кальяны. The bottles' arrangement exactly. -->
          <span
            v-for="h in hookahsShown"
            :key="h.i"
            class="fintech-hookah"
            data-testid="fintech-hookah"
            :style="propStyle(h)"
            aria-hidden="true"
          />
          <!-- WHERE SOMETHING JUST HAPPENED. One short ring, keyed so that a
               second event re-mounts it and restarts the animation rather than
               being swallowed by the first. -->
          <span
            v-if="pop"
            :key="pop.key"
            class="fintech-pop"
            data-testid="fintech-pop"
            :data-kind="pop.kind"
            :style="{ '--x': String(pop.u), '--y': String(pop.v) }"
            aria-hidden="true"
          />
          <span ref="bossEl" class="fintech-boss" data-testid="fintech-boss" aria-hidden="true">
            <span v-if="bossSays" class="fintech-say" data-testid="fintech-boss-say">{{ bossSays }}</span>
            <span class="fintech-fig-body" />
            <span class="fintech-fig-head">
              <span class="fintech-fig-shine" />
              <span class="fintech-boss-grin" />
            </span>
          </span>
          <!-- СЕРЕГА AND ТЁМА, who are not playing. Rendered from the reactive
               roster like the colleagues are, so their words and their smoke are a
               vdom patch while their POSITIONS are custom properties written from
               the draw loop — the same split everything else on this plane uses.
               `data-npc` is which of them it is, and the stylesheet draws the
               T-shirt or the paraglider off it. -->
          <span
            v-for="npc in npcs"
            :key="npc.key"
            :ref="(el) => setNpcEl(npc.key, el as Element | null)"
            class="fintech-npc"
            data-testid="fintech-npc"
            :data-npc="npc.key"
            aria-hidden="true"
          >
            <span v-if="npc.say" class="fintech-say" data-testid="fintech-npc-say">{{ npc.say }}</span>
            <span class="fintech-fig-body" />
            <span class="fintech-fig-head"><span class="fintech-fig-hair" /></span>
            <!-- What tells them apart at thirty pixels: a caption on one shirt and a
                 canopy over the other. -->
            <span class="fintech-npc-mark" />
            <!-- His own кальян, in his hand, with the cloud that never goes out. No
                 logic behind either: he is scenery, and the picture is the point. -->
            <span class="fintech-npc-pipe" />
          </span>
          <!-- GONE WHILE THE ROUTER IS DOWN, and gone from the DOM rather than
               hidden: the frame stops carrying him entirely (`cl` is omitted, `ca`
               says how long for), so there is no position to draw and an element
               left behind would sit wherever he was standing when he walked off. -->
          <span
            v-if="claudeAwayMs === 0"
            ref="claudeEl"
            class="fintech-claude"
            data-testid="fintech-claude"
            aria-hidden="true"
          >
            <span v-if="claudeSays" class="fintech-say" data-testid="fintech-claude-say">{{
              claudeSays
            }}</span>
            <span class="fintech-fig-body" />
            <span class="fintech-fig-head">
              <span class="fintech-fig-hair" />
              <!-- The stubble, the cigarette and the ember. Three shapes, because a
                   figure at 30 px has to be identifiable by silhouette rather than
                   by detail — and the ember is the readout of how close he is,
                   exactly as the лысый's grin is. -->
              <span class="fintech-claude-stubble" />
              <span class="fintech-claude-cig" />
            </span>
            <!-- The mark on his shirt. A stylised burst rather than the trademark:
                 eight tapered spokes, which is what reads at thirty pixels and is
                 what makes him recognisable as the tool he is. -->
            <span class="fintech-claude-mark" />
          </span>
          <span
            v-for="peer in peers"
            :key="peer.id"
            :ref="(el) => setPeerEl(peer.id, el as Element | null)"
            class="fintech-peer"
            data-testid="fintech-peer"
            :data-peer="peer.id"
            :style="{ '--body': peerColour(peer.id) }"
            :data-cloud="peer.cloud ? '1' : undefined"
            :data-slow="peer.slow ? '1' : undefined"
            aria-hidden="true"
          >
            <span v-if="sayFor(playerLines, peer.line)" class="fintech-say" data-testid="fintech-peer-say">{{
              sayFor(playerLines, peer.line)
            }}</span>
            <span class="fintech-fig-body" />
            <span class="fintech-fig-head"><span class="fintech-fig-hair" /></span>
            <!-- WHO is standing there. The shirt colour tells two colleagues
                 apart at a glance; the face says which of your friends it is.
                 Absent until the fetch answers, and absent for good if he has no
                 picture — a 404 is the ordinary reply and means "draw a plain
                 figure", not "something went wrong". -->
            <img
              v-if="peer.avatar"
              class="fintech-face-badge"
              data-testid="fintech-peer-avatar"
              :src="peer.avatar"
              alt=""
              referrerpolicy="no-referrer"
              @error="onPeerFaceError(peer.id)"
            />
          </span>
          <span
            ref="meEl"
            class="fintech-me"
            data-testid="fintech-me"
            :data-cloud="cloudMs > 0 ? '1' : undefined"
            :data-slow="slowMs > 0 ? '1' : undefined"
            aria-hidden="true"
          >
            <span v-if="meSays" class="fintech-say" data-testid="fintech-me-say">{{ meSays }}</span>
            <span class="fintech-fig-body" />
            <span class="fintech-fig-head"><span class="fintech-fig-hair" /></span>
            <!-- YOUR OWN FACE, and it comes from the auth store rather than from
                 the wire. There is nothing to withhold from you about yourself,
                 the picture is already in memory before this view mounts, and the
                 server deliberately never sends your own handle — so the peer
                 redirector is not even reachable for you. Last child, so it paints
                 over the head: nothing in here carries a z-index, so paint order
                 is DOM order. -->
            <img
              v-if="meFace"
              class="fintech-face-badge"
              data-testid="fintech-me-avatar"
              :src="meFace"
              alt=""
              referrerpolicy="no-referrer"
              @error="onMeFaceError"
            />
          </span>
          </div>
        </div>
      </div>

      <div class="fintech-controls">
        <div
          ref="stickEl"
          class="fintech-stick"
          data-testid="fintech-stick"
          role="application"
          aria-label="Идти"
          @pointerdown="onStickDown"
          @pointermove="onStickMove"
          @pointerup="onStickUp"
          @pointercancel="onStickUp"
        >
          <span class="fintech-stick-knob" :style="knobStyle" />
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
        <div v-if="config?.redirect" class="fintech-verbs">
          <button
            v-for="peer in peers"
            :key="peer.id"
            class="fintech-verb"
            type="button"
            data-testid="fintech-redirect"
            :data-peer="peer.id"
            :style="{ '--body': peerColour(peer.id) }"
            :disabled="redirectMs > 0"
            :aria-label="config.redirect.label"
            @pointerdown.prevent="onRedirect(peer.id)"
            @click.prevent
          >
            <!-- NO `@error` HERE, deliberately. It is the same `src` as the badge on
                 his figure, which is rendered whenever this button is and does
                 carry the handler — so a CDN failure latches once and withdraws
                 both. A second handler on the same URL would be a second path to
                 one outcome. -->
            <img v-if="peer.avatar" class="fintech-verb-face" :src="peer.avatar" alt="" referrerpolicy="no-referrer" />
            <span class="fintech-verb-label">{{
              redirectMs > 0 ? formatSeconds(redirectMs) : config.redirect.label
            }}</span>
          </button>
        </div>

        <!-- THE THUMB'S COLUMN: «РОУТЕР УПАЛ» directly above РЫВОК, one over the
             other rather than side by side. The middle of the band belongs to the
             colleagues, which is where a redirect has always been — putting a
             third control there pushed them about and left three things competing
             for one thumb's width on a 360 px phone.
             The router is disabled while it is on its way back up AND while Claude
             is already away: pressing it then would be a press the office refuses,
             and a button that looks live and does nothing is worse than one that
             is visibly spent. -->
        <div class="fintech-thumb">
          <button
            v-if="config?.router"
            class="fintech-router"
            type="button"
            data-testid="fintech-router"
            :disabled="routerMs > 0 || claudeAwayMs > 0"
            :aria-label="config.router.label"
            @pointerdown.prevent="onRouter"
            @click.prevent
          >
            <!-- THE LABEL NEVER LEAVES, and the countdown appears UNDER it rather
                 than in its place. Swapping the words for «45,0 с.» changed how wide
                 and how tall the button was every time it was pressed, so the whole
                 column jumped — and the one thing a control under a thumb must not
                 do is move. Both lines live in a fixed box; only their text
                 changes. -->
            <span class="fintech-router-label">{{ config.router.label }}</span>
            <span class="fintech-router-timer">{{
              routerMs > 0 ? formatSeconds(routerMs) : claudeAwayMs > 0 ? 'НЕТ СВЯЗИ' : 'ГОТОВ'
            }}</span>
          </button>

          <button
            class="fintech-dash"
            type="button"
            data-testid="fintech-dash"
            :disabled="dashMs > 0"
            @pointerdown.prevent="onDash"
            @click.prevent
          >
            РЫВОК
          </button>
        </div>
      </div>
    </section>

    <!-- OVER — the ending the CATALOGUE names, the money, and the way back in. -->
    <section v-else class="fintech-splash" data-testid="fintech-over">
      <h1 class="fintech-title" data-testid="fintech-over-title">{{ overTitle }}</h1>
      <p class="fintech-lore">{{ overSub }}</p>
      <p class="fintech-over-salary" data-testid="fintech-over-salary">{{ money(over?.pay ?? 0) }}</p>
      <!-- The same clock the strip counted up and the boards rank by, so the
           number a player just watched is the number they are scored on. -->
      <p class="fintech-over-secs" data-testid="fintech-over-secs">
        за {{ formatClock(over?.secs ?? 0) }}
      </p>
      <!-- WHO WAS WORKING. The ending is the one screen where the persona is worth
           repeating: the shift is over, the figure is gone, and «ты был Саня» is the
           whole of the reframe in three words. -->
      <p v-if="personaName" class="fintech-over-secs" data-testid="fintech-over-who">
        ты был {{ personaName }}
      </p>
      <v-btn color="warning" size="large" data-testid="fintech-retry" :loading="starting" @click="start">
        ЕЩЁ РАЗ
      </v-btn>
      <v-btn variant="text" size="large" class="fintech-back" @click="backToSplash">НАЗАД</v-btn>
      <p v-if="error" class="fintech-error">{{ error }}</p>
    </section>
  </div>
</template>

<script setup lang="ts">
/**
 * «СИМУЛЯТОР ФИНТЕХА» — the fourth game, and the second the server simulates.
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
import { gameFintechApi } from '../api/endpoints';
import { ApiError } from '../api/client';
import type {
  FintechConfig,
  FintechRect,
  FintechShift,
  FintechShiftRow,
  FintechTopBoards,
} from '../api/types';
import {
  FINTECH_DISCLAIMER,
  FINTECH_LORE,
  buffsFor,
  buildRules,
  endingFor,
  tempoFor,
} from '../lib/fintechRules';
import {
  applyBoss,
  applyFigure,
  fintechAvatarEndpoint,
  movingFast,
  peerColour,
  sameRoster,
  type PeerLook,
  deskBox,
  sayFor,
  withName,
  formatClock,
  formatMoney,
  formatMultiplier,
  formatSeconds,
  grinState,
  planeBox,
  rampFraction,
  toPlane,
  UNIT_CQW,
} from '../lib/fintechPlane';
import { MAX_STEP_SECONDS, stepConstants } from '../lib/fintechStep';
import {
  REDUNDANT_COMMANDS,
  buildInputFrame,
  createEmitter,
  DASH_KEY,
  MOVE_KEYS,
  axesFromKeys,
  dashAxes,
  createPredictor,
  stickVector,
  type Emitter,
  type FintechAxes,
  type Predictor,
} from '../lib/fintechPredict';
import type { StepCommand, StepConstants } from '../lib/fintechStep';
import { createInterpolator, type Interpolator } from '../lib/fintechInterp';
import { realtimeClient, type ConnectionStatus, type RealtimeFrame } from '../realtime/socket';
import { useAuthStore } from '../stores/auth';

type Phase = 'splash' | 'playing' | 'over';

const phase = ref<Phase>('splash');
const loading = ref(true);
const starting = ref(false);
const error = ref('');

const config = ref<FintechConfig | null>(null);
const myShifts = ref<FintechShiftRow[]>([]);
/**
 * BOTH LEADERBOARDS, as the one request served them.
 *
 * The titles live here rather than on the server: they are jokes about not
 * working, not data — the server publishes what can be RETUNED (the rates, the
 * ramp, the endings), and a config key with exactly one possible value is not a
 * config key. The same rule the lore and the disclaimer follow.
 */
const topShifts = ref<FintechTopBoards>({ salary: [], seconds: [] });
const boards = computed(() => [
  { key: 'salary', title: 'Кто больше не работал', rows: topShifts.value.salary },
  { key: 'seconds', title: 'Кто дольше не работал', rows: topShifts.value.seconds },
]);
const hasBoards = computed(() => boards.value.some((b) => b.rows.length > 0));
const rules = computed(() => buildRules(config.value));
const desks = computed<FintechRect[]>(() => config.value?.office.desks ?? []);
/**
 * The plane's box, which is the ROOM PLUS THE WALL ABOVE IT.
 *
 * The office keeps the catalogue's own shape; the plane it sits in is taller,
 * because a figure is feet-anchored and the top wall is reachable, so at the wall
 * a man's whole box is above the room and the plane would clip it. `planeBox`
 * owns that arithmetic and is unit-tested; this is only the wiring.
 *
 * `--unit-cqw` travels with them so the stylesheet and `fintechPlane` cannot
 * disagree about how big a person is — the wall's depth is derived from exactly
 * that coefficient, so a scale change is one number in one file.
 */
const box = computed(() => {
  const o = config.value?.office;
  return planeBox(o?.w ?? 0, o?.h ?? 0);
});

/**
 * How far into the past to draw everything that is NOT predicted — the лысый,
 * Claude, the colleagues, the two who are not playing.
 *
 * SERVED, NOT COMPUTED. The office rewinds by exactly this much to decide
 * whether he reached you (its `seenBy`), so both ends have to agree on it and
 * only one of them can be authoritative. Choosing our own here would be choosing
 * how far behind the office believes this browser to be.
 *
 * The fallback matters only if the catalogue never arrived, in which case the
 * game is not playable anyway; it is the server's own value at today's rates
 * rather than a guess, so a figure is drawn in roughly the right past rather than
 * in the present.
 */
function renderDelayMs(): number {
  return config.value?.sim.render_delay_ms ?? 75;
}

/**
 * How long one simulation tick is, in milliseconds.
 *
 * The interpolation buffer is keyed on the office's TICK rather than on when a
 * frame happened to arrive, so it needs this to put a tick on a local clock —
 * see fintechInterp. Served like everything else about the rates.
 */
function tickMs(): number {
  return 1000 / (config.value?.sim.hz || 20);
}

// --- what the server last told us ------------------------------------------
// Read at ten hertz and rendered as text, which is exactly what reactivity is
// for. Positions are the opposite and never come near it — see drawFrame.
const pay = ref(0);
const mult = ref(100);
const ramp = ref(0);
const dashMs = ref(0);
/**
 * THE OFFICE'S TICK, and the tick this shift clocked in on.
 *
 * Two integers that between them carry both new readouts and cost the wire
 * nothing. `tick` is `k`, already on every snapshot because the interpolation
 * buffer is keyed on it; `startTick` is `k0`, sent once on the ready frame because
 * it is constant for the life of a shift and a repeating payload is the wrong
 * place for anything that never changes.
 *
 * `startTick` is null until the ready frame lands, which is a state rather than a
 * default: zero is a real answer — the first person into a fresh office started on
 * tick zero — so «we have not been told yet» cannot be spelled `0`. Until it
 * arrives the clock reads 0:00 rather than the age of the whole office.
 */
const tick = ref(0);
const startTick = ref<number | null>(null);
// WHICH LINE, not the line itself: the wire sends an index and the catalogue —
// fetched once — holds the words (ADR-037). Reactive because they are text and
// change at snapshot rate, which is exactly what reactivity is for; the two
// POSITIONS beside them are the opposite and never come near it.
const meLine = ref(0);
const bossLine = ref(0);
const claudeLine = ref(0);
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

/**
 * YOUR OWN FACE, which never travels on the wire.
 *
 * `Account.avatar_url` is already in memory: the store's `ensureLoaded()` gates
 * the router before this view can mount, and the shell already draws it. A peer's
 * face goes through `/api/game-fintech/avatar/{handle}` because the redirector
 * exists to stop a client learning a colleague's CDN URL from a frame — there is
 * nothing to withhold from you about yourself, and routing self through it would
 * mean inventing a self handle on the wire to fetch a picture already sitting in a
 * pinia ref.
 *
 * `|| undefined` rather than the empty string, which is what every Яндекс account
 * and every forgotten one carries.
 */
const auth = useAuthStore();
const meFaceBroken = ref(false);

/**
 * WHO YOU ARE THIS SHIFT.
 *
 * The office is a fintech rather than one man's office, so the person standing
 * perfectly still is Карен, or Андрюха, or Саня, or Темирлан, drawn when you clock in.
 * The shift response carries the INDEX and the catalogue carries the names, so
 * retuning the cast is a backend deploy — and the index rides the two shift
 * responses plus the ready frame rather than a repeating payload, because it never
 * changes for the life of a shift.
 *
 * Out of range answers the first name rather than nothing: zero is Карен and is
 * also what an older server would send by omission.
 */
const persona = ref(0);
const personaName = computed(() => {
  const cast = config.value?.personas;
  if (!cast || cast.length === 0) return '';
  return cast[persona.value] ?? cast[0];
});
const meFace = computed(() =>
  meFaceBroken.value ? undefined : auth.account?.avatar_url || undefined,
);
function onMeFaceError(): void {
  meFaceBroken.value = true;
}

/** The redirect verb's cooldown, milliseconds, straight off the snapshot. */
const redirectMs = ref(0);
/**
 * The router verb's cooldown and how long Claude is away, both straight off the
 * snapshot in milliseconds.
 *
 * The cooldown is the OFFICE'S — everybody is told the same number, because the
 * router can only fall once every wait however many people are in the room, and
 * two players both being offered a press the office would refuse is exactly the
 * disagreement a server-owned cooldown exists to prevent.
 */
const routerMs = ref(0);
const claudeAwayMs = ref(0);

/**
 * WHICH SPOTS HAVE A BOTTLE ON THEM, as a bit per catalogue spot.
 *
 * The office keeps one per person on the floor, so «which spot» stopped being a
 * question with one answer — and a mask is the encoding that did not grow with
 * it. From the catalogue by INDEX either way, never from a coordinate on the
 * frame: they move every ten seconds, and a position per prop would be twenty
 * bytes each, twenty times a second, per viewer.
 */
const bottleMask = ref(0);
/** The same, for the кальяны. */
const hookahMask = ref(0);
/** How long YOU are behind a cloud, in milliseconds. Zero means catchable. */
const cloudMs = ref(0);
/** How long the лысый stays drunk, for the row. */
const drunkMs = ref(0);
/** How long Claude Code's slow has left on YOU, in milliseconds. */
const slowMs = ref(0);

/** What is currently true of you, longest first. Absent when nothing is running. */
const buffs = computed(() =>
  buffsFor({
    cloudMs: cloudMs.value,
    drunkMs: drunkMs.value,
    slowMs: slowMs.value,
    awayMs: claudeAwayMs.value,
  }),
);

/**
 * Every prop of one kind that is standing right now, ready to draw.
 *
 * PURE, AND SHARED BY BOTH KINDS, because a bottle and a кальян differ only in
 * what they look like and what taking one does — neither of which is this
 * function's business. It returns the catalogue INDEX beside the coordinates so
 * the `v-for` has a stable key that survives a prop being taken somewhere else in
 * the room.
 */
function standingProps(
  spots: { x: number; y: number }[] | undefined,
  mask: number,
): { i: number; u: number; v: number }[] {
  if (!spots || !spots.length || !constants) return [];
  const out: { i: number; u: number; v: number }[] = [];
  for (let i = 0; i < spots.length; i++) {
    if (!(mask & (1 << i))) continue;
    const at = toPlane(spots[i].x, spots[i].y, constants.officeW, constants.officeH);
    out.push({ i, u: at.u, v: at.v });
  }
  return out;
}

const bottlesShown = computed(() => standingProps(config.value?.bottle?.spots, bottleMask.value));
const hookahsShown = computed(() => standingProps(config.value?.hookah?.spots, hookahMask.value));

const propStyle = (p: { u: number; v: number }) => ({ '--x': String(p.u), '--y': String(p.v) });

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
/**
 * Takes the router down, which takes Claude off the floor.
 *
 * No target: there is one Claude and the effect is the whole office's. Like every
 * verb here it is not answered — the outcome arrives as the next snapshot, which
 * is what stops this client believing something the office refused.
 */
function onRouter(): void {
  if (!shift || routerMs.value > 0 || claudeAwayMs.value > 0) return;
  realtimeClient(shift.room).send({ t: 'fintech_do', v: 'router' });
}

function onRedirect(peerID: string): void {
  if (!shift || redirectMs.value > 0) return;
  realtimeClient(shift.room).send({ t: 'fintech_do', v: 'redirect', tg: peerID });
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
 * Marks every prop of one kind that has just been taken.
 *
 * A BIT GOING 1 → 0 IS THE WHOLE EVENT: it says both that something was taken and
 * WHICH ONE, so the ring lands on the spot it happened at rather than on "a
 * bottle somewhere". A bit going the other way is a prop coming back, which is
 * not a thing anybody did and gets no mark.
 *
 * It marks at most one per frame in practice — two props being taken on the same
 * tick is possible in a full office, and the last one wins because there is one
 * ring. That is the right trade: two rings a fifth of a second apart read as one
 * flicker, and the plane's rule is that a mark says WHERE, briefly, and nothing
 * more.
 */
function markTaken(
  spots: { x: number; y: number }[] | undefined,
  was: number,
  now: number,
  kind: string,
): void {
  if (!spots || !constants) return;
  const taken = was & ~now;
  if (!taken) return;
  for (let i = 0; i < spots.length; i++) {
    if (!(taken & (1 << i))) continue;
    markAt(kind, toPlane(spots[i].x, spots[i].y, constants.officeW, constants.officeH));
  }
}

/** Where Claude last was, which is where he walked off from. */
function claudePlace(): { u: number; v: number } | null {
  if (!claudeAt || !constants) return null;
  return toPlane(claudeAt.x, claudeAt.y, constants.officeW, constants.officeH);
}

/**
 * Which line index is «ЭТО НУЖНО УТОЧНИТЬ У ДРУГОГО».
 *
 * FOUND BY MATCHING THE STRING THE CATALOGUE ALREADY PUBLISHES, rather than by
 * serving an index or hardcoding one. `redirect.say` and `player_lines` are both
 * already on the config this client fetched once, so the join costs nothing on
 * the wire and nothing at runtime — and the server can still rearrange its pools
 * without a client deploy, which is the property that made the layout server-side
 * in the first place.
 */
const redirectLine = computed(() => {
  const say = config.value?.redirect?.say;
  const lines = config.value?.player_lines;
  if (!say || !lines) return -1;
  return lines.indexOf(say);
});

// THERE IS NO `routerLine` HERE, unlike the redirect's, and the asymmetry is
// deliberate. The redirect has to be derived from the balloon index because
// nothing else on the frame says it happened. The router does say so — `ca` is on
// every occupant's frame, because the effect is the office's — so the mark is
// taken off that edge instead, and lands where Claude was standing rather than on
// whoever pressed the button. Who pressed it is the balloon's job, and «РОУТЕР
// УПАЛ» is already over their head on every screen.

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

/**
 * WHERE THE OFFICE'S TEMPO HAS GOT TO, derived rather than sent.
 *
 * `tempoFor` is the port of the server's `TempoAt`; both read the same two served
 * constants against the office tick this frame already carries. See the helper for
 * why the ramp is not a field on the wire.
 */
const tempo = computed(() =>
  tempoFor(
    tick.value,
    config.value?.sim.hz,
    config.value?.tempo?.every_ms,
    config.value?.tempo?.step_pct,
  ),
);

/**
 * The multiplier as the strip shows it, through the SAME formatter the money ramp
 * uses — so ×1,1 and ×2,75 are punctuated identically and a player reads one kind
 * of number rather than two.
 */
const tempoLabel = computed(() => formatMultiplier(Math.round(tempo.value.mult * 100)));

/** How long this shift has lasted, in seconds. See `startTick` for why it is null-gated. */
const aliveSecs = computed(() => {
  const k0 = startTick.value;
  if (k0 === null) return 0;
  return Math.max(0, (tick.value - k0) / (config.value?.sim.hz || 20));
});

/**
 * True for a moment after the tempo steps up.
 *
 * A LEVEL-UP IS AN EVENT NOBODY CAN SEE. Both men simply start walking 10 % faster,
 * which is under the threshold at which a moving figure looks different — so
 * without a mark the office quietly gets harder and reads as the лысый behaving
 * inconsistently. It is one cell flashing for well under a second, cleared on a
 * TIMER rather than on `animationend` (which never fires when the animation is
 * switched off under prefers-reduced-motion), exactly like the plane's own marks.
 */
const tempoBump = ref(false);
let tempoBumpTimer: number | undefined;
function bumpTempo(): void {
  tempoBump.value = true;
  if (tempoBumpTimer !== undefined) window.clearTimeout(tempoBumpTimer);
  tempoBumpTimer = window.setTimeout(() => {
    tempoBump.value = false;
  }, 900);
}

function onPeerFaceError(id: string): void {
  const next = new Set(brokenFaces.value);
  next.add(id);
  brokenFaces.value = next;
}
const playerLines = computed(() => config.value?.player_lines);

const meSays = computed(() => sayFor(config.value?.player_lines, meLine.value));
const claudeSays = computed(() => sayFor(config.value?.claude_lines, claudeLine.value));

const bossSays = computed(() => {
  const line = sayFor(config.value?.boss_lines, bossLine.value);
  // HE NAMES WHOEVER VANISHED, and only the client that vanished can fill it in:
  // the server sends the templated line to that occupant alone, and a persona is
  // never sent for anybody else. Any other screen gets «ОН», which is what the
  // fallback is for.
  return withName(line, cloudMs.value > 0 ? personaName.value : '');
});

const overTitle = computed(() => endingFor(config.value, over.value?.cause ?? '')?.title ?? 'СМЕНА ОКОНЧЕНА');
const overSub = computed(() => endingFor(config.value, over.value?.cause ?? '')?.sub ?? '');

const meEl = ref<HTMLElement | null>(null);
const bossEl = ref<HTMLElement | null>(null);
const claudeEl = ref<HTMLElement | null>(null);
const stickEl = ref<HTMLElement | null>(null);

let shift: FintechShift | null = null;
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
// arrived. See fintechInterp.
let bossInterp: Interpolator | null = null;
let claudeInterp: Interpolator | null = null;
let claudeAt: { x: number; y: number } | null = null;
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
// Who was behind a cloud on the previous frame, so the mark lands on the EDGE.
const peerClouds = new Set<string>();
// And who was slowed, for the same edge comparison.
const peerSlows = new Set<string>();
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
let axes: FintechAxes = { mx: 0, my: 0 };

const knobStyle = computed(() => ({
  transform: `translate3d(${knob.value.dx}px, ${knob.value.dy}px, 0)`,
}));

const money = (v: number) => formatMoney(v);
// A shift's length is `formatClock` everywhere it appears now — the strip, both
// boards, your own list and the ending — so the number a player watched counting
// up is the number they are ranked by. The old `decimal(v, 0)` wrapper that
// printed «73 с» went with the last screen that used it.

/** How a finished shift is marked in the list. Presentation, not a rule. */
function causeIcon(cause: string): string {
  return cause === 'promoted' ? '🎉' : '🚪';
}

function deskStyle(d: FintechRect): Record<string, string> {
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
    config.value = await gameFintechApi.config();
  } catch (e) {
    error.value = e instanceof ApiError ? `не открылось (${e.code})` : 'не открылось';
  }
  await loadLists();

  // A shift may already be going — a reload, or the game open on another tab.
  // The office outlives a dropped socket by design, so pick it back up rather
  // than stranding the player behind a button that answers 409.
  try {
    shift = await gameFintechApi.current();
    enterPlay();
  } catch {
    // 404 is the ordinary answer. Nothing to resume.
  }
  loading.value = false;
});

// Bound on the WINDOW rather than on an element, because nothing in the play screen
// is focusable except the two buttons — a keyboard player never has focus anywhere a
// local listener would fire. Guarded on `phase` inside the handler instead, so a key
// pressed on the splash or the over screen does nothing.
window.addEventListener('keydown', onKeyDown);
window.addEventListener('keyup', onKeyUp);
window.addEventListener('blur', releaseKeys);

onBeforeUnmount(() => {
  window.removeEventListener('keydown', onKeyDown);
  window.removeEventListener('keyup', onKeyUp);
  window.removeEventListener('blur', releaseKeys);
  teardownPlay();
});

async function loadLists(): Promise<void> {
  const [mine, top] = await Promise.allSettled([gameFintechApi.myShifts(), gameFintechApi.topShifts()]);
  myShifts.value = mine.status === 'fulfilled' ? mine.value.shifts : [];
  // Both boards come from ONE request, and each half is defaulted separately: a
  // server a version behind sends neither key, and the splash then simply has no
  // boards rather than throwing on the way to the start button.
  topShifts.value = {
    salary: (top.status === 'fulfilled' ? top.value.salary : undefined) ?? [],
    seconds: (top.status === 'fulfilled' ? top.value.seconds : undefined) ?? [],
  };
}

async function start(): Promise<void> {
  if (starting.value) return;
  starting.value = true;
  error.value = '';
  try {
    shift = await gameFintechApi.start();
    persona.value = shift.persona ?? 0;
    enterPlay();
  } catch (e) {
    if (e instanceof ApiError && e.code === 'shift_in_progress') {
      // Somebody's other tab. Resuming is the right answer, not a second shift.
      try {
        shift = await gameFintechApi.current();
        persona.value = shift.persona ?? 0;
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
    await gameFintechApi.leave();
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
  // BOTH CLOCKS BACK TO NOTHING. The office's tick is whatever the first snapshot
  // says, and the shift's start is whatever the ready frame says — until then the
  // readout is 0:00 rather than the last shift's ending.
  tick.value = 0;
  startTick.value = null;
  tempoBump.value = false;
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

  bossInterp = createInterpolator(renderDelayMs(), tickMs());
  claudeInterp = createInterpolator(renderDelayMs(), tickMs());
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
  if (tempoBumpTimer !== undefined) window.clearTimeout(tempoBumpTimer);
  tempoBumpTimer = undefined;
  tempoBump.value = false;
  brokenFaces.value = new Set();
  meFaceBroken.value = false;
  redirectMs.value = 0;
  routerMs.value = 0;
  claudeAwayMs.value = 0;
  bottleMask.value = 0;
  hookahMask.value = 0;
  cloudMs.value = 0;
  drunkMs.value = 0;
  slowMs.value = 0;
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
  claudeInterp = null;
  claudeAt = null;
  npcs.value = [];
  npcEls.clear();
  npcInterp.clear();
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
    placeClaude(now);
    placeNpcs(now);
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
// Gambetta rung, and the reason it is worth a module is in fintechInterp.
function placeClaude(now: number): void {
  const el = claudeEl.value;
  if (!el || !claudeInterp || !constants) return;
  const at = claudeInterp.at(now);
  if (!at) return;
  // `applyBoss` rather than a third writer: the cigarette is the grin's property
  // and the stylesheet reads it from `--grin` on this figure too, so the same call
  // writes the position, the band, the depth, the balloon flip and the ember.
  applyBoss(el, toPlane(at.x, at.y, constants.officeW, constants.officeH), at.grin);
}

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
/** One of the two non-players, as the template needs him. */
interface NpcShown {
  key: string;
  say: string;
}

const npcs = ref<NpcShown[]>([]);
const npcEls = new Map<string, HTMLElement>();
const npcInterp = new Map<string, Interpolator>();

function setNpcEl(key: string, el: Element | null): void {
  if (!el) {
    npcEls.delete(key);
    return;
  }
  const node = el as HTMLElement;
  if (npcEls.get(key) === node) return;
  npcEls.set(key, node);
  const at = npcInterp.get(key)?.at(performance.now());
  if (at && constants) applyFigure(node, toPlane(at.x, at.y, constants.officeW, constants.officeH));
}

/**
 * Folds the two non-players into the same two tiers everybody else uses.
 *
 * `np` is never omitted — they are always on the floor — so unlike `pr` there is no
 * absent case to mean «nobody here». The catalogue's ORDER is which of them each
 * entry is, so the frame carries no name and no key.
 */
function applyNpcs(raw: unknown, tick: number): void {
  const cast = config.value?.npcs ?? [];
  const list = Array.isArray(raw) ? (raw as Record<string, number>[]) : [];
  const shown: NpcShown[] = [];
  for (let i = 0; i < list.length && i < cast.length; i++) {
    const f = list[i];
    const kind = cast[i];
    shown.push({ key: kind.key, say: sayFor(kind.lines, num(f.p)) });
    let interp = npcInterp.get(kind.key);
    if (!interp) {
      interp = createInterpolator(renderDelayMs(), tickMs());
      npcInterp.set(kind.key, interp);
    }
    interp.push({ x: num(f.x) / 100, y: num(f.y) / 100, grin: 0 }, tick, performance.now());
  }
  // Membership and speech through Vue, positions never — and only when something
  // actually changed, so an ambling man does not cost a vdom patch ten times a
  // second for words that have not moved.
  if (!sameNpcs(npcs.value, shown)) npcs.value = shown;
}

function sameNpcs(a: readonly NpcShown[], b: readonly NpcShown[]): boolean {
  if (a.length !== b.length) return false;
  return a.every((n, i) => n.key === b[i].key && n.say === b[i].say);
}

function placeNpcs(now: number): void {
  if (!constants) return;
  for (const [key, interp] of npcInterp) {
    const el = npcEls.get(key);
    const at = interp.at(now);
    if (el && at) applyFigure(el, toPlane(at.x, at.y, constants.officeW, constants.officeH));
  }
}

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
    case 'fintech_ready':
      // The office has us. Until this lands the socket may be open while the
      // occupant is not yet attached, which is a different thing and is what the
      // link line is honestly reporting.
      link.value = 'open';
      // AND WHO WE ARE, which a second device or a reconnect would not otherwise
      // know: this tab may be attaching to a shift it did not start.
      if (typeof frame.persona === 'number') persona.value = frame.persona;
      // AND WHEN THE SHIFT STARTED, as an office tick. Everything else about the
      // clock is subtraction — see `startTick`. Read from a frame that arrives once
      // per attach, so a reconnect mid-shift recovers the true age rather than
      // restarting the readout at zero.
      if (typeof frame.k0 === 'number') startTick.value = frame.k0;
      break;
    case 'fintech_snap':
      applySnapshot(frame);
      break;
    case 'fintech_over':
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
  // THE OFFICE'S CLOCK, which drives both derived readouts. The level is compared
  // BEFORE the tick is written, because the comparison is against the previous
  // frame and an assignment first would destroy the edge being looked for — the
  // mistake this function has already made once, a few lines below.
  const wasLevel = tempo.value.level;
  tick.value = num(frame.k);
  if (tempo.value.level > wasLevel) bumpTempo();

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
  // THE PROPS, AND THE MARK COMES OFF THE MASK ITSELF. A bit that was set and is
  // no longer means somebody has just taken THAT one — which is both the edge and
  // the place, so the mark lands where it happened even in a room where three
  // bottles are standing and one of them goes. Compared before the ref is written,
  // because the previous value is what the comparison is against.
  markTaken(config.value?.bottle?.spots, bottleMask.value, num(frame.bs), 'bottle');
  markTaken(config.value?.hookah?.spots, hookahMask.value, num(frame.hs), 'hookah');
  bottleMask.value = num(frame.bs);
  hookahMask.value = num(frame.hs);
  redirectMs.value = num(frame.rc);

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

  // THE CLOUD, marked on the EDGE and where it happened. A cloud is a buff rather
  // than an event, so it is drawn on the figure for the whole ten seconds — but its
  // ARRIVAL is a verb nobody can see, so it also gets the brief mark every other
  // verb on this plane gets. Compared against the previous frame's value, before
  // that value is overwritten, which is the mistake this view has already made
  // once.
  // BEING CAUGHT BY CLAUDE IS A VERB DONE TO YOU, so it gets the mark every verb on
  // this plane gets — on the EDGE, and before the previous value is overwritten.
  const sl = num(frame.sl);
  if (sl > 0 && slowMs.value === 0) markAt('slow', mePlace());
  slowMs.value = sl;
  const iv = num(frame.iv);
  if (iv > 0 && cloudMs.value === 0) markAt('cloud', mePlace());
  cloudMs.value = iv;

  if (!predictor) {
    // The first authoritative position is what the predictor is seeded with —
    // see enterPlay for why there is nothing better to start from.
    predictor = createPredictor({ desks: desks.value, constants, start: { x, y } });
  }
  // The cooldown is folded in beside the position, because the client SIMULATES
  // from it: `step` refuses a dash while it is running, and a client whose own
  // copy had gone stale would refuse the dash the server was about to grant.
  // The slow is folded in beside the cooldown and for the identical reason: the
  // predicted player's own timer only advances when a command is emitted, and a
  // player standing perfectly still emits nothing — so a locally-held slow would
  // still be running long after the office had let it expire, and the client would
  // predict 5.12 m/s against the server's 6.4.
  predictor.reconcile({
    x,
    y,
    ack,
    dashCooldown: dashMs.value / 1000,
    slowLeft: slowMs.value / 1000,
  });

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
    // a `background` on `.fintech-boss` paints the positioning rectangle and reads
    // as a broken sprite (§17.5 of the build plan, learned the hard way).
    const drunk = num(b.d) > 0;
    // The ROW needs the number, not the boolean — and it is reactive where the
    // class flip above deliberately is not: a readout is text and belongs in Vue,
    // a class on a figure is a patch we avoid on every frame.
    drunkMs.value = num(b.d);
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
    bossInterp?.push({ x: bossAt.x, y: bossAt.y, grin }, seenTick, performance.now());
  }

  // THE ROUTER, read before Claude because it decides whether there is a Claude to
  // read. `ca` is how long he is away and `rd` is when the button comes back —
  // both omitted in the resting state, so absent means zero and never "unchanged".
  const wasAway = claudeAwayMs.value;
  const ca = num(frame.ca);
  claudeAwayMs.value = ca;
  routerMs.value = num(frame.rd);
  // MARKED WHERE HE WAS STANDING when he walked off, on the EDGE — the office
  // getting quieter is a thing that HAPPENED, and the only place it happened is
  // wherever the man who is now missing used to be. Compared before the buffer is
  // dropped below, because that is what still knows where he was.
  if (ca > 0 && wasAway === 0) markAt('router', claudePlace());
  if (ca > 0) {
    // NOTHING BUFFERED WHILE HE IS GONE. The interpolator eases between the last
    // two samples it holds, so keeping them would have him drifting on across a
    // floor he is not on — and when he returns it is at his spawn, which is not a
    // step from wherever he vanished but a jump the buffer must not smooth.
    claudeInterp = createInterpolator(renderDelayMs(), tickMs());
    claudeAt = null;
  }

  // CLAUDE, buffered exactly as the лысый is: not predicted, because his intent is
  // no more ours to guess than the other man's, so his position arrives ten times a
  // second and is eased across the gap between two frames. Absent while the router
  // is down, which is the one state where `cl` is not on the frame at all.
  const cl = frame.cl as Record<string, number> | undefined;
  if (cl) {
    claudeLine.value = num(cl.p);
    claudeAt = { x: num(cl.x) / 100, y: num(cl.y) / 100 };
    claudeInterp?.push({ x: claudeAt.x, y: claudeAt.y, grin: num(cl.c) / 255 }, seenTick, performance.now());
  }

  applyNpcs(frame.np, seenTick);
  applyPeers(frame.pr, seenTick);
}

/**
 * Folds the office's other occupants into the two tiers.
 *
 * `pr` is OMITTED when you are alone, which is the common case and the reason it
 * costs nothing on the wire — so absent means "nobody here", never "unchanged".
 * Read the other way a colleague who walked out would stand in the office for
 * the rest of the shift.
 */
function applyPeers(raw: unknown, tick: number): void {
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
    const cloud = num(p.iv) > 0;
    const slow = num(p.sl) > 0;
    // A COLLEAGUE'S cloud, marked where HE is standing, on the edge — the same rule
    // as his redirect. Somebody stepping out of the лысый's reach is the most
    // useful thing that can happen to a colleague, and a mark only its owner saw
    // would be exactly the asymmetry this office forbids.
    if (cloud && !peerClouds.has(id) && constants) {
      markAt('cloud', toPlane(num(p.x) / 100, num(p.y) / 100, constants.officeW, constants.officeH));
    }
    if (cloud) peerClouds.add(id);
    else peerClouds.delete(id);
    // A COLLEAGUE being caught, marked where HE is standing. Same edge rule, and the
    // same reason: him losing a fifth of his walk is who the лысый reaches first.
    if (slow && !peerSlows.has(id) && constants) {
      markAt('slow', toPlane(num(p.x) / 100, num(p.y) / 100, constants.officeW, constants.officeH));
    }
    if (slow) peerSlows.add(id);
    else peerSlows.delete(id);
    roster.push({
      id,
      line,
      cloud,
      slow,
      // Derived from the handle, never sent with it (ADR-037). The browser
      // caches the answer, so this is one request per colleague per shift even
      // though the string is recomputed on every roster change.
      avatar: brokenFaces.value.has(id) ? undefined : fintechAvatarEndpoint(id),
    });
    let interp = peerInterp.get(id);
    if (!interp) {
      // Same period as the лысый's, from the same served rate, so everybody who
      // is not predicted is drawn at the same instant in the past.
      interp = createInterpolator(renderDelayMs(), tickMs());
      peerInterp.set(id, interp);
    }
    // Buffered, never drawn here — see placePeers.
    interp.push({ x: num(p.x) / 100, y: num(p.y) / 100 }, tick, now);
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
    if (shift) realtimeClient(shift.room).send({ t: 'fintech_hello' });
    // Still 'connecting' until fintech_ready: an open socket is not yet a desk.
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
 * WASD (or the arrows) and space, for anybody playing at a desk.
 *
 * THE SAME SEAM THE THUMB USES. Both write the module's one `axes` value, which the
 * emitter samples at the served rate — so there is one input path, the prediction and
 * the wire cannot tell a key from a stick, and nothing about the netcode had to learn
 * about keyboards.
 *
 * HELD KEYS RATHER THAN EVENTS, because a walk is a state and a keypress is not: on a
 * key repeat the browser fires `keydown` over and over, and building an axis per event
 * would make movement a function of the repeat rate.
 *
 * CLEARED ON BLUR, which is the bug this would otherwise ship with: alt-tab away
 * mid-walk and the `keyup` lands on the other window, so the office keeps walking you
 * into a wall until you come back. The same reason the stick clears on
 * `pointercancel`.
 */
const held = new Set<string>();

function onKeyDown(e: KeyboardEvent): void {
  if (phase.value !== 'playing') return;
  if (e.code === DASH_KEY) {
    // Space scrolls a page by default, and while a shift is running the page must not
    // move — the office is the screen. Prevented before the repeat guard, so holding it
    // does not scroll either.
    e.preventDefault();
    if (!e.repeat) onDash();
    return;
  }
  if (!MOVE_KEYS.includes(e.code)) return;
  e.preventDefault();
  held.add(e.code);
  axes = axesFromKeys(held);
}

function onKeyUp(e: KeyboardEvent): void {
  if (!held.delete(e.code)) return;
  axes = axesFromKeys(held);
}

function releaseKeys(): void {
  if (held.size === 0) return;
  held.clear();
  axes = axesFromKeys(held);
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
.fintech-root {
  height: calc(100dvh - 72px);
  overflow: hidden;
  position: relative;
}

/* --- splash and ending ---------------------------------------------------- */

.fintech-splash {
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

.fintech-title {
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

.fintech-lore {
  margin: 0;
  max-width: 34rem;
  font-size: 0.95rem;
  line-height: 1.45;
  opacity: 0.85;
}

/* Quiet, and at the very bottom. It has to be legible — that is the whole point
   of it — so it is dimmed rather than shrunk past reading. */
.fintech-disclaimer {
  margin: 4px 0 0;
  max-width: 34rem;
  font-size: 0.72rem;
  line-height: 1.35;
  opacity: 0.55;
}

.fintech-rules,
.fintech-list {
  width: 100%;
  max-width: 34rem;
  text-align: left;
}

.fintech-rules {
  display: flex;
  flex-direction: column;
  gap: 12px;
}

.fintech-rule-title {
  font-size: 0.8rem;
  text-transform: uppercase;
  letter-spacing: 0.12em;
  opacity: 0.6;
  margin: 0 0 4px;
}

.fintech-rule-list {
  margin: 0;
  display: grid;
  grid-template-columns: minmax(6.5rem, auto) 1fr;
  gap: 4px 10px;
  font-size: 0.86rem;
  line-height: 1.35;
}

.fintech-rule-list dt {
  font-weight: 700;
  /* NOT `nowrap`: these labels are Russian words rather than «ВАНЯДУМ»'s two-
     character icons, and at 360px an unbreakable «👨‍🦲 скорость» pushes the grid
     past the viewport. */
  overflow-wrap: anywhere;
}

.fintech-rule-list dd {
  margin: 0;
  opacity: 0.85;
  overflow-wrap: anywhere;
}

.fintech-list ul,
.fintech-list ol {
  margin: 0;
  padding-left: 1.2rem;
  font-size: 0.86rem;
  line-height: 1.5;
}

.fintech-list li {
  overflow-wrap: anywhere;
}

/* THE TWO BOARDS, side by side where there is room and stacked where there is
   not — one column on a phone, two from 560 px up, which is where a 34 rem card
   can hold two readable columns of «имя — деньги · время». `auto-fit` rather
   than a media query, so a board that is absent (nobody has played that way yet)
   leaves the other one full width instead of half of it. */
.fintech-boards {
  display: grid;
  grid-template-columns: repeat(auto-fit, minmax(15rem, 1fr));
  gap: 12px 18px;
  width: 100%;
  max-width: 34rem;
}

/* A NAME AND ITS SCORE ON ONE ROW, with the score pushed right and never
   wrapped: the boards are read by scanning the numbers down a column, and a
   «1 234 567 ₽ · 12:03» that broke across two lines would destroy that. The name
   is the part allowed to shrink, because a long one is the author's own problem
   and an ellipsis still identifies them. */
.fintech-boards li {
  display: flex;
  align-items: baseline;
  gap: 8px;
}

.fintech-board-name {
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
  min-width: 0;
}

.fintech-board-score {
  margin-left: auto;
  white-space: nowrap;
  font-variant-numeric: tabular-nums;
  color: #f0b429;
  font-weight: 700;
}

.fintech-start,
.fintech-back {
  min-height: 48px;
  font-weight: 900;
  letter-spacing: 0.06em;
}

.fintech-over-salary {
  margin: 0;
  font-size: clamp(28px, 10vw, 44px);
  font-weight: 900;
  font-variant-numeric: tabular-nums;
  color: #f0b429;
}

.fintech-over-secs {
  margin: 0;
  opacity: 0.7;
}

.fintech-error {
  font-size: 0.85rem;
  color: #ffb4a8;
  max-width: 30rem;
}

.fintech-loading {
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
.fintech-play {
  position: absolute;
  inset: 0;
  overflow: hidden;
  /* A SIZE CONTAINER, so the readouts and the thumbs can be given the same width as
     the room. Without it they were laid out against the whole play box: on a phone
     that is the same thing, because the plane is full-bleed — on a desktop the room is
     a column in the middle and the money ended up in the far top-left corner with the
     quit button in the far top-right, an armspan away from the office they describe.
     The stage below is its own size container, so nothing inside the plane changed. */
  container-type: size;
  background: #16181d;
  color: rgba(255, 255, 255, 0.94);

  /* THE ROOM'S OWN WIDTH, for everything drawn on top of it.
   The same expression `.fintech-plane` sizes itself with, so the readouts, the buff
   row and the two thumbs line up with the office's edges rather than the screen's.
   On a phone this is the whole width and changes nothing; on a desktop it is what
   stops the HUD being an armspan away from the room it describes. */
.fintech-hud,
.fintech-streak,
.fintech-buffs,
.fintech-controls {
  width: min(100cqw, calc(100cqh * var(--box-ratio, 0.718)));
  margin-inline: auto;
}

/* HOW TALL THE READOUTS ARE, in one place, because more than one thing has to
     agree with it and none of them can measure it.

     It is the HUD strip plus the streak bar: `.fintech-quit`'s `min-height: 44px`
     plus 6 px of padding top and bottom is 56, and the streak is 6 px with 4 px
     of margin under it. Both are in normal flow at the top of this box.

     THE STAGE IS NOT INSET BY IT, and that is worth writing down because it looks
     like a bug and is not. The readouts DO stand over the office — they are
     transparent, text with a shadow and no background at all, which is the whole
     point of the overlay layout: the room gets the entire box and the numbers
     float on it. A figure standing at the top wall is therefore drawn among a few
     glyphs of ЗАРПЛАТА rather than behind a panel, which is a cosmetic overlap
     and not the reported bug — the reported bug was that he was DELETED, by the
     plane's `overflow: hidden`, and that is what the wall fixes.
     Insetting the stage was tried, and it costs a phone its full-width office:
     the plane becomes bounded by height instead of width and the room draws ~7 %
     narrower with dead space down both sides. That trade is the wrong way round. */
  --fintech-hud-h: 66px;

  /* WITH THE BUFF ROW, when one is running: 66 plus a ~24 px strip. It is a
     separate property rather than a bigger `--fintech-hud-h`, because the row is
     ABSENT most of the time and the «связь…» line should not sit lower for a state
     that is not on screen. This is why the height was declared in one place. */
  --fintech-hud-h-buffs: 90px;
}

/* IN FLOW, BUT OVER THE OFFICE. `.fintech-play` is no longer a column, so normal
   flow starts at its top edge and stacks these two exactly where they were —
   while `z-index` puts them above the stage behind them. Keeping them in flow
   rather than absolutely positioning each is what lets the streak sit under the
   HUD without anybody having to know how tall the HUD is.
   `pointer-events: none` because they are readouts standing on the floor now: a
   tap that lands on the word ЗАРПЛАТА has to reach the office, not be eaten by
   a label. The one real control in here turns them back on for itself. */
.fintech-hud {
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

.fintech-hud-cell {
  display: flex;
  flex-direction: column;
  line-height: 1.15;
  min-width: 0;
}

.fintech-hud-label {
  font-size: 0.62rem;
  letter-spacing: 0.1em;
  opacity: 0.55;
}

.fintech-hud-value {
  font-size: 1.05rem;
  font-weight: 800;
  color: #f0b429;
  white-space: nowrap;
}

.fintech-hud-mult {
  font-size: 1.05rem;
  font-weight: 800;
  white-space: nowrap;
}

/* THE TEMPO CELL, and the one moment it is worth looking at.

   Ordinarily it is the same grey-labelled readout as the rest of the strip. When
   the office steps up a level `data-bump` lands for well under a second: the value
   goes red-hot and grows a fraction, which is enough to catch an eye that is
   watching a man walk across a room and small enough not to take that eye off him.
   No shake, no flash of the plane, no sound — the rule for every mark in this game. */
.fintech-hud-tempo[data-bump] .fintech-hud-value {
  color: #ff6b57;
  animation: fintech-tempo-bump 900ms ease-out;
}

@keyframes fintech-tempo-bump {
  0% {
    scale: 1;
  }
  22% {
    scale: 1.28;
  }
  100% {
    scale: 1;
  }
}

.fintech-hud-dash {
  margin-left: auto;
  font-size: 0.66rem;
  letter-spacing: 0.06em;
  opacity: 0.72;
  text-align: right;
  white-space: nowrap;
}

/* The way out is a real target and sits at the top, clear of both thumbs — the
   yard shipped a control standing where another one takes the tap once. */
.fintech-quit {
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
.fintech-streak {
  position: relative;
  z-index: 2;
  pointer-events: none;
  height: 6px;
  margin: 0 10px 4px;
  border-radius: 3px;
  background: rgba(255, 255, 255, 0.1);
  overflow: hidden;
}

.fintech-streak-fill {
  display: block;
  height: 100%;
  width: 100%;
  border-radius: 3px;
  background: linear-gradient(90deg, #6b8f3a, #f0b429);
  transform-origin: 0 50%;
  transform: scaleX(var(--fill, 0));
  transition: transform 120ms linear;
}

/* THE BUFF ROW — the third in-flow thing at the top, under the streak bar.
   `pointer-events: none` because it is a readout standing on the floor like the
   rest of them: a tap that lands on «в дыму» has to reach the office.
   The top strip is the only free one — the bottom third of the plane is asserted to
   remain office so the thumbs have somewhere to rest, and the right-hand column
   already grows upward as colleagues arrive. */
.fintech-buffs {
  position: relative;
  z-index: 2;
  display: flex;
  flex-wrap: wrap;
  gap: 6px;
  padding: 0 10px 4px;
  pointer-events: none;
  font-variant-numeric: tabular-nums;
}

.fintech-buff {
  padding: 2px 8px;
  border-radius: 10px;
  background: rgba(12, 14, 18, 0.66);
  border: 1px solid rgba(255, 255, 255, 0.22);
  color: rgba(255, 255, 255, 0.94);
  font-size: 0.62rem;
  font-weight: 700;
  text-shadow: 0 1px 2px rgba(0, 0, 0, 0.9);
  white-space: nowrap;
}

/* Anything working against you reads warm, so the row can be scanned rather than
   read. Nothing sets this yet — the slow arrives with the second chaser — and it is
   here because the row's shape is decided now rather than twice. */
.fintech-buff[data-bad] {
  border-color: rgba(224, 100, 74, 0.75);
  color: #ffcbb8;
}

.fintech-link {
  position: absolute;
  /* Just under the readouts, derived rather than measured a second time: this
     used to be a hardcoded 56px that happened to agree with the HUD's height. */
  top: calc(var(--fintech-hud-h) - 10px);
  left: 12px;
  margin: 0;
  font-size: 0.76rem;
  color: #ffd28a;
  pointer-events: none;
}

/* The stage is the only flexible child, and `min-height: 0` is what lets it give
   up space rather than pushing the controls off a short screen.

   IT FILLS THE BOX, READOUTS AND ALL, and that is deliberate — see the note on
   `.fintech-play`. Insetting it below the HUD was tried and reverted: it costs a
   phone its full-width office, because the plane then becomes bounded by height
   rather than width and the room draws ~7 % narrower with dead space down both
   sides, which is the exact defect the overlay layout exists to remove. */
/* THE PANE IS FULL-BLEED; THE ROOM INSIDE IT IS NOT, AND CANNOT BE.

   The office is a portrait 16 × 22 room and the plane keeps that shape exactly —
   distorting it would move every metre-to-pixel mapping in the game — so the
   plane can only ever fill ONE axis of whatever box it is given. Measured: on a
   360 × 800 phone it is the full 360 wide and 501 of 728 tall; on a 1440 × 900
   desktop it is 595 of 1184 wide. The rest is unavoidable.

   What WAS avoidable is that the rest used to be flat `#16181d` while the plane
   carried a gradient, so the game read as a card sitting on a slab of a different
   colour — "the pane is not full width". So the wall's own gradient is drawn HERE,
   across the whole play box, and the plane is the same material with its edges
   marked. Nothing about geometry moves: this element is `inset: 0` and paints
   only, and the room stays the exact size the catalogue's ratio says. */
.fintech-stage {
  position: absolute;
  inset: 0;
  display: flex;
  align-items: center;
  justify-content: center;
  container-type: size;
  background: linear-gradient(180deg, #14161b, #1c1f26 55%, #23262d);
}

/* THE PLANE IS THE ROOM PLUS THE WALL ABOVE IT, AND IT IS THE CLIPPER.
   `--box-ratio` is w / (h + wall), written by the template from `planeBox`, so
   `min(100cqw, 100cqh × box-ratio)` is still the largest box of that shape that
   fits whichever way the stage is shaped.

   WHY THERE ARE WALLS AT ALL. A figure is feet-anchored — the coordinate is where
   somebody is STANDING and the box hangs above it — and every wall is reachable,
   because the simulation clamps only to `PlayerRadius`, 0.35 m in a 16 × 22 m room.
   So a man at the top wall had his whole box above the room, `overflow: hidden`
   deleted it, and what a player saw was the bottom sliver of a body with no head and
   no words at all. At a SIDE wall the same clip took half of him, and half of every
   balloon over him — reported second, fixed the same way. Most shifts OPENED like that: the spawn sampler
   draws the first point far enough from the лысый, and he starts at the bottom,
   so the qualifying region is a strip along the top.
   This is not a `z-index` problem and could not be fixed by reordering — the
   pixels are clipped before anything composites.

   TWO ELEMENTS, NOT ONE, and the reason is worth keeping. The room inside keeps
   the catalogue's own shape and is the query container, so `toPlane`, `deskBox`
   and every `100cqw`/`100cqh` consumer are untouched: a coordinate is still a
   fraction of the room and nothing that maps metres to pixels knows the wall
   exists. `padding-top` on this element instead would have resolved its
   percentage against `.fintech-stage`'s WIDTH — 216 px on a desktop against the
   87 wanted — and folding the wall into the four transform sites would have been
   four independent edits that must agree forever. */
.fintech-plane {
  width: min(100cqw, calc(100cqh * var(--box-ratio, 0.718)));
  aspect-ratio: var(--box-ratio, 0.718);
  position: relative;
  overflow: hidden;
  border-radius: 10px;
  /* The wall. Darker than the floor and unlit, so the room reads as a room seen
     from slightly above rather than as a plane that got taller. */
  background: linear-gradient(180deg, #1b1e24, #23262d);
  /* WHERE THE ROOM ENDS, now that what surrounds it is the same material rather
     than a flat slab. A hairline and a drop shadow, because the walls are a rule
     of the game — you are clamped to them and the лысый corners you against them —
     so the boundary has to be readable even where the two gradients meet at
     nearly the same colour. */
  box-shadow:
    0 0 0 1px rgba(255, 255, 255, 0.07),
    0 12px 34px rgba(0, 0, 0, 0.5);
}

/* THE ROOM ITSELF — the catalogue's 16 × 22, sitting at the bottom of the plane.
   `top` as a percentage resolves against the containing block's HEIGHT, which is
   what makes this the right element to carry the wall's share; the plane's height
   is definite because of its `aspect-ratio`.
   It is the query container, so every `cqw`/`cqh` inside resolves against the
   ROOM — which is why nothing else in this stylesheet had to change. */
.fintech-office {
  position: absolute;
  /* Inset on THREE sides: a figure's height of wall above, and half a figure's width
     down each side. The bottom stays flush, because a figure hangs above its feet and
     nothing but the small ground ring extends below them.
     A percentage `top`/`bottom` resolves against the containing block's HEIGHT and a
     percentage `left`/`right` against its WIDTH, which is exactly what is wanted
     here — the plane's box is definite in both directions because of its
     `aspect-ratio`. */
  inset: calc(var(--head-share, 0.088) * 100%) calc(var(--side-share, 0.038) * 100%) 0;
  container-type: size;
  border-radius: 0 0 10px 10px;
  background:
    linear-gradient(180deg, rgba(255, 255, 255, 0.05), rgba(0, 0, 0, 0.25)),
    repeating-linear-gradient(0deg, #2a2e36 0 4.5455%, #262a31 4.5455% 9.0909%);
  /* HOW BIG A PERSON IS, IN THIS OFFICE. Every fixed length inside the room is a
     fraction of this one, so the world is drawn at the same apparent scale on
     every screen instead of shrinking as the room grows. Declared here and used
     only by descendants — `.fintech-office` is itself the query container, so a
     `cqw` in a property ON this rule would resolve against `.fintech-plane`.

     `--unit-cqw` is written by the template from `fintechPlane.UNIT_CQW`, which
     is also what the wall's depth is derived from. That is the whole point: the
     coefficient lives in ONE place, so resizing the world cannot leave the wall
     behind at its old depth. */
  --unit: clamp(20px, calc(var(--unit-cqw, 0.0825) * 100cqw), 96px);
}

/* Furniture. Static — it comes off the catalogue once and never moves — so
   unlike the figures these go through Vue exactly once. */
.fintech-desk {
  position: absolute;
  border-radius: calc(var(--unit) * 0.075);
  background: #4a3b28;
  box-shadow: inset 0 calc(var(--unit) * -0.05) 0 rgba(0, 0, 0, 0.35);
}

/* THE FIGURES. Feet-anchored: the coordinate is where somebody is STANDING, so
   the box hangs above it rather than being centred on it — which is what makes
   a figure in front of a desk look like it is in front of the desk.
   `translate3d` only and `will-change: transform`, so a position write is a
   compositor job and never a layout one. */
.fintech-me,
.fintech-boss,
.fintech-peer {
  position: absolute;
  left: 0;
  top: 0;
  /* WHAT AN `em` MEANS INSIDE A FIGURE. The shadows on the head and the body are
     written in `em`, which LOOKED relative and was not: nothing else sets a
     font-size in here, so they resolved against the root's 16px and would have
     kept their old size while the world shrank around them. Anchoring the figure's
     font-size to `--unit` makes every one of them a fraction of the man, which is
     what they always read as. Nothing inside a figure prints text — the balloon
     deliberately uses `rem` and is excluded from the world's scale. */
  font-size: var(--unit);
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
.fintech-peer {
  --skin: #f0d9bd;
  --body: #4a5a6a;
  /* Slightly recessed, so a glance tells you which one you are steering — but
     not so faint that a colleague is easy to lose, since he is the thing you are
     negotiating with. */
  opacity: 0.88;
}

/* A FACE, beside the head rather than on it: a colleague is a cut-out figure, and
   painting a photograph into the head would make him a token seen from above,
   which is the one thing this plane is not. It serves BOTH figures now — yours and
   everybody else's — which is why it is no longer called the peer's.

   TWO EXPLICIT `calc()` LENGTHS RATHER THAN A PERCENTAGE PAIR PLUS `aspect-ratio`.
   The figure box is 1 : 1.6, so a percentage width and height can never be a
   circle; and `aspect-ratio` on a replaced element with no `height` degrades to a
   stretched strip on an engine that does not support it — which is the weak
   remnant of the bug where this rule was missing altogether and a CDN photograph
   drew at its natural size over the office. Sized off `--unit` like everything
   else in the room, so it rides the depth ramp with the man and shrank with him.

   It sits ON the scalp and never over the face: the head spans 4 %…96 %
   horizontally, this spans 74 %…112 %, so it overlaps the head's right edge by
   about a third of the head's width, on the hair rather than the face — the yard's
   shape, whose own comment records that a first attempt overlapping by 30 %
   covered the face, which a badge sitting on the SCALP does not.

   `top: 1%` rather than the old `-6%`, so the badge is inside the figure's own box.
   The reason is not the top wall: the wall is a whole figure deep while nobody can
   stand nearer than `PlayerRadius` to it, so at the tested sizes a badge hanging
   6 % above the box still cleared it. It is that `--unit` has a `clamp()` FLOOR, and
   where that floor engages — a narrow or landscape plane — the figure is taller
   than the wall and anything outboard of the box is the first thing lost. Inside
   the box it cannot happen at any width.

   IT STILL HANGS 6 % OUTSIDE THE BOX HORIZONTALLY (74 % + 38 % = 112 % was 12 %;
   `left: 68%` halves it), and that is accepted rather than solved: pressed against
   a side wall a figure is already half outside the plane and clipped, so the face
   is the least of what is missing there. Pulling the badge fully inboard would push
   it over the face, which is the defect the yard already recorded. */
.fintech-face-badge {
  position: absolute;
  /* Pulled back in as it grew: at 0.76 wide, `left: 68%` would hang 44 % of a figure
     outside the box and be clipped at a side wall. `44%` puts its right edge at 120 %
     — the same overhang the small one had — and its left edge over the ear rather
     than over the face. */
  left: 44%;
  top: 1%;
  /* TWICE THE SIZE, owner-directed: at 0.38 of a figure it was a colour cue rather
     than a face, and the whole point of a badge is telling which of your friends is
     standing there. 0.76 is most of the head's width, which is as large as it can be
     before it stops reading as a badge beside him and starts reading as his face. */
  width: calc(var(--unit) * 0.76);
  height: calc(var(--unit) * 0.76);
  border-radius: 50%;
  object-fit: cover;
  border: max(1px, calc(var(--unit) * 0.045)) solid rgba(0, 0, 0, 0.45);
  background: rgba(0, 0, 0, 0.25);
  /* Decoration on a figure: it must never take a tap meant for the office
     underneath. */
  pointer-events: none;
}

/* A COLLEAGUE MID-DASH. A buff, so it is shown for as long as it lasts rather
   than flashed once, and it is a property of the figure rather than something
   orbiting him — the same shape the bald man's green follows. Small on purpose:
   it says "he is moving fast", not "look over here". */
.fintech-peer[data-fast] {
  filter: drop-shadow(0 0 calc(var(--unit) * 0.15) rgba(255, 255, 255, 0.55));
}

/* Placeholder shapes rather than art: iteration 1 ships with no uploaded assets
   at all, on purpose, and a missing sprite must be a shape and never a broken
   screen. */
.fintech-me {
  --skin: #f0d9bd;
  --body: #2f6ea8;
  /* NO TRANSITION. This one is predicted and rewritten every animation frame, so
     a transition would be the compositor easing towards a value that has already
     been replaced — which reads as lag, which is the exact thing prediction is
     here to remove. */
}

/* WHICH ONE IS YOU, PART TWO — AN OUTLINE ROUND THE MAN HIMSELF.
   The ground ring below was not enough: with three figures built identically, all
   wearing a face and all the same size, a ring under the feet is something you have
   to look for rather than something you see.

   ON THE HEAD AND THE BODY, NOT ON THE BOX. A figure's box is a COORDINATE and not a
   surface — nothing may set a `background`, a `border` or an `outline` on it, because
   the лысый shipped once as a filled rectangle for exactly that reason and a test
   pins it now. So the outline goes on the two shapes that ARE him, exactly as the
   drunk green and the grin steps do, and it therefore follows his silhouette instead
   of drawing a box around his coordinate.

   `box-shadow` rather than `outline`, for two reasons: an `outline` is drawn on the
   element's box even on a rounded shape, and a spread-only shadow follows the
   `border-radius` that makes him a person. Sized in `em`, which is `--unit` on a
   figure, so it thickens with him down the depth ramp instead of becoming a thread at
   the back of the room.

   Warm white against a dark floor and a blue shirt, and it is the same hue as the
   ground ring so the two read as one marker rather than two decorations. */
.fintech-me .fintech-fig-head {
  box-shadow:
    0 0 0 0.06em rgba(255, 255, 255, 0.95),
    0 0 0 0.1em rgba(0, 0, 0, 0.45),
    inset -0.18em -0.22em 0.5em rgba(0, 0, 0, 0.18);
}

.fintech-me .fintech-fig-body {
  box-shadow:
    0 0 0 0.06em rgba(255, 255, 255, 0.95),
    0 0 0 0.1em rgba(0, 0, 0, 0.45),
    inset 0 -0.35em 0.5em rgba(0, 0, 0, 0.22);
}

/* WHICH ONE IS YOU — a ring on the floor you are standing in.
   With three colleagues in one опенспейс, all built the same way and all wearing a
   face, `opacity: 0.88` on everybody else is not enough to find yourself while
   something is walking towards you.

   ON THE FLOOR RATHER THAN AROUND THE BOX, and that is not a preference: a
   figure's box is a COORDINATE, not a surface, and nothing may paint a background,
   a border or an outline on it — the лысый shipped once as a filled rectangle for
   exactly that reason, and a test pins it now. `bottom: 0` is the standing point,
   because a figure is feet-anchored; the ellipse's centre is dropped 45 % of its
   own height below that, so the man stands IN it rather than on top of it.

   IT IS EXACTLY HIS COLLISION DISC, and that is where the width comes from.
   `PlayerRadius` is 0.35 m, so a 0.70 m circle is precisely the ground the
   simulation will not let anybody else or any desk occupy — which means the ring
   can never be drawn over a surface you could not be standing on. 0.52 × `--unit`
   is that 0.70 m at this scale (`--unit` is 0.0825 of a 16 m room, so a unit is
   1.32 m). It was 0.72 first, which is 0.95 m — wider than the disc, so pressed
   against a desk the arc painted over the desk's edge and claimed ground the
   player was not on.

   WHAT `z-index: -1` DOES AND DOES NOT DO. It orders the ring behind its OWN body
   and head, which is all it is for. It does NOT keep the marker below the
   furniture, and an earlier version of this comment claimed it did: this element
   carries `z-index: var(--band)` while a desk is positioned with `z-index: auto`,
   so the whole figure — pseudo-element included — always paints above every desk,
   and no value here could change that. Sizing the ring to the collision disc is
   what makes that harmless rather than a lie.

   NOTHING FOR `prefers-reduced-motion`, and that is the point: it is a static
   border with no animation to switch off, so somebody who asked for less motion
   sees exactly the same marker as everybody else. A mark that only exists while it
   pulses is a mark they never see. Nothing for the theme either — the plane is
   hardcoded dark, so white-on-dark reads the same under either app theme. */
.fintech-me::after {
  content: '';
  position: absolute;
  left: 50%;
  bottom: 0;
  width: calc(var(--unit) * 0.52);
  height: calc(var(--unit) * 0.145);
  transform: translate(-50%, 45%);
  border-radius: 50%;
  border: max(1px, calc(var(--unit) * 0.05)) solid rgba(255, 255, 255, 0.9);
  z-index: -1;
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
.fintech-fig-body {
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
.fintech-fig-head {
  position: absolute;
  left: 50%;
  top: 0;
  width: 92%;
  aspect-ratio: 1;
  transform: translateX(-50%);
  border-radius: 50%;
  background: var(--skin, #e8d7b0);
  box-shadow:
    0 0 0 calc(var(--unit) * 0.05) rgba(0, 0, 0, 0.35),
    inset -0.18em -0.22em 0.5em rgba(0, 0, 0, 0.18);
  overflow: hidden;
}
/* The bald shine. It is the only thing marking him as bald, and one soft
   highlight reads as a scalp far better than drawing an absence of hair. */
.fintech-fig-shine {
  position: absolute;
  left: 26%;
  top: 12%;
  width: 34%;
  height: 22%;
  border-radius: 50%;
  background: rgba(255, 255, 255, 0.5);
  filter: blur(calc(var(--unit) * 0.03));
}
/* And you are not bald, which is the only reason this exists. */
.fintech-fig-hair {
  position: absolute;
  inset: -6% -4% auto -4%;
  height: 46%;
  border-radius: 50% 50% 34% 34%;
  background: #2f2a26;
}

.fintech-boss {
  --skin: #e8d7b0;
  --body: #7a5a86;
  /* NO TRANSITION. He is interpolated in JS now (fintechInterp), so a CSS
     transition here would smooth an already-smooth position — adding a second
     lag on top of the interpolation delay and reintroducing the stop-start it
     exists to remove. */
}

/* HOW CLOSE HE IS, PAINTED ON THE MAN AND NOT ON HIS BOX.
   `--skin` and `--body` are what `.fintech-fig-head` and `.fintech-fig-body` draw
   themselves with, so the step lands on the two shapes that are actually him.
   These rules used to set `background` on `.fintech-boss` itself — which is the
   figure's positioning box, a plain `--unit` × `--unit * 1.6` rectangle with
   the head and body painted on top of it. The result was a filled orange
   RECTANGLE appearing behind him the moment he got close, which read as a
   selection box or a broken sprite rather than as a man getting nearer. The
   colours below are the ones those gradients already carried; only the element
   they land on has moved. Nothing may set `background` on `.fintech-me` or
   `.fintech-boss` — a figure's box is a coordinate, not a surface. */
/* The bottle. A small thing on the floor you have to walk to — deliberately
   drawn at ground level and NOT feet-anchored like a figure, because it is not
   standing, it is lying about. */
.fintech-bottle {
  position: absolute;
  left: 0;
  top: 0;
  width: calc(var(--unit) * 0.28);
  height: calc(var(--unit) * 0.62);
  transform: translate3d(calc(var(--x, 0.5) * 100cqw - 50%), calc(var(--y, 0.5) * 100cqh - 50%), 0);
  border-radius: 40% 40% 22% 22%;
  background: linear-gradient(180deg, #cfe3d0 0%, #7fa886 55%, #4c6b52 100%);
  box-shadow: 0 0 0 max(1px, calc(var(--unit) * 0.025)) rgba(0, 0, 0, 0.4);
  pointer-events: none;
  z-index: 1;
}

/* СЕРЕГА AND ТЁМА — the two who are not playing.
   Built from the same head and body as everybody else, and DIMMER than a colleague,
   because the one thing a player must never do is spend a second wondering whether
   the figure walking about is somebody who matters. Nobody chases them and they
   chase nobody; they are the room being a room. */
.fintech-npc {
  --skin: #f0d9bd;
  --body: #52565f;
  position: absolute;
  left: 0;
  top: 0;
  font-size: var(--unit);
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
  opacity: 0.72;
}

/* WHAT TELLS THEM APART AT THIRTY PIXELS. One shape each, positioned by the same
   rule: a caption across Серега's shirt, a canopy over Тёма's head. Both are drawn
   from `.fintech-npc-mark` with the difference in a `data-npc` branch, because two
   rules that share a box are easier to read than one rule with two exceptions. */
.fintech-npc-mark {
  position: absolute;
  pointer-events: none;
}

/* СЕРЕГА'S SHIRT. The caption is real text rather than a shape, so it is legible at
   the size it has to be legible at and needs no art — and it is `content` on a
   pseudo-element-free span, so no test has to guess at a background image. */
.fintech-npc[data-npc='serega'] .fintech-npc-mark {
  left: 50%;
  bottom: 12%;
  transform: translateX(-50%);
  width: 68%;
  text-align: center;
  font-size: 0.3em;
  font-weight: 900;
  letter-spacing: 0.04em;
  line-height: 1;
  color: rgba(255, 255, 255, 0.92);
}
.fintech-npc[data-npc='serega'] .fintech-npc-mark::after {
  content: 'ХУЙ';
}

/* ТЁМА'S PARAGLIDER. A canopy above the head with two lines to it — three shapes
   would be more accurate and less readable, so it is one arc and a shadow. */
.fintech-npc[data-npc='tema'] .fintech-npc-mark {
  left: 50%;
  top: -34%;
  transform: translateX(-50%);
  width: 130%;
  height: 30%;
  border-radius: 50% 50% 8% 8%;
  background: linear-gradient(180deg, #e0b74a 0%, #c9743f 60%, #a8562c 100%);
  box-shadow: 0 calc(var(--unit) * 0.04) 0 rgba(0, 0, 0, 0.28);
}

/* AND ТЁМА'S WORDS START ABOVE HIS CANOPY, rather than inside it.
   The canopy reaches 34 % of a figure's height above the box top and a balloon sits at
   `bottom: 100%` — the box's own top edge — so the two shared a strip of air. Painting
   the words over the canopy makes them readable; lifting them clear makes them
   legible, which is not the same thing when the canopy is a warm gradient and the
   words are white on near-black.
   In `--unit`, so it holds at every width and down the depth ramp. Only Тёма needs it:
   Серега wears a caption on his shirt, inside the box, and Claude's burst is on his
   chest. */
.fintech-npc[data-npc='tema'] .fintech-say {
  margin-bottom: calc(var(--unit) * 0.72);
}

/* HIS OWN SMOKE, AND IT NEVER GOES OUT. He carries his own кальян, so there is no
   state to it and no flag on the frame — the cloud is simply part of what he looks
   like. Dimmer and smaller than a player's, because a player's means uncatchable and
   this one means nothing at all. */
.fintech-npc::before {
  content: '';
  position: absolute;
  left: 50%;
  bottom: 18%;
  width: calc(var(--unit) * 1.5);
  height: calc(var(--unit) * 1.05);
  transform: translateX(-50%);
  border-radius: 50%;
  background: radial-gradient(
    circle at 50% 50%,
    rgba(226, 232, 240, 0.5) 0%,
    rgba(210, 220, 232, 0.28) 55%,
    rgba(200, 212, 226, 0) 100%
  );
  pointer-events: none;
}

/* THE КАЛЬЯН IN HIS HAND, and it is the FLOOR one's shapes at a smaller size rather
   than a second look invented for it. The floor кальян reads correctly, so the
   handheld one is the same four parts — wide glass base, stem, bowl, hose — in the
   same colours; only the scale and where it hangs differ, because one is a thing you
   walk to and this is a thing he is holding at his side.
   Three elements would be the accurate way and one is the readable way: the box is
   the base, `::before` is the stem and bowl, `::after` is the hose. */
.fintech-npc-pipe {
  position: absolute;
  left: 72%;
  bottom: 16%;
  width: 24%;
  height: 22%;
  border-radius: 46% 46% 42% 42%;
  background: linear-gradient(180deg, #7fc7d9 0%, #3f8fa8 55%, #1f5a70 100%);
  box-shadow: 0 0 0 max(1px, calc(var(--unit) * 0.015)) rgba(0, 0, 0, 0.45);
}

/* The stem, and the bowl on top of it. */
.fintech-npc-pipe::before {
  content: '';
  position: absolute;
  left: 50%;
  bottom: 78%;
  width: 34%;
  height: 150%;
  transform: translateX(-50%);
  background: linear-gradient(180deg, #d8c48f 0%, #a8842f 100%);
}
.fintech-npc-pipe::after {
  content: '';
  position: absolute;
  left: 50%;
  bottom: 210%;
  width: 96%;
  height: 62%;
  transform: translateX(-50%);
  border-radius: 50% 50% 30% 30%;
  background: linear-gradient(180deg, #c9743f 0%, #a8562c 100%);
  /* The hose, leaving the base towards him — a curved shadow rather than a fifth
     shape, which at this size is the difference between a кальян and a smudge. */
  box-shadow:
    calc(var(--unit) * -0.1) calc(var(--unit) * 0.34) 0 calc(var(--unit) * -0.055)
    #6b5320;
}

/* CLAUDE CODE. Built from the same head and body as everybody else, because the
   scene has to read as three people in a room rather than as two people and a
   mascot — the differences are the ones you can see at thirty pixels: the terracotta
   of the company whose tool he is, dark stubble across the jaw, and a cigarette with
   an ember that brightens as he closes.

   NO LOGO AND NO LIKENESS, deliberately and per the art rules this project already
   set for itself: he is identifiable by COLOUR and SILHOUETTE. The hue is the one
   everybody who has opened a terminal this year recognises, and that is the whole of
   the reference.

   He is `.fintech-claude` and not a variant of `.fintech-boss`, for the reason
   chaser.go gives about the Go: they are two things that resemble each other, and a
   shared rule with two exceptions is harder to read than two rules. */
.fintech-claude {
  --skin: #e8d3b6;
  --body: #c96442;
  position: absolute;
  left: 0;
  top: 0;
  font-size: var(--unit);
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

/* THE MARK ON HIS SHIRT — a burst, so it is obvious whose tool he is.
   Eight tapered spokes from a `repeating-conic-gradient`, which is one declaration
   and reads at thirty pixels where a drawn glyph would be a smudge. Deliberately an
   EVOCATION rather than the trademark: the shape and the terracotta together are the
   whole reference, and there is no wordmark and no file.
   It sits on the chest, so it scales with the body and rides the depth ramp with the
   man. `mask` rather than a background so the shirt's colour shows through the gaps
   instead of a second colour having to be kept in step with `--body`. */
.fintech-claude-mark {
  position: absolute;
  left: 50%;
  bottom: 22%;
  width: 42%;
  aspect-ratio: 1;
  transform: translateX(-50%);
  background: rgba(255, 255, 255, 0.93);
  -webkit-mask: repeating-conic-gradient(
    from 0deg,
    #000 0deg 9deg,
    transparent 9deg 45deg
  );
  mask: repeating-conic-gradient(from 0deg, #000 0deg 9deg, transparent 9deg 45deg);
}

/* The stubble: a band across the lower half of the face rather than drawn hairs,
   which is the only thing that reads at this size. */
.fintech-claude-stubble {
  position: absolute;
  left: 8%;
  right: 8%;
  bottom: 6%;
  height: 34%;
  border-radius: 0 0 46% 46%;
  background: rgba(48, 38, 32, 0.42);
}

/* The cigarette, with the ember driven by `--grin` — the same 0..1 the лысый's
   smile uses, written by the same call. So how lit it is IS how close he is, and a
   player reads the danger off the figure rather than off a meter. */
.fintech-claude-cig {
  position: absolute;
  left: 62%;
  bottom: 20%;
  width: 30%;
  height: 7%;
  border-radius: 2px;
  background: linear-gradient(90deg, #f2ece0 0%, #f2ece0 66%, #6b3a1f 100%);
  box-shadow: calc(var(--unit) * 0.06) 0 calc(var(--unit) * 0.05)
    rgba(255, 138, 60, calc(0.25 + 0.75 * var(--grin, 0)));
}

/* SLOWED — a debuff, so it is shown for as long as it lasts and on whoever is
   carrying it, yours and every colleague's alike. Warm, because it is the one state
   on this plane that is working against you; on the SKIN and the BODY rather than on
   the positioning box, which is the rule the лысый's green already follows. */
.fintech-me[data-slow],
.fintech-peer[data-slow] {
  --skin: #e6c9b4;
  --body: #8a4a34;
}

/* THE КАЛЬЯН. A small thing on the floor you walk to, drawn at ground level and
   NOT feet-anchored, because like the bottle it is not standing — it is sitting
   there. Sized off `--unit` so it shrank with the world. */
.fintech-hookah {
  position: absolute;
  left: 0;
  top: 0;
  /* TALLER AND NARROWER THAN THE BOTTLE, because it was drawn as one and read as
     one. A кальян is a bowl on a stem on a base with a hose off the side, and at
     this size that is four shapes rather than a silhouette. */
  width: calc(var(--unit) * 0.4);
  height: calc(var(--unit) * 0.78);
  transform: translate3d(calc(var(--x, 0.5) * 100cqw - 50%), calc(var(--y, 0.5) * 100cqh - 70%), 0);
  pointer-events: none;
  z-index: 1;
}

/* The BASE — the wide glass bottom, which is the part that says кальян at a glance. */
.fintech-hookah::before {
  content: '';
  position: absolute;
  left: 50%;
  bottom: 0;
  width: 100%;
  height: 46%;
  transform: translateX(-50%);
  border-radius: 46% 46% 44% 44%;
  background: linear-gradient(180deg, #7fc7d9 0%, #3f8fa8 55%, #1f5a70 100%);
  box-shadow: 0 0 0 max(1px, calc(var(--unit) * 0.02)) rgba(0, 0, 0, 0.45);
}

/* The STEM and the BOWL on top of it, plus the hose leaving to one side — one
   element carrying three gradients, because four positioned children for a thing
   this size is markup nobody can read. */
.fintech-hookah::after {
  content: '';
  position: absolute;
  left: 50%;
  top: 0;
  width: 100%;
  height: 60%;
  transform: translateX(-50%);
  background:
    /* the bowl */
    radial-gradient(
      ellipse 42% 26% at 50% 8%,
      #c9743f 0%,
      #a8562c 70%,
      rgba(168, 86, 44, 0) 71%
    ),
    /* the stem */
    linear-gradient(
      90deg,
      rgba(0, 0, 0, 0) 40%,
      #d8c48f 40%,
      #a8842f 60%,
      rgba(0, 0, 0, 0) 60%
    ),
    /* the hose, leaving to the right and drooping */
    radial-gradient(
      circle at 118% 72%,
      rgba(0, 0, 0, 0) 58%,
      #6b5320 60%,
      #6b5320 66%,
      rgba(0, 0, 0, 0) 68%
    );
}

/* BEHIND A CLOUD, and it is a property of the FIGURE for as long as it lasts —
   drawn on you and on every colleague alike, because a buff only its owner can see
   is unfinished, and which colleague the лысый can no longer walk at is the single
   most useful thing to know about somebody else in the room.

   The cloud is a `::before` so it can sit OVER the man without being a child of him
   in the template — and it is deliberately not `z-index`ed above the balloon: the
   words still have to be readable through it, since a hidden player is exactly the
   one whose line («КУДА ПРОПАЛ ЭТОТ ЕБЛАН» is the лысый's, not his) is worth
   reading. Feet-anchored like the figure, centred on the body.

   Its opacity animates, and under `prefers-reduced-motion` it does not — it stays
   at a legible middle instead of switching off, because somebody who asked for less
   motion still has to be able to see that they are uncatchable. */
.fintech-me[data-cloud]::before,
.fintech-peer[data-cloud]::before {
  content: '';
  position: absolute;
  left: 50%;
  bottom: 18%;
  /* BIGGER THAN THE FIGURE, deliberately: a cloud that fits inside the man reads as
     a puff of breath. This one is something you are hiding behind, which is what it
     mechanically is. */
  width: calc(var(--unit) * 1.9);
  height: calc(var(--unit) * 1.35);
  transform: translateX(-50%);
  border-radius: 50%;
  background: radial-gradient(
    circle at 50% 50%,
    rgba(232, 238, 245, 0.78) 0%,
    rgba(210, 220, 232, 0.5) 55%,
    rgba(200, 212, 226, 0) 100%
  );
  pointer-events: none;
  animation: fintech-cloud 2.4s ease-in-out infinite;
}

@keyframes fintech-cloud {
  0%,
  100% {
    opacity: 0.72;
    transform: translateX(-50%) scale(1);
  }
  50% {
    opacity: 0.95;
    transform: translateX(-52%) scale(1.06);
  }
}

@media (prefers-reduced-motion: reduce) {
  .fintech-me[data-cloud]::before,
  .fintech-peer[data-cloud]::before {
    animation: none;
    opacity: 0.85;
  }
}

/* Drunk: green, and it is the SKIN and the BODY that go green rather than the
   positioning box — the same rule the grin steps follow, and for the same
   reason. He is still coming; he is just not coming in a straight line. */
.fintech-boss[data-drunk] {
  --skin: linear-gradient(180deg, #b9d8a4 0%, #7fae6a 100%);
  --body: #4f7a46;
}

.fintech-boss[data-grin='closing'] {
  --skin: linear-gradient(180deg, #f3c98a 0%, #d99a52 100%);
  --body: #9a5f2a;
}

.fintech-boss[data-grin='here'] {
  --skin: linear-gradient(180deg, #ff9f7a 0%, #e0644a 100%);
  --body: #a83b1f;
}

/* THE BALLOON, and it hangs off the figure rather than being placed beside it.
   `.fintech-me` / `.fintech-boss` are already positioned every animation frame by a
   transform, so a child anchored to the top of that box is carried along by the
   compositor for nothing — no second write, no chance of the words being a frame
   behind the man saying them.
   `bottom: 100%` puts it above the head; the figure's box is feet-anchored, so
   that is above him rather than over him.

   AND IT MOVES BELOW HIS FEET AT THE TOP WALL. The wall above the room is one
   figure deep, which is what makes the MAN visible there; covering his words too
   would need it half again as deep, and that is floor space and screen taken from
   the room for two lines of text. So near the wall the balloon goes under him
   instead: `--say-below` is written by `applyFigure` from the same coordinate that
   positions him, so it can never be a frame behind. `100%` in the translation is
   the balloon's OWN height, so it self-adjusts between a one-row and a two-row
   line, and at `--say-below: 0` this renders exactly as it always did.

   IT USED TO SAY «it does not scale with --depth, deliberately», and that was
   simply false: this is a child of an element whose transform ends in
   `scale(var(--depth))`, so the whole subtree scales with it, and the layout
   suite has documented the real behaviour all along (a drawn balloon is bounded
   at 160 × 1.4). The claim is removed rather than corrected upward, because
   nothing here depends on it either way. */
.fintech-say {
  position: absolute;
  left: 50%;
  bottom: 100%;
  transform: translateX(-50%)
    translateY(calc(var(--say-below, 0) * (100% + var(--unit) * 1.6 + 8px)));
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
  /* THE WORDS ARE ON TOP OF WHATEVER HE IS WEARING. A balloon is a readout, and a
     figure's decoration is not — Тёма's paraglider is a later sibling and so painted
     over his own words until this existed. One `z-index` inside the figure's own
     stacking context, which is all it needs: it cannot escape him, because the figure
     carries `z-index: var(--band)` and a transform. */
  z-index: 1;
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
.fintech-boss-grin {
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
.fintech-controls {
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

.fintech-stick {
  position: relative;
  pointer-events: auto;
  /* NEITHER THUMB TARGET MAY SHRINK. They are sized to a thumb, and the flex row
     they share now has a third control in it — a default `flex-shrink: 1` let the
     router pill take the stick's own padding back off the left edge, which put the
     half of it a thumb reaches first back inside the phone's edge-swipe strip.
     The pill is the thing that gives way instead; see `.fintech-router`. */
  flex: 0 0 auto;
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

.fintech-stick-knob {
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
.fintech-verbs {
  pointer-events: none;
  display: flex;
  flex-direction: column-reverse;
  align-items: flex-end;
  gap: 8px;
}

.fintech-verb {
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

.fintech-verb:disabled {
  opacity: 0.45;
}

/* His face ON the button, which is what makes two colleagues distinguishable
   without a name — the same picture the plane draws beside his head. */
.fintech-verb-face {
  width: 34px;
  height: 34px;
  border-radius: 50%;
  object-fit: cover;
  background: rgba(255, 255, 255, 0.12);
}

.fintech-verb-label {
  white-space: nowrap;
}

/* THE THUMB'S COLUMN — «РОУТЕР УПАЛ» stacked directly on top of РЫВОК.
   One thumb reaches both without moving across the glass, and the middle of the
   band stays the colleagues', which is where a redirect has always been. Side by
   side, the three of them competed for one phone's width and the stick lost its
   clearance from the edge. */
.fintech-thumb {
  pointer-events: none;
  flex: 0 0 auto;
  display: flex;
  flex-direction: column;
  align-items: center;
  gap: 8px;
}

/* «РОУТЕР УПАЛ» — the coolest colour on the plane on purpose: nothing about it is
   urgent, and the eye should find РЫВОК first.

   A FIXED BOX, and that is the point of every dimension here being written out.
   The label and the countdown are two lines inside it and only their TEXT changes,
   so pressing the button cannot resize it and the column under the thumb never
   moves. Sizing it off its own content — which is what a pill whose words swap
   between «РОУТЕР УПАЛ» and «45,0 с.» does — made it jump on every press.

   It is the dash's width, so the two read as one column rather than as two
   controls that happen to be near each other. */
.fintech-router {
  pointer-events: auto;
  flex: 0 0 auto;
  display: flex;
  flex-direction: column;
  align-items: center;
  justify-content: center;
  gap: 1px;
  width: 84px;
  /* ≥ 44 px, like every control on this plane. */
  height: 46px;
  padding: 0 4px;
  border-radius: 14px;
  border: 2px solid rgba(120, 170, 210, 0.45);
  background: rgba(30, 46, 60, 0.72);
  color: rgba(255, 255, 255, 0.92);
  letter-spacing: 0.02em;
  overflow: hidden;
  touch-action: manipulation;
}

.fintech-router-label {
  font-size: 0.52rem;
  font-weight: 800;
  line-height: 1.05;
  text-align: center;
}

/* The state line: a countdown, «ГОТОВ», or «НЕТ СВЯЗИ» while he is actually away.
   Tabular figures and a fixed line box, so the digits changing ten times a second
   move nothing. */
.fintech-router-timer {
  font-size: 0.5rem;
  font-weight: 700;
  line-height: 1.05;
  opacity: 0.72;
  font-variant-numeric: tabular-nums;
}

.fintech-router:disabled {
  opacity: 0.45;
  border-color: rgba(255, 255, 255, 0.18);
  background: rgba(255, 255, 255, 0.06);
}

.fintech-dash {
  pointer-events: auto;
  flex: 0 0 auto;
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

.fintech-dash:disabled {
  opacity: 0.4;
  border-color: rgba(255, 255, 255, 0.18);
  background: rgba(255, 255, 255, 0.06);
}

/* THE ACKNOWLEDGEMENT. One ring, expanding and fading, under half a second.
   Deliberately restrained: it says "that landed", not "well done" — a bigger
   effect would be the thing you are watching when the bald man arrives, which is
   the one moment this game cannot afford to take your eye off.
   Sized off `--unit` like everything else here, so it scales with the plane. */
.fintech-pop {
  position: absolute;
  left: 0;
  top: 0;
  width: calc(var(--unit) * 0.9);
  height: calc(var(--unit) * 0.9);
  margin: calc(var(--unit) * -0.45) 0 0 calc(var(--unit) * -0.45);
  transform: translate3d(calc(var(--x, 0.5) * 100cqw), calc(var(--y, 0.5) * 100cqh), 0);
  border-radius: 50%;
  border: max(1px, calc(var(--unit) * 0.05)) solid var(--pop, rgba(255, 255, 255, 0.85));
  pointer-events: none;
  z-index: 4;
  animation: fintech-pop 420ms ease-out forwards;
}

/* A colour per kind, so two things happening in the same second are still two
   things. No text: the balloons already say what happened. */
.fintech-pop[data-kind='bottle'] {
  --pop: rgba(150, 220, 150, 0.9);
}
.fintech-pop[data-kind='drunk'] {
  --pop: rgba(120, 210, 110, 0.95);
}
.fintech-pop[data-kind='redirect'] {
  --pop: rgba(240, 180, 41, 0.95);
}

/* The router falling, marked where Claude was standing when he walked off — a
   cool blue, so it is told apart from the redirect's amber when both can land in
   the same second. */
.fintech-pop[data-kind='router'] {
  --pop: rgba(122, 190, 235, 0.95);
}

@keyframes fintech-pop {
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
  .fintech-streak-fill {
    transition: none;
  }

  /* A dash is a state rather than motion, so its aura stays under reduced
     motion — there is nothing moving about it to reduce. */

  /* Still SHOWN, just not animated — somebody who has asked for less motion
     still needs to know their verb landed. It is cleared on a timer rather than
     on `animationend` precisely so that this branch works. */
  .fintech-pop {
    animation: none;
    opacity: 0.55;
  }

  /* The tempo step keeps its COLOUR and loses its motion, for the same reason:
     the office just got harder and that is worth knowing however you have asked
     to be told. Its timer is what clears it, not the animation ending. */
  .fintech-hud-tempo[data-bump] .fintech-hud-value {
    animation: none;
  }
}
</style>
