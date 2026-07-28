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
            <h2 class="text-h6 mb-2">вход через VK ID или Яндекс ID</h2>
            <p class="text-body-2 text-medium-emphasis mb-4">
              Логинимся через VK ID или Яндекс ID — как удобнее. Передается только базовая инфа
              (картинка, имя, фамилия, пол, дата рождения). Почту не запрашиваем и не храним.
            </p>

            <!-- CONSENT GATE. Neither provider is reachable until this is ticked:
                 consent has to precede any processing of personal data, and both
                 login paths start that processing. The VK widget is not even
                 mounted, and the Яндекс button is not on screen. -->
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

            <div v-show="consented" class="mt-4">
              <!-- VK OneTap mounts here once consent is given. -->
              <div data-testid="login-vk">
                <div ref="vkContainer" class="vk-container" />
                <div v-if="mounting" class="d-flex align-center ga-2 mt-2 text-medium-emphasis">
                  <v-progress-circular indeterminate size="20" width="2" />
                  <span class="text-caption">грузим VK ID…</span>
                </div>
              </div>

              <!-- Two providers, so they need separating rather than stacking. -->
              <div class="or-divider my-4" aria-hidden="true">
                <span class="or-divider__word text-caption text-medium-emphasis">или</span>
              </div>

              <!-- Яндекс needs no SDK and no widget: one button, one navigation.
                   No logo mark is drawn — a hand-made imitation of somebody's
                   trademark is worse than a neutral icon. -->
              <v-btn
                data-testid="login-yandex"
                block
                size="large"
                color="primary"
                variant="tonal"
                class="yandex-btn"
                prepend-icon="mdi-login-variant"
                :loading="yandexBusy"
                @click="startYandex"
              >
                Войти с Яндекс ID
              </v-btn>
            </div>

            <p v-show="!consented" class="text-caption text-disabled mt-2">
              поставь галочку выше, чтобы появились кнопки входа
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
import { useYandexLogin } from '../composables/useYandexLogin';
import { useErrorStore } from '../stores/error';

const consented = ref(false);
const vkContainer = ref<HTMLElement | null>(null);
const mounting = ref(false);
const yandexBusy = ref(false);
let mounted = false;
let cleanup: (() => void) | null = null;

const { mountOneTap } = useVkLogin();
const { start: startYandexLogin } = useYandexLogin();
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

// Яндекс: no widget to mount, so nothing happens until the button is pressed.
// A failure here is a real one — an unconfigured provider (503) or a refused
// state mint — and the user cannot proceed, so it gets the modal.
async function startYandex() {
  yandexBusy.value = true;
  try {
    await startYandexLogin();
    // Deliberately NOT clearing yandexBusy on success: the browser is on its
    // way to Яндекс, and a button that springs back to life mid-navigation
    // invites a second click and a second state cookie.
  } catch (err) {
    yandexBusy.value = false;
    errorStore.report(err);
  }
}

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
/* A rule with the word «или» sitting in it. Flex rather than a pseudo-element
   trick so it cannot overflow: the lines shrink, the word does not. */
.or-divider {
  display: flex;
  align-items: center;
  gap: 12px;
}
.or-divider::before,
.or-divider::after {
  content: '';
  flex: 1 1 0;
  min-width: 0;
  height: 1px;
  background: rgba(var(--v-border-color), var(--v-border-opacity));
}
.or-divider__word {
  flex: 0 0 auto;
}
/* The layout suite enforces a 44px tap target at 360px; Vuetify's `large` is
   44px on the nose, so this is the margin that keeps a rounding error from
   failing the gate. */
.yandex-btn {
  min-height: 48px;
}
</style>
