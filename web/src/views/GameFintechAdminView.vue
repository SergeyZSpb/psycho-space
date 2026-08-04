<template>
  <v-container class="py-6" style="max-width: 900px" data-testid="fintech-admin">
    <h1 class="text-h5 mb-1">АДМИН ФИНТЕХА</h1>
    <p class="text-body-2 text-medium-emphasis mb-4 ps-wrap">
      Этаж, на котором сейчас работают. Пока только смотрим — ничего отсюда не меняется.
    </p>

    <div v-if="loading" class="text-center py-8" data-testid="fintech-admin-loading">
      <v-progress-circular indeterminate color="primary" />
    </div>

    <!-- A REFUSAL IS A SCREEN, not an empty box. The router's guard turns a
         non-admin back at the door, so a 403 here means the two locks disagree —
         a role that changed under a tab that was already open. Saying so beats
         drawing a room with nothing in it, which reads as an office that has
         been emptied. -->
    <v-alert v-else-if="error" type="error" variant="tonal" data-testid="fintech-admin-error" class="ps-wrap">
      {{ error }}
    </v-alert>

    <template v-else-if="floor">
      <!-- WHAT THE FLOOR IS. Everything here is a fact about the layout that is
           not in its geometry, which is exactly why the plan alone is not
           enough: two floors can look identical and have arrived by completely
           different routes. -->
      <v-card class="pa-4 mb-4" data-testid="fintech-admin-status">
        <div class="status">
          <div class="status-row">
            <span class="status-label">откуда этот этаж</span>
            <span class="status-value" data-testid="fintech-admin-source">{{ sourceText }}</span>
          </div>
          <div class="status-row">
            <span class="status-label">поставлен</span>
            <span class="status-value" data-testid="fintech-admin-installed">{{ installedText }}</span>
          </div>
          <div class="status-row">
            <!-- HOW MANY PEOPLE ARE STANDING ON IT RIGHT NOW, and the reason it is
                 on this card at all: it is what turns «перестроить офис» from a
                 button into a decision. A number rather than a sentence, because
                 the noun would have to decline and this one word is not worth a
                 pluraliser. -->
            <span class="status-label">сейчас в офисе</span>
            <span class="status-value" data-testid="fintech-admin-occupants">{{ floor.occupants }}</span>
          </div>
          <div class="status-row">
            <span class="status-label">размер</span>
            <span class="status-value" data-testid="fintech-admin-size">{{ roomText }}</span>
          </div>
          <div class="status-row">
            <!-- The content hash. It is what a player's client compares its cached
                 geometry against, so it is the one string worth quoting when
                 somebody says the office looks wrong. -->
            <span class="status-label">id раскладки</span>
            <code class="status-value ps-wrap" data-testid="fintech-admin-id">{{ floor.layout.id }}</code>
          </div>
        </div>
      </v-card>

      <v-card class="pa-4" data-testid="fintech-admin-plan-card">
        <!-- THE PLAN. The room from directly above with nothing standing in it:
             no figures, no HUD, no depth. Real DOM rather than a canvas, like
             everything else in this project that has to be asserted on — and
             here it matters twice over, because every square on this plan is
             about to become something an editor can drag. -->
        <div
          class="plan"
          data-testid="fintech-admin-plan"
          role="img"
          :aria-label="planLabel"
          :style="{
            '--ratio': String(box.ratio),
            '--row-stripe': box.rowStripe,
            '--col-stripe': box.columnStripe,
          }"
        >
          <span
            v-for="(solid, i) in floor.layout.solids"
            :key="`s${i}`"
            class="plan-solid"
            data-testid="fintech-admin-solid"
            :data-kind="solid.kind"
            :style="solidStyle(solid)"
          />
          <!-- The glazing. Decorative in the game and decorative here, but drawn
               because an admin moving a desk is looking at a room, and a room
               with no windows in the plan is a different room. -->
          <span
            v-for="(pane, i) in panes"
            :key="`w${i}`"
            class="plan-window"
            data-testid="fintech-admin-window"
            :data-wall="pane.wall"
            :style="pane.style"
          />
          <!-- LAST, SO THEY ARE ON TOP. A spot with furniture on it is the exact
               state the validator refuses with «spot_blocked», so the marker has
               to be visible through whatever is standing on it. -->
          <span
            v-for="(spot, i) in floor.spots"
            :key="`p${i}`"
            class="plan-spot"
            data-testid="fintech-admin-spot"
            :data-what="spot.what"
            :style="spotStyle(spot)"
          />
        </div>

        <p class="text-caption text-medium-emphasis mt-2 mb-3">
          Клетка — один метр. Мебель держится в {{ gapText }} от стен и друг от друга.
        </p>

        <!-- THE LEGEND NAMES EVERY MARK ON THE PLAN, and the kinds are named by
             the server: the day the office grows a fourth kind this list grows
             with it and nothing here is deployed. -->
        <div class="legend" data-testid="fintech-admin-legend">
          <span
            v-for="kind in floor.kinds"
            :key="kind.key"
            class="legend-row"
            data-testid="fintech-admin-legend-kind"
            :data-kind="kind.key"
          >
            <span class="legend-swatch" :data-kind="kind.key" />
            {{ kind.label }}
          </span>

          <span
            v-if="panes.length"
            class="legend-row"
            data-testid="fintech-admin-legend-window"
          >
            <span class="legend-swatch legend-swatch--window" />
            остекление · {{ panes.length }}
          </span>
          <!-- A FLOOR WITH NO GLAZING IS A REAL FLOOR, not a failure: the
               validator has nothing to say about windows and a generated layout
               may carry none. Saying so is what stops the blank wall reading as
               a plan that failed to draw something. -->
          <span v-else class="legend-row text-medium-emphasis" data-testid="fintech-admin-no-windows">
            остекления нет
          </span>

          <span
            v-for="row in spotRows"
            :key="row.what"
            class="legend-row"
            data-testid="fintech-admin-legend-spot"
            :data-what="row.what"
          >
            <span class="legend-dot" :data-what="row.what" />
            {{ row.label }} · {{ row.count }}
          </span>
        </div>

        <p class="text-caption text-medium-emphasis mt-3 mb-0 ps-wrap">
          Точки — места, которые мебель обязана обходить: старты, бутылки и кальяны.
        </p>
      </v-card>
    </template>
  </v-container>
