<template>
  <v-container class="py-6" style="max-width: 900px" data-testid="fintech-admin">
    <h1 class="text-h5 mb-1">АДМИН ФИНТЕХА</h1>
    <p class="text-body-2 text-medium-emphasis mb-4 ps-wrap">
      Этаж, на котором сейчас работают. Отсюда его можно переставить — по одному
      предмету или целиком.
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
      <!-- WHAT THE FLOOR IN FORCE IS. Everything here is a fact about the layout
           that is not in its geometry, which is exactly why the plan alone is not
           enough: two floors can look identical and have arrived by completely
           different routes. It describes the INSTALLED floor and never the
           draft — «поставлен» is a fact about the office people are standing in,
           and a status card that moved with a drag would be describing something
           that does not exist yet. -->
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
                 on this card at all: it is what turns «переставить офис» from a
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
        <!-- THE PLAN, AND IT IS NOW THE DRAFT. The room from directly above with
             nothing standing in it: no figures, no HUD, no depth. Real DOM rather
             than a canvas, like everything else in this project that has to be
             asserted on — and here it earns that twice over, because every square
             on it is something a thumb can pick up and a test can find.
             `tabindex` is what makes the whole editor reachable without a
             pointer: the selection cycle, the arrows and the bin all hang off
             this element having focus. -->
        <div
          ref="planEl"
          class="plan"
          data-testid="fintech-admin-plan"
          role="group"
          tabindex="0"
          :aria-label="planLabel"
          :style="{
            '--ratio': String(box.ratio),
            '--row-stripe': box.rowStripe,
            '--col-stripe': box.columnStripe,
          }"
          @pointerdown="onPlanDown"
          @pointermove="onPlanMove"
          @pointerup="onPlanUp"
          @pointercancel="onPlanUp"
          @keydown="onPlanKey"
        >
          <span
            v-for="(solid, i) in draft.solids"
            :key="`s${i}`"
            class="plan-solid"
            data-testid="fintech-admin-solid"
            :data-kind="solid.kind"
            :data-index="i"
            :data-selected="isSelected('solid', i) ? '1' : null"
            :data-problem="hasProblem('solid', i) ? '1' : null"
            :style="solidStyle(solid)"
          />
          <!-- The glazing. Decorative in the game and decorative here, but drawn
               because an admin moving a desk is looking at a room, and a room
               with no windows in the plan is a different room. -->
          <span
            v-for="pane in panes"
            :key="`w${pane.index}`"
            class="plan-window"
            data-testid="fintech-admin-window"
            :data-wall="pane.wall"
            :data-index="pane.index"
            :data-selected="isSelected('window', pane.index) ? '1' : null"
            :data-problem="hasProblem('window', pane.index) ? '1' : null"
            :style="pane.style"
          />
          <!-- LAST BUT ONE, SO THEY ARE OVER THE FURNITURE. A spot with furniture
               on it is the exact state the validator refuses with «spot_blocked»,
               so the marker has to be visible through whatever is standing on it. -->
          <span
            v-for="(spot, i) in floor.spots"
            :key="`p${i}`"
            class="plan-spot"
            data-testid="fintech-admin-spot"
            :data-what="spot.what"
            :style="spotStyle(spot)"
          />
          <!-- AND THE HANDLE ON TOP OF EVERYTHING, because it is the one thing
               here you grab rather than look at. It is a full tap target and it
               is deliberately NOT CLIPPED by the plan: a solid may stand right at
               the edge of the room, and a handle clipped to nothing is a control
               a thumb cannot find and a test cannot measure. -->
          <span
            v-if="handleStyle"
            class="plan-handle"
            data-testid="fintech-admin-handle"
            :style="handleStyle"
            @pointerdown.stop="onHandleDown"
          />
        </div>

        <p class="text-caption text-medium-emphasis mt-2 mb-1">
          Клетка — один метр. Мебель держится в {{ gapText }} от стен и друг от друга.
        </p>
        <p class="text-caption text-medium-emphasis mb-3 ps-wrap" data-testid="fintech-admin-hint">
          Ткните в предмет, чтобы выбрать; тяните, чтобы двигать; за угол — чтобы
          менять размер. С клавиатуры: Tab — следующий, стрелки — на {{ gridText }},
          Delete — убрать, Esc — снять выделение.
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
               validator has nothing to say about a wall with no glass on it, and
               a generated layout may carry none. Saying so is what stops the
               blank wall reading as a plan that failed to draw something. -->
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

      <!-- THE CONSTRUCTOR. Everything that changes the draft lives in one card,
           under the plan it changes — the same reasoning that puts the rebuild
           button below the floor rather than over it. -->
      <v-card class="pa-4 mt-4" data-testid="fintech-admin-editor">
        <h2 class="text-subtitle-1 mb-1">Что стоит на этаже</h2>

        <!-- THE READOUT IS LOAD-BEARING, not decoration. A drag on a plan cannot
             be checked without comparing pixels, which this project does not do,
             so this line is what makes «стол переехал на два с половиной метра»
             a claim anybody — a person or a test — can read. It is also how you
             actually edit on a phone, where a quarter of a metre is five pixels. -->
        <p class="readout mb-2" data-testid="fintech-admin-readout">{{ readout }}</p>

        <div class="steppers mb-3">
          <span
            v-for="field in stepFields"
            :key="field.key"
            class="stepper"
          >
            <span class="stepper-label">{{ field.label }}</span>
            <v-btn
              variant="tonal"
              :style="TAP"
              :disabled="!selection"
              :data-testid="`fintech-admin-step-${field.key}-down`"
              :aria-label="`${field.label} меньше`"
              @click="step(field.key, -1)"
            >
              −
            </v-btn>
            <v-btn
              variant="tonal"
              :style="TAP"
              :disabled="!selection"
              :data-testid="`fintech-admin-step-${field.key}-up`"
              :aria-label="`${field.label} больше`"
              @click="step(field.key, 1)"
            >
              +
            </v-btn>
          </span>

          <v-btn
            color="error"
            variant="tonal"
            :style="TAP"
            :disabled="!selection"
            data-testid="fintech-admin-delete"
            @click="removeSelected"
          >
            УБРАТЬ
          </v-btn>
        </div>

        <!-- THE PALETTE NAMES THE KINDS THE SERVER NAMES. A fourth kind of thing
             on the floor is a backend change and no deploy here — the same
             property the legend above already has, for the same reason. -->
        <div class="palette mb-3" data-testid="fintech-admin-palette">
          <v-btn
            v-for="kind in floor.kinds"
            :key="kind.key"
            variant="tonal"
            :style="TAP"
            :disabled="draft.solids.length >= MAX_SOLIDS"
            :data-testid="`fintech-admin-add-${kind.key}`"
            @click="addSolid(kind.key)"
          >
            + {{ kind.label }}
          </v-btn>
          <!-- THE WALLS ARE BUTTONS AND NOT A DRAG, deliberately: a pane is five
               pixels of wall, which is a third of the smallest thing a thumb can
               hit, so there is no honest way to grab one. It is placed here,
               selected by Tab, and moved by the steppers. -->
          <v-btn
            v-for="wall in WALLS"
            :key="wall"
            variant="tonal"
            :style="TAP"
            :disabled="draft.windows.length >= MAX_WINDOWS"
            :data-testid="`fintech-admin-add-window-${wall}`"
            @click="addWindow(wall)"
          >
            + окно {{ wallLabel(wall) }}
          </v-btn>
        </div>

        <!-- WHAT THE SERVER THINKS OF THE DRAFT. Never this browser's opinion:
             everything past «стоит в комнате и лежит на сетке» is asked over the
             wire, so there is no second implementation of «playable» here to
             drift out of step with the one the game is actually run against. -->
        <div v-if="dirty" class="check" data-testid="fintech-admin-check" :data-state="checkStatus">
          <p v-if="checkStatus === 'pending'" class="text-body-2 text-medium-emphasis mb-0">
            Проверяем этаж…
          </p>
          <p v-else-if="checkStatus === 'failed'" class="text-body-2 text-medium-emphasis mb-0">
            Проверить не вышло. Поправьте что-нибудь ещё раз — спросим заново.
          </p>
          <p v-else-if="checkStatus === 'ok'" class="text-body-2 mb-0" data-testid="fintech-admin-check-ok">
            Этаж проходит проверку — можно ставить.
          </p>
          <template v-else>
            <p class="text-body-2 mb-1">Так поставить нельзя:</p>
            <ul class="problems">
              <li
                v-for="(problem, i) in problems ?? []"
                :key="`${problem.problem}-${problem.index}-${i}`"
                class="ps-wrap"
                data-testid="fintech-admin-problem"
                :data-problem="problem.problem"
              >
                {{ problemLine(problem, draft, floor.kinds) }}
              </li>
            </ul>
          </template>
        </div>

        <div class="actions mt-3">
          <v-btn
            variant="tonal"
            :style="TAP"
            data-testid="fintech-admin-random"
            :loading="drawing"
            :disabled="drawing || saving"
            @click="drawProposal"
          >
            СЛУЧАЙНЫЙ
          </v-btn>
          <v-btn
            variant="text"
            :style="TAP"
            data-testid="fintech-admin-revert"
            :disabled="!dirty || saving"
            @click="revert"
          >
            ОТМЕНИТЬ
          </v-btn>
          <!-- SAVING IS DESTRUCTIVE, so it is gated twice: on the server having
               judged THIS draft and found nothing wrong, and then on somebody
               confirming it with the live occupant count in front of them.
               Gating on the problem list alone would leave it live through the
               debounce and the request — exactly the window in which nothing has
               been asked yet. -->
          <v-btn
            color="error"
            variant="tonal"
            :style="TAP"
            data-testid="fintech-admin-apply"
            :loading="saving"
            :disabled="!canSave || saving"
            @click="saveConfirmOpen = true"
          >
            ПРИМЕНИТЬ
          </v-btn>
        </div>

        <p
          v-if="saveError"
          class="text-body-2 text-error mt-3 mb-0 ps-wrap"
          data-testid="fintech-admin-save-error"
        >
          {{ saveError }}
        </p>
        <p
          v-else-if="saveResult"
          class="text-body-2 mt-3 mb-0 ps-wrap"
          data-testid="fintech-admin-save-result"
        >
          {{ saveResult }}
        </p>
      </v-card>

      <!-- THE OTHER DESTRUCTIVE THING THIS PAGE CAN DO, and it is last on
           purpose: the constructor above is the considered way to change the
           floor, and this is the one that throws it away. -->
      <v-card class="pa-4 mt-4" data-testid="fintech-admin-rebuild">
        <h2 class="text-subtitle-1 mb-1">Пересобрать офис</h2>
        <p class="text-body-2 text-medium-emphasis mb-3 ps-wrap">
          Игра нарисует новый этаж и поставит его вместо этого. Все, кто сейчас
          работает, выйдут с концовкой «РЕМОНТ» — смена и зарплата им засчитаются.
        </p>
        <!-- The 48 px floor is inline for the same reason the dialog's is: this
             is a destructive control and it deserves the bigger target, and
             Vuetify's own borders shave a pixel or two off a box asked for at
             exactly 44. -->
        <v-btn
          color="error"
          variant="tonal"
          size="large"
          :style="TAP"
          data-testid="fintech-admin-reroll"
          :loading="rebuilding"
          :disabled="rebuilding"
          @click="confirmOpen = true"
        >
          ПЕРЕСОБРАТЬ ОФИС
        </v-btn>

        <!-- WHAT HAPPENED, and the failure wins the slot: a refusal is the one
             of the two that needs acting on, and a stale success line under it
             would read as though the rebuild had half worked. -->
        <p
          v-if="rebuildError"
          class="text-body-2 text-error mt-3 mb-0 ps-wrap"
          data-testid="fintech-admin-rebuild-error"
        >
          {{ rebuildError }}
        </p>
        <p
          v-else-if="rebuildResult"
          class="text-body-2 mt-3 mb-0 ps-wrap"
          data-testid="fintech-admin-rebuild-result"
        >
          {{ rebuildResult }}
        </p>
      </v-card>

      <!-- THE CONFIRMATION NAMES THE COST IN PEOPLE. «Пересобрать офис?» on its
           own is a question about geometry; the same question carrying the live
           occupant count is a question about somebody's shift, which is the
           decision actually being taken. -->
      <v-dialog v-model="confirmOpen" max-width="460">
        <v-card data-testid="fintech-admin-reroll-dialog">
          <v-card-title>Пересобрать офис?</v-card-title>
          <v-card-text class="ps-wrap">
            <p class="mb-2" data-testid="fintech-admin-reroll-warning">{{ occupantsText }}</p>
            <p class="mb-0 text-caption">
              Новый этаж рисуется случайно, и вернуть этот будет нечем — обратной кнопки нет.
            </p>
          </v-card-text>
          <!-- Both buttons carry an inline min-height: Vuetify's default in a
               dialog is 41 px, under the 44 px floor the layout suite enforces,
               and a scoped class never reaches a dialog because its content is
               teleported to the body. The way out gets it too — on a
               one-way action the easy target should be «Отмена». -->
          <v-card-actions>
            <v-spacer />
            <v-btn
              variant="text"
              :style="TAP"
              data-testid="fintech-admin-reroll-cancel"
              @click="confirmOpen = false"
            >
              Отмена
            </v-btn>
            <v-btn
              color="error"
              variant="tonal"
              :style="TAP"
              data-testid="fintech-admin-reroll-confirm"
              :loading="rebuilding"
              @click="rebuild"
            >
              Пересобрать
            </v-btn>
          </v-card-actions>
        </v-card>
      </v-dialog>

      <!-- AND THE SAME QUESTION FOR THE HAND-DRAWN FLOOR, because installing one
           costs exactly what rebuilding costs: every shift in progress ends with
           «РЕМОНТ». A confirmation that named the cost for the random button and
           not for this one would be saying the careful path is the cheap one. -->
      <v-dialog v-model="saveConfirmOpen" max-width="460">
        <v-card data-testid="fintech-admin-apply-dialog">
          <v-card-title>Поставить этот этаж?</v-card-title>
          <v-card-text class="ps-wrap">
            <p class="mb-2" data-testid="fintech-admin-apply-warning">{{ occupantsText }}</p>
            <p class="mb-0 text-caption">
              Нынешний этаж заменится тем, что на плане. Вернуть его будет нечем.
            </p>
          </v-card-text>
          <v-card-actions>
            <v-spacer />
            <v-btn
              variant="text"
              :style="TAP"
              data-testid="fintech-admin-apply-cancel"
              @click="saveConfirmOpen = false"
            >
              Отмена
            </v-btn>
            <v-btn
              color="error"
              variant="tonal"
              :style="TAP"
              data-testid="fintech-admin-apply-confirm"
              :loading="saving"
              @click="save"
            >
              Поставить
            </v-btn>
          </v-card-actions>
        </v-card>
      </v-dialog>
    </template>
  </v-container>
