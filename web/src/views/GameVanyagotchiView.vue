<template>
  <v-container
    :class="phase === 'intro' || phase === 'play' ? 'pa-0 play-root' : 'py-4'"
    style="max-width: 900px"
  >
    <!-- Intro / start screen — the RULES CHEATSHEET.
         Everything outside `.splash-scroll` is fixed-size, so the title, the CTA
         and the disclaimer can never be the thing that gets clipped; the
         cheatsheet in the middle is the one child that takes the slack and, on a
         short phone, scrolls INSIDE ITSELF. The page itself still never scrolls,
         which is the layout rule this screen shares with the yard.
         Not one sentence in here is assembled by the template: the stat and
         action rows come out of buildRules() so that retuning a number in
         internal/gamevanyagotchi/content.go changes what the player is told with
         no edit to this file. The prose block is the exception, and says so. -->
    <div v-if="phase === 'intro'" class="splash">
      <div class="splash-emoji">🫃</div>
      <h1 class="splash-title">Ванягоччи</h1>

      <div class="splash-scroll" data-test="rules">
        <p class="splash-lore">
          Ваня — офигенный чел. Он любит пиво, но постоянно теряет ключи, что их
          приходится искать. В этой игре каждый может почувствовать себя Ваней.
        </p>

        <!-- Derived from GET /api/game-vanyagotchi/config. Absent, rather than
             empty, when the catalogue did not arrive — see loadConfig. -->
        <section v-if="rules.stats.length" class="rules" data-test="rules-stats">
          <h2 class="rules-head">Тикает само</h2>
          <div
            v-for="stat in rules.stats"
            :key="stat.key"
            class="rule"
            :data-test="`rule-stat-${stat.key}`"
          >
            <span class="rule-emoji" aria-hidden="true">{{ stat.emoji }}</span>
            <div class="rule-body">
              <p class="rule-line">
                <span class="rule-label">{{ stat.label }}</span>
                <span class="rule-main">{{ stat.drift }}</span>
              </p>
              <p v-for="note in stat.notes" :key="note" class="rule-note">{{ note }}</p>
            </div>
          </div>
        </section>

        <!-- The lifetime tallies, and the reason they are NOT in the section
             above: «тикает само» is a promise that everything under it drifts on
             its own, and a counter is precisely the stat that does not. Listed
             here so the two numbers on the yard screen have a name, and because
             surviving a fresh start is a real rule that nothing else states. -->
        <section v-if="rules.counters.length" class="rules" data-test="rules-counters">
          <h2 class="rules-head">Просто счётчики</h2>
          <div
            v-for="counter in rules.counters"
            :key="counter.key"
            class="rule"
            :data-test="`rule-counter-${counter.key}`"
          >
            <span class="rule-emoji" aria-hidden="true">{{ counter.emoji }}</span>
            <div class="rule-body">
              <p class="rule-line">
                <span class="rule-label">{{ counter.label }}</span>
                <span class="rule-main">{{ counter.note }}</span>
              </p>
            </div>
          </div>
        </section>

        <section v-if="rules.actions.length" class="rules" data-test="rules-actions">
          <h2 class="rules-head">Жмёшь ты</h2>
          <div
            v-for="action in rules.actions"
            :key="action.key"
            class="rule"
            :data-test="`rule-action-${action.key}`"
          >
            <span class="rule-emoji" aria-hidden="true">{{ action.emoji }}</span>
            <div class="rule-body">
              <p class="rule-line">
                <span class="rule-label">{{ action.label }}</span>
                <span class="rule-main">{{ action.effects }}</span>
              </p>
              <p v-for="note in action.notes" :key="note" class="rule-note">{{ note }}</p>
            </div>
          </div>
        </section>

        <!-- The hardcoded half: what the server does not put on the wire. The
             comment on YARD_PROSE names it as the part a rules change has to
             come back and edit by hand. -->
        <section class="rules" data-test="rules-prose">
          <h2 class="rules-head">Двор</h2>
          <p v-for="line in YARD_PROSE" :key="line" class="rule-prose">{{ line }}</p>
        </section>
      </div>

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
            // Something lying on the ground: half the size and no circle around
            // it. Decided by the entity having an EXPIRY and never by a kind —
            // see `drawn` and PeerAppearance.expires.
            'peer--prop': peer.prop,
          }"
          data-test="peer"
          :data-you="peer.id === store.youId ? '1' : undefined"
          :data-peer="peer.id"
          :data-prop="peer.prop ? '1' : undefined"
          :style="{
            // NOT EMITTED AT ALL for a thing on the ground, which is the only way
            // to take the circle away: an inline style beats any class, so a
            // `background: none` in the stylesheet could not undo this one. It is
            // also the honest reading — the colour is hashed from the id to say
            // WHO somebody is, and a deposit is not a who.
            background: peer.prop ? undefined : peerColour(peer.id),
            // How much of its life is left, as a fraction. Unconditional because
            // it is 1 for everybody who is not going anywhere, and unread by any
            // rule but `.peer--prop`'s.
            '--life': peer.life,
          }"
        >
          <!-- Everybody wears their own face and their own condition, and both
               come off the wire rather than out of this screen's own state. The
               yard is one world: a pose worked out locally would be worked out
               from numbers only its owner can see, so your Ваня would look ill
               to you and fine to everybody standing next to him. -->
          <span class="peer-face" data-test="peer-face" :data-condition="peer.pose">
            <!-- ONE element for both kinds of picture — the person's own VK
                 avatar and the catalogue's sprite — because they are the same
                 thing to draw: a round picture inset by a world unit so the
                 identity colour survives as a rim. A second img would be a
                 second place to keep that true.
                 What the two are NOT is the same thing to a test, so the
                 `data-test` says which one this is rather than merely that a
                 picture is there. That is the difference between "the avatar
                 reached the screen" and "something did", and a selector on the
                 class could not tell them apart at all.
                 `referrerpolicy="no-referrer"`: this request starts at our own
                 origin but does not end there — the endpoint answers a redirect
                 to a third-party CDN, and the policy travels with the fetch
                 across it, so the CDN is never told which page of this site the
                 viewer is on. It costs nothing and it is the whole of what the
                 browser would otherwise volunteer.
                 A face that does not arrive — a 404, which is the ordinary
                 answer for an NPC and for anybody VK has no picture of, or a
                 CDN that is down — falls back to the catalogue art rather than
                 drawing the browser's broken-image mark; see onArtError. -->
            <img
              v-if="peer.image"
              class="peer-sprite"
              :data-test="peer.avatar ? 'peer-avatar' : 'peer-sprite'"
              :src="peer.image"
              alt=""
              referrerpolicy="no-referrer"
              @error="onArtError(peer)"
            />
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

      <!-- The lifetime tallies, and deliberately NOT bars: a counter's scale
           runs to a million so the clamp has a bound, so a track drawn against
           it would sit empty forever and say the opposite of what the number
           means. One wrapping row of «emoji label число», which is the whole of
           what a total needs — the panel is the tightest thing on this screen
           and the 320x568 case in the mobile suite is what holds that. -->
      <div v-if="tallies.length" class="tallies" data-test="pet-tallies">
        <span
          v-for="tally in tallies"
          :key="tally.key"
          class="tally"
          :data-test="`tally-${tally.key}`"
        >
          <span class="tally-emoji" aria-hidden="true">{{ tally.emoji }}</span>
          <span class="tally-label">{{ tally.label }}</span>
          <span class="tally-value" :data-test="`tally-value-${tally.key}`">{{ tally.shown }}</span>
        </span>
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
  avatarEndpoint,
  huntRestarted,
  isRenderablePosition,
  propScale,
  readAppearances,
  readHere,
  readHunt,
  resolveArt,
  sameAppearance,
  tapToPosition,
  type PeerAppearance,
} from '../lib/vanyagotchiPlane';
import { decayedValue, inTrouble, skewMs, statFraction } from '../lib/vanyagotchiPet';
import { YARD_PROSE, buildRules } from '../lib/vanyagotchiRules';
import { gameVanyagotchiApi } from '../api/endpoints';
import type { VanyagotchiAction, VanyagotchiConfig, VanyagotchiState } from '../api/types';

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
/** A verb, or a batch of them, on their way to the server. */
const TYPE_DO = 'vanyagotchi_do';
/** Our own pet, pushed after it changes. Not a reply — see onFrame. */
const TYPE_STATE = 'vanyagotchi_state';