</template>

<script setup lang="ts">
/**
 * «АДМИН ФИНТЕХА» — the office's own control room, and today only a window into it.
 *
 * IT IS THE GAME'S PAGE AND NOT THE ADMIN SECTION'S. Deleting this game has to
 * stay «remove its package, its migration, its routes and its views» (ADR-028),
 * so the floor plan lives in the game's own view, reads the game's own route
 * group, and shares nothing with `AdminView` but the gate — which is generic and
 * knows nothing about any game.
 *
 * READ-ONLY ON PURPOSE. There is no rebuild button, no palette and no dragging,
 * because there is no endpoint behind any of them yet: a disabled control for a
 * thing that does not exist is scaffolding, and this project does not ship it.
 *
 * NOTHING IS COMPUTED IN THE TEMPLATE. Every position, label and sentence comes
 * from `lib/fintechAdmin.ts`, which reuses the game's own `solidBox`,
 * `windowBand`, `toPlane` and `floorStripe` — one placement implementation, so
 * the plan and the office cannot start disagreeing about the room they both draw.
 */
import { computed, onMounted, ref } from 'vue';
import { ApiError } from '../api/client';
import { gameFintechAdminApi } from '../api/endpoints';
import {
  formatInstalled,
  formatRoom,
  planBoxFor,
  planSolidStyle,
  planSpotStyle,
  planWindowStyle,
  sourceLabel,
  spotLegend,
  type PlanStyle,
} from '../lib/fintechAdmin';
import { decimal } from '../lib/fintechPlane';
import type { FintechAdminFloor, FintechAdminSpot, FintechSolid } from '../api/types';

