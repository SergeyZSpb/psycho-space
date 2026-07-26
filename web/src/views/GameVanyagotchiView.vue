<template>
  <v-container
    :class="phase === 'intro' || phase === 'play' ? 'pa-0 play-root' : 'py-4'"
    style="max-width: 900px"
  >
    <!-- Intro / start screen. Same shape as the first game's splash: everything
         is fixed-size except the artwork, so the title, the CTA and the
         disclaimer can never be the thing that gets clipped. -->
    <div v-if="phase === 'intro'" class="splash">
      <div class="splash-emoji">🫃</div>
      <h1 class="splash-title">Ванягоччи</h1>
      <p class="splash-lore">
        Ваня — офигенный чел. Он любит пиво, но постоянно теряет ключи, что их
        приходится искать. В этой игре каждый может почувствовать себя Ваней.
      </p>
      <p class="splash-intro">
        Общий двор. Все Вани стоят на одной поляне — тапни, чтобы дойти туда,
        куда хочешь. Остальные видят тебя, ты видишь их.
      </p>
      <v-btn color="primary" size="large" class="splash-cta" @click="enterYard">
        Во двор
      </v-btn>
      <p class="splash-disclaimer">
        Все персонажи вымышлены; любые совпадения с реальными людьми случайны.
      </p>
    </div>

    <!-- The yard. -->
    <div v-else class="stage">
      <!-- THE one flexible child. Everything else in this column is
           `flex: 0 0 auto`, so the plane is what absorbs and gives up slack and
           the status row below can never be pushed off a short screen. -->
      <div class="plane-frame">
      <div
        ref="planeEl"
        class="plane"
        :class="{ 'plane--stale': isStale }"
        data-test="plane"
        :data-stale="isStale ? '1' : undefined"
        role="application"
        :aria-label="`Двор, во дворе ${here}`"
        @pointerdown="onPlaneTap"
      >
        <div
          v-for="peer in drawn"
          :key="peer.id"
          :ref="(el) => setPeerEl(peer.id, el as Element | null)"
          class="peer"
          :class="{
            'peer--you': peer.id === store.youId,
            // Dims the whole dot rather than only the face: a sleeper is not a
            // player who is doing badly, he is a player who is not here, and the
            // thing that has to read at a glance is that nobody is home.
            'peer--asleep': peer.pose === 'asleep',
          }"
          data-test="peer"
          :data-you="peer.id === store.youId ? '1' : undefined"
          :data-peer="peer.id"
          :style="{ background: peerColour(peer.id) }"
        >
          <!-- Everybody wears their own face and their own condition, and both
               come off the wire rather than out of this screen's own state. The
               yard is one world: a pose worked out locally would be worked out
               from numbers only its owner can see, so your Ваня would look ill
               to you and fine to everybody standing next to him. -->
          <span class="peer-face" data-test="peer-face" :data-condition="peer.pose">
            <img v-if="peer.image" class="peer-sprite" :src="peer.image" alt="" />
            <template v-else>{{ peer.emoji }}</template>
          </span>
          <!-- Absent until a Ваня has been named, which is most of them today. -->
          <span v-if="peer.label" class="peer-label" data-test="peer-label">{{
            peer.label
          }}</span>
          <!-- What he just said, for the few seconds the server keeps it on the
               wire. Reactive like the rest of appearance — a line arrives twice
               a minute at most, so it costs one render each way, and the guard
               in front of it (sameAppearance) is what keeps the other 299
               frames free. -->
          <span v-if="peer.say" class="peer-say" data-test="peer-say">{{ peer.say }}</span>
        </div>
        <p v-if="store.peerIds.length === 0" class="plane-empty">
          {{ emptyMessage }}
        </p>
      </div>
      </div>

      <!-- Everything that is not the plane, in one block.
           Grouped rather than laid out as four siblings so that the landscape
           rule below can move the whole lot beside the plane with one
           `flex-direction`, instead of each row having to opt out of being
           squeezed into a column that is 350px tall. -->
      <div class="panel">
      <!-- Fixed-size stat row: it costs the plane its height, never the other
           way round. Bars rather than numbers because the number that matters is
           "is he all right", and every label, bound and threshold in here comes
           from the catalogue. -->
      <div v-if="bars.length" class="stats" data-test="pet-stats">
        <div
          v-for="bar in bars"
          :key="bar.key"
          class="stat"
          :data-test="`stat-${bar.key}`"
          :data-trouble="bar.trouble ? '1' : undefined"
        >
          <span class="stat-emoji" aria-hidden="true">{{ bar.emoji }}</span>
          <span class="stat-track" role="img" :aria-label="`${bar.label}: ${bar.shown}`">
            <span class="stat-fill" :style="{ width: `${bar.percent}%` }" />
          </span>
          <span class="stat-value" :data-test="`stat-value-${bar.key}`">{{ bar.shown }}</span>
        </div>
      </div>

      <!-- What is going on with him, in one line. Fixed height whether or not
           there is anything to say, so the plane above never resizes as the text
           comes and goes. -->
      <p class="petline" data-test="pet-line">{{ petLine }}</p>

      <!-- Fixed-size action row. One button per catalogue action, so adding a
           verb that moves a stat needs no change here. -->
      <div v-if="actions.length" class="actions">
        <v-btn
          v-for="action in actions"
          :key="action.key"
          class="action-btn"
          :data-test="`action-${action.key}`"
          color="primary"
          variant="tonal"
          :disabled="acting"
          @click="act(action)"
        >
          {{ action.emoji }} {{ action.label }}
        </v-btn>
      </div>

      <!-- Fixed-size status row. -->
      <div class="hud">
        <span class="hud-count">во дворе: {{ here }}</span>
        <span class="hud-status" :class="`hud-status--${store.status}`">
          {{ statusLabel }}
        </span>
      </div>
      </div>
    </div>
  </v-container>