type Phase = 'intro' | 'play';
const phase = ref<Phase>('intro');

const store = useGameVanyagotchiStore();
const client = realtimeClient();

// ---------------------------------------------------------------------------
// The pet. Everything below is ordinary HTTP against a persistent thing, and it
// shares nothing with the socket above except this screen — which is the whole
// authority split: presence is in memory and dies with the process, the pet is
// in Postgres and does not.
// ---------------------------------------------------------------------------

const config = ref<VanyagotchiConfig | null>(null);
/**
 * The splash cheatsheet, rebuilt whenever the catalogue arrives.
 *
 * A computed rather than a snapshot taken at mount, because the config is
 * fetched asynchronously and is allowed to fail: the rows appear when it lands
 * and stay absent when it does not, leaving the hardcoded prose on its own. The
 * derivation itself is pure and lives in `buildRules` — the template renders
 * rows and never assembles a sentence.
 */
const rules = computed(() => buildRules(config.value));
const petState = ref<VanyagotchiState | null>(null);
const acting = ref(false);
/**
 * The line under the bars: the two things this SCREEN has to say rather than the
 * server.
 *
 * It used to carry the action's `done` text too, and no longer does: what a
 * verb did is the SERVER's to say, and it says it in the balloon over your own
 * Ваня, where the rest of the yard can read it as well. Duplicating that here
 * would be two renderings of one fact that can disagree.
 *
 * What is left is what nobody else can say. The death notice is the instruction
 * for getting out of a state the player is stuck in, and the hunt line is a
 * TRANSITION — the roster carries which hunt is running, never that one has just
 * begun, so the only place that difference exists at all is in a client that was
 * watching the previous one (see `huntRestarted`).
 */
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
/**
 * How long a button stays disabled after a verb is sent.
 *
 * Not a guess at a round trip — there is no round trip. It is a double-tap
 * guard, set just under the server's own one-second bound on verbs so the
 * button is never grey while the server would in fact accept.
 */