const floor = ref<FintechAdminFloor | null>(null);
const loading = ref(true);
const error = ref('');

/**
 * What went wrong, in words rather than in a code.
 *
 * The three the server can answer with are all worth telling apart: a refusal
 * means the role changed under an open tab, an unwired game means the deploy is
 * half-done, and anything else is worth quoting with its code. The trace id is
 * not shown — this page has no form and nothing to retry, and the global modal
 * already carries one for the failures a person can act on.
 */
function messageFor(err: unknown): string {
  if (err instanceof ApiError) {
    if (err.code === 'forbidden') return 'Нужны права администратора.';
    if (err.code === 'game_unavailable') return 'Игра сейчас не подключена.';
    return `Не открылось (${err.code}).`;
  }
  return 'Не открылось.';
}

const box = computed(() => planBoxFor(floor.value?.office.w ?? 0, floor.value?.office.h ?? 0));

const sourceText = computed(() => sourceLabel(floor.value?.source ?? ''));
const installedText = computed(() => formatInstalled(floor.value?.installed_at ?? ''));
const roomText = computed(() => formatRoom(floor.value?.office.w ?? 0, floor.value?.office.h ?? 0));
const gapText = computed(() => `${decimal(floor.value?.office.min_gap ?? 0)} м`);

/**
 * The panes that can actually be placed.
 *
 * A wall this client has never heard of, or a pane of no length, answers no
 * style at all and is dropped here rather than drawn at zero size in a corner —
 * the same judgement `windowBand` makes for the game.
 */
const panes = computed<{ wall: string; style: PlanStyle }[]>(() => {
  const o = floor.value;
  if (!o) return [];
  return o.layout.windows.flatMap((pane) => {
    const style = planWindowStyle(pane, o.office.w, o.office.h);
    return style ? [{ wall: pane.wall, style }] : [];
  });
});

const spotRows = computed(() => spotLegend(floor.value?.spots ?? []));

/**
 * What the plan is, for anybody who cannot see it.
 *
 * The plan is a diagram made of empty boxes — every mark on it is a `span` with
 * no text — so without this a screen reader would find a large nothing where the
 * room is. `role="img"` plus one sentence is the honest description: the counts
 * are the part that carries information, and the legend below already names
 * everything they are counting.
 */
const planLabel = computed(() => {
  const o = floor.value;
  if (!o) return 'План этажа';
  return `План этажа ${roomText.value}: предметов — ${o.layout.solids.length}, окон — ${o.layout.windows.length}, отмеченных мест — ${o.spots.length}`;
});

function solidStyle(solid: FintechSolid): PlanStyle {
  const o = floor.value;
  return planSolidStyle(solid, o?.office.w ?? 0, o?.office.h ?? 0);
}

function spotStyle(spot: FintechAdminSpot): PlanStyle {
  const o = floor.value;
  return planSpotStyle(spot, o?.office.w ?? 0, o?.office.h ?? 0);
}

onMounted(async () => {
  try {
    floor.value = await gameFintechAdminApi.layout();
  } catch (err) {
    // Reported on the page rather than through the global modal: the whole
    // screen failed, so there is nothing behind the modal worth returning to.
    error.value = messageFor(err);
  } finally {
    loading.value = false;
  }
});
</script>

<style scoped>
/* THE STATUS CARD is label/value pairs that wrap: at 360 px a Moscow timestamp
   and its label do not fit on one line, and a fixed two-column grid would push
   the value off the screen rather than under it. */
.status {
  display: flex;
  flex-direction: column;
  gap: 8px;
}

.status-row {
  display: flex;
  flex-wrap: wrap;
  align-items: baseline;
  gap: 4px 10px;
}

.status-label {
  color: rgba(var(--v-theme-on-surface), 0.6);
  font-size: 0.85rem;
}

.status-value {
  font-weight: 600;
}