</template>

<script setup lang="ts">
/**
 * «АДМИН ФИНТЕХА» — the office's own control room, and now its field constructor.
 *
 * IT IS THE GAME'S PAGE AND NOT THE ADMIN SECTION'S. Deleting this game has to
 * stay «remove its package, its migration, its routes and its views» (ADR-028),
 * so the floor plan lives in the game's own view, reads the game's own route
 * group, and shares nothing with `AdminView` but the gate — which is generic and
 * knows nothing about any game.
 *
 * THE PLAN IS THE DRAFT. There is one plan on this page, it starts as the floor
 * in force, and everything — a drag, a stepper, a palette press, «СЛУЧАЙНЫЙ» —
 * changes that one object. There is no read-only mode and no edit mode to be in
 * the wrong one of: the floor people are standing on is described by the status
 * card, the draft is described by the plan, and «ОТМЕНИТЬ» is what puts the
 * second back to the first.
 *
 * THIS BROWSER DECIDES TWO THINGS AND NO MORE: that a rectangle stays inside the
 * room, and that it lands on the quarter-metre lattice. Both are control
 * affordances — a control that lets you drag a desk into the car park is broken
 * — and everything past them is asked over the wire. There is deliberately no
 * copy of the separation rule, the spot rule or the connectivity flood fill in
 * this repository's client half: two implementations of «is this office
 * playable» drift the moment one is retuned, and the one that would be wrong is
 * the one the game is not actually run against.
 *
 * THE CHECK FIRES FROM THE DRAFT AND NOT FROM A GESTURE. It is a deep watch,
 * debounced, because the things that change a floor are not all drags: the
 * palette and the wall buttons never touch the plan, and DELETING renumbers the
 * array that the last answer's indexes point into — so a check hung off
 * `pointerup` would leave stale marks pointing at the wrong desk. Any change at
 * all clears the problems to «unknown», which disables saving exactly as a
 * non-empty list does.
 *
 * SAVING IS DESTRUCTIVE and is gated on `checkedRevision === draftRevision`
 * rather than on the list being empty. The difference is the window between a
 * change and its answer — the debounce plus the request — during which nothing
 * has been asked yet and an empty list is merely the last question's answer to a
 * different floor.
 *
 * NOTHING IS COMPUTED IN THE TEMPLATE. Every position, label and sentence comes
 * from `lib/fintechAdmin.ts` and `lib/fintechEditor.ts`, which reuse the game's
 * own `solidBox`, `windowBand`, `toPlane` and `floorStripe` — one placement
 * implementation, so the plan and the office cannot start disagreeing about the
 * room they both draw.
 */