const ACT_COOLDOWN_MS = 900;

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

/**
 * One bar per catalogue stat that IS a bar, in catalogue order — the display
 * order.
 *
 * A lifetime tally is deliberately not one of them. A bar's whole job is to say
 * how full something is, and a counter's scale runs to a million so that the
 * clamp has something to clamp to: drawn as a bar, «выпито пива: 12» is an empty
 * track that will still look empty after a year of drinking, which tells the
 * player the opposite of what the number means. They go to `tallies` below.
 */
const bars = computed(() =>
  stats.value.flatMap((def) => {
    if (def.counter) return [];
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

/**
 * The lifetime tallies: emoji, name, integer, and nothing else.
 *
 * No track, no percentage and no trouble flag — a total has no bad end of the
 * scale, which is why the server never reports one as trouble either
 * (`Stat.Troubled` in decay.go returns false for a counter outright). Still
 * decayed through the same `values` map as everything else rather than read
 * straight off the response: a counter's rate is nought, so the arithmetic is
 * the identity, and routing it through the one path means there is no second
 * place that has to remember a counter is special.
 */
const tallies = computed(() =>
  stats.value.flatMap((def) => {
    if (!def.counter) return [];
    const value = values.value.get(def.key);
    if (value === undefined) return [];
    return [{ key: def.key, label: def.label, emoji: def.emoji, shown: Math.round(value) }];
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

/**
 * Loads the catalogue on its own, for the splash.
 *
 * Separate from `loadPet` because the two reads are not alike. The catalogue is
 * a pure, cacheable GET that describes the game, and the splash is now a rules
 * cheatsheet built out of it, so it is needed BEFORE the player goes in. The
 * state read is not pure at all — it creates the pet and can record a death
 * (ADR-038) — so it stays behind the CTA, where the player has actually asked
 * to play. Fetching both here would mint a Ваня for somebody who only opened
 * the page.
 *
 * Quiet on failure for the same reason as `loadPet`: the cheatsheet loses its
 * derived rows and keeps its prose, and the yard is unaffected.
 */
async function loadCatalogue(): Promise<void> {
  if (config.value) return;
  try {
    config.value = await gameVanyagotchiApi.config();
  } catch {
    /* the splash falls back to YARD_PROSE alone; see above */
  }
}
void loadCatalogue();

/** Records a fresh state and re-measures the clock skew it was computed against. */
function applyPetState(next: VanyagotchiState): void {
  petState.value = next;
  clockSkew = next.server_now ? skewMs(next.server_now, Date.now()) : 0;
  displayNow.value = Date.now() - clockSkew;
}

/**
 * Asks the server to apply one catalogue action.
 *
 * A VERB OVER THE SOCKET, AND NOTHING COMES BACK. No acknowledgement and no
 * error reply, deliberately: the yard already reconciles from a full-state
 * frame five times a second, exactly as it does for a tap, so a verb that never
 * arrives is simply pressed again. What the server decided arrives as STATE
 * instead — a line over your own Ваня that the whole yard can read, and a push
 * of the pet itself so the bars move.
 *
 * There is therefore nothing to await and nothing to catch. `acting` is held
 * only long enough to stop a double-tap becoming two verbs.
 */
function act(action: VanyagotchiAction): void {
  if (acting.value) return;
  // Dropped rather than queued when the socket is down, the same rule a tap
  // follows: the server stamps every event, so a verb queued now and delivered
  // in a minute would be applied at the wrong instant.
  if (!client.send({ t: TYPE_DO, verbs: [action.key] })) return;
  // Cleared rather than set. What the verb DID is the server's to say, and it
  // says it in the balloon; leaving the old line up would caption the new
  // action with the previous one's result.
  flash.value = '';
  acting.value = true;
  window.setTimeout(() => {
    acting.value = false;
  }, ACT_COOLDOWN_MS);
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
 * The hunt this screen has already seen, so that a CHANGE of hunt can be told
 * from the first sight of one.
 *
 * A plain `let` rather than a ref, like `clockSkew` and for the same reason:
 * nothing renders it. What is rendered is the line it decides to put in `flash`,
 * and only at the instant it decides to.
 */
let seenHunt = '';

/** What the screen says when somebody has just lost the keys again. */
const HUNT_LINE = 'нам повезло, ключи снова потерялись';

/**
 * How long that line stays up.
 *
 * Long enough to be read by somebody who was looking at the plane rather than at
 * the panel, short enough that it is gone well before the hunt it announces
 * plausibly ends — a stale «снова потерялись» sitting under the bars while the
 * keys are being claimed by somebody else would be describing the wrong world.
 */
const HUNT_FLASH_MS = 6_000;
let huntTimer: number | undefined;

/**
 * Puts the flavour line up, briefly.
 *
 * The clear is conditional on the line still being OURS, which matters because
 * three things write to `flash` and they must not undo each other: pressing a
 * verb clears it deliberately, a death replaces it (via `petLine`), and a second
 * hunt can start before the first line has expired. Clearing unconditionally
 * would let a timer set six seconds ago wipe whatever happened to be there.
 */
function announceHunt(): void {
  flash.value = HUNT_LINE;
  if (huntTimer !== undefined) window.clearTimeout(huntTimer);
  huntTimer = window.setTimeout(() => {
    huntTimer = undefined;
    if (flash.value === HUNT_LINE) flash.value = '';
  }, HUNT_FLASH_MS);
}

/**
 * The entities whose face this browser has already asked for and not got.
 *
 * Keyed by the entity's id, which is now the same thing as keying by the URL,
 * because the URL is derived from the id rather than sent alongside it: one
 * entity has exactly one address for as long as the process it lives in does.
 * What that costs is worth naming rather than discovering — a picture that
 * appears mid-session, for somebody who had none when the yard first asked, is
 * not picked up until the page is reloaded. That is the right way round anyway,
 * because the alternative is asking again for a picture that is not there, and
 * most of the yard is NPCs, for whom 404 is not a temporary condition.
 *
 * Reactive because `drawn` reads it; it is the one thing in this screen's
 * imperative half that has to notify.
 */
const brokenAvatars = ref(new Set<string>());

/**
 * A face that did not arrive, remembered so the fallback can take over.
 *
 * A MISSING AVATAR MUST NEVER LEAVE A HOLE WHERE A ВАНЯ IS, and missing is the
 * common case rather than the exceptional one: the endpoint answers 404 for
 * every NPC in the yard and for every player VK has no picture of, and beyond
 * that the picture itself comes from a CDN this project neither runs nor can
 * repair. Left to itself the browser answers all of those with its broken-image
 * mark, which reads as a bug in the yard rather than as a face nobody has.
 *
 * Remembering it is also what stops the asking repeating. The browser is told
 * to cache the answer for five minutes, 404s included, but a cache is a
 * courtesy and the keyed list rebuilds an element every time the yard's
 * membership changes; this makes the fallback a fact of the render rather than
 * something re-derived from a request that may go out again.
 */
function onArtError(peer: { id: string; avatar: boolean }): void {
  // False when the picture that failed was a catalogue sprite rather than a
  // face. That is this server's own asset store and its own bug to fix, and
  // there is no third picture to fall back to, so it is left alone.
  if (!peer.avatar || brokenAvatars.value.has(peer.id)) return;
  brokenAvatars.value.add(peer.id);
}

/**
 * Everybody, ready to draw: the wire's appearance with its art key resolved
 * against the catalogue this screen already fetched.
 *
 * Recomputed when appearance changes or when the catalogue finally arrives — the
 * plane runs on the socket and the catalogue comes over HTTP, so the yard is
 * routinely populated before it lands, and every dot is a placeholder until it
 * does.
 *
 * THREE THINGS CAN BE DRAWN ON A DOT AND THEY ARE RANKED, most personal first:
 * the person's own VK avatar, then the catalogue's sprite for their skin, then
 * the catalogue's emoji. The avatar wins because it answers the question the
 * yard is actually for — which of these is my friend — and the skin below it
 * answers only what kind of thing this is, which in a yard where everybody wears
 * the same Ваня is no answer at all. The identity colour survives underneath all
 * three as the rim, so the two questions are still layered rather than traded.
 *
 * IT READS THE CLOCK, which is new and is the one thing here worth defending.
 * `displayNow` ticks once a second, so this recomputes once a second — and that
 * is not a new render, because the stat bars have made this component re-render
 * on that same tick since they arrived. What it buys is a deposit that visibly
 * ages instead of standing at full size until the server drops it. What it must
 * NOT become is a per-frame dependency: positions still bypass Vue entirely, and
 * the guard on `appearance` is still what keeps the five frames a second that
 * describe an unchanged yard free.
 */
const drawn = computed(() => {
  const now = displayNow.value;
  return appearance.value.map((peer) => {
    const art = resolveArt(config.value?.skins, peer.art);
    // ASKED FOR ON EVERY ENTITY, AND THE ADDRESS IS DERIVED RATHER THAN READ.
    // No frame carries a URL — a couple of hundred unchanging characters
    // re-broadcast five times a second to everybody would be most of what the
    // socket costs, and a URL out of Postgres would also be the one thing on an
    // otherwise per-process frame that survived a restart. So the endpoint is
    // built from the id, which means there is no field whose absence this screen
    // would have to interpret, which in turn is what keeps it kind-agnostic: it
    // holds no notion of what an NPC is, asks for every face it draws, and lets
    // the 404 be the answer. Special-casing the ids that look like regulars
    // would put the cast list in the browser and make adding one a deploy.
    const avatar = brokenAvatars.value.has(peer.id) ? undefined : avatarEndpoint(peer.id);
    return {
      ...peer,
      ...art,
      // Overrides `art.image`, so the template keeps its single `v-if` on one
      // field and never has to know which of the two kinds of picture it is
      // showing. `avatar` is what says which — see the img's `data-test` — and
      // it is a flag rather than the URL because the URL is now recoverable
      // from the id anywhere it is wanted.
      image: avatar ?? art.image,
      avatar: avatar !== undefined,
      // WHETHER THIS IS A THING RATHER THAN A PERSON, AND THE CLIENT IS NOT
      // TOLD — it works it out from the one field only a world object ever
      // carries. Nothing here maps an art key to a kind, holds a list of kinds,
      // or would have to be redeployed for a kind added on the server; all it
      // knows is that this entity has an end, which people, sleepers and NPCs
      // do not (see PeerAppearance.expires for why that is honest rather than
      // clever, and for what it costs).
      prop: peer.expires !== undefined,
      // And how far through that end it is. 1 for everybody without an expiry,
      // so the binding below is unconditional and no branch has to ask what
      // anything is.
      life: propScale(peer.expires, now),
    };
  });
});

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
  // Forgotten with the rest of the world, so that whatever the yard looks like
  // when it comes back is a FIRST sight again and passes in silence. We were away
  // long enough to have missed the hunt starting, and a line claiming it has just
  // happened would be this screen inventing the one thing the wire cannot say. The
  // line already on the panel is left to expire on its own — the outage that gets
  // here lasted longer than the line does.
  seenHunt = '';
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
  if (frame.t === TYPE_STATE) {
    // Our own pet, after it changed. NOT a reply to anything: it also arrives
    // when this same account acts on another device, which is the point —
    // that screen's bars would otherwise sit stale until it re-read.
    const next = (frame as { state?: VanyagotchiState }).state;
    if (next && typeof next === 'object') applyPetState(next);
    return;
  }
  if (frame.t !== TYPE_ROSTER) return;
  const peers = Array.isArray(frame.peers) ? frame.peers : [];

  // Who is here, and what they look like, from one pass through one guard — so
  // the keyed list and the head count cannot end up disagreeing about the yard.
  const looks = readAppearances(peers);
  const ids = looks.map((look) => look.id);
  here.value = readHere(frame.here, ids.length);

  // The one thing on a roster frame that is read as a DIFFERENCE rather than as
  // state. Everything else here is idempotent — a frame repeated is a frame that
  // changed nothing — and this deliberately is not, because "a fresh hunt has
  // started" exists nowhere on the wire: the server publishes which hunt is
  // running, and only a client that watched the previous one can tell that this
  // is a different one. `huntRestarted` is what keeps the difference honest, and
  // in particular keeps a late joiner quiet.
  const hunt = readHunt(frame.hunt);
  if (huntRestarted(seenHunt, hunt)) announceHunt();
  seenHunt = hunt;

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
  if (huntTimer !== undefined) {
    window.clearTimeout(huntTimer);
    huntTimer = undefined;
  }
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
/* The cheatsheet is the ONLY shrinkable child on this screen, and that is
   deliberate: `overflow: hidden` on the splash means something has to give on a
   short phone, and it must not be the call to action or the disclaimer. So the
   rules take all the slack and scroll INSIDE THEMSELVES — the page still never
   scrolls, which is the layout rule this screen shares with the yard.

   `overscroll-behavior: contain` stops a flick at the end of the rules turning
   into a pull on the page behind them, which on iOS is the difference between a
   panel that scrolls and a screen that bounces. */
.splash-scroll {
  flex: 0 1 auto;
  min-height: 0;
  overflow-y: auto;
  overscroll-behavior: contain;
  -webkit-overflow-scrolling: touch;
  max-width: 560px;
  width: 100%;
  text-align: left;
  display: flex;
  flex-direction: column;
  gap: 10px;
}
.splash-lore,
.splash-intro {
  max-width: 560px;
  line-height: 1.45;
}

/* The cheatsheet itself. One grid per row — emoji in a fixed gutter, everything
   else in a column beside it — so the labels line up down the screen however
   long a stat's name happens to be. */
.rules {
  display: flex;
  flex-direction: column;
  gap: 6px;
}
.rules-head {
  font-size: 0.78rem;
  font-weight: 700;
  text-transform: uppercase;
  letter-spacing: 0.6px;
  opacity: 0.6;
}
.rule {
  display: grid;
  grid-template-columns: 22px 1fr;
  gap: 8px;
  align-items: start;
}
.rule-emoji {
  font-size: 1rem;
  line-height: 1.35;
}
.rule-body {
  min-width: 0;
}
.rule-line {
  font-size: 0.9rem;
  line-height: 1.35;
}
.rule-label {
  font-weight: 700;
}
.rule-label::after {
  content: ' — ';
  font-weight: 400;
  opacity: 0.6;
}
.rule-main {
  opacity: 0.95;
}
/* The consequences under a rule — the penalties that actually kill him, and the
   one thing an action row has to warn about. Dimmer than the rule, but not so
   dim that the causal half of the game reads as a footnote. */
.rule-note {
  font-size: 0.82rem;
  line-height: 1.35;
  opacity: 0.78;
}
.rule-prose {
  font-size: 0.85rem;
  line-height: 1.4;
  opacity: 0.85;
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

  /* HOW BIG A ВАНЯ IS, IN THIS YARD. Every fixed length inside the plane is a
     fraction of this one, so the world is drawn at the same apparent scale on
     every screen instead of shrinking as the plane grows.
     It has to be, because the plane is sized from its container while an entity
     used to be a fixed 44px: measured, that put a dot at 19.1% of the plane's
     width on a 320px phone and 7.2% on a 1920px desktop — a 2.7x spread, i.e.
     two different games. The spread is also not monotonic in viewport width,
     because `min(100cqw, 75cqh)` above switches which term binds, so no
     breakpoint-keyed fix would have been correct.

     THE NUMBERS WERE 44 / 13cqw / 88 AND THE WHOLE WORLD WAS TOO BIG. That is
     the owner's report from a phone, and it is a judgement about legibility
     rather than about accessibility, which is what made it safe to act on: a dot
     is `pointer-events: none` and the plane takes every tap (see `.peer`), so
     nothing in this yard has ever been a tap target and no 44px rule applies to
     any of it. The floor here is only ever "how small can a face be and still be
     read across a phone-sized yard", and a third off it leaves room for the yard
     to be a place with things in it rather than four faces filling a box.
     `9cqw` keeps the proportion the same everywhere — that is the property the
     spread above violated and it is untouched — with 32 and 64 as the same
     floor-and-double-it pair the old numbers were. The ceiling is still a guard
     rather than an active constraint; it stops a very tall window drawing an
     absurd Ваня.

     TWO PROPERTIES RATHER THAN ONE, and only because a world object is drawn at
     half scale. A custom property cannot be redefined in terms of itself — a
     descendant writing `--unit: calc(var(--unit) * 0.5)` is a cycle and computes
     to nothing — so the base is published separately for `.peer--prop` to halve.
     `--unit` is what everything reads; `--unit-base` exists to be scaled.
     Retuning the world means changing the clamp on `--unit-base` and nothing
     else: every length inside the plane is already a fraction of `--unit`.

     DECLARED HERE, USED ONLY BY DESCENDANTS. `.plane` is itself the query
     container, so a `cqw` in a property ON `.plane` would resolve against
     `.plane-frame` instead. This works because a custom property substitutes as
     a token stream and the `9cqw` is resolved at the point of use, where the
     nearest container really is the plane — which holds through the indirection
     as well, since `--unit: var(--unit-base)` is itself only a substitution. Do
     not use `var(--unit)` in a property on this rule, and do not register either
     of them with `@property`.
     It is deliberately INDEPENDENT of `--depth`: this says how big a Ваня is
     here, `--depth` says how much nearer this one is, and they compose by
     multiplication without either knowing about the other. Keeping them apart is
     what lets sprites arrive later without touching any of this. */
  --unit-base: clamp(32px, 9cqw, 64px);
  --unit: var(--unit-base);
}
.plane-empty {
  position: absolute;
  inset: 0;
  display: flex;
  align-items: center;
  justify-content: center;
  color: rgba(255, 255, 255, 0.6);
  /* Deliberately NOT a world unit, unlike everything else inside the plane. This
     is a message to the reader about the yard, not a thing standing in it — it
     is the only text here that is chrome — so it stays at the document's own
     scale and respects the user's font size the way the panel below does. */
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
  /* One world unit — see `--unit` on `.plane`. The mobile suite measures this
     box, and the nearest band scales it UP (see DEPTH_SCALES — the far band is
     1), so the clamp's floor is the smallest an entity is ever drawn rather than
     the largest. That floor is LEGIBILITY and never a tap target: a dot cannot
     be tapped at all (`pointer-events: none` below), the plane takes the tap.
     Which is exactly why lowering it from 44 to 32 was a drawing decision and
     not an accessibility regression. */
  width: var(--unit);
  height: var(--unit);
  border-radius: 50%;
  /* Rim and shadow in world units too, or a big Ваня wears a hairline and a
     small one wears a hoop. The fractions are 2/44 and 6/44 — the proportions a
     44px dot was drawn with, kept as proportions so they follow the unit
     wherever it goes. */
  border: calc(var(--unit) * 0.045) solid rgba(255, 255, 255, 0.85);
  box-shadow: 0 calc(var(--unit) * 0.045) calc(var(--unit) * 0.136) rgba(0, 0, 0, 0.35);
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
  /* World units, like every other length in here — 3/44 and 2/44 + 8/44 of the
     unit. A fixed 3px ring is a bold outline on a phone and a thread on a
     desktop, which is the same bug this whole file's `--unit` fixes. */
  box-shadow:
    0 0 0 calc(var(--unit) * 0.068) rgba(0, 0, 0, 0.45),
    0 calc(var(--unit) * 0.045) calc(var(--unit) * 0.182) rgba(0, 0, 0, 0.5);
  /* No z-index of its own any more. It used to be 1, which was harmless while
     everything else was 0 and is wrong now that the stacking order MEANS
     something: lifting your own Ваня above his band would draw him in front of
     somebody standing nearer the viewer than he is, in a yard where that is the
     one cue for how far away anybody is. The ring already says which one is you,
     and it says it without lying about where you are standing. */
}

/* SOMETHING LYING ON THE GROUND, rather than somebody standing on it.

   Half a Ваня and no circle at all — no background, no rim, no shadow, just the
   glyph. A deposit used to be drawn exactly like a person: a full-size coloured
   disc with an emoji on it, which in a yard of four people and a dozen deposits
   made the litter as loud as the players and, at the identity hue, made each
   piece of it look like somebody. Both of those are wrong in the same direction,
   and the owner's report from a phone was the blunter version of it.

   WHICH ENTITIES THESE ARE IS NOT SOMETHING THIS CLIENT WAS TOLD. The class is
   applied when the frame gave an entity an EXPIRY, which only a world object
   ever gets — see PeerAppearance.expires. No kind key, no lookup against the
   catalogue's object kinds, nothing that would have to be redeployed when a kind
   is added on the server (ADR-028). The cost is stated where the decision is: an
   object with no expiry keeps its circle, which today means the key, and that is
   the right way round since the key is the one thing meant to be spotted.

   THE SIZE IS THE UNIT, NOT THE BOX. Halving `--unit` here halves everything
   derived from it at once — the dot, the face inside it, the rim, the shadow,
   the caption, the balloon — because every length in this file is a fraction of
   it. Overriding `width`/`height` instead would have shrunk the box and left a
   full-size glyph hanging out of it. It reads `--unit-base` rather than `--unit`
   because a custom property defined in terms of itself is a cycle and computes
   to nothing; that indirection is the whole reason `.plane` publishes two.

   AND IT SHRINKS AS IT AGES, on the same multiplication. `--life` is the
   fraction of its life the thing has left, written by the template once a second
   off the server clock the stat bars already track (see `propScale`), so a
   deposit fades out of the yard over its life and is drawn at nothing by the
   time the server stops sending it. The default of 1 matters: an entity whose
   life the template did not write is drawn at full half-size rather than
   vanishing, which is the failure this must have if `--life` ever goes missing.

   `border-radius` is left alone deliberately — with nothing to fill or outline
   it, rounding is invisible, and a rule that turned it off would be one more
   thing to keep consistent for no drawn difference. */
.peer--prop {
  --unit: calc(var(--unit-base) * 0.5 * var(--life, 1));
  border: none;
  box-shadow: none;
}

/* Everybody's face, centred on their dot. `pointer-events: none` is inherited
   from .peer — the plane is the tap target, not the sprite. */
.peer-face {
  position: absolute;
  inset: 0;
  display: flex;
  align-items: center;
  justify-content: center;
  /* 26/44 of the dot — the face has to grow with the head it is drawn on. */
  font-size: calc(var(--unit) * 0.59);
  line-height: 1;
}

/* A sprite, once the catalogue has one for this art key. Inset rather than
   filling the dot, so the entity's own colour survives as a rim: the picture
   says WHAT this is and the hue says WHO, and a skin everybody happens to be
   wearing would otherwise erase the second. */
.peer-sprite {
  /* The inset is the rim, so it is a world unit — 6/44 — and not a fixed 6px.
     `100%` already follows the dot; a fixed inset would eat the whole rim on a
     small screen and leave a thick ring on a large one. */
  width: calc(100% - var(--unit) * 0.136);
  height: calc(100% - var(--unit) * 0.136);
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
  /* Sized in world units, the WHOLE label and not only its ceiling. It used to
     be `min(80px, 30cqw)` wide with a fixed 10px face: the width followed the
     yard and the text did not, so a name was a third of the plane wide on a
     phone and a seventh on a tablet — two different games. Now both come off
     `--unit` (80/44 and 10/44), which is already clamped, so the two-branch
     `min()` is not needed to stop it growing past legible. */
  max-width: calc(var(--unit) * 1.818);
  overflow: hidden;
  white-space: nowrap;
  text-overflow: ellipsis;
  text-align: center;
  font-size: calc(var(--unit) * 0.227);
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
  /* Hung off the dot in world units — 10/44, 6/44 and 15/44 — so the badge stays
     in the same place on the head at every size instead of sliding towards the
     middle as the dot grows. */
  top: calc(var(--unit) * -0.227);
  right: calc(var(--unit) * -0.136);
  font-size: calc(var(--unit) * 0.341);
  line-height: 1;
  /* The dot it sits on is at 55% opacity and so, being a child, is this — which
     over the pale bottom of the plane's gradient was very nearly invisible at
     360 px. The shadow is what gives it an edge to be seen against. */
  text-shadow: 0 1px 2px rgba(0, 0, 0, 0.75);
}
/* Lying down. The one condition drawn as a rotation instead of a filter: at the
   sizes a Ваня is ever drawn, a change of ORIENTATION is visible across the yard
   where a change of tone is not, and it is also literally what the server means
   — he is lying where he was standing when his owner left. */
.peer-face[data-condition='asleep'] {
  transform: rotate(74deg);
}

/* HAPPY and SAD: the two poses that are a MOOD rather than a condition — the
   cosmetic outcome of a contested claim, on the wire for a few seconds after
   somebody finds the keys and then simply gone. Winning and losing move no stat
   whatsoever, so these two rules ARE the entire consequence of the race, and
   they have to carry it on their own.

   NEITHER ANIMATES, which is where they part company with `poorly`. A wobble is
   a fine way to draw a condition that lasts as long as the bar behind it,
   because there is always another cycle to catch; a mood lasts about as long as
   one cycle does, so an animated one would be legible only to whoever happened
   to be watching that dot at the top of it — and would vanish altogether under
   prefers-reduced-motion, where the block at the foot of this file switches
   animations off. A static transform is the same picture in every frame it is up
   for, for everybody, which is what a three-second state needs.

   Both channels at once, because the yard is small and the player is usually
   looking somewhere else on it. The face changes SIZE and HEIGHT — lifted and
   grown against sunk and shrunk, which leaves the two a full 1.5× apart, and
   which is the geometry the `asleep` rule above notes reads across a plane where
   a change of tone does not — and it changes TONE as well, bright against
   drained. Sad also tips forward, using the one axis happy deliberately leaves
   alone: it is nowhere near `asleep`'s 74deg, and unlike `poorly` it is holding
   still. */
.peer-face[data-condition='happy'] {
  transform: translateY(calc(var(--unit) * -0.14)) scale(1.2);
  filter: saturate(1.4) brightness(1.25);
}
.peer-face[data-condition='sad'] {
  transform: translateY(calc(var(--unit) * 0.12)) rotate(16deg) scale(0.8);
  filter: saturate(0.35) brightness(0.8);
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
  /* Every length here is a world unit — 6/44 gap, 96/44 wide, 2/44 and 6/44 of
     padding, 9/44 of radius, 10/44 of text. The flip distance is exactly TWO
     units, which is what 88px always was: it has to clear the dot and the name
     hanging under it, and both of those are now `--unit`-sized, so a fixed 88px
     would under-clear on a desktop and over-clear on a phone. */
  bottom: calc(100% + var(--unit) * 0.136);
  transform: translateX(-50%) translateY(calc(var(--say-below, 0) * var(--unit) * 2));
  max-width: calc(var(--unit) * 2.182);
  padding: calc(var(--unit) * 0.045) calc(var(--unit) * 0.136);
  border-radius: calc(var(--unit) * 0.205);
  background: rgba(12, 18, 26, 0.86);
  color: rgba(255, 255, 255, 0.95);
  font-size: calc(var(--unit) * 0.227);
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

/* Stat bars. Fixed height, and one row per stat that IS a bar — three today, the
   lifetime tallies having their own row below — and the row is a grid so a
   fourth arrives without a layout decision. Each row costs about 19px and the
   plane pays for it, which is measured rather than assumed — the 320x568 case in
   the mobile suite is what holds that. */
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

/* The lifetime tallies. ONE ROW, and staying one row is the constraint: the
   panel is what the plane pays for, and the 320px case has about 296px of usable
   width for two of these. `flex-wrap` is the honest fallback rather than a
   preference — a third counter, or a longer label, takes a second line and the
   plane gives up another sixteen pixels, which is a better failure than a label
   truncated to something unreadable. Smaller than the bar row on purpose: a
   total is something you check, not something you watch. */
.tallies {
  flex: 0 0 auto;
  display: flex;
  flex-wrap: wrap;
  gap: 4px 12px;
  font-size: 0.7rem;
  line-height: 1.15;
  opacity: 0.8;
}
.tally {
  display: inline-flex;
  align-items: baseline;
  gap: 4px;
  min-width: 0;
}
.tally-emoji {
  font-size: 0.8rem;
}
.tally-label {
  white-space: nowrap;
  overflow: hidden;
  text-overflow: ellipsis;
}
.tally-value {
  font-variant-numeric: tabular-nums;
  font-weight: 700;
  opacity: 0.95;
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
   thumb wide. 6px of gutter rather than 8 buys each of the four another pixel
   and a half of label at 320px, which is where the row is tightest. */
.actions {
  flex: 0 0 auto;
  display: grid;
  grid-template-columns: repeat(auto-fit, minmax(64px, 1fr));
  gap: 6px;
}

/* MAKING FOUR RUSSIAN VERBS FIT A 320px PHONE, which they did not.

   The row is one verb per catalogue action and the catalogue now carries four —
   «выпить пива», «покакать», «искать ключи», «восстать из мертвых» — sharing
   about 288px. Vuetify draws a button UPPERCASE, letter-spaced, at 0.875rem,
   inside 16px of horizontal padding, with `white-space: nowrap` on its content:
   at ~66px a track that is roughly a third of what «ВЫПИТЬ ПИВА» needs, and the
   overflow was simply cut. The owner read it on a phone and reported exactly
   that.

   Four changes, each undoing one of those, and NONE of them touching the words
   themselves — the labels come from the server's catalogue, so truncating one
   here would be this screen inventing content, and an abbreviation would go
   stale the moment a verb is renamed. Wrapping is the honest way to fit text you
   do not own:
     - the type drops to 0.62rem and the tracking to nothing (uppercase Russian
       with 0.089em of letter-spacing is the single most expensive thing here);
     - the padding goes from 16px a side to 2, which on a 66px track is a fifth
       of it back;
     - `text-transform: none` — lower case is narrower, and it also matches the
       catalogue's own wording, which is written lower case;
     - the content is allowed to WRAP, and to break inside a word if some future
       verb has one longer than the track.

   `height: auto` is what lets a two- or three-line label make the button taller
   instead of being clipped by a fixed 36px box; `min-height: 44px` keeps the
   floor. Both matter: the panel is what the plane pays for, so a button that
   grows costs the yard a few pixels, and a button that clips costs the player
   the ability to read what he is pressing. Measured at 320 and 360 by the pet
   suite, which asserts no content box overflows its button in either axis. */
.action-btn {
  min-height: 44px;
  height: auto;
  padding: 3px 2px;
  min-width: 0;
  text-transform: none;
  letter-spacing: 0;
  text-indent: 0;
}
.action-btn :deep(.v-btn__content) {
  white-space: normal;
  overflow-wrap: anywhere;
  font-size: 0.62rem;
  line-height: 1.12;
  text-align: center;
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
