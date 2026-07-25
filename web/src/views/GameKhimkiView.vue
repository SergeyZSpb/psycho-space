<template>
  <v-container
    :class="phase === 'play' || phase === 'intro' ? 'pa-0 play-root' : 'py-4'"
    style="max-width: 900px"
  >
    <div v-if="phase === 'loading'" class="text-center py-12">
      <v-progress-circular indeterminate color="primary" />
    </div>

    <template v-else-if="config && character">
      <div v-if="phase === 'ending'" class="mb-2">
        <h1 class="text-h5">{{ config.title }}</h1>
      </div>

      <!-- Splash / start screen -->
      <div v-if="phase === 'intro'" class="splash" :style="{ background: splashArt.gradient }">
        <img
          v-if="artImg(splashArt)"
          :src="artImg(splashArt)"
          class="splash-img"
          alt=""
          @error="failedArts.push(splashArt.key)"
        />
        <div v-else class="splash-emoji">{{ splashArt.emoji }}</div>
        <h1 class="splash-title">{{ config.title }}</h1>
        <p class="splash-intro">{{ config.intro }}</p>
        <v-btn color="primary" size="large" class="splash-cta" @click="start">Погнали домой</v-btn>
        <p class="splash-disclaimer">
          Все персонажи вымышлены; любые совпадения с реальными людьми случайны.
        </p>
      </div>

      <!-- Play (portrait + landscape) -->
      <div v-else-if="phase === 'play'" class="stage">
        <div class="portrait-pane" :style="{ background: currentArt.gradient }">
          <img
            v-if="artImg(currentArt)"
            :src="artImg(currentArt)"
            class="art-img"
            alt=""
            @error="failedArts.push(currentArt.key)"
          />
          <div v-else class="face">{{ currentArt.emoji }}</div>
        </div>

        <div class="dialog-pane">
          <div class="goal text-caption text-medium-emphasis">🎯 {{ character.goal }}</div>
          <!-- Tension scale: filled = дядя Ваня snaps and the run is lost. Kept to
               one tiny caption row + a 5px bar so the art pane absorbs the height
               and the play screen still never scrolls. -->
          <div class="anger">
            <div class="anger-cap d-flex align-center justify-space-between">
              <span>😤 {{ angerLabel(anger, angerBarMax) }}</span>
              <span>{{ anger }}/{{ angerBarMax }}</span>
            </div>
            <v-progress-linear
              :model-value="anger"
              :max="angerBarMax"
              :color="angerColor(anger, angerBarMax)"
              height="5"
              rounded
            />
          </div>
          <v-alert
            v-if="rateLimited"
            type="warning"
            variant="tonal"
            density="compact"
            class="mb-2"
            text="Слишком много запросов с вашего IP — кошелёк Сергея плачет 😢 Подожди минутку."
          />
          <v-alert variant="tonal" density="compact" class="bubble mb-2">
            <!-- A verse too tall for the box ROLLS like post-credits instead of
                 being clipped: the text is rendered twice and the reel shifts by
                 exactly half its height, so the loop is seamless. It holds still
                 before it starts and settles after a couple of passes — see
                 lib/gameKhimkiCredits.ts for the pacing and why.

                 The frame is the skip affordance: tap (or Enter/Space on a focused
                 frame) drops the whole verse into a scrollable box at once, so
                 nobody is ever stuck waiting for a line to come round again. -->
            <div
              class="verse-frame"
              :role="verse.skippable.value ? 'button' : undefined"
              :tabindex="verse.skippable.value ? 0 : undefined"
              :aria-label="verse.skippable.value ? 'Показать куплет целиком' : undefined"
              @click="revealVerse"
              @keydown.enter.prevent="revealVerse"
              @keydown.space.prevent="revealVerse"
            >
              <div
                ref="verseBox"
                class="verse-viewport"
                :class="{ 'verse-viewport--revealed': verse.revealed.value }"
              >
                <div
                  class="verse-reel"
                  :class="{ rolling: verse.rolling.value }"
                  :style="verse.reelStyle.value"
                >
                  <div ref="verseFirst" class="verse">{{ reply }}</div>
                  <div v-if="verse.rolling.value" class="verse" aria-hidden="true">{{ reply }}</div>
                </div>
              </div>
              <span v-if="verse.skippable.value" class="verse-hint">весь текст</span>
            </div>
          </v-alert>
          <!-- Options keep their height while the judge thinks; the loader
               overlays them (no layout shift). -->
          <div class="actions">
            <div class="options" :class="{ 'options--busy': busy }">
              <v-btn
                v-for="(opt, i) in options"
                :key="i"
                class="text-none option-btn"
                variant="outlined"
                block
                :disabled="busy"
                @click="choose(opt)"
              >
                {{ opt }}
              </v-btn>
            </div>
            <div v-if="busy" class="loader-overlay">
              <v-progress-circular indeterminate :size="60" :width="6" color="primary" />
            </div>
          </div>
        </div>
      </div>

      <!-- Ending -->
      <template v-else-if="phase === 'ending'">
        <div class="portrait-pane ending mb-3" :style="{ background: currentArt.gradient }">
          <img
            v-if="artImg(currentArt)"
            :src="artImg(currentArt)"
            class="art-img"
            alt=""
            @error="failedArts.push(currentArt.key)"
          />
          <div v-else class="face">{{ currentArt.emoji }}</div>
        </div>
        <v-alert
          :type="success ? 'success' : gameOver ? 'error' : 'warning'"
          variant="tonal"
          class="mb-3"
          :title="endingTitle"
          :text="endingText"
        />
        <p class="text-body-2 mb-4">
          Шагов: <strong>{{ steps }}</strong>
          <template v-if="stats"> · твой рекорд: <strong>{{ bestLabel }}</strong></template>
        </p>
        <v-btn color="primary" size="large" block class="mb-6" @click="start">Ещё раз</v-btn>
      </template>

      <!-- Leaderboard (only on the end screen; the play screen stays scroll-free).
           Four record boards, one per tab: longest/shortest win, longest/shortest loss. -->
      <template v-if="phase === 'ending'">
      <v-divider class="my-4" />
      <h2 class="text-subtitle-1 mb-1">Таблица позора</h2>

      <v-tabs
        v-model="board"
        density="compact"
        grow
        show-arrows
        class="mb-1 board-tabs"
      >
        <v-tab v-for="b in BOARDS" :key="b.key" :value="b.key" class="text-none px-1">
          {{ b.tab }}
        </v-tab>
      </v-tabs>
      <p class="text-caption text-medium-emphasis mb-2">{{ currentBoard.caption }}</p>

      <p v-if="!boardEntries.length" class="text-medium-emphasis">{{ currentBoard.empty }}</p>
      <v-list v-else density="compact" class="bg-transparent">
        <v-list-item
          v-for="(e, i) in boardEntries"
          :key="i"
          :class="{ 'bg-surface-light rounded': e.mine }"
        >
          <template #prepend>
            <span class="text-medium-emphasis mr-3">{{ i + 1 }}</span>
            <v-avatar size="28" color="secondary">
              <v-img v-if="e.player.avatar_url" :src="e.player.avatar_url" alt="" />
              <span v-else class="text-caption">{{ (e.player.display_name || '?').charAt(0) }}</span>
            </v-avatar>
          </template>
          <v-list-item-title class="text-body-2">
            {{ e.player.display_name || 'аноним' }}
            <span v-if="e.mine" class="text-primary">(вы)</span>
          </v-list-item-title>
          <v-list-item-subtitle class="text-caption">
            попыток {{ e.plays }} · 🏆 {{ e.wins }} · 💀 {{ e.losses }}
          </v-list-item-subtitle>
          <template #append>
            <v-chip size="x-small" variant="tonal">{{ e.steps }} шаг.</v-chip>
          </template>
        </v-list-item>
      </v-list>
      </template>
    </template>
  </v-container>
