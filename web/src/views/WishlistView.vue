<template>
  <v-container class="py-6" style="max-width: 820px">
    <h1 class="text-h5 mb-4">Вишлист</h1>

    <!-- Intro / context: what this is + solicit ideas -->
    <v-card variant="tonal" color="primary" class="pa-5 mb-6">
      <div class="d-flex align-center ga-2 mb-2">
        <v-icon icon="mdi-lightbulb-on-outline" />
        <h2 class="text-h6 font-weight-bold">что это вообще такое</h2>
      </div>
      <p class="text-body-2 mb-2">
        психоспасе — приложулька для своих, чисто плейграунд для нас. Сейчас тут один
        раздел — вот этот <strong>вишлист</strong> — но дальше будет больше, оххх.
      </p>
      <p class="text-body-2 mb-3">
        Собираем идеи, <strong>что бы такого запилить</strong>: что-нибудь крутое,
        интерактивное и на несколько человек. Кидай идею, голосуй за чужие, комментируй —
        лучшие реально запилим.
      </p>
      <div class="d-flex flex-wrap ga-2">
        <v-chip size="small" color="primary" variant="elevated" prepend-icon="mdi-plus">
          добавь идею ниже
        </v-chip>
        <v-chip size="small" color="primary" variant="elevated" prepend-icon="mdi-arrow-up-bold">
          голосуй
        </v-chip>
        <v-chip size="small" color="primary" variant="elevated" prepend-icon="mdi-comment-outline">
          комментируй
        </v-chip>
      </div>
    </v-card>

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
      <v-btn variant="text" icon="mdi-refresh" title="Обновить" aria-label="Обновить" @click="load()" />
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
        <div class="flex-grow-1" style="min-width: 0">
          <!-- Clickable header (title + body) toggles this item's comments. -->
          <div
            class="item-head"
            role="button"
            tabindex="0"
            :aria-expanded="expanded.has(item.id)"
            @click="toggleComments(item)"
            @keydown.enter="toggleComments(item)"
          >
            <div class="d-flex align-center ga-2">
              <h3 class="text-subtitle-1 font-weight-bold ps-wrap flex-grow-1" style="min-width: 0">
                {{ item.title }}
              </h3>
              <v-chip v-if="item.mine" size="x-small" color="secondary" variant="tonal" class="flex-shrink-0">
                вы
              </v-chip>
              <v-icon
                size="18"
                class="text-medium-emphasis flex-shrink-0"
                :icon="expanded.has(item.id) ? 'mdi-chevron-up' : 'mdi-chevron-down'"
              />
            </div>
            <p v-if="item.body" class="text-body-2 text-medium-emphasis mt-1 ps-wrap" style="white-space: pre-wrap">
              {{ item.body }}
            </p>
          </div>

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

            <!-- Comment count toggles this item's comments. -->
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

            <!-- Delete: author or admin. -->
            <v-btn
              v-if="canDelete(item)"
              variant="text"
              size="small"
              color="error"
              icon="mdi-trash-can-outline"
              title="Удалить идею"
              aria-label="Удалить идею"
              @click="askDeleteItem(item)"
            />
          </div>
        </div>
      </div>

      <!-- Comments: expanded by default; component lazy-fetches on mount. -->
      <CommentSection
        v-if="expanded.has(item.id)"
        :item-id="item.id"
        @created="item.comment_count += 1"
        @deleted="item.comment_count = Math.max(0, item.comment_count - 1)"
      />
    </v-card>

    <!-- Delete-idea confirmation. -->
    <v-dialog v-model="confirmItemOpen" max-width="420">
      <v-card>
        <v-card-title>Удалить идею?</v-card-title>
        <v-card-text class="ps-wrap">
          «{{ pendingItem?.title }}» — удалить без возможности вернуть?
        </v-card-text>
        <v-card-actions>
          <v-spacer />
          <v-btn variant="text" @click="confirmItemOpen = false">Отмена</v-btn>
          <v-btn color="error" variant="tonal" :loading="deletingItem" @click="confirmDeleteItem">
            Удалить
          </v-btn>
        </v-card-actions>
      </v-card>
    </v-dialog>
  </v-container>
</template>

<script setup lang="ts">
import { onMounted, onUnmounted, reactive, ref, watch } from 'vue';
import { useDisplay } from 'vuetify';
import VoteButton from '../components/VoteButton.vue';
import CommentSection from '../components/CommentSection.vue';
import { wishlistApi } from '../api/endpoints';
import { ApiError } from '../api/client';
import { applyToggle } from '../lib/vote';
import { useAuthStore } from '../stores/auth';
import { useErrorStore } from '../stores/error';
import type { WishlistItem, WishlistSort } from '../api/types';

const REFRESH_MS = 30_000;

const auth = useAuthStore();
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

// Ids of items whose comments section is expanded (comments show by default).
const expanded = reactive(new Set<string>());
// Items we've already decided expansion for — new items default to expanded,
// but a background refresh must not re-expand what the user collapsed.
const decided = new Set<string>();

const confirmItemOpen = ref(false);
const pendingItem = ref<WishlistItem | null>(null);
const deletingItem = ref(false);

let refreshTimer: ReturnType<typeof setInterval> | undefined;

function canDelete(item: WishlistItem): boolean {
  return item.mine || auth.isAdmin;
}

function toggleComments(item: WishlistItem) {
  if (expanded.has(item.id)) expanded.delete(item.id);
  else expanded.add(item.id);
}

function authorInitial(name: string): string {
  const n = name.trim();
  return n ? n.charAt(0).toUpperCase() : '?';
}

// background=true → silent poll: no spinner, no error modal on failure.
async function load(opts: { background?: boolean } = {}) {
  if (!opts.background) loading.value = true;
  try {
    const res = await wishlistApi.list(sort.value);
    items.value = res.items;
    for (const it of res.items) {
      if (!decided.has(it.id)) {
        decided.add(it.id);
        expanded.add(it.id); // default: comments expanded
      }
    }
  } catch (err) {
    if (!opts.background) errorStore.report(err);
  } finally {
    if (!opts.background) loading.value = false;
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
    items.value = [created, ...items.value];
    decided.add(created.id);
    expanded.add(created.id);
    title.value = '';
    body.value = '';
    titleError.value = '';
  } catch (err) {
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

function askDeleteItem(item: WishlistItem) {
  pendingItem.value = item;
  confirmItemOpen.value = true;
}

async function confirmDeleteItem() {
  const item = pendingItem.value;
  if (!item) return;
  deletingItem.value = true;
  try {
    await wishlistApi.deleteItem(item.id);
    items.value = items.value.filter((i) => i.id !== item.id);
    expanded.delete(item.id);
    decided.delete(item.id);
    confirmItemOpen.value = false;
    pendingItem.value = null;
  } catch (err) {
    errorStore.report(err); // 403 forbidden / 404 not_found
  } finally {
    deletingItem.value = false;
  }
}

watch(sort, () => load());

onMounted(() => {
  void load();
  refreshTimer = setInterval(() => void load({ background: true }), REFRESH_MS);
});
onUnmounted(() => {
  if (refreshTimer) clearInterval(refreshTimer);
});
</script>

<style scoped>
.item-head {
  cursor: pointer;
  border-radius: 8px;
}
.item-head:hover h3 {
  color: rgb(var(--v-theme-primary));
}
.author-link {
  color: inherit;
  text-decoration: none;
}
.author-link:hover {
  color: rgb(var(--v-theme-primary));
}
</style>
