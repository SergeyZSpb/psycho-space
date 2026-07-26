import type {
  VanyagotchiAction,
  VanyagotchiConfig,
  VanyagotchiStat,
  VanyagotchiStatDelta,
} from '../api/types';

// The rules cheatsheet «Ванягоччи» shows before you go into the yard.
//
// WHY THIS IS DERIVED RATHER THAN WRITTEN OUT. Nearly everything a player needs
// to know is already on the wire: GET /api/game-vanyagotchi/config carries every
// stat with its start, its signed rate, its warning line, whether it can kill
// him and what makes it fall faster, and every action with the deltas it applies
// and whether it revives a corpse. A cheatsheet with those numbers typed into it
// would be a second copy of the catalogue, and it would be wrong the first
// afternoon somebody retuned a constant — silently wrong, because nothing
// compares the two. So the copy is BUILT from the config, and
// internal/gamevanyagotchi/content.go stays the single place a rule lives:
// moving a number there is still a backend deploy with no client change, and now
// the screen that teaches the rule moves with it.
//
// The one part that cannot be derived is at the bottom of this file, YARD_PROSE.
// Read its comment before adding anything to it.
//
// Everything here is pure and has no side effects, which is the point: the
// template renders rows and never builds a sentence, so the interesting half of
// the splash is unit-testable without a browser (see __tests__/vanyagotchiRules).

/**
 * The typographic minus, so «−6» reads as a negative number rather than as a
 * hyphen standing in for one. Paired with a plain `+`, which has no such twin.
 */
const MINUS = '−';

/** One stat, as the cheatsheet lists it. */
export interface RuleStat {
  key: string;
  emoji: string;
  label: string;
  /** Where it starts and which way it drifts: «старт 65, −1 в час». */
  drift: string;
  /**
   * The rest of what is true about it: the extra drain its drivers inflict, the
   * value at which its bar turns amber, and whether reaching the floor kills
   * him. Ordered worst-first, because the penalties are the causal story.
   */
  notes: string[];
}

/** One action, as the cheatsheet lists it. */
export interface RuleAction {
  key: string;
  emoji: string;
  label: string;
  /** What one press moves: «пиво +40 · здоровье +15 · мочевой пузырь +25». */
  effects: string;
  /** Whether it works on a dead Ваня — the one thing an action row must warn about. */
  notes: string[];
}

/** The whole cheatsheet, in the catalogue's own order — which is display order. */
export interface VanyagotchiRules {
  stats: RuleStat[];
  actions: RuleAction[];
}

/**
 * Turns the served catalogue into the rows the splash renders.
 *
 * Tolerant of everything, because the config fetch is deliberately allowed to
 * fail: the yard runs on the socket, so a catalogue that never arrives costs the
 * cheatsheet's derived half and nothing else. A missing config, an empty one, a
 * `{}` from a server that has not been redeployed, a stat with no penalties, an
 * action with no effects — every one of them yields empty or partial rows rather
 * than a throw, and the screen falls back to YARD_PROSE alone.
 */
export function buildRules(config: VanyagotchiConfig | null | undefined): VanyagotchiRules {
  const stats = named(config?.stats);
  const actions = named(config?.actions);
  // Effects and penalties both refer to a stat by key, and both want its human
  // label — «пока пиво ≤ 20» rather than «пока beer ≤ 20».
  const byKey = new Map(stats.map((stat) => [stat.key, stat]));
  return {
    stats: stats.map((stat) => statRow(stat, byKey)),
    actions: actions.map((action) => actionRow(action, byKey)),
  };
}

/** The entries of a possibly-absent catalogue list that carry a usable key. */
function named<T extends { key?: unknown }>(list: T[] | undefined): T[] {
  if (!Array.isArray(list)) return [];
  return list.filter((entry): entry is T => !!entry && typeof entry.key === 'string' && !!entry.key);
}

function statRow(def: VanyagotchiStat, byKey: Map<string, VanyagotchiStat>): RuleStat {
  const notes: string[] = [];
  for (const penalty of Array.isArray(def.penalties) ? def.penalties : []) {
    if (!penalty || !Number.isFinite(penalty.rate_per_hour) || penalty.rate_per_hour === 0) continue;
    const driver = byKey.get(penalty.when_key)?.label || penalty.when_key;
    // `rate_per_hour` is ADDED TO THE DRAIN, so what the player watches the bar
    // do is its negation — the same sign flip `drift` makes below, and for the
    // same reason.
    notes.push(
      `ещё ${signed(-penalty.rate_per_hour)} в час, пока ${driver} ` +
        `${penalty.above ? '≥' : '≤'} ${amount(penalty.threshold)}`,
    );
  }
  if (Number.isFinite(def.warn_at)) {
    // Which side is the bad side is catalogue data (`good_high`), exactly as it
    // is for the bar's colour — this line and that colour must not be able to
    // disagree about which end of the scale you are supposed to fear.
    notes.push(`бар желтеет ${def.good_high ? 'ниже' : 'выше'} ${amount(def.warn_at)}`);
  }
  if (def.fatal) notes.push(`дойдёт до ${amount(def.min)} — всё, помер`);
  return {
    key: def.key,
    emoji: def.emoji || '',
    label: def.label || def.key,
    drift: drift(def),
    notes,
  };
}

