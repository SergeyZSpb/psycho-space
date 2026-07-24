<template>
  <v-container class="py-4" style="max-width: 900px">
    <div v-if="phase === 'loading'" class="text-center py-12">
      <v-progress-circular indeterminate color="primary" />
    </div>

    <template v-else-if="config && character">
      <div class="d-flex align-center justify-space-between mb-2">
        <h1 class="text-h5">{{ config.title }}</h1>
        <v-chip size="small" variant="tonal" color="primary">
          успехов: {{ stats?.successes ?? 0 }}
        </v-chip>
      </div>

      <!-- Intro -->
      <template v-if="phase === 'intro'">
        <p class="text-body-1 mb-4">{{ config.intro }}</p>
        <v-btn color="primary" size="large" block @click="start">Погнали домой</v-btn>
      </template>

      <!-- Play (works portrait + landscape) -->
      <div v-else-if="phase === 'play'" class="stage">
        <div class="portrait-pane" :style="{ background: bg }">
          <div class="face">{{ face }}</div>
          <div class="who">{{ character.name }}</div>
          <div class="steps">шаг {{ steps }} / {{ character.max_steps }}</div>
        </div>

        <div class="dialog-pane">
          <div class="goal text-medium-emphasis mb-2">🎯 {{ character.goal }}</div>
          <v-alert variant="tonal" class="bubble mb-3" :text="lastReply" />
          <div class="options">
            <v-btn
              v-for="opt in availableOptions"
              :key="opt.id"
              class="mb-2 text-none option-btn"
              variant="outlined"
              size="large"
              block
              :disabled="busy"
              @click="choose(opt.id)"
            >
              {{ opt.label }}
            </v-btn>
          </div>
        </div>
      </div>

      <!-- Ending -->
      <template v-else-if="phase === 'ending'">
        <div class="portrait-pane ending mb-3" :style="{ background: bg }">
          <div class="face">{{ face }}</div>
        </div>
        <v-alert
          :type="success ? 'success' : 'warning'"
          variant="tonal"
          class="mb-3"
          :title="success ? 'Дядя Витя пропустил!' : 'Не в этот раз'"
          :text="
            success
              ? 'Ты дома. Кот наблевал на шторы. Но ты дома.'
              : 'Дядя Витя тебя не пропустил. Стоишь во дворе, курить бамбук.'
          "
        />
        <p class="text-body-2 mb-4">
          Шагов: <strong>{{ steps }}</strong>
          <template v-if="stats"> · твой рекорд: <strong>{{ bestLabel }}</strong></template>
        </p>
        <v-btn color="primary" size="large" block class="mb-6" @click="start">Ещё раз</v-btn>
      </template>

      <!-- Leaderboard -->
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
  </v-container>
</template>

<script setup lang="ts">
import { computed, onMounted, ref } from 'vue';
import { gameApi } from '../api/endpoints';
import { useErrorStore } from '../stores/error';
import { backgroundFor, emotionFace } from '../game/assets';
import type { GameCharacter, GameConfig, GameLeaderboardEntry, GameStats } from '../api/types';

const GAME = 'smalltalk_khimki';
const START_EMOTION = 'suspicious';

const errorStore = useErrorStore();

type Phase = 'loading' | 'intro' | 'play' | 'ending';
const phase = ref<Phase>('loading');

const config = ref<GameConfig | null>(null);
const stats = ref<GameStats | null>(null);
const leaderboard = ref<GameLeaderboardEntry[]>([]);

const history = ref<string[]>([]);
const used = ref<string[]>([]);
const steps = ref(0);
const emotion = ref(START_EMOTION);
const lastReply = ref('');
const success = ref(false);
const busy = ref(false);

const character = computed<GameCharacter | null>(() => {
  const c = config.value;
  if (!c) return null;
  return c.characters.find((ch) => ch.key === c.default_character) ?? c.characters[0] ?? null;
});
const availableOptions = computed(() =>
  (character.value?.options ?? []).filter((o) => !used.value.includes(o.id)),
);
const bg = computed(() => backgroundFor(character.value?.background ?? ''));
const face = computed(() => emotionFace(emotion.value));
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
  history.value = [];
  used.value = [];
  steps.value = 0;
  emotion.value = START_EMOTION;
  lastReply.value = character.value?.greeting ?? '';
  success.value = false;
  phase.value = 'play';
}

async function choose(optionId: string) {
  const ch = character.value;
  if (busy.value || !ch) return;
  busy.value = true;
  try {
    const res = await gameApi.attempt(GAME, ch.key, history.value, optionId);
    history.value.push(optionId);
    used.value.push(optionId);
    steps.value += 1;
    emotion.value = res.emotion;
    lastReply.value = res.reply;
    if (res.achieved) {
      await finish(true);
    } else if (steps.value >= ch.max_steps || availableOptions.value.length === 0) {
      await finish(false);
    }
  } catch (err) {
    errorStore.report(err);
  } finally {
    busy.value = false;
  }
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
/* Portrait (default): character banner on top, dialogue below. */
.stage {
  display: flex;
  flex-direction: column;
  gap: 12px;
}
.portrait-pane {
  position: relative;
  border-radius: 12px;
  display: flex;
  flex-direction: column;
  align-items: center;
  justify-content: center;
  min-height: 140px;
  padding: 12px;
  color: rgba(255, 255, 255, 0.92);
}
.portrait-pane.ending {
  min-height: 120px;
}
.face {
  font-size: 72px;
  line-height: 1;
}
.who {
  margin-top: 6px;
  font-weight: 600;
}
.steps {
  position: absolute;
  top: 8px;
  right: 12px;
  font-size: 0.8rem;
  opacity: 0.85;
}
.dialog-pane {
  flex: 1;
  min-width: 0;
}
.bubble :deep(.v-alert__content) {
  font-style: italic;
}
.option-btn {
  min-height: 48px;
  white-space: normal;
}
/* Landscape on phones: character beside the dialogue so it fits without scroll. */
@media (orientation: landscape) and (max-height: 600px) {
  .stage {
    flex-direction: row;
    align-items: stretch;
  }
  .portrait-pane {
    flex: 0 0 38%;
    min-height: 0;
  }
  .face {
    font-size: 56px;
  }
  .dialog-pane {
    overflow-y: auto;
    max-height: 78vh;
  }
}
</style>