</template>

<script setup lang="ts">
import { computed, onBeforeUnmount, ref, shallowRef } from 'vue';
import { realtimeClient, type ConnectionStatus, type RealtimeFrame } from '../realtime/socket';
import { useGameVanyagotchiStore } from '../stores/gameVanyagotchi';
import {
  applyFrame,
  applyPosition,
  isRenderablePosition,
  readAppearances,
  readHere,
  resolveArt,
  sameAppearance,
  tapToPosition,
  type PeerAppearance,
} from '../lib/vanyagotchiPlane';
import { decayedValue, inTrouble, skewMs, statFraction } from '../lib/vanyagotchiPet';
import { gameVanyagotchiApi } from '../api/endpoints';
import type { VanyagotchiAction, VanyagotchiConfig, VanyagotchiState } from '../api/types';
import { ApiError } from '../api/client';
import { useErrorStore } from '../stores/error';

// «Ванягоччи» — the shared plane.
//
// The layout below is COPIED from GameKhimkiView, not imported from it, and
// that is the rule rather than laziness: games share no UI code, so that
// deleting one is deleting its own files and nothing else (ARCHITECTURE ADR-028).
// The copy is worth its duplication because what it encodes is a scar — a fixed
// height minus the app bar, exactly one flexible child, and `overflow: hidden`
// so "never scrolls" is literal.
//
// The two-tier state split is the other thing to preserve, and the line is drawn
// by RATE OF CHANGE rather than by kind. Membership and appearance — who is in
// the yard, what they look like, how they are doing — change a few times an hour
// and go through the keyed v-for, each behind a guard that only assigns on a real
// change (sameIds, sameAppearance). POSITIONS DO NOT ENTER VUE AT ALL: a frame
// arrives five times a second, and binding it to reactivity would spend a
// scheduler pass and a vdom patch per entity to produce a transform the
// compositor could have been handed directly.

/** Server wire types, mirrored from internal/gamevanyagotchi/message.go. */
const TYPE_ROSTER = 'vanyagotchi_roster';
const TYPE_MOVE = 'vanyagotchi_move';
/** Asks the server which entity we are; it unicasts a `you` back. */
const TYPE_HELLO = 'vanyagotchi_hello';
const TYPE_YOU = 'vanyagotchi_you';

type Phase = 'intro' | 'play';
const phase = ref<Phase>('intro');

const store = useGameVanyagotchiStore();
const client = realtimeClient();
const errorStore = useErrorStore();

// ---------------------------------------------------------------------------
// The pet. Everything below is ordinary HTTP against a persistent thing, and it
// shares nothing with the socket above except this screen — which is the whole
// authority split: presence is in memory and dies with the process, the pet is
// in Postgres and does not.
// ---------------------------------------------------------------------------

const config = ref<VanyagotchiConfig | null>(null);
const petState = ref<VanyagotchiState | null>(null);
const acting = ref(false);
/** The line under the bars — what just happened, or that he is dead. */
const flash = ref('');

/**
 * How far this device's clock is ahead of the server's.
 *
 * Not reactive: it is an input to the interpolation, not something rendered, and
 * it is re-measured on every response — so the only thing that should trigger a
 * redraw is `displayNow` ticking.
 */
let clockSkew = 0;

/** This instant on the SERVER's clock. Ticked once a second while the yard is open. */
const displayNow = ref(0);
let displayTimer: number | undefined;

/**
 * How often the bars are recomputed.
 *
 * A second, because that is the coarsest interval at which a change is still
 * perceptible as movement rather than as a jump, and because the arithmetic
 * behind it is two subtractions. It is a redraw, not a poll: nothing is fetched,
 * and the values it produces are the same closed form the server would compute.
 */
const DISPLAY_TICK_MS = 1_000;

const stats = computed(() => config.value?.stats ?? []);
const actions = computed(() => config.value?.actions ?? []);

/** The decayed value of every stat, keyed, as of `displayNow`. */
const values = computed(() => {
  const out = new Map<string, number>();
  const now = displayNow.value;
  for (const def of stats.value) {
    const stored = petState.value?.stats?.find((s) => s.key === def.key);
    if (!stored) continue;
    out.set(def.key, decayedValue(def, stored, now));
  }
  return out;
});

/** One bar per catalogue stat, in catalogue order — which is the display order. */
const bars = computed(() =>
  stats.value.flatMap((def) => {
    const value = values.value.get(def.key);
    if (value === undefined) return [];
    return [
      {
        key: def.key,
        label: def.label,
        emoji: def.emoji,
        percent: Math.round(statFraction(def, value) * 100),
        shown: Math.round(value),
        trouble: inTrouble(def, value),
      },
    ];
  }),
);

const alive = computed(() => petState.value?.alive !== false);

