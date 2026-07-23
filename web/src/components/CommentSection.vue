<template>
  <div class="comment-section mt-3 pt-3">
    <h4 class="text-subtitle-2 mb-3">Комментарии</h4>

    <div v-if="loading" class="text-center py-4">
      <v-progress-circular indeterminate color="primary" size="24" />
    </div>

    <template v-else>
      <p v-if="comments.length === 0" class="text-caption text-medium-emphasis mb-3">
        Пока нет комментариев. Будь первым!
      </p>

      <!-- messenger-style comment: avatar · header (name + handle) · body · footer (vote + time) -->
      <div v-for="c in comments" :key="c.id" class="d-flex ga-3 mb-4">
        <a
          :href="c.author.vk_url || undefined"
          target="_blank"
          rel="noopener noreferrer"
          class="flex-shrink-0"
        >
          <v-avatar size="36" color="secondary">
            <v-img v-if="c.author.avatar_url" :src="c.author.avatar_url" alt="" />
            <span v-else>{{ initial(c.author.display_name) }}</span>
          </v-avatar>
        </a>

        <div class="flex-grow-1" style="min-width: 0">
          <div class="d-flex align-center ga-2 flex-wrap">
            <span class="font-weight-bold text-body-2 ps-wrap">{{ c.author.display_name || 'аноним' }}</span>
            <a
              v-if="handle(c.author.vk_url)"
              :href="c.author.vk_url"
              target="_blank"
              rel="noopener noreferrer"
              class="handle-link text-caption ps-wrap"
            >{{ handle(c.author.vk_url) }}</a>
            <v-chip v-if="c.mine" size="x-small" color="secondary" variant="tonal">вы</v-chip>
          </div>

          <p class="text-body-2 mt-1 mb-2 ps-wrap" style="white-space: pre-wrap">{{ c.body }}</p>

          <div class="d-flex align-center ga-3">
            <VoteButton
              inline
              size="x-small"
              :votes="c.votes"
              :voted="c.voted_by_me"
              :loading="votingId === c.id"
              @toggle="toggleVote(c)"
            />
            <span class="text-caption text-medium-emphasis">{{ shortTime(c.created_at) }}</span>
          </div>
        </div>
      </div>
    </template>

    <!-- Add-comment form -->
    <v-form class="mt-2" @submit.prevent="submit">
      <v-textarea
        v-model="draft"
        label="Оставьте комментарий…"
        rows="1"
        auto-grow
        density="compact"
        maxlength="2000"
        counter
        :error-messages="draftError"
        @update:model-value="draftError = ''"
      />
      <div class="d-flex justify-end">
        <v-btn
          type="submit"
          color="primary"
          size="small"
          variant="tonal"
          :block="smAndDown"
          :loading="creating"
          prepend-icon="mdi-comment-plus-outline"
        >
          Добавить комментарий
        </v-btn>
      </div>
    </v-form>
  </div>
</template>

<script setup lang="ts">
import { onMounted, ref } from 'vue';
import { useDisplay } from 'vuetify';
import VoteButton from './VoteButton.vue';
import { wishlistApi } from '../api/endpoints';
import { ApiError } from '../api/client';
import { applyToggle } from '../lib/vote';
import { vkHandle as handle } from '../lib/vk';
import { useErrorStore } from '../stores/error';
import type { WishlistComment } from '../api/types';

const props = defineProps<{ itemId: string }>();
const emit = defineEmits<{ created: [] }>();

const errorStore = useErrorStore();
const { smAndDown } = useDisplay();

const comments = ref<WishlistComment[]>([]);
const loading = ref(false);
const draft = ref('');
const draftError = ref('');
const creating = ref(false);
const votingId = ref<string | null>(null);

function initial(name: string): string {
  const n = name.trim();
  return n ? n.charAt(0).toUpperCase() : '?';
}

// Short relative timestamp (RU): «только что» / «N мин» / «N ч» / «N дн» / date.
function shortTime(iso: string): string {
  const d = new Date(iso);
  if (Number.isNaN(d.getTime())) return '';
  const min = Math.floor((Date.now() - d.getTime()) / 60000);
  if (min < 1) return 'только что';
  if (min < 60) return `${min} мин`;
  const hr = Math.floor(min / 60);
  if (hr < 24) return `${hr} ч`;
  const day = Math.floor(hr / 24);
  if (day < 7) return `${day} дн`;
  return d.toLocaleDateString('ru-RU');
}

async function load() {
  loading.value = true;
  try {
    const res = await wishlistApi.comments(props.itemId);
    comments.value = res.comments;
  } catch (err) {
    errorStore.report(err);
  } finally {
    loading.value = false;
  }
}

async function submit() {
  const body = draft.value.trim();
  if (!body) {
    draftError.value = 'Введите комментарий';
    return;
  }
  creating.value = true;
  try {
    const created = await wishlistApi.createComment(props.itemId, body);
    // Backend sorts top-voted first; a brand-new comment (0 votes) goes last.
    comments.value = [...comments.value, created];
    draft.value = '';
    draftError.value = '';
    emit('created');
  } catch (err) {
    if (err instanceof ApiError && err.code === 'comment_required') {
      draftError.value = 'Введите комментарий';
    } else if (err instanceof ApiError && err.code === 'too_long') {
      draftError.value = 'Слишком длинно';
    } else {
      errorStore.report(err);
    }
  } finally {
    creating.value = false;
  }
}

async function toggleVote(c: WishlistComment) {
  votingId.value = c.id;
  const wasVoted = c.voted_by_me;
  try {
    if (wasVoted) await wishlistApi.unvoteComment(c.id);
    else await wishlistApi.voteComment(c.id);
    applyToggle(c);
  } catch (err) {
    errorStore.report(err);
  } finally {
    votingId.value = null;
  }
}

onMounted(load);
</script>

<style scoped>
.comment-section {
  border-top: 1px solid rgba(var(--v-border-color), var(--v-border-opacity));
}
.handle-link {
  color: rgb(var(--v-theme-on-surface));
  opacity: 0.6;
  text-decoration: none;
}
.handle-link:hover {
  opacity: 1;
  color: rgb(var(--v-theme-primary));
}
</style>