import { computed, onBeforeUnmount, onMounted, ref, watch } from 'vue';
import { ApiError } from '../api/client';
import { gameFintechAdminApi } from '../api/endpoints';
import {
  formatInstalled,
  formatRoom,
  occupantsWarning,
  planBoxFor,
  planSolidStyle,
  planSpotStyle,
  planWindowStyle,
  rerollReport,
  sourceLabel,
  spotLegend,
  type PlanStyle,
} from '../lib/fintechAdmin';
import {
  GRID,
  MAX_SOLIDS,
  MAX_WINDOWS,
  NEW_WINDOW_LEN,
  PICK_TOLERANCE_PX,
  clampWindow,
  cycleSelection,
  draftFrom,
  freeSpot,
  freeWindowAt,
  installReport,
  metres,
  moveSolidTo,
  newSize,
  pickAt,
  pointToMetres,
  problemLine,
  problemTarget,
  problemsFrom,
  resizeSolidTo,
  sameLayout,
  selectionReadout,
  stepSolid,
  stepWindow,
  toleranceMetres,
  wallLabel,
  type PlanBox,
  type Room,
  type Selection,
  type SolidField,
  type WindowField,
} from '../lib/fintechEditor';
import { decimal } from '../lib/fintechPlane';
import type {
  FintechAdminFloor,
  FintechAdminSpot,
  FintechLayoutDraft,
  FintechLayoutProblem,
  FintechSolid,
} from '../api/types';