</template>

<script setup lang="ts">
import { computed, nextTick, onMounted, ref, watch } from 'vue';
import { gameKhimkiApi } from '../api/endpoints';
import { ApiError } from '../api/client';
import { useErrorStore } from '../stores/error';
import { BOARDS, boardMeta } from '../lib/gameKhimkiBoards';
import { angerColor, angerLabel } from '../lib/gameKhimkiAnger';
import { recordExchange } from '../lib/gameKhimkiTranscript';
import { useVersePacing } from '../composables/useVersePacing';
import type {
  GameKhimkiArt,
  GameKhimkiBoardKey,
  GameKhimkiBoards,
  GameKhimkiCharacter,
  GameKhimkiConfig,
  GameKhimkiExchange,
  GameKhimkiStats,
} from '../api/types';

const GAME = 'smalltalk_khimki';
const FALLBACK_ART: GameKhimkiArt = { key: '', emoji: '🧑', gradient: 'linear-gradient(160deg, #333, #111)' };

const errorStore = useErrorStore();

type Phase = 'loading' | 'intro' | 'play' | 'ending';
const phase = ref<Phase>('loading');

const config = ref<GameKhimkiConfig | null>(null);
const stats = ref<GameKhimkiStats | null>(null);
const boards = ref<GameKhimkiBoards | null>(null);
const board = ref<GameKhimkiBoardKey>('longest_win'); // selected record board (tab)