/* THE PLAN, AND THE RATIO IS THE CLAIM IT MAKES. `--ratio` is the SERVED w / h,
   so a room of any shape draws at its own shape and a plan can never quietly
   describe distances that are not the ones the server enforces. The width is
   capped by height as well as by the column, because a portrait office sized by
   width alone would be taller than the screen on a desktop and the whole point
   of a plan is seeing all of it at once.

   BOTH GRIDS ARE ONE METRE, derived from the room (see planBoxFor): the rows are
   the game's own floorboards and the columns are the same helper given the other
   dimension. It is what makes «мебель держится в 1,5 м» something a reader can
   check by eye rather than something they have to believe. */
.plan {
  position: relative;
  width: min(100%, calc(68vh * var(--ratio, 0.727)));
  aspect-ratio: var(--ratio, 0.727);
  margin-inline: auto;
  overflow: hidden;
  border-radius: 6px;
  /* Theme-neutral throughout: the floor is the page's own ink at low opacity, so
     the plan is pale on a light theme and dark on a dark one without a second
     palette to keep in step. */
  border: 1px solid rgba(var(--v-theme-on-surface), 0.28);
  background:
    repeating-linear-gradient(
      0deg,
      transparent 0 calc(var(--row-stripe, 100%) - 1px),
      rgba(var(--v-theme-on-surface), 0.1) calc(var(--row-stripe, 100%) - 1px) var(--row-stripe, 100%)
    ),
    repeating-linear-gradient(
      90deg,
      transparent 0 calc(var(--col-stripe, 100%) - 1px),
      rgba(var(--v-theme-on-surface), 0.1) calc(var(--col-stripe, 100%) - 1px) var(--col-stripe, 100%)
    ),
    rgba(var(--v-theme-on-surface), 0.04);
}

/* EVERYTHING YOU CAN WALK INTO. The base rule is also the fallback, exactly as
   in the game: a kind is a plain string on the wire so the server can learn a
   fourth one without breaking a deployed client, and a solid this build has
   never heard of still collides — so it is drawn as a neutral block rather than
   left out of the plan an admin is about to rearrange. */
.plan-solid {
  position: absolute;
  background: var(--kind-unknown);
  border-radius: 2px;
}

/* The three kinds, in the game's own colours so the plan reads as the office
   rather than as a diagram of it — but at mid-tone, because this page lives in
   the app shell and has to hold up on a light theme as well as a dark one. */
.plan-solid[data-kind='desk'] {
  background: var(--kind-desk);
}

.plan-solid[data-kind='flower'],
.plan-solid[data-kind='tree'] {
  border-radius: 50%;
}

.plan-solid[data-kind='flower'] {
  background: var(--kind-flower);
}

.plan-solid[data-kind='tree'] {
  background: var(--kind-tree);
}

/* THE GLAZING SITS INSIDE THE ROOM'S EDGE, unlike the game's, and that is the
   whole difference between a plan and a play view: the office draws its room
   inside a taller plane because a figure hangs above the coordinate it stands
   on, while nothing stands on a plan — so the plan's box IS the room and a pane
   is a strip along the inside of its wall. `--at` and `--len` are fractions of
   the pane's own wall, straight from `windowBand`, with no wall share to
   subtract. */
.plan-window {
  position: absolute;
  background: var(--pane);
}

.plan-window[data-wall='top'] {
  top: 0;
  height: var(--pane-thickness);
  left: calc(var(--at, 0) * 100%);
  width: calc(var(--len, 0) * 100%);
}

.plan-window[data-wall='left'],
.plan-window[data-wall='right'] {
  width: var(--pane-thickness);
  top: calc(var(--at, 0) * 100%);
  height: calc(var(--len, 0) * 100%);
}

.plan-window[data-wall='left'] {
  left: 0;
}

.plan-window[data-wall='right'] {
  right: 0;
}

/* A FIXED POINT, DRAWN AT A FIXED SIZE. A spot has a position and no area — the
   validator asks whether furniture covers the point, not whether it overlaps a
   circle — so drawing it to scale would make the six bottles vanish on a phone.
   The ring is the page's own surface, so the dot stays legible over a desk and
   over the floor alike, on either theme. */