const floor = ref<FintechAdminFloor | null>(null);
const loading = ref(true);
const error = ref('');

const confirmOpen = ref(false);
const rebuilding = ref(false);
const rebuildResult = ref('');
const rebuildError = ref('');

/**
 * The 48 px floor, inline, everywhere on this page.
 *
 * A CONSTANT RATHER THAN A CLASS because half of these buttons live in a dialog,
 * whose content is teleported to the body where no scoped class can reach it —
 * and having two mechanisms for one rule is how one of them ends up forgotten.
 * 48 rather than the 44 the layout suite enforces: Vuetify's own borders shave a
 * pixel or two off a box asked for at exactly the floor.
 */
const TAP = { minHeight: '48px', minWidth: '48px' } as const;

/** The walls glazing may go on, in the order the palette offers them. */
const WALLS = ['top', 'left', 'right'] as const;

/** How long to wait after the last change before asking the server. */
const CHECK_DEBOUNCE_MS = 400;

// --- the draft --------------------------------------------------------------

const draft = ref<FintechLayoutDraft>({ solids: [], windows: [] });
const selection = ref<Selection | null>(null);
const planEl = ref<HTMLElement | null>(null);

/**
 * Which draft the last answer was about, and which draft is on the screen.
 *
 * TWO NUMBERS RATHER THAN A BOOLEAN, because «has it been checked» is not a
 * state — it is a comparison. A flag would be set by an answer that was already
 * stale when it arrived, which is precisely the case a debounced check produces
 * every time somebody drags twice quickly.
 */
const draftRevision = ref(0);
const checkedRevision = ref(-1);
const problems = ref<FintechLayoutProblem[] | null>(null);
const checkFailed = ref(false);
let checkTimer: ReturnType<typeof setTimeout> | null = null;

const drawing = ref(false);
const saving = ref(false);
const saveConfirmOpen = ref(false);
const saveResult = ref('');
const saveError = ref('');

const room = computed<Room>(() => ({ w: floor.value?.office.w ?? 0, h: floor.value?.office.h ?? 0 }));

/** Whether the draft differs from the floor people are actually standing on. */
const dirty = computed(() => {
  const f = floor.value;
  if (!f) return false;
  return !sameLayout(draft.value, { solids: f.layout.solids, windows: f.layout.windows });
});

/**
 * Where the check stands, as one word the template can switch on.
 *
 * DERIVED RATHER THAN STORED, so it cannot fall out of step with the revisions
 * it is about. `clean` is its own state and costs no request: a draft identical
 * to the installed floor needs no judgement, because the server has already
 * judged that floor by installing it.
 */
const checkStatus = computed<'clean' | 'pending' | 'failed' | 'ok' | 'bad'>(() => {
  if (!dirty.value) return 'clean';
  if (checkedRevision.value !== draftRevision.value) return checkFailed.value ? 'failed' : 'pending';
  return (problems.value?.length ?? 0) === 0 ? 'ok' : 'bad';
});

const canSave = computed(() => dirty.value && checkStatus.value === 'ok');

/**
 * Ask the server what is wrong, after every change, once things have settled.
 *
 * A DEEP WATCH AND NOT A GESTURE HANDLER — see the file header. It clears the
 * answer FIRST, so there is no instant in which a mark points at an object that
 * has been renumbered by a deletion.
 */
watch(
  draft,
  () => {
    draftRevision.value += 1;
    problems.value = null;
    checkFailed.value = false;
    if (checkTimer !== null) clearTimeout(checkTimer);
    checkTimer = null;
    if (!dirty.value) return;
    checkTimer = setTimeout(runCheck, CHECK_DEBOUNCE_MS);
  },
  { deep: true },
);

onBeforeUnmount(() => {
  if (checkTimer !== null) clearTimeout(checkTimer);
});

async function runCheck(): Promise<void> {
  checkTimer = null;
  const revision = draftRevision.value;
  try {
    const res = await gameFintechAdminApi.check(draftFrom(draft.value));
    // SUPERSEDED ANSWERS ARE DROPPED, not applied late: the floor they describe
    // is not the floor on the screen, and its indexes address a different array.
    if (revision !== draftRevision.value) return;
    // `?? []` IS THE CLEAN CASE AND NOT A GUARD. The validator answers a nil
    // slice when it has nothing to say, which is `null` on the wire — so a legal
    // floor arrives as an absent list rather than an empty one.
    problems.value = res.problems ?? [];
    checkedRevision.value = revision;
  } catch {
    if (revision !== draftRevision.value) return;
    // Unknown, which disables saving exactly as a non-empty list does. There is
    // no retry here on purpose: the next change asks again, and a failed check
    // on a floor nobody is touching any more is not worth a second request.
    checkFailed.value = true;
  }
}

// --- reading the plan -------------------------------------------------------

/** The plan's box in viewport pixels, or null before it has been laid out. */
function planBoxOf(): PlanBox | null {
  const el = planEl.value;
  if (!el) return null;
  const r = el.getBoundingClientRect();
  if (!(r.width > 0) || !(r.height > 0)) return null;
  return { left: r.left, top: r.top, width: r.width, height: r.height };
}