const transcript = ref<GameKhimkiExchange[]>([]);
const steps = ref(0);
const currentArtKey = ref('');
const reply = ref('');
const options = ref<string[]>([]);
const success = ref(false);
const gameOver = ref(false); // lost by being punched, not by running out of options
// Tension, carried between turns like the transcript: we send it, the judge
// returns the new value, and at max_anger the backend ends the run.
const anger = ref(0);
// Theme progress the judge reports, carried back next turn so it can steer toward
// what is still closed. Deliberately NOT rendered anywhere: showing the player
// which themes remain would play the game for them.
const themesDone = ref<string[]>([]);
const busy = ref(false);
// Rolling state for a verse that outgrows its box (see lib/gameKhimkiCredits.ts
// for the pacing and composables/useVersePacing.ts for the state machine).
const verseBox = ref<HTMLElement | null>(null);
const verseFirst = ref<HTMLElement | null>(null);
const verse = useVersePacing();

// Re-measure whenever he says something new: `start` cancels whatever the last
// reply left running, so two verses can never roll over each other. Measuring
// happens after the DOM has caught up with the new text.
watch(reply, async () => {
  verse.cancel();
  await nextTick();
  const box = verseBox.value;
  const first = verseFirst.value;
  if (!box || !first) return;
  verse.start(first.scrollHeight, box.clientHeight);
});

// Tap or keyboard on the verse: skip the wait and show the whole thing at once.
function revealVerse() {
  if (verse.skippable.value) verse.reveal();
}
const rateLimited = ref(false);

const character = computed<GameKhimkiCharacter | null>(() => {
  const c = config.value;
  if (!c) return null;
  return c.characters.find((ch) => ch.key === c.default_character) ?? c.characters[0] ?? null;
});
// Art catalog resolved from the backend config (adding arts needs no client change).
const artMap = computed<Record<string, GameKhimkiArt>>(() =>
  Object.fromEntries((character.value?.arts ?? []).map((a) => [a.key, a])),
);
const currentArt = computed<GameKhimkiArt>(() => artMap.value[currentArtKey.value] ?? FALLBACK_ART);
const splashArt = computed<GameKhimkiArt>(() => character.value?.arts[0] ?? FALLBACK_ART);

// Art image URL if the backend provided one and it hasn't failed to load;
// otherwise "" so we fall back to the emoji placeholder.
const failedArts = ref<string[]>([]);
function artImg(a: GameKhimkiArt): string {
  return a.image && !failedArts.value.includes(a.key) ? a.image : '';
}
const bestLabel = computed(() =>
  stats.value && stats.value.best_steps > 0 ? `${stats.value.best_steps} шаг.` : '—',
);

// Three endings: convinced him, got punched, or simply never got in.
const endingTitle = computed(() => {
  if (success.value) return 'Ты дома!';
  return gameOver.value ? 'Game over' : 'Не в этот раз';
});
const endingText = computed(() => {
  if (success.value) {
    return 'Дядя Ваня открылся и пропустил тебя. Дома кот наблевал на шторы. Но ты дома.';
  }
  return gameOver.value
    ? 'Дядя Ваня сорвался и вмазал тебе. Сидишь на бордюре, домой так и не попал.'
    : 'Дядя Ваня так тебя и не пропустил. Стоишь во дворе.';
});

// The bar fills at the point he actually swings, so a full bar means the punch.
const angerBarMax = computed(() => config.value?.anger_lose_at ?? 90);
const currentBoard = computed(() => boardMeta(board.value));
const boardEntries = computed(() => boards.value?.[board.value] ?? []);

async function refreshBoard() {
  const [lb, st] = await Promise.all([gameKhimkiApi.leaderboard(GAME), gameKhimkiApi.stats(GAME)]);
  boards.value = lb.boards;
  stats.value = st;
}

onMounted(async () => {
  try {
    config.value = await gameKhimkiApi.config(GAME);
    await refreshBoard();
    phase.value = 'intro';
  } catch (err) {
    errorStore.report(err);
  }
});

function start() {
  const ch = character.value;
  if (!ch) return;
  // Static opening: the iconic greeting + the first options, no LLM call. The
  // judge takes over from the player's first pick.
  transcript.value = [];
  steps.value = 0;
  currentArtKey.value = ch.arts[0]?.key ?? '';
  reply.value = ch.greeting;
  options.value = [...ch.opening_options];
  success.value = false;
  gameOver.value = false;
  anger.value = config.value?.start_anger ?? 0; // he opens hostile, not calm
  themesDone.value = [];
  phase.value = 'play';
}

