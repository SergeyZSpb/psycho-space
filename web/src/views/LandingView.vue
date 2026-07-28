<template>
  <PublicLayout>
    <v-container class="py-12">
      <!-- Hero -->
      <section class="text-center mb-10">
        <h1 class="brand-title mb-4">психоспасе</h1>
        <p class="hero-cringe text-h6 font-weight-regular mx-auto">
          это супер нейрослоп приложулька оххх оххх психоспасе
        </p>
      </section>

      <!-- Login card -->
      <v-row justify="center">
        <v-col cols="12" sm="9" md="6" lg="5">
          <v-card class="pa-6">
            <h2 class="text-h6 mb-2">вход через VK ID</h2>
            <p class="text-body-2 text-medium-emphasis mb-4">
              Логинимся через VK ID. Передается только базовая инфа (картинка, имя, фамилия, пол, дата рождения - то же, что видно в вк)
            </p>

            <!-- Consent gate: the VK widget mounts ONLY after this is ticked. -->
            <v-checkbox v-model="consented" density="comfortable" hide-details class="mb-2">
              <template #label>
                <span class="text-body-2">
                  Я соглашаюсь с
                  <router-link class="consent-link" :to="{ name: 'privacy' }" target="_blank">
                    Политикой обработки ПД</router-link>
                  и
                  <router-link class="consent-link" :to="{ name: 'consent' }" target="_blank">
                    Согласием на обработку ПД</router-link>
                </span>
              </template>
            </v-checkbox>

            <!-- VK OneTap mounts here once consent is given. -->
            <div v-show="consented" class="mt-4">
              <div ref="vkContainer" class="vk-container" />
              <div v-if="mounting" class="d-flex align-center ga-2 mt-2 text-medium-emphasis">
                <v-progress-circular indeterminate size="20" width="2" />
                <span class="text-caption">грузим VK ID…</span>
              </div>
            </div>

            <p v-show="!consented" class="text-caption text-disabled mt-2">
              поставь галочку выше, чтобы появилась кнопка входа
            </p>
          </v-card>
        </v-col>
      </v-row>
    </v-container>
  </PublicLayout>
</template>

<script setup lang="ts">
import { ref, watch, onBeforeUnmount } from 'vue';
import PublicLayout from '../components/layout/PublicLayout.vue';
import { useVkLogin } from '../composables/useVkLogin';
import { useErrorStore } from '../stores/error';

const consented = ref(false);
const vkContainer = ref<HTMLElement | null>(null);
const mounting = ref(false);
let mounted = false;
let cleanup: (() => void) | null = null;

const { mountOneTap } = useVkLogin();
const errorStore = useErrorStore();

// Mount the VK widget the first time consent is granted (and the container exists).
watch(consented, async (yes) => {
  if (!yes || mounted || !vkContainer.value) return;
  mounted = true;
  mounting.value = true;
  try {
    cleanup = await mountOneTap(vkContainer.value, {
      // The widget could not do something — nearly always that it could not
      // personalise its button, because the browser would not let VK's iframe
      // see VK's cookies. Firefox partitions third-party storage per top-level
      // site, so that is the normal state of affairs there and will be
      // everywhere as third-party cookies go away.
      //
      // NOTHING IS BROKEN WHEN THIS FIRES. The button still logs you in: it
      // opens VK top-level in a new tab, where VK is first-party and can see
      // your session. So this must not open the error modal, which used to
      // report it as code `unexpected` with an EMPTY trace id and tell the user
      // to send that to Sergei — a meaningless code, no id, and nothing wrong.
      onWidgetError: (err) => console.warn('VK widget could not personalise', err),
      // A backend refusal, on the other hand, is a real ApiError with a real
      // trace id, and the user genuinely cannot get in. That keeps the modal.
      onExchangeError: (err) => errorStore.report(err),
    });
  } catch (err) {
    // The SDK failed to initialise at all — no widget, so no way to log in.
    // That is worth the modal.
    mounted = false; // allow a retry on next toggle
    errorStore.report(err);
  } finally {
    mounting.value = false;
  }
});

onBeforeUnmount(() => cleanup?.());
</script>

<style scoped>
.brand-title {
  font-size: clamp(2.5rem, 8vw, 4.5rem);
  font-weight: 800;
  letter-spacing: 1px;
  color: rgb(var(--v-theme-primary));
  text-shadow: 0 0 28px rgba(45, 212, 191, 0.45);
}
.hero-cringe {
  max-width: 640px;
  opacity: 0.85;
}
.consent-link {
  color: rgb(var(--v-theme-primary));
}
.vk-container {
  min-height: 44px;
}
</style>