const petLine = computed(() => {
  if (!petState.value) return '';
  if (!alive.value) return 'Ваня не выдержал. Откачай его.';
  return flash.value;
});

/**
 * Loads the catalogue and the pet.
 *
 * A failure here is deliberately quiet. The plane is the point of this screen
 * and it runs on the socket, so a pet that cannot be fetched costs the bars and
 * the buttons and nothing else — popping the global modal over a working yard
 * would be a worse answer than showing one less row. A real failure still
 * reaches the modal when the player actually presses something.
 */
async function loadPet(): Promise<void> {
  try {
    const [cfg, st] = await Promise.all([
      config.value ? Promise.resolve(config.value) : gameVanyagotchiApi.config(),
      gameVanyagotchiApi.state(),
    ]);
    config.value = cfg;
    applyPetState(st);
  } catch {
    /* the yard still works; see above */
  }
}

/** Records a fresh state and re-measures the clock skew it was computed against. */
function applyPetState(next: VanyagotchiState): void {
  petState.value = next;
  clockSkew = next.server_now ? skewMs(next.server_now, Date.now()) : 0;
  displayNow.value = Date.now() - clockSkew;
}

/** Applies one catalogue action. The server answers with the state it computed. */
async function act(action: VanyagotchiAction): Promise<void> {
  if (acting.value) return;
  acting.value = true;
  try {
    applyPetState(await gameVanyagotchiApi.act(action.key));
    flash.value = action.done;
  } catch (err) {
    if (err instanceof ApiError && err.code === 'pet_dead') {
      // Live now that a verb exists which cannot revive him: a dead Ваня does
      // not go to the toilet, and the server refuses with a 409. The remedy is
      // already written on the screen — the line below the bars says to bring
      // him round — so this clears the stale `done` text and re-reads, rather
      // than popping the global "something went wrong" modal over a situation
      // the player is being told how to fix.
      flash.value = '';
      await loadPet();
    } else {
      errorStore.report(err);
    }
  } finally {
    acting.value = false;
  }
}

/**
 * Re-reads the pet when the tab comes back.
 *
 * Both events, for the same reason the socket listens to both: a bfcache restore
 * fires `pageshow` where `visibilitychange` may not, notably on iOS — and a page
 * restored from the bfcache is exactly the one whose bars are most out of date.
 */
function onWake(): void {
  if (document.visibilityState !== 'visible') return;
  void loadPet();
}

const planeEl = ref<HTMLElement | null>(null);

/**
 * Elements by peer id, and their last known positions. Both plain Maps, neither
 * reactive, neither ever read during render — this is the imperative tier.
 */
const peerEls = new Map<string, HTMLElement>();
const lastPos = new Map<string, { x: number; y: number }>();

/**
 * What everybody looks like — the reactive tier, and the only part of a roster
 * frame that is allowed in here.
 *
 * A shallowRef holding a frozen array, exactly like the store's peerIds and for
 * the same reason: the whole array is replaced when something really changes and
 * nothing deeper is tracked. It is replaced only when sameAppearance says a face,
 * a name or a pose actually moved, so the five frames a second that describe an
 * unchanged yard cost one comparison and no render.
 */
const appearance = shallowRef<readonly PeerAppearance[]>([]);

/**
 * How many PEOPLE are in the yard — the number the status row shows.
 *
 * Its own ref rather than `drawn.length`, because the two stopped being the same
 * number: the roster carries the NPCs and everybody who is asleep in it as well,
 * and all of those are entities to draw and none of them is somebody you can
 * talk to. The server counts it and sends it, so this screen never has to hold
 * an opinion about which entity is a person. A ref of a primitive only notifies
 * on a real change, so the five frames a second that say "still two" cost
 * nothing.
 */
const here = ref(0);

/**
 * Everybody, ready to draw: the wire's appearance with its art key resolved
 * against the catalogue this screen already fetched.
 *
 * Recomputed when appearance changes or when the catalogue finally arrives — the
 * plane runs on the socket and the catalogue comes over HTTP, so the yard is
 * routinely populated before it lands, and every dot is a placeholder until it
 * does.
 */
const drawn = computed(() =>
  appearance.value.map((peer) => ({ ...peer, ...resolveArt(config.value?.skins, peer.art) })),
);

let release: (() => void) | undefined;

/**
 * How long a disconnected plane keeps showing the world it last saw.
 *
 * A phone loses its socket constantly — a tunnel, a lock screen, a handover
 * between cells — and reconnecting takes a second. Emptying the yard the instant
 * the socket drops made every one of those look like everybody had left, which
 * is both alarming and wrong. So the dots stay, visibly stale, for long enough
 * to cover an ordinary reconnect, and are cleared only if the outage outlasts
 * that. Longer than the reconnect backoff's first few attempts, short enough
 * that nobody studies a frozen world believing it is live.
 */
const STALE_HOLD_MS = 8_000;

/** True while the plane is showing a world that is no longer being updated. */
const isStale = ref(false);
let staleTimer: number | undefined;

function clearStaleTimer() {
  if (staleTimer !== undefined) {
    window.clearTimeout(staleTimer);
    staleTimer = undefined;
  }
}

/** Drops the world entirely: nothing on screen, nothing remembered. */
function forgetWorld() {
  clearStaleTimer();
  isStale.value = false;
  store.clearRoster();
  appearance.value = [];
  here.value = 0;
  lastPos.clear();
  peerEls.clear();
}