async function turn(choice: string) {
  const ch = character.value;
  if (busy.value || !ch) return;
  busy.value = true;
  rateLimited.value = false;
  try {
    const res = await gameKhimkiApi.attempt(
      GAME,
      ch.key,
      transcript.value,
      choice,
      anger.value,
      themesDone.value,
    );
    if (choice !== '') {
      // Record the options offered alongside the reply: the judge needs them next
      // turn to avoid proposing the same lines again.
      transcript.value.push(recordExchange(choice, res));
      steps.value += 1;
    }
    anger.value = res.anger;
    themesDone.value = res.themes_done ?? [];
    reply.value = res.reply;
    if (res.art) currentArtKey.value = res.art;
    options.value = res.options ?? [];
    if (res.achieved) {
      await finish(true);
    } else if (res.game_over) {
      // Ваня сорвался и вмазал — run over, and the backend already forced the
      // game-over art, so the ending screen shows the punch.
      gameOver.value = true;
      await finish(false);
    } else if (choice !== '' && options.value.length === 0) {
      await finish(false);
    }
  } catch (err) {
    if (err instanceof ApiError && err.status === 429) {
      rateLimited.value = true; // too many judge calls from this IP
    } else {
      errorStore.report(err);
    }
  } finally {
    busy.value = false;
  }
}

function choose(label: string) {
  if (!busy.value) void turn(label);
}

async function finish(won: boolean) {
  const ch = character.value;
  if (!ch) return;
  success.value = won;
  // Open on a board this run could have landed on, so the player sees their own
  // row rather than an empty win board after a loss.
  board.value = won ? 'longest_win' : 'longest_loss';
  phase.value = 'ending';
  try {
    await gameKhimkiApi.submitRun(GAME, ch.key, won, steps.value);
    await refreshBoard();
  } catch (err) {
    errorStore.report(err);
  }
}
</script>