/** Where a pointer is, in office metres. */
function metresAt(e: PointerEvent): { x: number; y: number } | null {
  const box = planBoxOf();
  if (!box) return null;
  return pointToMetres(e.clientX, e.clientY, box, room.value);
}

// --- dragging ---------------------------------------------------------------

let dragPointer: number | null = null;
let dragMode: 'move' | 'resize' | null = null;
/**
 * Where inside the object the drag started, in metres.
 *
 * A GRAB OFFSET RATHER THAN AN ACCUMULATED DELTA, so the object's position is a
 * function of where the pointer IS rather than of every move event that has
 * happened. A coalesced or dropped move then costs nothing, and the result does
 * not depend on how many intermediate events the browser chose to deliver.
 */
let grab = { x: 0, y: 0 };

function beginDrag(e: PointerEvent, mode: 'move' | 'resize'): void {
  dragPointer = e.pointerId;
  dragMode = mode;
  // Capture LAST and forgivingly, exactly as the game's stick does: it is what
  // keeps a thumb that slides off the plan still dragging, but it throws for a
  // pointer the browser does not think is active — which a synthetic one is —
  // and losing the whole gesture to that would trade a real control for a nicety.
  try {
    planEl.value?.setPointerCapture?.(e.pointerId);
  } catch {
    /* not capturable; the drag still works, it just ends at the plan's edge */
  }
  e.preventDefault();
}

function onPlanDown(e: PointerEvent): void {
  if (dragPointer !== null) return;
  // WITHOUT `preventScroll` THIS MOVES THE PLAN UNDER THE FINGER. Focusing an
  // element that is only partly in view scrolls it fully into view, and doing
  // that on `pointerdown` means the room slides while the first pixel of the
  // drag is being measured against it — the desk then jumps by however far the
  // page moved. Focus is still taken, because the arrows and Tab have to work
  // straight after a tap.
  planEl.value?.focus({ preventScroll: true });
  const box = planBoxOf();
  const at = metresAt(e);
  if (!box || !at) return;
  const hit = pickAt(draft.value.solids, at, toleranceMetres(PICK_TOLERANCE_PX, box, room.value));
  if (hit < 0) {
    // Tapping the bare floor clears the selection, which is the only way a
    // pointer has of saying «none of them» — and it is what stops the steppers
    // quietly editing whatever happened to be selected an action ago.
    selection.value = null;
    return;
  }
  selection.value = { list: 'solid', index: hit };
  const s = draft.value.solids[hit];
  grab = { x: at.x - s.x, y: at.y - s.y };
  beginDrag(e, 'move');
}

/**
 * How near the corner a grab has to be, in pixels, to be a resize rather than a
 * move.
 *
 * THE HANDLE IS BIGGER THAN SOME OF THE FURNITURE, and that is the problem this
 * number solves. A thumb needs 44 px; the smallest object the validator allows
 * is 0.75 m, which is about eighteen pixels of a 360 px plan. So a handle sized
 * for a thumb covers a flowerpot completely — and a handle that always resized
 * would make small things the one kind of object nobody can move.
 *
 * So the handle's answer depends on WHERE inside it the grab landed: over the
 * body of the object it moves, at the corner or outside the object it resizes.
 * The resize region is still thumb-sized in every direction that matters — three
 * quarters of the handle lie outside a large object, and this grip is the small
 * bite it takes out of the fourth.
 */
const RESIZE_GRIP_PX = 10;

function onHandleDown(e: PointerEvent): void {
  const sel = selection.value;
  if (dragPointer !== null || !sel || sel.list !== 'solid') return;
  const box = planBoxOf();
  const at = metresAt(e);
  const s = draft.value.solids[sel.index];
  if (!box || !at || !s) return;
  const grip = toleranceMetres(RESIZE_GRIP_PX, box, room.value);
  const overTheBody =
    at.x > s.x && at.y > s.y && at.x < s.x + s.w - grip && at.y < s.y + s.h - grip;
  if (overTheBody) {
    grab = { x: at.x - s.x, y: at.y - s.y };
    beginDrag(e, 'move');
    return;
  }
  grab = { x: at.x - (s.x + s.w), y: at.y - (s.y + s.h) };
  beginDrag(e, 'resize');
}

function onPlanMove(e: PointerEvent): void {
  if (e.pointerId !== dragPointer || dragMode === null) return;
  const sel = selection.value;
  if (!sel || sel.list !== 'solid') return;
  const at = metresAt(e);
  const s = draft.value.solids[sel.index];
  if (!at || !s) return;
  draft.value.solids[sel.index] =
    dragMode === 'move'
      ? moveSolidTo(s, at.x - grab.x, at.y - grab.y, room.value)
      : resizeSolidTo(s, at.x - grab.x - s.x, at.y - grab.y - s.y, room.value);
}

function onPlanUp(e: PointerEvent): void {
  if (e.pointerId !== dragPointer) return;
  try {
    planEl.value?.releasePointerCapture?.(e.pointerId);
  } catch {
    /* it was never captured; nothing to release */
  }
  dragPointer = null;
  dragMode = null;
}

// --- the keyboard, which has to be able to do all of it ---------------------

/**
 * Tab cycles, arrows nudge, Delete removes, Escape leaves.
 *
 * TAB IS CAPTURED ONLY WHILE THE PLAN HAS FOCUS, and Escape is the way out — it
 * clears the selection and drops focus, so the rest of the page is still
 * reachable by keyboard. Capturing Tab globally would trap somebody on a plan
 * they cannot leave; not capturing it at all would leave the bin, the steppers
 * and the whole editor unreachable without a pointer, because SELECTION has no
 * other keyboard route.
 */
