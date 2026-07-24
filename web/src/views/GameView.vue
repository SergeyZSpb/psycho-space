<template>
  <v-container
    :class="phase === 'play' || phase === 'intro' ? 'pa-0 play-root' : 'py-4'"
    style="max-width: 900px"
  >
    <div v-if="phase === 'loading'" class="text-center py-12">
      <v-progress-circular indeterminate color="primary" />
    </div>

    <template v-else-if="config && character">
      <div v-if="phase === 'ending'" class="d-flex align-center justify-space-between mb-2">
        <h1 class="text-h5">{{ config.title }}</h1>
        <v-chip size="small" variant="tonal" color="primary">
          успехов: {{ stats?.successes ?? 0 }}
        </v-chip>
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
        <v-chip size="small" variant="tonal" class="splash-badge">успехов: {{ stats?.successes ?? 0 }}</v-chip>
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
          <div class="goal text-caption text-medium-emphasis mb-1">🎯 {{ character.goal }}</div>
          <v-alert
            v-if="rateLimited"
            type="warning"
            variant="tonal"
            density="compact"
            class="mb-2"
            text="Слишком много запросов с вашего IP — кошелёк Сергея плачет 😢 Подожди минутку."
          />
          <v-alert variant="tonal" density="compact" class="bubble mb-2" :text="reply" />
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
          :type="success ? 'success' : 'warning'"
          variant="tonal"
          class="mb-3"
          :title="success ? 'Ты дома!' : 'Не в этот раз'"
          :text="
            success
              ? 'Дядя Ваня открылся и пропустил тебя. Дома кот наблевал на шторы. Но ты дома.'
              : 'Дядя Ваня так тебя и не пропустил. Стоишь во дворе.'
          "
        />
        <p class="text-body-2 mb-4">
          Шагов: <strong>{{ steps }}</strong>
          <template v-if="stats"> · твой рекорд: <strong>{{ bestLabel }}</strong></template>
        </p>
        <v-btn color="primary" size="large" block class="mb-6" @click="start">Ещё раз</v-btn>
      </template>

      <!-- Leaderboard (only on the end screen; the play screen stays scroll-free) -->
      <template v-if="phase === 'ending'">
      <v-divider class="my-4" />
      <h2 class="text-subtitle-1 mb-2">Таблица позора</h2>
      <p v-if="!leaderboard.length" class="text-medium-emphasis">Пока никто не прошёл. Будь первым.</p>
      <v-list v-else density="compact" class="bg-transparent">
        <v-list-item
          v-for="(e, i) in leaderboard"
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
          <v-list-item-subtitle>прошёл {{ e.successes }} · попыток {{ e.plays }}</v-list-item-subtitle>
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
import { computed, onMounted, ref } from 'vue';
import { gameApi } from '../api/endpoints';
import { ApiError } from '../api/client';
import { useErrorStore } from '../stores/error';
import type {
  GameArt,
  GameCharacter,
  GameConfig,
  GameExchange,
  GameLeaderboardEntry,
  GameStats,
} from '../api/types';

const GAME = 'smalltalk_khimki';
const FALLBACK_ART: GameArt = { key: '', emoji: '🧑', gradient: 'linear-gradient(160deg, #333, #111)' };

const errorStore = useErrorStore();

type Phase = 'loading' | 'intro' | 'play' | 'ending';
const phase = ref<Phase>('loading');

const config = ref<GameConfig | null>(null);
const stats = ref<GameStats | null>(null);
const leaderboard = ref<GameLeaderboardEntry[]>([]);

const transcript = ref<GameExchange[]>([]);
const steps = ref(0);
const currentArtKey = ref('');
const reply = ref('');
const options = ref<string[]>([]);
const success = ref(false);
const busy = ref(false);
const rateLimited = ref(false);

const character = computed<GameCharacter | null>(() => {
  const c = config.value;
  if (!c) return null;
  return c.characters.find((ch) => ch.key === c.default_character) ?? c.characters[0] ?? null;
});
// Art catalog resolved from the backend config (adding arts needs no client change).
const artMap = computed<Record<string, GameArt>>(() =>
  Object.fromEntries((character.value?.arts ?? []).map((a) => [a.key, a])),
);
const currentArt = computed<GameArt>(() => artMap.value[currentArtKey.value] ?? FALLBACK_ART);
const splashArt = computed<GameArt>(() => character.value?.arts[0] ?? FALLBACK_ART);

// Art image URL if the backend provided one and it hasn't failed to load;
// otherwise "" so we fall back to the emoji placeholder.
const failedArts = ref<string[]>([]);
function artImg(a: GameArt): string {
  return a.image && !failedArts.value.includes(a.key) ? a.image : '';
}
const bestLabel = computed(() =>
  stats.value && stats.value.best_steps > 0 ? `${stats.value.best_steps} шаг.` : '—',
);

async function refreshBoard() {
  const [lb, st] = await Promise.all([gameApi.leaderboard(GAME), gameApi.stats(GAME)]);
  leaderboard.value = lb.entries;
  stats.value = st;
}

onMounted(async () => {
  try {
    config.value = await gameApi.config(GAME);
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
  phase.value = 'play';
}

async function turn(choice: string) {
  const ch = character.value;
  if (busy.value || !ch) return;
  busy.value = true;
  rateLimited.value = false;
  try {
    const res = await gameApi.attempt(GAME, ch.key, transcript.value, choice);
    if (choice !== '') {
      transcript.value.push({ choice, reply: res.reply });
      steps.value += 1;
    }
    reply.value = res.reply;
    if (res.art) currentArtKey.value = res.art;
    options.value = res.options ?? [];
    if (res.achieved) {
      await finish(true);
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
  phase.value = 'ending';
  try {
    await gameApi.submitRun(GAME, ch.key, won, steps.value);
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
  padding: 20px 20px 24px;
  display: flex;
  flex-direction: column;
  align-items: center;
  justify-content: center;
  text-align: center;
  gap: 12px;
  overflow: hidden;
  color: rgba(255, 255, 255, 0.95);
}
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
  font-size: 2rem;
  font-weight: 800;
  letter-spacing: 0.5px;
}
.splash-badge {
  flex: 0 0 auto;
}
.splash-intro {
  flex: 0 1 auto;
  min-height: 0;
  overflow: hidden;
  max-width: 560px;
  line-height: 1.5;
}
.splash-cta {
  flex: 0 0 auto;
  min-width: 220px;
}
.splash-disclaimer {
  flex: 0 0 auto;
  font-size: 0.78rem;
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
/* Clamp the character's line so a long reply can't push the options off-screen. */
.bubble :deep(.v-alert__content) {
  font-style: italic;
  display: -webkit-box;
  -webkit-line-clamp: 3;
  -webkit-box-orient: vertical;
  overflow: hidden;
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
