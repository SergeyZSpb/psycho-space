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
        :aria-label="`Двор, во дворе ${store.peerIds.length}`"
        @pointerdown="onPlaneTap"
      >
        <div
          v-for="id in store.peerIds"
          :key="id"
          :ref="(el) => setPeerEl(id, el as Element | null)"
          class="peer"
          :class="{ 'peer--you': id === store.youId }"
          data-test="peer"
          :data-you="id === store.youId ? '1' : undefined"
          :data-peer="id"
          :style="{ background: peerColour(id) }"
        >
          <!-- Your own Ваня wears his face and his condition. Everybody else is
               still a plain dot: the roster does not carry a skin yet, and
               inventing one client-side would show two people different worlds. -->
          <span
            v-if="id === store.youId && face"
            class="peer-face"
            data-test="peer-face"
            :data-condition="petCondition"
            >{{ face }}</span
          >
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
        <span class="hud-count">во дворе: {{ store.peerIds.length }}</span>
        <span class="hud-status" :class="`hud-status--${store.status}`">
          {{ statusLabel }}
        </span>
      </div>
      </div>
    </div>
  </v-container>
</template>

<script setup lang="ts">
import { computed, onBeforeUnmount, ref } from 'vue';
import { realtimeClient, type ConnectionStatus, type RealtimeFrame } from '../realtime/socket';
import { useGameVanyagotchiStore } from '../stores/gameVanyagotchi';
import { applyFrame, isRenderablePosition, tapToPosition } from '../lib/vanyagotchiPlane';
import {
  condition,
  decayedValue,
  inTrouble,
  skewMs,
  statFraction,
  type PetCondition,
} from '../lib/vanyagotchiPet';
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
// The two-tier state split is the other thing to preserve. Membership goes
// through pinia and a keyed v-for; POSITIONS DO NOT ENTER VUE AT ALL. A frame
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
const petCondition = computed<PetCondition>(() =>
  condition(stats.value, values.value, alive.value),
);

/** The skin the server says this pet wears, resolved against the catalogue. */
const skin = computed(
  () => config.value?.skins?.find((s) => s.key === petState.value?.pet?.skin_key) ?? null,
);

/** What to draw on your own dot. A death is legible without reading anything. */
const face = computed(() => {
  if (!petState.value) return '';
  if (!alive.value) return '💀';
  return skin.value?.emoji ?? '';
});

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
  peerEls.set(id, node);
  const known = lastPos.get(id);
  if (!known) return;
  node.classList.add('peer--instant');
  node.style.setProperty('--x', String(known.x));
  node.style.setProperty('--y', String(known.y));
  requestAnimationFrame(() => node.classList.remove('peer--instant'));
}

function onFrame(frame: RealtimeFrame) {
  if (frame.t === TYPE_YOU) {
    if (typeof frame.id === 'string' && frame.id) store.setYou(frame.id);
    return;
  }
  if (frame.t !== TYPE_ROSTER) return;
  const peers = Array.isArray(frame.peers) ? frame.peers : [];

  const ids: string[] = [];
  for (const peer of peers) {
    if (!isRenderablePosition(peer)) continue;
    ids.push(peer.id);
    lastPos.set(peer.id, { x: peer.x, y: peer.y });
  }
  // Every frame is full state, so anybody absent from it has gone.
  for (const id of [...lastPos.keys()]) {
    if (!ids.includes(id)) lastPos.delete(id);
  }

  // Membership first (reactive, usually a no-op), then positions (not reactive).
  store.applyRoster(ids);
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
   composited, and the two custom properties are the only thing JavaScript
   writes. The -50% centres it on its own coordinates.
   The duration tracks the server's BroadcastInterval (200 ms, see
   internal/gamevanyagotchi/service.go) times about 1.1, so consecutive segments
   overlap slightly instead of visibly stopping between frames. `linear` because
   consecutive eased segments pulse. */
.peer {
  position: absolute;
  top: 0;
  left: 0;
  width: 44px;
  height: 44px;
  border-radius: 50%;
  border: 2px solid rgba(255, 255, 255, 0.85);
  box-shadow: 0 2px 6px rgba(0, 0, 0, 0.35);
  transform: translate3d(
    calc(var(--x, 0.5) * 100cqw - 50%),
    calc(var(--y, 0.5) * 100cqh - 50%),
    0
  );
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
  z-index: 1;
}

/* Your own Ваня's face, centred on the dot. `pointer-events: none` is inherited
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