function onPlanKey(e: KeyboardEvent): void {
  const counts = { solids: draft.value.solids.length, windows: draft.value.windows.length };

  if (e.key === 'Tab') {
    const next = cycleSelection(selection.value, counts, e.shiftKey ? -1 : 1);
    // An empty floor has nothing to cycle, so focus leaves normally rather than
    // being swallowed by a widget with nothing in it.
    if (next === null) return;
    e.preventDefault();
    selection.value = next;
    return;
  }

  if (e.key === 'Escape') {
    e.preventDefault();
    selection.value = null;
    planEl.value?.blur();
    return;
  }

  if (e.key === 'Delete' || e.key === 'Backspace') {
    if (!selection.value) return;
    e.preventDefault();
    removeSelected();
    return;
  }

  const nudges: Record<string, { dx: number; dy: number }> = {
    ArrowLeft: { dx: -1, dy: 0 },
    ArrowRight: { dx: 1, dy: 0 },
    ArrowUp: { dx: 0, dy: -1 },
    ArrowDown: { dx: 0, dy: 1 },
  };
  const by = nudges[e.key];
  const sel = selection.value;
  if (!by || !sel) return;
  e.preventDefault();
  if (sel.list === 'solid') {
    if (by.dx !== 0) step('x', by.dx);
    else step('y', by.dy);
  } else {
    // A pane has one axis, so both pairs of arrows drive it along its own wall.
    step('at', by.dx !== 0 ? by.dx : by.dy);
  }
}

// --- changing the draft -----------------------------------------------------

/** Which numbers the steppers offer, which depends on what is selected. */
const stepFields = computed<{ key: SolidField | WindowField; label: string }[]>(() => {
  if (selection.value?.list === 'window') {
    return [
      { key: 'at', label: 'от' },
      { key: 'len', label: 'дл' },
    ];
  }
  return [
    { key: 'x', label: 'X' },
    { key: 'y', label: 'Y' },
    { key: 'w', label: 'Ш' },
    { key: 'h', label: 'В' },
  ];
});

function step(field: SolidField | WindowField, by: number): void {
  const sel = selection.value;
  if (!sel) return;
  if (sel.list === 'solid') {
    const s = draft.value.solids[sel.index];
    if (!s || (field !== 'x' && field !== 'y' && field !== 'w' && field !== 'h')) return;
    draft.value.solids[sel.index] = stepSolid(s, field, by, room.value);
    return;
  }
  const p = draft.value.windows[sel.index];
  if (!p || (field !== 'at' && field !== 'len')) return;
  draft.value.windows[sel.index] = stepWindow(p, field, by, room.value);
}

function addSolid(kind: string): void {
  const f = floor.value;
  if (!f || draft.value.solids.length >= MAX_SOLIDS) return;
  const size = newSize(kind);
  const at = freeSpot(size, room.value, draft.value.solids, f.spots, f.office.min_gap);
  draft.value.solids.push({ kind, x: at.x, y: at.y, w: size.w, h: size.h });
  // Selected on arrival, so the readout names it and the steppers act on it.
  selection.value = { list: 'solid', index: draft.value.solids.length - 1 };
  handOver();
}

function addWindow(wall: string): void {
  if (draft.value.windows.length >= MAX_WINDOWS) return;
  const at = freeWindowAt(wall, NEW_WINDOW_LEN, room.value, draft.value.windows);
  draft.value.windows.push(clampWindow({ wall, at, len: NEW_WINDOW_LEN }, room.value));
  selection.value = { list: 'window', index: draft.value.windows.length - 1 };
  handOver();
}

/**
 * Puts focus on the plan, so the thing just added can be nudged straight away.
 *
 * IT IS WHAT MAKES THE PALETTE REACHABLE WITHOUT A POINTER. Adding from the
 * keyboard leaves focus on the button that was pressed, where the arrows do
 * nothing and Tab walks off down the page — so somebody would have to find their
 * way back to the plan to move the object they had just created, and there is no
 * obvious route. Handing focus over is also right for a thumb: what you do
 * immediately after adding something is position it.
 */
function handOver(): void {
  planEl.value?.focus();
}

function removeSelected(): void {
  const sel = selection.value;
  if (!sel) return;
  if (sel.list === 'solid') draft.value.solids.splice(sel.index, 1);
  else draft.value.windows.splice(sel.index, 1);
  // Cleared rather than moved to a neighbour: the array has just renumbered, so
  // any index kept here would be pointing at whatever slid into the gap.
  selection.value = null;
}

/** Puts the draft back to the floor everybody is standing on. */
function revert(): void {
  const f = floor.value;
  if (!f) return;
  draft.value = draftFrom(f.layout);
  selection.value = null;
  saveError.value = '';
}

/**
 * Fills the draft from a floor the server drew, and installs nothing.
 *
 * FREE TO PRESS, which is the whole point of the endpoint being separate from
 * the rebuild: trying three offices before keeping one must not throw three
 * rooms full of people out on the way.
 */
async function drawProposal(): Promise<void> {
  if (drawing.value) return;
  drawing.value = true;
  saveError.value = '';
  try {
    const res = await gameFintechAdminApi.proposal();
    draft.value = draftFrom(res.layout);
    selection.value = null;
    saveResult.value = '';
  } catch (err) {
    saveError.value = `Не нарисовалось (${codeOf(err)}). Этаж не тронут.`;
  } finally {
    drawing.value = false;
  }
}

/** Installs the draft, which ends every shift standing on the old floor. */
async function save(): Promise<void> {
  if (saving.value) return;
  saving.value = true;
  saveError.value = '';
  try {
    const next = await gameFintechAdminApi.save(draftFrom(draft.value));
    adopt(next);
    saveResult.value = installReport(next.ended);
  } catch (err) {
    const refused = err instanceof ApiError ? problemsFrom(err.body) : null;
    if (refused && refused.length > 0) {
      // THE SERVER HAS JUDGED EXACTLY THIS DRAFT, so the answer is current by
      // definition — which is why the revision moves with it and the panel can
      // show the list instead of «unknown».
      problems.value = refused;
      checkedRevision.value = draftRevision.value;
      checkFailed.value = false;
      saveError.value = 'Так поставить нельзя — что именно, написано выше. Этаж не тронут.';
    } else if (codeOf(err) === 'layout_invalid') {
      // REFUSED WITH NOTHING WE COULD READ. The answer stays UNKNOWN rather than
      // becoming an empty list: empty means «legal», and setting it here would
      // re-enable the very button the server has just turned down.
      checkFailed.value = true;
      saveError.value = 'Так поставить нельзя. Этаж не тронут.';
    } else {
      saveError.value = `Не поставилось (${codeOf(err)}). Этаж остался прежним.`;
    }
    saveResult.value = '';
  } finally {
    saving.value = false;
    saveConfirmOpen.value = false;
  }
}