const statusLabel = computed(() => {
  switch (store.status) {
    case 'connecting':
      return 'подключаемся…';
    case 'open':
      return 'на связи';
    case 'closed':
      return `${closedLabel(store.closeDetail?.code)} — переподключаемся`;
    case 'terminal':
      return closedLabel(store.closeDetail?.code);
    default:
      return 'не подключено';
  }
});

/**
 * The close code arrives in a `bye` frame rather than as a WebSocket close code
 * — a browser reports 1006 for everything. Saying which of the three happened
 * is the whole reason that frame exists, so the screen says it.
 */
function closedLabel(code: number | undefined): string {
  switch (code) {
    case 1001:
      return 'сервер перезапускается';
    case 1013:
      return 'слишком много подключений';
    case 4001:
      return 'доступ отозван';
    default:
      return 'связь потеряна';
  }
}

const emptyMessage = computed(() =>
  store.status === 'open' ? 'во дворе пусто' : 'ждём двор…',
);

/**
 * A stable colour per connection, derived rather than stored.
 *
 * The id is a server-side UUID, so hashing it gives every dot the same colour on
 * every screen at once with nothing to synchronise — and a peer keeps its colour
 * across frames without any of it entering the reactive graph.
 *
 * KEPT now that the roster carries art, rather than replaced by the skin's own
 * gradient, and the reason is what the two answer. The hash answers *which one
 * is that*; the art answers *what is that*. There is one skin in the catalogue
 * today, so painting dots with it would make a yard of twenty players twenty
 * identical dots — the art would be doing no work and the only thing that
 * distinguished anybody would have been thrown away to make room for it. They
 * layer instead: hue underneath, face on top.
 */
function peerColour(id: string): string {
  let hash = 0;
  for (let i = 0; i < id.length; i += 1) {
    hash = (hash * 31 + id.charCodeAt(i)) >>> 0;
  }
  return `hsl(${hash % 360} 70% 55%)`;
}

/**
 * Collects the element for a peer as Vue creates it.
 *
 * A newly-created dot is positioned immediately and WITHOUT a transition: its
 * custom properties are unset until the first write, so a transition here would
 * animate it in from the plane's top-left corner every time somebody joined.
 */
function setPeerEl(id: string, el: Element | null) {
  if (!el) {
    peerEls.delete(id);
    return;
  }
  const node = el as HTMLElement;
  // Already holding this exact element, so this is a re-render rather than an
  // arrival, and there is nothing to place instantly.
  //
  // Vue invokes a function ref on EVERY patch, not only when the element
  // changes — and it re-invokes regardless of the callback's identity, so a
  // stable callback would not help. Without this guard the "first placement"
  // path below ran on every re-render, which since the stat bars started
  // redrawing once a second means roughly once a second forever: each time
  // adding `peer--instant`, which suppresses the transition for that frame. A
  // move landing in one of those windows jumped instead of gliding.
  if (peerEls.get(id) === node) return;

  peerEls.set(id, node);
  const known = lastPos.get(id);
  if (!known) return;
  node.classList.add('peer--instant');
  // Through the same writer the frames use, so a dot cannot arrive holding a
  // position without the depth that goes with it — it would be drawn at the
  // back of the plane at full size for the 200 ms until the next frame.
  applyPosition(node, known.x, known.y);
  requestAnimationFrame(() => node.classList.remove('peer--instant'));
}

function onFrame(frame: RealtimeFrame) {
  if (frame.t === TYPE_YOU) {
    if (typeof frame.id === 'string' && frame.id) store.setYou(frame.id);
    return;
  }
  if (frame.t !== TYPE_ROSTER) return;
  const peers = Array.isArray(frame.peers) ? frame.peers : [];

  // Who is here, and what they look like, from one pass through one guard — so
  // the keyed list and the head count cannot end up disagreeing about the yard.
  const looks = readAppearances(peers);
  const ids = looks.map((look) => look.id);
  here.value = readHere(frame.here, ids.length);

  for (const peer of peers) {
    if (!isRenderablePosition(peer)) continue;
    lastPos.set(peer.id, { x: peer.x, y: peer.y });
  }
  // Every frame is full state, so anybody absent from it has gone.
  for (const id of [...lastPos.keys()]) {
    if (!ids.includes(id)) lastPos.delete(id);
  }

  // The two reactive facts first — each usually a no-op, and each behind its own
  // guard so an unchanged yard costs no render — then positions, which are not
  // reactive at all.
  store.applyRoster(ids);
  if (!sameAppearance(appearance.value, looks)) appearance.value = looks;
  applyFrame(peers, peerEls);
}

function onStatus(status: ConnectionStatus, detail?: Parameters<typeof store.setStatus>[1]) {
  store.setStatus(status, detail);
  if (status === 'open') {
    // Every time, not just the first: a reconnect is a new connection and the
    // pseudonym that identifies us is derived per connection lifetime.
    client.send({ t: TYPE_HELLO });
  }
  if (status === 'open') {
    clearStaleTimer();
    isStale.value = false;
    return;
  }
  if (status === 'terminal') {
    // No reconnect is coming, so there is nothing to hold the world for.
    forgetWorld();
    return;
  }
  // Down, but probably briefly. Keep the last world on screen and SAY it is
  // stale rather than pretending it is live — showing a frozen plane as though
  // it were current would be the actual lie. Clear only if the outage lasts.
  if (store.peerIds.length === 0) return;
  isStale.value = true;
  clearStaleTimer();
  staleTimer = window.setTimeout(forgetWorld, STALE_HOLD_MS);
}

