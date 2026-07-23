<template>
  <v-container class="py-6" style="max-width: 820px">
    <h1 class="text-h5 mb-4">Вишлист</h1>

    <!-- Add-idea form -->
    <v-card class="pa-4 mb-6">
      <v-form @submit.prevent="submit">
        <v-text-field
          v-model="title"
          label="Идея (обязательно)"
          :error-messages="titleError"
          maxlength="200"
          counter
          @update:model-value="titleError = ''"
        />
        <v-textarea
          v-model="body"
          label="Подробности (необязательно)"
          rows="2"
          auto-grow
          maxlength="2000"
          counter
        />
        <div class="d-flex justify-end">
          <v-btn
            type="submit"
            color="primary"
            :block="smAndDown"
            :loading="creating"
            prepend-icon="mdi-plus"
          >
            Добавить
          </v-btn>
        </div>
      </v-form>
    </v-card>

    <!-- Sort toggle -->
    <div class="d-flex align-center justify-space-between mb-3">
      <v-btn-toggle v-model="sort" mandatory density="comfortable" color="primary" variant="outlined">
        <v-btn value="top" prepend-icon="mdi-fire">топ</v-btn>
        <v-btn value="new" prepend-icon="mdi-clock-outline">новое</v-btn>
      </v-btn-toggle>
      <v-btn variant="text" icon="mdi-refresh" title="Обновить" @click="load" />
    </div>

    <!-- List -->
    <div v-if="loading" class="text-center py-8">
      <v-progress-circular indeterminate color="primary" />
    </div>

    <v-alert v-else-if="items.length === 0" type="info" variant="tonal" class="my-4">
      Пока пусто. Будь первым — добавь идею!
    </v-alert>

    <v-card v-for="item in items" :key="item.id" class="pa-4 mb-3">
      <div class="d-flex ga-4">
        <!-- Vote column -->
        <div style="min-width: 56px">
          <VoteButton
            :votes="item.votes"
            :voted="item.voted_by_me"
            :loading="votingId === item.id"
            @toggle="toggleVote(item)"
          />
        </div>

        <!-- Body -->
        <div class="flex-grow-1">
          <div class="d-flex align-center ga-2">
            <h3 class="text-subtitle-1 font-weight-bold ps-wrap flex-grow-1" style="min-width: 0">
              {{ item.title }}
            </h3>
            <v-chip v-if="item.mine" size="x-small" color="secondary" variant="tonal" class="flex-shrink-0">
              вы
            </v-chip>
          </div>
          <p v-if="item.body" class="text-body-2 text-medium-emphasis mt-1 ps-wrap" style="white-space: pre-wrap">
            {{ item.body }}
          </p>

          <div class="d-flex align-center ga-2 mt-3 flex-wrap">
            <!-- author attribution: "автор: <avatar> Имя Фамилия" -->
            <span class="d-flex align-center ga-2">
              <span class="text-caption text-medium-emphasis">автор:</span>
              <a
                :href="item.author.vk_url || undefined"
                target="_blank"
                rel="noopener noreferrer"
                class="author-link d-flex align-center ga-2"
              >
                <v-avatar size="24" color="secondary">
                  <v-img v-if="item.author.avatar_url" :src="item.author.avatar_url" alt="" />
                  <span v-else class="text-caption">{{ authorInitial(item.author.display_name) }}</span>
                </v-avatar>
                <span class="text-caption font-weight-medium">{{ item.author.display_name || 'аноним' }}</span>
              </a>
            </span>

            <v-spacer />

            <!-- Comment count toggles the (lazy-loaded) comments section. -->
            <v-btn
              variant="text"
              size="small"
              :aria-label="`Комментарии (${item.comment_count})`"
              :prepend-icon="expanded.has(item.id) ? 'mdi-comment-remove-outline' : 'mdi-comment-outline'"
              @click="toggleComments(item)"
            >
              {{ item.comment_count }}
              <span class="d-none d-sm-inline ml-1">Комментарии</span>
            </v-btn>
          </div>
        </div>
      </div>

      <!-- Lazy-loaded comments: the component fetches only once mounted. -->
      <CommentSection
        v-if="expanded.has(item.id)"
        :item-id="item.id"
        @created="item.comment_count += 1"
      />
    </v-card>
  </v-container>
</template>

<script setup lang="ts">
import { onMounted, reactive, ref, watch } from 'vue';
import { useDisplay } from 'vuetify';
import VoteButton from '../components/VoteButton.vue';
import CommentSection from '../components/CommentSection.vue';
import { wishlistApi } from '../api/endpoints';
import { ApiError } from '../api/client';
import { applyToggle } from '../lib/vote';
import { useErrorStore } from '../stores/error';
import type { WishlistItem, WishlistSort } from '../api/types';

const errorStore = useErrorStore();
const { smAndDown } = useDisplay();

const items = ref<WishlistItem[]>([]);
const loading = ref(false);
const sort = ref<WishlistSort>('top');

const title = ref('');
const body = ref('');
const titleError = ref('');
const creating = ref(false);
const votingId = ref<string | null>(null);

// Ids of items whose comments section is currently expanded (lazy-loaded).
const expanded = reactive(new Set<string>());

function toggleComments(item: WishlistItem) {
  if (expanded.has(item.id)) expanded.delete(item.id);
  else expanded.add(item.id);
}

function authorInitial(name: string): string {
  const n = name.trim();
  return n ? n.charAt(0).toUpperCase() : '?';
}

async function load() {
  loading.value = true;
  try {
    const res = await wishlistApi.list(sort.value);
    items.value = res.items;
  } catch (err) {
    errorStore.report(err);
  } finally {
    loading.value = false;
  }
}

async function submit() {
  const t = title.value.trim();
  if (!t) {
    titleError.value = 'Введите название идеи';
    return;
  }
  creating.value = true;
  try {
    const created = await wishlistApi.create(t, body.value.trim());
    // Prepend the new item so it is immediately visible.
    items.value = [created, ...items.value];
    title.value = '';
    body.value = '';
    titleError.value = '';
  } catch (err) {
    // Known validation codes go inline; anything else uses the global modal.
    if (err instanceof ApiError && err.code === 'title_required') {
      titleError.value = 'Введите название идеи';
    } else if (err instanceof ApiError && err.code === 'too_long') {
      titleError.value = 'Слишком длинно';
    } else {
      errorStore.report(err);
    }
  } finally {
    creating.value = false;
  }
}

async function toggleVote(item: WishlistItem) {
  votingId.value = item.id;
  const wasVoted = item.voted_by_me;
  try {
    if (wasVoted) await wishlistApi.unvote(item.id);
    else await wishlistApi.vote(item.id);
    applyToggle(item);
  } catch (err) {
    errorStore.report(err);
  } finally {
    votingId.value = null;
  }
}

watch(sort, load);
onMounted(load);
</script>

<style scoped>
.author-link {
  color: inherit;
  text-decoration: none;
}
.author-link:hover {
  color: rgb(var(--v-theme-primary));
}
</style>