/** The machine code a failure carried, for a message that quotes it. */
function codeOf(err: unknown): string {
  return err instanceof ApiError ? err.code : 'network';
}

/**
 * Takes a floor the server just confirmed, and starts the draft again from it.
 *
 * ONE PLACE THAT MOVES THE INSTALLED FLOOR, so the draft, the selection and the
 * problem marks cannot be left describing the floor before it.
 */
function adopt(next: FintechAdminFloor): void {
  floor.value = next;
  draft.value = draftFrom(next.layout);
  selection.value = null;
}

// --- what the page says -----------------------------------------------------

/**
 * What went wrong, in words rather than in a code.
 *
 * The three the server can answer with are all worth telling apart: a refusal
 * means the role changed under an open tab, an unwired game means the deploy is
 * half-done, and anything else is worth quoting with its code. The trace id is
 * not shown — this page has nothing to retry that a person cannot simply press
 * again, and the global modal already carries one for the failures they can act
 * on.
 */
function messageFor(err: unknown): string {
  if (err instanceof ApiError) {
    if (err.code === 'forbidden') return 'Нужны права администратора.';
    if (err.code === 'game_unavailable') return 'Игра сейчас не подключена.';
    return `Не открылось (${err.code}).`;
  }
  return 'Не открылось.';
}

/**
 * What went wrong with the REBUILD, which is a different sentence from the one
 * above even where the code is the same.
 *
 * Every branch ends by saying the floor is untouched, and that is the whole
 * reason this is not `messageFor` with a different prefix: a refused install
 * changes nothing on the server — the people working carry on, on the floor they
 * are standing on — and somebody who has just pressed a destructive button and
 * seen an error needs to be told that before anything else.
 */
function rebuildMessageFor(err: unknown): string {
  if (err instanceof ApiError) {
    if (err.code === 'forbidden') return 'Нужны права администратора. Этаж не тронут.';
    if (err.code === 'game_unavailable') return 'Игра сейчас не подключена. Этаж не тронут.';
    return `Не пересобралось (${err.code}). Этаж остался прежним.`;
  }
  return 'Не пересобралось. Этаж остался прежним.';
}

/**
 * Rebuilds the floor everybody is standing on.
 *
 * REDRAWN FROM THE ANSWER, never from a second read: the response is the read's
 * own payload plus the count, so fetching again would be a round trip spent to
 * learn what is already in hand — and a window in which this page draws the floor
 * it has just replaced.
 *
 * A FAILURE LEAVES THE PAGE EXACTLY AS IT WAS, because the server does too. The
 * dialog closes either way: an error belongs on the page, next to the button that
 * produced it, rather than under a modal somebody now has to dismiss twice.
 */
async function rebuild(): Promise<void> {
  if (rebuilding.value) return;
  rebuilding.value = true;
  rebuildError.value = '';
  try {
    const next = await gameFintechAdminApi.reroll();
    adopt(next);
    rebuildResult.value = rerollReport(next.ended);
    saveResult.value = '';
    saveError.value = '';
  } catch (err) {
    rebuildError.value = rebuildMessageFor(err);
    rebuildResult.value = '';
  } finally {
    rebuilding.value = false;
    confirmOpen.value = false;
  }
}

const occupantsText = computed(() => occupantsWarning(floor.value?.occupants ?? 0));

const box = computed(() => planBoxFor(floor.value?.office.w ?? 0, floor.value?.office.h ?? 0));

const sourceText = computed(() => sourceLabel(floor.value?.source ?? ''));
const installedText = computed(() => formatInstalled(floor.value?.installed_at ?? ''));
const roomText = computed(() => formatRoom(floor.value?.office.w ?? 0, floor.value?.office.h ?? 0));
const gapText = computed(() => `${decimal(floor.value?.office.min_gap ?? 0)} м`);
const gridText = computed(() => `${metres(GRID)} м`);

const readout = computed(() => selectionReadout(selection.value, draft.value, floor.value?.kinds ?? []));

/** Whether this object is the selected one. */
function isSelected(list: 'solid' | 'window', index: number): boolean {
  return selection.value?.list === list && selection.value.index === index;
}

/**
 * Which objects the server named, as a set of keys.
 *
 * A SET RATHER THAN A SEARCH PER BOX, because the plan redraws on every frame of
 * a drag and a linear scan per solid would be quadratic in the furniture for the
 * whole gesture.
 */
const flagged = computed(() => {
  const out = new Set<string>();
  for (const problem of problems.value ?? []) {
    const target = problemTarget(problem);
    if (target) out.add(`${target.list}:${target.index}`);
  }
  return out;
});

function hasProblem(list: 'solid' | 'window', index: number): boolean {
  return flagged.value.has(`${list}:${index}`);
}

/**
 * The panes that can actually be placed.
 *
 * A wall this client has never heard of, or a pane of no length, answers no
 * style at all and is dropped here rather than drawn at zero size in a corner —
 * the same judgement `windowBand` makes for the game. The DRAFT's index travels
 * with each one, because dropping a pane would otherwise shift every index after
 * it and the selection would mark the wrong wall.
 */
const panes = computed<{ index: number; wall: string; style: PlanStyle }[]>(() => {
  const r = room.value;
  return draft.value.windows.flatMap((pane, index) => {
    const style = planWindowStyle(pane, r.w, r.h);
    return style ? [{ index, wall: pane.wall, style }] : [];
  });
});

const spotRows = computed(() => spotLegend(floor.value?.spots ?? []));

/**
 * What the plan is, for anybody who cannot see it.
 *
 * The plan is a diagram made of empty boxes — every mark on it is a `span` with
 * no text — so without this a screen reader would find a large nothing where the
 * room is. A labelled group plus one sentence is the honest description now that
 * the thing is interactive: the counts are the part that carries information,
 * the legend below already names everything they are counting, and the readout
 * says what is selected.
 */