/** A tap is a request to stand somewhere. The server decides whether it happens. */
function onPlaneTap(event: PointerEvent) {
  const el = planeEl.value;
  if (!el) return;
  const pos = tapToPosition(el.getBoundingClientRect(), event.clientX, event.clientY);
  if (!pos) return;
  // Deliberately no optimistic move: the dot moves when the server says it
  // moved, which is the same rule every other client is playing by.
  client.send({ t: TYPE_MOVE, x: pos.x, y: pos.y });
}

/**
 * Enter the yard, and only then open the socket.
 *
 * Deliberately not on mount. Connecting while the intro is still on screen would
 * put your dot in front of everybody else before you had read the disclaimer,
 * and would spend one of the three connections the server allows per account on
 * a screen that shows no one.
 */
function enterYard() {
  if (release) return;
  phase.value = 'play';
  release = client.subscribe({ frames: onFrame, status: onStatus });

  // The pet comes with the yard, not with the route: fetching it behind the
  // intro would spend a request on a screen that shows none of it.
  void loadPet();
  displayNow.value = Date.now() - clockSkew;
  displayTimer = window.setInterval(() => {
    displayNow.value = Date.now() - clockSkew;
  }, DISPLAY_TICK_MS);
  document.addEventListener('visibilitychange', onWake);
  window.addEventListener('pageshow', onWake);
}

onBeforeUnmount(() => {
  release?.();
  release = undefined;
  clearStaleTimer();
  if (displayTimer !== undefined) {
    window.clearInterval(displayTimer);
    displayTimer = undefined;
  }
  document.removeEventListener('visibilitychange', onWake);
  window.removeEventListener('pageshow', onWake);
  peerEls.clear();
  lastPos.clear();
  // The socket may outlive this view by the grace period, but nothing is
  // rendering its frames any more — leaving the roster behind would show the
  // next visit a world that stopped updating when we left.
  store.clearRoster();
  appearance.value = [];
  here.value = 0;
});
</script>

<style scoped>
/* Height = visible viewport minus the app bar, with headroom so the mobile
   browser's top/bottom chrome never triggers a scrollbar. Copied from the first
   game, including the 72px: it is the empirical value that survived the bug
   where chrome sliding in produced a permanent scrollbar. */
.play-root {
  height: calc(100dvh - 72px);
  overflow: hidden;
}

/* Intro: everything fixed except nothing — there is no artwork yet, so the
   column simply centres. The disclaimer is `flex: 0 0 auto` like every other
   text row, so it cannot be the thing squeezed out on a short screen. */