<style scoped>
/* Splash / start screen — fills the play-root height with no scroll: the image
   flexes/shrinks so the title, CTA and disclaimer always stay on screen. */
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
  color: rgba(255, 255, 255, 0.95);
}
/* Only the image flexes/shrinks; text + CTA are fixed so nothing gets clipped. */
.splash-emoji {
  flex: 0 0 auto;
  font-size: 84px;
  line-height: 1;
}
.splash-img {
  flex: 1 1 auto;
  min-height: 0;
  max-width: 100%;
  border-radius: 14px;
  object-fit: contain;
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

/* Play fills the viewport minus the app bar and never scrolls: the pic flexes
   and shrinks so the 4 options always fit. */
/* Height = visible viewport minus the app bar, with a little extra headroom so
   the mobile browser's top/bottom chrome never triggers a scrollbar. */
.play-root {
  height: calc(100dvh - 72px);
  overflow: hidden;
}
.stage {
  height: 100%;
  display: flex;
  flex-direction: column;
  gap: 8px;
  padding: 8px 12px 12px;
  overflow: hidden;
}
.portrait-pane {
  position: relative;
  border-radius: 12px;
  display: flex;
  align-items: center;
  justify-content: center;
  padding: 6px;
  color: rgba(255, 255, 255, 0.92);
  overflow: hidden;
}
/* In play, the pic takes the leftover space and shrinks so options always fit. */
.stage > .portrait-pane {
  flex: 1 1 auto;
  min-height: 0;
}
.portrait-pane.ending {
  min-height: 120px;
  padding: 12px;
}
.face {
  font-size: 64px;
  line-height: 1;
}
.art-img {
  max-height: 100%;
  max-width: 100%;
  object-fit: contain;
}
.dialog-pane {
  flex: 0 0 auto;
  min-width: 0;
}
.goal {
  line-height: 1.2;
}
/* Tension row: one 12px caption line + a 5px bar ≈ 22px total. The art pane is
   `flex: 1 1 auto; min-height: 0`, so this comes out of the picture rather than
   out of the options — the play screen still never scrolls. */
.anger {
  margin: 3px 0 6px;
}
.anger-cap {
  font-size: 0.65rem;
  line-height: 1.2;
  opacity: 0.7;
  margin-bottom: 2px;
}
/* Four record-board tabs have to fit across a 360px phone: small type, minimal
   padding, and no per-tab minimum width. */
.board-tabs {
  --v-tabs-height: 34px;
}
.board-tabs :deep(.v-tab) {
  min-width: 0;
  font-size: 0.72rem;
  letter-spacing: 0;
}
/* Clamp the character's line so a long reply can't push the options off-screen. */
/* His reply is verse now, so line breaks are meaningful and must survive. It used
   to be clamped to three lines with overflow hidden, which silently ATE the rest of
   a longer answer — unreadable and unreachable. Now it keeps its line breaks and
   scrolls inside its own box: the page itself still never scrolls, and the art pane
   above gives up the space. */
.bubble :deep(.v-alert__content) {
  overflow: hidden;
}
/* The frame wraps the rolling window so the skip hint can sit over it without
   scrolling away with the text and without costing a row of layout height —
   vertical space on the play screen is the scarcest thing there is. */
.verse-frame {
  position: relative;
}
/* Only a frame that can actually skip becomes a control, and then it has to meet
   the 44px tap-target rule. An overflowing verse box is taller than that already;
   the minimum is here for the case where it somehow is not. */
.verse-frame[role='button'] {
  min-height: 44px;
  cursor: pointer;
}
.verse-frame[role='button']:focus-visible {
  outline: 2px solid currentColor;
  outline-offset: 2px;
  border-radius: 4px;
}
.verse-hint {
  position: absolute;
  right: 0;
  bottom: 0;
  padding: 1px 5px;
  border-radius: 6px;
  font-size: 0.6rem;
  line-height: 1.3;
  opacity: 0.65;
  background: rgba(var(--v-theme-surface), 0.82);
  pointer-events: none; /* the whole frame is the target, not this badge */
}
/* The window the verse rolls through. Capped by BOTH font size and viewport: a
   fixed em height overflowed 320x568 by 27px and pushed the options off-screen,
   which is worse than a cramped verse. */
.verse-viewport {
  max-height: min(8.5em, 15dvh);
  overflow: hidden;
}
/* Skipped (or reduced-motion): the box gives up as much height as it can spare
   and the reader scrolls the remainder. The cap keeps the four options on screen
   — the art pane above is `flex: 1 1 auto; min-height: 0`, so the extra height
   comes out of the picture and the play screen still never scrolls. */
.verse-viewport--revealed {
  max-height: min(24em, 34dvh);
  overflow-y: auto;
  overscroll-behavior: contain;
}
.verse {
  font-style: italic;
  white-space: pre-line; /* he raps: line breaks are the verse */
  padding-bottom: 0.7em; /* gap between the loop's two copies */
}
/* Two copies, shifted by exactly half the reel: the second lands where the first
   started, so the loop has no visible seam. Duration, the opening hold and the
   number of passes all arrive as inline style from useVersePacing, because they
   are computed from the measured verse and from the constants in
   lib/gameKhimkiCredits.ts — duplicating them here would let the two drift. */
.verse-reel.rolling {
  animation-name: verse-credits;
  animation-timing-function: linear;
}
@keyframes verse-credits {
  from {
    transform: translateY(0);
  }
  to {
    transform: translateY(-50%);
  }
}
/* Motion-sensitive users get a scrollable box instead of a moving one. The
   composable already checks the same query and never starts a roll for them, so
   this is a backstop for the case where `matchMedia` is unavailable and the
   probe has to answer "no preference". */
@media (prefers-reduced-motion: reduce) {
  .verse-reel.rolling {
    animation: none;
  }
  .verse-viewport {
    overflow-y: auto;
    overscroll-behavior: contain;
  }
}
/* Compact, 2-line, smaller-text options that never overflow the button. */
.option-btn {
  min-height: 44px;
  height: auto;
  margin-bottom: 6px;
}
.option-btn :deep(.v-btn__content) {
  white-space: normal;
  font-size: 0.78rem;
  line-height: 1.15;
  text-align: center;
  display: -webkit-box;
  -webkit-line-clamp: 2;
  -webkit-box-orient: vertical;
  overflow: hidden;
}
/* Loader overlays the options so switching to "thinking" causes no reflow. */
.actions {
  position: relative;
}
.options--busy {
  opacity: 0.18;
  pointer-events: none;
}
.loader-overlay {
  position: absolute;
  inset: 0;
  display: flex;
  align-items: center;
  justify-content: center;
}
/* Landscape phones: pic beside the dialogue; the dialogue side scrolls if needed. */
@media (orientation: landscape) and (max-height: 600px) {
  .stage {
    flex-direction: row;
    align-items: stretch;
  }
  .stage > .portrait-pane {
    flex: 0 0 40%;
  }
  .dialog-pane {
    flex: 1 1 auto;
    overflow-y: auto;
  }
  .face {
    font-size: 44px;
  }
}
</style>