.plan-spot {
  position: absolute;
  width: 10px;
  height: 10px;
  margin: -5px 0 0 -5px;
  border-radius: 50%;
  background: var(--spot-unknown);
  box-shadow: 0 0 0 1.5px rgba(var(--v-theme-surface), 0.85);
}

.plan-spot[data-what='player'] {
  background: var(--spot-player);
}
.plan-spot[data-what='boss'] {
  background: var(--spot-boss);
}
.plan-spot[data-what='chaser'] {
  background: var(--spot-chaser);
}
.plan-spot[data-what='npc'] {
  background: var(--spot-npc);
}
.plan-spot[data-what='bottle'] {
  background: var(--spot-bottle);
}
.plan-spot[data-what='hookah'] {
  background: var(--spot-hookah);
}

/* THE LEGEND WRAPS RATHER THAN SCROLLS. Nine entries at 360 px is three or four
   rows, and a legend you have to swipe sideways is a legend nobody reads. */
.legend {
  display: flex;
  flex-wrap: wrap;
  gap: 6px 14px;
  font-size: 0.85rem;
}

.legend-row {
  display: inline-flex;
  align-items: center;
  gap: 6px;
}

/* The swatches read the SAME custom properties the plan does, keyed off the same
   attribute — so a colour lives in one place and a legend can never end up
   naming a colour the plan does not use. */
.legend-swatch {
  width: 14px;
  height: 14px;
  border-radius: 2px;
  background: var(--kind-unknown);
}

.legend-swatch[data-kind='desk'] {
  background: var(--kind-desk);
}

.legend-swatch[data-kind='flower'] {
  border-radius: 50%;
  background: var(--kind-flower);
}

.legend-swatch[data-kind='tree'] {
  border-radius: 50%;
  background: var(--kind-tree);
}

.legend-swatch--window {
  background: var(--pane);
}

.legend-dot {
  width: 10px;
  height: 10px;
  border-radius: 50%;
  background: var(--spot-unknown);
}

.legend-dot[data-what='player'] {
  background: var(--spot-player);
}
.legend-dot[data-what='boss'] {
  background: var(--spot-boss);
}
.legend-dot[data-what='chaser'] {
  background: var(--spot-chaser);
}
.legend-dot[data-what='npc'] {
  background: var(--spot-npc);
}
.legend-dot[data-what='bottle'] {
  background: var(--spot-bottle);
}
.legend-dot[data-what='hookah'] {
  background: var(--spot-hookah);
}

/* THE PALETTE, ONCE. Every colour on this page is declared here and read by both
   the plan and the legend, so the two cannot drift — and every one is a mid-tone
   chosen to survive both themes, because the plan's floor is the page's own ink
   at four per cent and that is nearly white on one theme and nearly black on the
   other. */
.plan,
.legend {
  --kind-desk: #9a7b52;
  --kind-flower: #6fae52;
  --kind-tree: #3f8a45;
  --kind-unknown: #8a8f98;
  --pane: #5aa9dd;
  /* How thick a wall is drawn. A plan has no wall band outside the room, so this
     is a mark ON the room's edge and a fixed length is right: it says «there is
     glazing here», not «the wall is this deep». */
  --pane-thickness: 5px;
  /* NOT ONE OF THEM IS GREEN OR BROWN, and that is a rule rather than taste: the
     furniture above owns those two, and a spawn marker the colour of a ficus is a
     marker somebody reads as a plant. The six are spread across the rest of the
     wheel, and the legend carries the name and the count for the pairs that are
     nearest each other. */
  --spot-player: #f2c53d;
  --spot-boss: #e0503f;
  --spot-chaser: #b06fe0;
  --spot-npc: #ef7d2e;
  --spot-bottle: #e0559f;
  --spot-hookah: #3fc9e0;
  --spot-unknown: #8a8f98;
}
</style>