const planLabel = computed(() => {
  if (!floor.value) return 'План этажа';
  return `План этажа ${roomText.value}: предметов — ${draft.value.solids.length}, окон — ${draft.value.windows.length}, отмеченных мест — ${floor.value.spots.length}`;
});

function solidStyle(solid: FintechSolid): PlanStyle {
  const r = room.value;
  return planSolidStyle(solid, r.w, r.h);
}

function spotStyle(spot: FintechAdminSpot): PlanStyle {
  const r = room.value;
  return planSpotStyle(spot, r.w, r.h);
}

/** Where the resize handle sits: the selected solid's far corner, or nowhere. */
const handleStyle = computed<PlanStyle | null>(() => {
  const sel = selection.value;
  if (sel?.list !== 'solid') return null;
  const s = draft.value.solids[sel.index];
  const r = room.value;
  if (!s || !(r.w > 0) || !(r.h > 0)) return null;
  return {
    left: `${((s.x + s.w) / r.w) * 100}%`,
    top: `${((s.y + s.h) / r.h) * 100}%`,
  };
});

onMounted(async () => {
  try {
    adopt(await gameFintechAdminApi.layout());
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
   check by eye rather than something they have to believe.

   THE EDGE IS AN INSET RING RATHER THAN A BORDER, and that is arithmetic rather
   than taste. `getBoundingClientRect()` includes a border while a child's
   percentage offsets are resolved against the padding box, so a 1 px border puts
   «where the pointer is» and «where the desk is drawn» a pixel out of step — and
   a pixel is a fifth of the quarter-metre lattice at 360 px, which is enough to
   land a drag on the wrong quarter. With the ring, the plan's box IS the room,
   which is the property lib/fintechEditor.ts is written against.

   AND IT DOES NOT CLIP. The resize handle is a full tap target hanging off the
   corner of a solid that may stand at the very edge of the room; `overflow:
   hidden` would collapse it to nothing, leaving a control a thumb cannot find
   and `boundingBox()` cannot measure. Nothing else here can escape the box: a
   solid is clamped into the room before it is drawn.

   `touch-action: none` is what makes a drag a drag on a phone rather than a
   scroll. */
.plan {
  position: relative;
  width: min(100%, calc(68vh * var(--ratio, 0.727)));
  aspect-ratio: var(--ratio, 0.727);
  margin-inline: auto;
  border-radius: 6px;
  touch-action: none;
  /* Theme-neutral throughout: the floor is the page's own ink at low opacity, so
     the plan is pale on a light theme and dark on a dark one without a second
     palette to keep in step. */
  box-shadow: inset 0 0 0 1px rgba(var(--v-theme-on-surface), 0.28);
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

/* The focus ring is the only sign a keyboard user has that the plan is the thing
   listening to Tab and the arrows. */
.plan:focus-visible {
  outline: 2px solid rgb(var(--v-theme-primary));
  outline-offset: 2px;
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

/* WHAT IS SELECTED, AND WHAT IS WRONG, ARE TWO DIFFERENT MARKS. They land at the
   same time constantly — you drag a desk into another one and it is both the
   thing you are holding and the thing the server is complaining about — so they
   are told apart by kind rather than by colour alone: a solid ring for «this is
   the one you are moving», a dashed one outside it for «the server says no». */
.plan-solid[data-selected],
.plan-window[data-selected] {
  outline: 2px solid rgb(var(--v-theme-primary));
  outline-offset: 1px;
}

.plan-solid[data-problem],
.plan-window[data-problem] {
  box-shadow: 0 0 0 2px var(--flaw);
}

/* THE HANDLE IS A REAL TAP TARGET, painted small and hit large. A 12 px square
   is what the eye needs to see a corner; 48 px is what a thumb needs to catch
   it, and the difference between the two is transparent padding rather than a
   bigger drawing that would cover the object it belongs to. */
.plan-handle {
  position: absolute;
  width: 48px;
  height: 48px;
  margin: -24px 0 0 -24px;
  touch-action: none;
  cursor: nwse-resize;
  /* The painted part: a square centred in the target, drawn with a light ring so
     it reads over the furniture as well as over the floor. */
  background:
    radial-gradient(
      circle at 50% 50%,
      rgb(var(--v-theme-primary)) 0 6px,
      rgba(var(--v-theme-surface), 0.9) 6px 8px,
      transparent 8px
    );
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
   over the floor alike, on either theme.

   `pointer-events: none` because a spot is a fact about the room rather than a
   thing you can pick up: without it, a marker sitting on a desk would swallow
   the tap meant for the desk under it. */
.plan-spot {
  position: absolute;
  width: 10px;
  height: 10px;
  margin: -5px 0 0 -5px;
  border-radius: 50%;
  pointer-events: none;
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

/* THE READOUT IS MONOSPACED so a number does not change width as it changes
   value — this line updates on every quarter-metre of a drag, and a proportional
   digit makes the whole row twitch sideways while you are watching it. */
.readout {
  font-family: monospace;
  font-size: 0.95rem;
  overflow-wrap: anywhere;
}

/* THE CONTROLS WRAP RATHER THAN SCROLL, like the legend and for the same reason:
   at 360 px four steppers and a bin are two rows, and a control strip you have
   to swipe sideways is a control strip with half its buttons invisible. */
.steppers,
.palette,
.actions {
  display: flex;
  flex-wrap: wrap;
  gap: 8px;
}

.stepper {
  display: inline-flex;
  align-items: center;
  gap: 4px;
}

.stepper-label {
  min-width: 1.4em;
  color: rgba(var(--v-theme-on-surface), 0.7);
  font-size: 0.85rem;
}

.check {
  margin-top: 12px;
}

.problems {
  margin: 0;
  padding-left: 1.2em;
  font-size: 0.9rem;
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
  /* What the server is objecting to. Deliberately a hotter red than the лысый's
     marker: the two can sit on the same square, and «the boss spawns here» and
     «this is why you cannot save» must not read as the same mark. */
  --flaw: #ff5a4d;
}
</style>