/** «старт 65, −1 в час» — the two things true of a stat nobody is touching. */
function drift(def: VanyagotchiStat): string {
  const parts: string[] = [];
  if (Number.isFinite(def.start)) parts.push(`старт ${amount(def.start)}`);
  // THE SIGN FLIPS HERE, and it is the one piece of arithmetic in this file that
  // can be wrong in a way that reads perfectly well. The catalogue's rate is a
  // DRAIN: positive falls towards `min`, and the bladder's is negative because
  // it fills. What the player sees the number do is therefore the negation.
  // Printed straight through, this line would tell them the bladder empties
  // itself and health climbs.
  const change = Number.isFinite(def.decay_per_hour) ? -def.decay_per_hour : 0;
  parts.push(change === 0 ? 'сам не меняется' : `${signed(change)} в час`);
  return parts.join(', ');
}

function actionRow(def: VanyagotchiAction, byKey: Map<string, VanyagotchiStat>): RuleAction {
  const effects = (Array.isArray(def.effects) ? def.effects : [])
    .filter(
      (effect): effect is VanyagotchiStatDelta =>
        !!effect && typeof effect.stat_key === 'string' && Number.isFinite(effect.delta),
    )
    .map((effect) => effectText(effect, byKey.get(effect.stat_key)));
  return {
    key: def.key,
    emoji: def.emoji || '',
    label: def.label || def.key,
    effects: effects.join(' · '),
    // `revives_fatal` says both things at once: the actions that carry it are
    // the way back from a death, and the ones that do not are refused with a 409
    // while he is dead. Which is why every action row says one or the other
    // rather than only the cheerful half.
    notes: [def.revives_fatal ? 'поднимает мёртвого' : 'мёртвому нельзя'],
  };
}

function effectText(effect: VanyagotchiStatDelta, def: VanyagotchiStat | undefined): string {
  const label = def?.label || effect.stat_key;
  // A delta at least as big as the whole scale is the catalogue's idiom for
  // "reset": relieving himself sends the bladder down by 100 against a 0..100
  // scale, and the clamp lands it exactly on the floor. Printing «−100» against
  // a bar that only goes to 100 is arithmetic the player has to do in their
  // head, so it is spelled out as the bound it actually lands on.
  if (
    def &&
    Number.isFinite(def.min) &&
    Number.isFinite(def.max) &&
    def.max > def.min &&
    Math.abs(effect.delta) >= def.max - def.min
  ) {
    return `${label} → ${amount(effect.delta < 0 ? def.min : def.max)}`;
  }
  return `${label} ${signed(effect.delta)}`;
}

/** A catalogue number as a player should read it: no trailing «.0», a real minus. */
function amount(value: number): string {
  if (!Number.isFinite(value)) return '?';
  const rounded = Math.round(value * 10) / 10;
  // `rounded === 0` also catches −0, which would otherwise print as «−0».
  return rounded === 0 ? '0' : String(rounded).replace('-', MINUS);
}

/** The same, with the sign always shown — how a delta and a rate are written. */
function signed(value: number): string {
  if (!Number.isFinite(value)) return '?';
  const rounded = Math.round(value * 10) / 10;
  if (rounded === 0) return '0';
  return `${rounded < 0 ? MINUS : '+'}${amount(Math.abs(rounded))}`;
}

/**
 * THE HARDCODED HALF OF THE CHEATSHEET — and the only hardcoded copy in it.
 *
 * ==> A RULES CHANGE THAT TOUCHES ANY OF THE BEHAVIOUR BELOW MUST COME BACK AND
 * ==> EDIT THIS ARRAY BY HAND. Nothing will fail to tell you it went stale.
 *
 * Everything above this constant is derived from the served catalogue, so
 * retuning a stat or an action in internal/gamevanyagotchi/content.go changes
 * what the player is told with no edit here at all. These four lines cannot work
 * that way, because none of what they describe is on the wire:
 *
 *   1. that decay keeps running while the tab is shut — a property of the model
 *      (value + as_of, evaluated on read) rather than a number in the catalogue;
 *   2. the walking speed and the chance of giving up part way — `walkSpeed`,
 *      `tiredFrom`, `tiredChance` and `tiredSays` in content.go, all deliberately
 *      server-side so no second implementation of the motion can appear here;
 *   3. the idle muttering — `idleChance` / `idlePeriod` / `idleSays`, same;
 *   4. who else is in the yard — the sleepers (`sleeperLimit`) and the NPC
 *      regulars, both of which the client draws without knowing what they are.
 *
 * If one of those numbers or behaviours moves, this text is what goes wrong. Keep
 * it honest, keep it short, and resist adding anything here that the config
 * already carries — a line that could have been derived is a line that will
 * eventually contradict the bars two screens later.
 */
export const YARD_PROSE: readonly string[] = [
  'Время идёт без тебя: пока вкладка закрыта, всё продолжает капать. Вернулся утром — вернулся к последствиям.',
  'Тапни по двору — Ваня пойдёт туда пешком, примерно пятая часть двора в секунду. С дальнего тапа может сесть на полпути и сообщить, что нога отваливается. Короткий шаг доходит всегда, а новый тап всегда отменяет старый, так что застрять нельзя.',
  'Стоит без дела — бормочет себе под нос.',
  'Остальные во дворе — живые люди. Кто ушёл, тот лежит спит там, где стоял. А пара местных вообще ничьи.',
];
