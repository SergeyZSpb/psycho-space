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
      <div
        ref="planeEl"
        class="plane"
        data-test="plane"
        role="application"
        :aria-label="`Двор, во дворе ${store.peerIds.length}`"
        @pointerdown="onPlaneTap"
      >
        <div
          v-for="id in store.peerIds"
          :key="id"
          :ref="(el) => setPeerEl(id, el as Element | null)"
          class="peer"
          data-test="peer"
          :data-peer="id"
          :style="{ background: peerColour(id) }"
        />
        <p v-if="store.peerIds.length === 0" class="plane-empty">
          {{ emptyMessage }}
        </p>
      </div>

      <!-- Fixed-size status row. It costs the plane its height, never the other
           way round. -->
      <div class="hud">
        <span class="hud-count">во дворе: {{ store.peerIds.length }}</span>
        <span class="hud-status" :class="`hud-status--${store.status}`">
          {{ statusLabel }}
        </span>
      </div>
    </div>
  </v-container>
</template>

<script setup lang="ts">
import { computed, onBeforeUnmount, ref } from 'vue';
import { realtimeClient, type ConnectionStatus, type RealtimeFrame } from '../realtime/socket';
import { useGameVanyagotchiStore } from '../stores/gameVanyagotchi';
import { applyFrame, isRenderablePosition, tapToPosition } from '../lib/vanyagotchiPlane';

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

type Phase = 'intro' | 'play';
const phase = ref<Phase>('intro');

const store = useGameVanyagotchiStore();
const client = realtimeClient();

const planeEl = ref<HTMLElement | null>(null);

/**
 * Elements by peer id, and their last known positions. Both plain Maps, neither
 * reactive, neither ever read during render — this is the imperative tier.
 */
const peerEls = new Map<string, HTMLElement>();
const lastPos = new Map<string, { x: number; y: number }>();

let release: (() => void) | undefined;

const statusLabel = computed(() => {
  switch (store.status) {
    case 'connecting':
      return 'подключаемся…';
    case 'open':
      return 'на связи';
    case 'closed':
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
  if (status === 'closed' || status === 'idle') {
    // Leaving the dots on a dead socket would show a world that is no longer
    // being updated as though it were live.
    store.clearRoster();
    lastPos.clear();
    peerEls.clear();
  }
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
}

onBeforeUnmount(() => {
  release?.();
  release = undefined;
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
.splash-emoji {
  flex: 0 0 auto;
  font-size: 84px;
  line-height: 1;
}
.splash-title {
  flex: 0 0 auto;
  font-size: 1.6rem;
  font-weight: 800;
  letter-spacing: 0.5px;
}
.splash-intro {
  flex: 0 0 auto;
  max-width: 560px;
  line-height: 1.45;
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

/* The plane is the ONLY flexible child. `min-height: 0` is load-bearing: without
   it a flex item refuses to shrink below its content and the status row below
   gets pushed off the screen.
   `container-type: size` is what lets a dot be placed in `cqw`/`cqh`, so the
   normalised 0..1 coordinates map to pixels entirely in CSS — there is no
   measured box cached in JavaScript to invalidate when mobile chrome slides and
   the plane resizes. */
.plane {
  flex: 1 1 auto;
  min-height: 0;
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
/* First placement: jump, do not fly in from the corner. */
.peer--instant {
  transition: none;
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
  color: rgb(var(--v-theme-error));
}

@media (prefers-reduced-motion: reduce) {
  .peer {
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
  .hud {
    flex: 0 0 auto;
    flex-direction: column;
    justify-content: flex-start;
    align-items: flex-end;
  }
}
</style>