.splash {
  height: 100%;
  border-radius: 16px;
  padding: 12px 16px 16px;
  display: flex;
  flex-direction: column;
  align-items: center;
  justify-content: center;
  text-align: center;
  gap: 8px;
  overflow: hidden;
  background: linear-gradient(160deg, #2b1b3d, #123043);
  color: rgba(255, 255, 255, 0.95);
}
/* Capped by both em and viewport: on a 568px-tall phone a fixed 84px emoji is
   most of the budget the copy below needs. */
.splash-emoji {
  flex: 0 0 auto;
  font-size: min(84px, 14svh);
  line-height: 1;
}
.splash-title {
  flex: 0 0 auto;
  font-size: 1.6rem;
  font-weight: 800;
  letter-spacing: 0.5px;
}
/* The two prose blocks are the ONLY shrinkable children on this screen, and that
   is deliberate: `overflow: hidden` on the splash means something has to give on
   a short phone, and it must not be the call to action or the disclaimer. So
   these two clip and everything else keeps its size. */
.splash-lore,
.splash-intro {
  flex: 0 1 auto;
  min-height: 0;
  overflow: hidden;
  max-width: 560px;
  line-height: 1.45;
}
.splash-lore {
  font-size: 0.95rem;
  opacity: 0.9;
}
.splash-cta {
  flex: 0 0 auto;
  min-width: 220px;
}
.splash-disclaimer {
  flex: 0 0 auto;
  font-size: 0.76rem;
  opacity: 0.72;
  max-width: 560px;
}

.stage {
  height: 100%;
  display: flex;
  flex-direction: column;
  gap: 8px;
  padding: 8px 12px 12px;
  overflow: hidden;
}

/* The frame is the ONLY flexible child, and it is what absorbs and gives up
   slack. `min-height: 0` says a flex item may shrink below its content, which is
   what stops the rows below being pushed off a short screen.
   Measured, so the comment does not overclaim: with the pet panel on the screen,
   removing it no longer breaks any layout assertion — `overflow: hidden` on
   `.stage` is carrying that now. It stays because it is the correct declaration
   for the one flexible child and because the thing it guards against is a silent
   regression, not because a test currently fails without it. */
.plane-frame {
  flex: 1 1 auto;
  min-height: 0;
  display: flex;
  align-items: center;
  justify-content: center;
  /* A size container so the plane below can be sized from THIS box rather than
     from the viewport — the frame is what is left after the app bar and the
     status row, and only it knows how much that is. */
  container-type: size;
}

/* THE YARD HAS A FIXED SHAPE, and that is a rule of the game rather than a
   layout preference.
   Coordinates on the wire are normalised 0..1 per axis, so a plane that simply
   took whatever space was left would give a phone a tall world and a tablet a
   wide one: the same coordinates, but different distances between them. Two
   players would not be looking at the same place. It matters more from Phase 2
   on, where distance is a mechanic — the beer delivery is a race to ARRIVE, and
   walking speed is defined in plane-widths per second, which only means anything
   if a plane-width is the same shape for everybody.
   3:4 portrait because that is close to what a phone already has left after the
   app bar and the status row, so phones lose almost nothing and only tablets and
   landscape letterbox. Changing this ratio changes the shape of the world for
   everyone at once, which is the point: it is one number, in one place.
   `container-type: size` is what lets a dot be placed in `cqw`/`cqh`, so the
   0..1 coordinates map to pixels entirely in CSS — there is no measured box
   cached in JavaScript to invalidate when mobile chrome slides and this resizes. */
.plane {
  /* Fit a 3:4 box inside the frame, exactly, in CSS alone.
     `75cqh` is the width at which a 3:4 box is precisely as tall as the frame,
     and `100cqw` is the width at which it is precisely as wide — so the smaller
     of the two is the largest 3:4 box that fits, whichever way the frame is
     shaped. Height then follows from the ratio.
     Two blind alleys, recorded so they are not retried: `max-width`/`max-height`
     alone leaves a flex item with no base size and it collapses to nothing; and
     a definite height with `width: auto` does not re-derive the height when
     `max-width` clamps the transferred width, so the ratio silently breaks on a
     narrow frame (measured 336x685 — 0.49, not 0.75). */
  width: min(100cqw, 75cqh);
  aspect-ratio: 3 / 4;
  container-type: size;
  position: relative;
  overflow: hidden;
  border-radius: 12px;
  background: linear-gradient(180deg, #24384a 0%, #35506a 55%, #4a6b57 100%);
  touch-action: manipulation;
}
.plane-empty {
  position: absolute;
  inset: 0;
  display: flex;
  align-items: center;
  justify-content: center;
  color: rgba(255, 255, 255, 0.6);
  font-size: 0.85rem;
  pointer-events: none;
}

/* A dot is placed purely by transform, never by `top`/`left`: transform is
   composited, and the custom properties are the only thing JavaScript writes.
   The -50% centres it on its own coordinates.
   The duration tracks the server's BroadcastInterval (200 ms, see
   internal/gamevanyagotchi/service.go) times about 1.1, so consecutive segments
   overlap slightly instead of visibly stopping between frames. `linear` because
   consecutive eased segments pulse.

   DEPTH rides on the same two declarations. `--depth` is the scale of the band
   the dot's `--y` puts it in and `--band` is that band's index, used directly as
   the stacking order — so "further down is nearer" is one scale and one z-index,
   both written imperatively alongside the position and neither of them reactive.
   Because the scale is part of the same `transform`, a band change GLIDES over
   the same 220 ms as the movement that caused it rather than snapping, which is
   what makes four discrete sizes read as depth instead of as four sizes.

   `transform-origin: 50% 100%` is not optional: scale about the centre and a dot
   grows in both directions, so the bottom of it — where the character meets the
   ground — slides down as it comes nearer and the whole yard looks like it is
   floating. Scaling about the feet keeps them on the floor.
   Deliberately NOT a `perspective`/`rotateX` floor and NOT `preserve-3d`: the
   first breaks `getBoundingClientRect`, and with it every measurement the
   Playwright suites make; the second makes the browser ignore `z-index`
   entirely, which is the half of depth this is actually for. */
.peer {
  position: absolute;
  top: 0;
  left: 0;
  /* THE TAP-TARGET FLOOR. The mobile suite measures this box, and the nearest
     band scales it UP (see DEPTH_SCALES — the far band is 1), so 44 px is the
     smallest an entity is ever drawn rather than the largest. */
  width: 44px;
  height: 44px;
  border-radius: 50%;
  border: 2px solid rgba(255, 255, 255, 0.85);
  box-shadow: 0 2px 6px rgba(0, 0, 0, 0.35);
  transform: translate3d(
      calc(var(--x, 0.5) * 100cqw - 50%),
      calc(var(--y, 0.5) * 100cqh - 50%),
      0
    )
    scale(var(--depth, 1));
  transform-origin: 50% 100%;
  z-index: var(--band, 0);
  transition: transform 220ms linear;
  will-change: transform;
  pointer-events: none; /* the plane is the tap target, not the dots */
}
/* A world that is no longer being updated, held on screen across a reconnect.
   Dimmed and drained of colour so it reads as "this is what was there" rather
   than as the current state — the status row alongside says why. */
.plane--stale .peer {
  opacity: 0.4;
  filter: grayscale(0.8);
}
.plane--stale {
  filter: brightness(0.85);
}
.plane,
.plane .peer {
  transition-property: transform, opacity, filter;
  transition-duration: 220ms, 400ms, 400ms;
  transition-timing-function: linear, ease, ease;
}

/* First placement: jump, do not fly in from the corner. */
.peer--instant {
  transition: none;
}
/* Your own Ваня. Marked by a second ring rather than by colour, because the
   colour is already carrying identity — hashed from the id so everybody's dot
   looks the same on every screen — and overriding it here would make you the
   one player who cannot see what colour you are to everyone else. */
.peer--you {
  border-color: #fff;
  box-shadow:
    0 0 0 3px rgba(0, 0, 0, 0.45),
    0 2px 8px rgba(0, 0, 0, 0.5);
  /* No z-index of its own any more. It used to be 1, which was harmless while
     everything else was 0 and is wrong now that the stacking order MEANS
     something: lifting your own Ваня above his band would draw him in front of
     somebody standing nearer the viewer than he is, in a yard where that is the
     one cue for how far away anybody is. The ring already says which one is you,
     and it says it without lying about where you are standing. */
}

/* Everybody's face, centred on their dot. `pointer-events: none` is inherited
   from .peer — the plane is the tap target, not the sprite. */
.peer-face {
  position: absolute;
  inset: 0;
  display: flex;
  align-items: center;
  justify-content: center;
  font-size: 26px;
  line-height: 1;
}

/* A sprite, once the catalogue has one for this art key. Inset rather than
   filling the dot, so the entity's own colour survives as a rim: the picture
   says WHAT this is and the hue says WHO, and a skin everybody happens to be
   wearing would otherwise erase the second. */
.peer-sprite {
  width: calc(100% - 6px);
  height: calc(100% - 6px);
  border-radius: 50%;
  object-fit: cover;
}

/* The name under the dot, capped in three independent places, because several
   long names in a small yard is the case that breaks this screen. `capLabel`
   caps the string, this caps the width, and the plane's own `overflow: hidden`
   is the backstop. Measured at 360x800, 390x844, 768x1024 and 320x568 with eight
   dots, four of them named far past the cap and one at each edge: no horizontal
   or vertical page overflow at any of them, so "never scrolls" survives labels.

   What the same measurement DOES show is that a name on a dot within half a
   label of an edge is clipped by the plane — half of it at the sides, all of it
   along the bottom. That is left alone deliberately. The fix would be to clamp
   the label inside the plane (`left: clamp(...)` off `--x`, which works), and the
   thing it buys is worse than the thing it costs: with the name no longer
   centred on its dot it drifts up to half a label sideways, and in a busy yard a
   drifted name reads as belonging to the dot NEXT to it. A cut name is still
   unambiguously this Ваня's; a wrong one is not. Clipping is also what the plane
   already does to a dot standing on the edge — half a circle — so the boundary
   behaves the same way for everything on it. */
.peer-label {
  position: absolute;
  top: 100%;
  left: 50%;
  transform: translateX(-50%);
  /* Sized in the WORLD's units before the device's: a 3:4 plane is 231px across
     on a 320px phone and 573px on a tablet, and a name that is a third of the
     yard wide on one and a seventh on the other is two different games. The px
     ceiling is what stops it growing past legible on a big screen. */
  max-width: min(80px, 30cqw);
  overflow: hidden;
  white-space: nowrap;
  text-overflow: ellipsis;
  text-align: center;
  font-size: 10px;
  line-height: 1.25;
  color: rgba(255, 255, 255, 0.94);
  /* Legible over both ends of the plane's gradient without a background plate,
     which at this size would be most of what you saw. */
  text-shadow: 0 1px 2px rgba(0, 0, 0, 0.8);
}
/* Dead reads at a glance without anybody having to look at a bar. */
.peer-face[data-condition='dead'] {
  filter: grayscale(1);
  opacity: 0.85;
}
/* Poorly wobbles, gently. Motion-sensitive users opt out below. */
.peer-face[data-condition='poorly'] {
  animation: peer-wobble 1.6s ease-in-out infinite;
}
@keyframes peer-wobble {
  0%,
  100% {
    transform: rotate(-6deg);
  }
  50% {
    transform: rotate(6deg);
  }
}

/* ASLEEP: a Ваня whose owner is not here, lying where he last stood.
   This is what stops a solo visit being an empty field, so it has to read as
   "somebody, dormant" rather than as "somebody, slightly faded" — a sleeper that
   looked merely dim would be indistinguishable from a player who is standing
   still, and the yard would seem full of people ignoring you.
   Three cues, none of which is colour alone: the whole dot dims, the face topples
   over, and a 💤 floats off it. */
.peer--asleep {
  opacity: 0.55;
  filter: saturate(0.45);
}
/* On the dot rather than on the face, because the face is rotated below and a
   💤 rotated with it would end up at his feet. */
.peer--asleep::after {
  content: '💤';
  position: absolute;
  top: -10px;
  right: -6px;
  font-size: 15px;
  line-height: 1;
  /* The dot it sits on is at 55% opacity and so, being a child, is this — which
     over the pale bottom of the plane's gradient was very nearly invisible at
     360 px. The shadow is what gives it an edge to be seen against. */
  text-shadow: 0 1px 2px rgba(0, 0, 0, 0.75);
}
/* Lying down. The one condition drawn as a rotation instead of a filter: at 44px
   a change of ORIENTATION is visible across the yard where a change of tone is
   not, and it is also literally what the server means — he is lying where he
   was standing when his owner left. */
.peer-face[data-condition='asleep'] {
  transform: rotate(74deg);
}

/* A line over somebody's head, for the few seconds the server keeps it on the
   wire. Capped in the same three independent places the name is — capSay caps
   the string, this caps the width, and the plane's `overflow: hidden` is the
   backstop — because a balloon is wider than a name and the yard is small.

   `pointer-events: none` is stated rather than left to inherit from `.peer`: the
   plane is the tap target, and a balloon that swallowed a tap would make the
   ground under a talking Ваня briefly unwalkable — an intermittent bug of
   exactly the kind nobody reports usefully.

   Above the head normally, and BELOW it in the top sixth of the plane, switched
   by `--say-below` — written imperatively off `--y` by the same call that writes
   the position (see sayBelow). The plane clips whatever leaves it, and unlike a
   clipped name, which is still legibly somebody's name, a clipped balloon is
   nothing at all: the line is transient, so if it is not readable now it is not
   readable. The drop clears the dot and the name underneath it. */
.peer-say {
  position: absolute;
  left: 50%;
  bottom: calc(100% + 6px);
  transform: translateX(-50%) translateY(calc(var(--say-below, 0) * 88px));
  max-width: min(96px, 34cqw);
  padding: 2px 6px;
  border-radius: 9px;
  background: rgba(12, 18, 26, 0.86);
  color: rgba(255, 255, 255, 0.95);
  font-size: 10px;
  line-height: 1.3;
  white-space: nowrap;
  overflow: hidden;
  text-overflow: ellipsis;
  pointer-events: none;
}

/* The non-plane block: fixed size, and the plane pays for it rather than the
   other way round. */
.panel {
  flex: 0 0 auto;
  display: flex;
  flex-direction: column;
  gap: 8px;
  min-width: 0;
}

/* Stat bars. Fixed height, and one row per stat: three today, and the row is a
   grid so a fourth arrives without a layout decision. Each row costs about 19px
   and the plane pays for it, which is measured rather than assumed — the 320x568
   case in the mobile suite is what holds that. */
.stats {
  flex: 0 0 auto;
  display: flex;
  flex-direction: column;
  gap: 4px;
}
.stat {
  display: grid;
  grid-template-columns: 20px 1fr 34px;
  align-items: center;
  gap: 6px;
  min-width: 0;
}
.stat-emoji {
  font-size: 15px;
  line-height: 1;
  text-align: center;
}
.stat-track {
  height: 8px;
  border-radius: 4px;
  background: rgba(127, 127, 127, 0.28);
  overflow: hidden;
}
.stat-fill {
  display: block;
  height: 100%;
  border-radius: 4px;
  background: rgb(var(--v-theme-success));
  transition: width 400ms linear;
}
/* Trouble is a colour, and which values count as trouble is catalogue data —
   the stylesheet is told, it does not know that 30 is a bad amount of health. */
.stat[data-trouble='1'] .stat-fill {
  background: rgb(var(--v-theme-warning));
}
.stat-value {
  font-size: 0.72rem;
  font-variant-numeric: tabular-nums;
  text-align: right;
  opacity: 0.85;
}

/* Always present, empty or not, so the plane above does not resize when a line
   of text appears and disappears. */
.petline {
  flex: 0 0 auto;
  min-height: 1.1rem;
  font-size: 0.76rem;
  line-height: 1.1rem;
  text-align: center;
  opacity: 0.85;
  white-space: nowrap;
  overflow: hidden;
  text-overflow: ellipsis;
}

/* auto-fit so one button fills the row and four share it, each still at least a
   thumb wide. */
.actions {
  flex: 0 0 auto;
  display: grid;
  grid-template-columns: repeat(auto-fit, minmax(64px, 1fr));
  gap: 8px;
}
/* 44px is the tap-target floor the mobile suite enforces; Vuetify's default
   button is 36. */
.action-btn {
  min-height: 44px;
}

.hud {
  flex: 0 0 auto;
  display: flex;
  align-items: center;
  justify-content: space-between;
  gap: 8px;
  min-width: 0;
  font-size: 0.78rem;
  line-height: 1.2;
  opacity: 0.85;
}
.hud-count,
.hud-status {
  white-space: nowrap;
  overflow: hidden;
  text-overflow: ellipsis;
}
.hud-status--open {
  color: rgb(var(--v-theme-success));
}
.hud-status--closed {
  color: rgb(var(--v-theme-warning));
}
.hud-status--terminal {
  color: rgb(var(--v-theme-error));
}

@media (prefers-reduced-motion: reduce) {
  .peer {
    transition: none;
  }
  .peer-face[data-condition='poorly'] {
    animation: none;
  }
  .stat-fill {
    transition: none;
  }
}

/* Landscape phones have ~350px of height: keep the status row beside the plane
   rather than under it, or the plane collapses to nothing. */
@media (orientation: landscape) and (max-height: 600px) {
  .stage {
    flex-direction: row;
    align-items: stretch;
  }
  /* Beside the plane rather than under it: there are ~350px of height here, and
     stacking four fixed rows below the plane collapses it to nothing. Capped so
     a wide screen does not hand the panel half the world. */
  .panel {
    width: min(46%, 260px);
    justify-content: flex-start;
    overflow: hidden;
  }
  .hud {
    flex: 0 0 auto;
    flex-direction: column;
    justify-content: flex-start;
    align-items: flex-end;
  }
  .plane-frame {
    min-width: 0;
  }
}
</style>
