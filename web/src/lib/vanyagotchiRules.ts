import type {
  VanyagotchiAction,
  VanyagotchiConfig,
  VanyagotchiObjectKind,
  VanyagotchiStat,
  VanyagotchiStatDelta,
} from '../api/types';
// The two facts about the key hunt that the yard already works out, imported
// rather than worked out a second time here. Which verb searches a hiding place
// is a reading of the catalogue's SHAPE (see `searchVerb`), and re-deriving it
// in this file would be two implementations of one predicate — the day one of
// them learned about a new kind of verb and the other did not, the cheatsheet
// and the yard would describe different games. It is the only edge between these
// two modules and it exists for exactly that reason.
import { hotspotsFor, searchVerb } from './vanyagotchiPlane';

// The rules cheatsheet «Ванягоччи» shows before you go into the yard.
//
// WHY THIS IS DERIVED RATHER THAN WRITTEN OUT. Nearly everything a player needs
// to know is already on the wire: GET /api/game-vanyagotchi/config carries every
// stat with its start, its signed rate, its warning line, whether it is a bar or
// a lifetime tally, whether it can kill him and what makes it fall faster, and
// every action with the deltas it applies, whether it starts him over and
// whether it revives a corpse. A cheatsheet with those numbers typed into it
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

/**
 * One lifetime tally, as the cheatsheet lists it.
 *
 * Separate from `RuleStat` because it is a different KIND of thing and the
 * section it belongs under says something different about it. A counter has no
 * drift line — that is the whole of what makes it a counter — and listing it
 * under «тикает само» beside stats that genuinely tick would teach the player
 * that his beer total drains overnight, which is exactly backwards. What moves
 * one is already on the screen as an effect of the verb that moves it, so this
 * row carries no numbers at all: a name, and the one rule about it that nothing
 * else says.
 */
export interface RuleCounter {
  key: string;
  emoji: string;
  label: string;
  /** The one thing true of every tally: it only goes up, and a reset spares it. */
  note: string;
}

/** One action, as the cheatsheet lists it. */
export interface RuleAction {
  key: string;
  emoji: string;
  label: string;
  /**
   * What one press does: «пиво +40 · здоровье +15 · мочевой пузырь +25», or —
   * for an action that starts him over — the values every stat lands back on.
   */
  effects: string;
  /**
   * What else pressing it means: what it leaves standing in the yard for
   * everybody to walk past, and whether it works on a dead Ваня.
   *
   * Ordered consequence-then-condition — what the press DOES to the world, then
   * the one state the server refuses it in — because the first is why a player
   * would press it and the second is why he sometimes cannot.
   */
  notes: string[];
}

/** The whole cheatsheet, in the catalogue's own order — which is display order. */
export interface VanyagotchiRules {
  stats: RuleStat[];
  /** The lifetime tallies, split out of `stats` — see `RuleCounter`. */
  counters: RuleCounter[];
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
  // label — «пока пиво ≤ 20» rather than «пока beer ≤ 20». Built from the WHOLE
  // list, counters included, because an effect that bumps a tally still has to
  // print «выпито пива +1» rather than «beers_drunk +1».
  const byKey = new Map(stats.map((stat) => [stat.key, stat]));
  // THE SPLIT THAT MATTERS on this screen: a bar and a tally are two kinds of
  // number and they belong under two different headings. Everything downstream
  // takes one list or the other rather than filtering again, so there is exactly
  // one place that decides what a counter is.
  const bars = stats.filter((stat) => !stat.counter);
  const counters = stats.filter((stat) => !!stat.counter);
  // Whether the way back from a death is unique is a fact about the CATALOGUE
  // rather than about any one action, so it is counted once here and handed
  // down. It is the rule that changed when reviving stopped being a side effect
  // of drinking, and a player who does not know it will press the wrong button
  // at the one moment the game asks him to press a particular one.
  const revivers = actions.filter((action) => action.revives_fatal).length;
  // What a verb leaves standing in the yard, by kind key. A verb names the kind
  // and the kind carries how long one of them lasts, so both halves of «оставляет
  // кое-что на 10 минут» come off the served catalogue rather than out of this
  // file — see `leavesNote` for why the two hops are worth making.
  const objectKinds = new Map(named(config?.object_kinds).map((kind) => [kind.key, kind]));
  // Whether anything in this catalogue starts him over at all — see `counterRow`
  // for why a tally only claims to survive a reset when a reset exists.
  const resettable = actions.some((action) => action.starts_over);
  // Which verb is a SEARCH rather than a press, and where a search can be made.
  // Both are facts about the catalogue rather than about any one action, so they
  // are worked out once here and handed down — the same treatment `revivers`
  // gets one line up, and for the same reason: a row must not have to re-read
  // the whole catalogue to describe itself.
  //
  // EVERY LOCATION, NOT THE DEFAULT ONE. This used to count the hiding places of
  // `default_location` alone, with a note saying it would have to say WHICH place
  // it meant the day a second one arrived. Four have arrived, and the answer went
  // the other way: the splash is read BEFORE the pet is fetched — the verb that
  // creates it is deliberately behind the CTA (see `loadCatalogue` in
  // GameVanyagotchiView.vue) — so at the moment this is built nobody knows which
  // place дядя Ваня is standing in, and a line about the default one would be
  // three quarters wrong. What is true of the whole world is true wherever he
  // turns out to be: how many places have something to search in, what they are
  // called, and how many hiding places there are between them.
  const search = searchVerb(actions);
  const places = named(config?.locations).map((location) => ({
    key: location.key,
    label: typeof location.label === 'string' ? location.label.trim() : '',
    spots: hotspotsFor(config, location.key).length,
  }));
  return {
    stats: bars.map((stat) => statRow(stat, byKey)),
    counters: counters.map((stat) => counterRow(stat, resettable)),
    actions: actions.map((action) =>
      actionRow(
        action,
        bars,
        byKey,
        objectKinds,
        revivers,
        action.key === search?.key,
        places,
        config?.store_location,
      ),
    ),
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

/**
 * One tally row: a name and the single rule that is true of every tally.
 *
 * Deliberately carries no number. `start` is always nought and `max` is a
 * million — a bound that exists only so the clamp has something to clamp to —
 * and printing either would be printing an implementation detail as if it were a
 * rule. What actually increments the tally is already visible one section down,
 * as an effect of the verb that does it.
 */
function counterRow(def: VanyagotchiStat, resettable: boolean): RuleCounter {
  return {
    key: def.key,
    emoji: def.emoji || '',
    label: def.label || def.key,
    // The second clause is claimed only when there is something in the catalogue
    // that could plausibly have reset it. Told to a player of a game with no
    // starting-over verb it would be an answer to a question nobody asked, and
    // the interesting half of the rule — that a lifetime total survives the one
    // thing that wipes everything else — would be diluted by sitting next to it.
    note: resettable ? 'только растёт: даже начав заново, его не обнулишь' : 'только растёт',
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

function actionRow(
  def: VanyagotchiAction,
  bars: VanyagotchiStat[],
  byKey: Map<string, VanyagotchiStat>,
  objectKinds: Map<string, VanyagotchiObjectKind>,
  revivers: number,
  searches: boolean,
  places: readonly SearchPlace[],
  storeLocation: string | undefined,
): RuleAction {
  const notes: string[] = [];
  // BEFORE EVERYTHING, on the one verb it applies to, because it corrects the
  // heading this row is sitting under. «Жмёшь ты» is a promise that the rows
  // beneath it are buttons, and the searching verb is the one that is not — it
  // has no button at all, and a player who went looking for one would find
  // three and conclude that finding keys had been removed from the game.
  for (const line of searchNotes(searches, places)) notes.push(line);
  // FIRST, because it says when the button is not available at all. The client
  // greys it for this now, so the cheatsheet is no longer the only warning the
  // player gets — but it is still the only one that says WHY the control is
  // grey, and a greyed button with no explanation is a mystery rather than a
  // rule.
  const needs = needsNote(def, byKey);
  if (needs) notes.push(needs);
  // Then that it may not come off even when everything above is satisfied. This
  // one has no greyed button and never will — the failure is the joke, and a
  // control that greyed itself at random would read as broken — so the splash is
  // the ONLY place a player is ever told it happens. Derived from the served
  // chance, so retuning it in content.go retunes this sentence.
  const flaky = failNote(def);
  if (flaky) notes.push(flaky);
  // SECOND, because it is the other note that says when the button will do
  // NOTHING, and the two are ordered as a player meets them: what he has to be
  // holding, then where he has to be standing. A verb gated on a place is
  // refused with «далековато» and applies nothing, exactly as the stat gate is
  // refused with «рано ещё» — the difference is only what the player has to do
  // about it, which is the whole reason the two lines are separate.
  const near = needsNearNote(def, objectKinds);
  if (near) notes.push(near);
  // Then WHERE that place is, once there is more than one place to be in. A
  // player who walked to лес looking for beer has otherwise been told nothing at
  // all, and «нужно стоять рядом: ящик пива» is cruelly incomplete advice when
  // the ящик is two locations away.
  const where = storeWhereNote(def, storeLocation, places);
  if (where) notes.push(where);
  // Then what he is drawing FROM, which is a fact about the world rather than
  // about the press: the thing he had to walk to is not bottomless.
  const stock = stockNote(def, objectKinds);
  if (stock) notes.push(stock);
  const leaves = leavesNote(def, objectKinds);
  if (leaves) notes.push(leaves);
  // `revives_fatal` says both things at once: the action that carries it is the
  // way back from a death, and the ones that do not are refused with a 409 while
  // he is dead. Which is why every action row ends with one or the other rather
  // than only the cheerful half.
  notes.push(reviveNote(def, revivers));
  return {
    key: def.key,
    emoji: def.emoji || '',
    label: def.label || def.key,
    // A RESET IS NOT A LIST OF DELTAS, and the server says so by ignoring
    // `effects` outright when `starts_over` is set — which is why the reviving
    // verb ships with an empty effects list. Rendered by the ordinary path it
    // would come out as an empty string, i.e. as a button the cheatsheet claims
    // moves nothing, on the one verb the player most needs explained.
    effects: def.starts_over ? resetText(bars) : effectsText(def, byKey),
    notes,
  };
}

/** One location, reduced to the two things the cheatsheet says about it. */
interface SearchPlace {
  /** Its catalogue key, so a note can resolve a served key back to a name. */
  key: string;
  /** What it is called, or the empty string for one the catalogue did not name. */
  label: string;
  /** How many hiding places it has. */
  spots: number;
}

/**
 * How the searching verb is used, and where — or nothing at all for every other
 * verb in the catalogue.
 *
 * THREE LINES BECAUSE THEY ARE THREE KINDS OF FACT, and only two of them can be
 * derived. The first is the CONTROL — that this verb has no button and is used
 * by tapping a hiding place and walking to it — and no catalogue field could say
 * it, because it is a property of this screen rather than of the game: the wire
 * carries the verb and the hiding places, and which of them is a button is a
 * decision the SPA makes. It is therefore hardcoded copy, and it is here rather
 * than in `YARD_PROSE` for the one reason that matters — it is ATTACHED to the
 * verb the predicate picked out, so a catalogue in which no verb searches
 * anything (an older server, or a world with no hunt) never shows it at all.
 *
 * The other two are derived outright, off `locations[].hotspots`: how many PLACES
 * have something to search in and what they are called, and how many HIDING
 * PLACES there are between them. Both are numbers the player plays against —
 * four places with three bushes each is a hunt you can sweep in a few minutes and
 * twenty is one you have to be lucky at — and both are exactly the sort of number
 * somebody retunes by feel one evening, so adding a bush or a whole location in
 * internal/gamevanyagotchi/content.go moves these sentences with no edit here.
 *
 * A PLACE WITH NOTHING TO SEARCH IS NOT COUNTED, because the line says where you
 * can look rather than where you can go. A location with no hunt in it is a
 * perfectly good location and the travel sheet still lists it; counting it here
 * would send the player somewhere with nothing to tap.
 *
 * THE NAMES ARE DROPPED WHOLESALE IF ANY IS MISSING, rather than a partial list
 * being printed. «искать можно в 4 местах: двор · лес» reads as a complete
 * enumeration and is a lie about the other two — the count is still true, so the
 * honest degradation is to keep it and say no more.
 */
function searchNotes(searches: boolean, places: readonly SearchPlace[]): string[] {
  if (!searches) return [];
  // ==> HARDCODED. This half describes the CONTROL, which is not on the wire.
  const lines = ['не кнопка: тапни укрытие, и он обыщет его, когда дойдёт'];
  const searchable = places.filter((place) => place.spots > 0);
  if (!searchable.length) return lines;
  const named = searchable.map((place) => place.label).filter(Boolean);
  // Prepositional case, which has two forms rather than three: «в 1 месте», «в
  // 2 местах», «в 5 местах». The shared helper takes three, so the last two are
  // the same word — deliberately, rather than a second pluraliser for one case.
  const where = `искать можно в ${searchable.length} ${plural(searchable.length, 'месте', 'местах', 'местах')}`;
  const total = searchable.reduce((sum, place) => sum + place.spots, 0);
  // Nominative after a numeral, which is a third agreement pattern again: «1
  // укрытие», «3 укрытия», «12 укрытий». «всего» rather than «в них», because a
  // pronoun would have to agree with the number of places as well and «в них 1
  // укрытие» about a single place is wrong in a way nobody would notice.
  const found = `всего ${total} ${plural(total, 'укрытие', 'укрытия', 'укрытий')}`;
  if (named.length !== searchable.length) return [...lines, where, found];
  return [...lines, `${where}: ${named.join(' · ')}`, found];
}

/**
 * What the pet must already have before this verb will do anything, or nothing
 * for a verb with no such condition.
 *
 * DERIVED FROM THE SERVED PAIR, so retuning the threshold in
 * internal/gamevanyagotchi/content.go changes this sentence on its own. That is
 * the whole reason it is here rather than in `YARD_PROSE`: «нужно 15» typed by
 * hand would be wrong the first afternoon somebody decided a quarter of a
 * bladder was too little, and nothing would ever compare the two.
 *
 * NAMED FROM THE STAT'S OWN LABEL, and yielding NO NOTE when the catalogue does
 * not describe the stat — the same discipline `leavesNote` follows below. A
 * client older than the server can be handed a gate on a stat it has never heard
 * of, and «нужно: bladder от 15» is worse than silence: it exposes a wire key to
 * a player as though it were a word.
 *
 * A threshold of nought yields no note either, and that is the honest reading
 * rather than an omission: the server compares `>=`, so a gate at nought is
 * satisfied by every value a stat can hold and is therefore not a rule at all.
 * Printing it would tell the player about a condition that can never fail.
 */
function needsNote(
  def: VanyagotchiAction,
  byKey: Map<string, VanyagotchiStat>,
): string | null {
  if (!def.needs_stat) return null;
  const amountNeeded = def.needs_at_least;
  if (!Number.isFinite(amountNeeded) || (amountNeeded ?? 0) <= 0) return null;
  const stat = byKey.get(def.needs_stat);
  if (!stat) return null;
  return `нужно накопить: ${stat.label || def.needs_stat} от ${amount(amountNeeded as number)}`;
}

/**
 * That this verb sometimes simply does not come off, or nothing for one that
 * always works.
 *
 * THE ONLY WARNING THE PLAYER EVER GETS about it. Every other thing that can
 * stop a press also greys the button, so the cheatsheet is a second telling; this
 * one deliberately does not — a control that greyed itself a quarter of the time
 * at random would read as faulty rather than funny — so a player who has not read
 * this line meets it as a button that occasionally does nothing.
 *
 * SAID AS ODDS RATHER THAN AS A PERCENTAGE, because «1 раз из 4» is a thing a
 * person can picture and «25%» is a thing a person has to convert. Derived from
 * the served number, so retuning `relieveFailChance` in content.go retunes the
 * sentence and nobody has to remember this file exists.
 *
 * A chance of one is a catalogue that has turned the verb off entirely, and it
 * gets its own sentence rather than the nonsensical «1 раз из 1» the arithmetic
 * would otherwise produce.
 */
function failNote(def: VanyagotchiAction): string | null {
  const chance = def.fail_chance;
  if (typeof chance !== 'number' || !Number.isFinite(chance) || chance <= 0) return null;
  if (chance >= 1) return 'никогда не получается';
  return `иногда не получается: примерно 1 раз из ${Math.max(2, Math.round(1 / chance))}`;
}

/**
 * Where the pet has to be STANDING before this verb will do anything, or nothing
 * for a verb he can press from anywhere.
 *
 * DERIVED IN TWO HOPS like `leavesNote` below: the verb carries a kind KEY
 * (`needs_near`) and the kind carries what it is called, both out of
 * internal/gamevanyagotchi/content.go. So moving the beer store, renaming it, or
 * gating a second verb on a second place are all backend edits that this
 * sentence follows on its own. What it deliberately does NOT say is how close
 * "beside it" is: `arrive_within` is a distance in plane widths, and a number
 * the player cannot measure by eye is not a rule he can act on — the instruction
 * is «walk over», and the yard shows him whether he has arrived by whether the
 * button lights up.
 *
 * A KIND THE CATALOGUE DOES NOT NAME YIELDS NO NOTE, and this is STRICTER than
 * `leavesNote`, which falls back to «кое-что» for an unnamed kind. The two
 * sentences survive namelessness differently: «оставляет кое-что на земле» still
 * teaches the whole rule — something appears, everybody sees it, it goes after
 * ten minutes — whereas the entire content of this one is WHICH THING TO WALK
 * TO. «Нужно стоять рядом: кое-что» is a fetch quest, not a rule, so silence is
 * the honest answer and the server's «далековато» stays the first the player
 * hears of it.
 */
function needsNearNote(
  def: VanyagotchiAction,
  objectKinds: Map<string, VanyagotchiObjectKind>,
): string | null {
  if (!def.needs_near) return null;
  const kind = objectKinds.get(def.needs_near);
  if (!kind?.label) return null;
  return `нужно стоять рядом: ${kind.label}`;
}

/**
 * How many draws the thing this verb races other players for holds, or nothing
 * for a verb that is not drawing from a finite pile.
 *
 * DERIVED IN THE SAME TWO HOPS, and the number is the whole point of making it a
 * derivation: `crateStock` is a constant in internal/gamevanyagotchi/content.go
 * chosen for pacing — small enough that the count on screen VISIBLY falls — and
 * it is exactly the sort of number somebody retunes by feel one evening. A
 * hand-typed «в ящике шесть» would be wrong that same evening, silently, because
 * nothing compares the two.
 *
 * IT SAYS NOTHING ABOUT WHAT HAPPENS WHEN THE PILE IS EMPTY, and that is not an
 * omission: the crate is replaced the instant it is drawn to nothing, which is a
 * property of the server's own write and appears nowhere in the catalogue. It is
 * in YARD_PROSE at the foot of this file, with the rest of what cannot be
 * derived — and without it «6 порций на всех» reads as a yard that runs dry,
 * which is the opposite of the truth.
 *
 * A CONTESTED KIND WITH NO STOCK YIELDS NO NOTE, which is not a fallback but the
 * correct reading of the other discipline: the lost key is contested too, and it
 * is won outright rather than drawn down, so it carries no stock and there is no
 * count to state. Nor does an unnamed kind, for the reason `needsNearNote` gives
 * above — the label is this sentence's subject.
 */
/**
 * Which place the thing this verb is gated on stands in, or nothing when there
 * is only one place to be in.
 *
 * DERIVED IN TWO HOPS like every other note here: the config names the store's
 * location key, and `locations` names that key. So moving the shop to заброшка
 * changes this sentence on its own.
 *
 * SILENT WITH ONE LOCATION, deliberately. «пиво только во дворе» is information
 * only when there is somewhere else to be; in a one-place world it is a rule
 * about a distinction the player cannot make, which is noise. It is also silent
 * for a verb that is not gated on a place at all, and for a location the
 * catalogue does not name — the same discipline `needsNearNote` follows, and for
 * the same reason: naming a key at a player is worse than saying nothing.
 */
function storeWhereNote(
  def: VanyagotchiAction,
  storeLocation: string | undefined,
  places: readonly SearchPlace[],
): string | null {
  if (!def.needs_near || !storeLocation) return null;
  if (places.length < 2) return null;

  const place = places.find((entry) => entry.key === storeLocation);
  if (!place?.label) return null;
  return `только тут: ${place.label}`;
}

function stockNote(
  def: VanyagotchiAction,
  objectKinds: Map<string, VanyagotchiObjectKind>,
): string | null {
  if (!def.contests) return null;
  const kind = objectKinds.get(def.contests);
  if (!kind?.label) return null;
  const stock = kind.stock;
  // An integer, because the next line agrees a Russian noun with it and half a
  // serving is not a thing the catalogue can mean. Nought or absent is the
  // ordinary case — it is what every kind that is not drawn down carries.
  if (typeof stock !== 'number' || !Number.isInteger(stock) || stock <= 0) return null;
  // «порция» rather than «бутылка»: what is in the crate is content and this
  // line is not allowed to know it, so the unit is the generic one — the same
  // licence `duration` takes with «минуту», where the number is derived and the
  // word around it is not.
  return `${kind.label} — ${stock} ${plural(stock, 'порция', 'порции', 'порций')} на всех`;
}

/**
 * What pressing this leaves standing in the yard, or nothing when it leaves
 * nothing.
 *
 * DERIVED IN TWO HOPS, and both of them are the point. The verb carries a kind
 * KEY (`leaves`); the kind carries what it is called and how long one lasts
 * (`lifetime_seconds`); both live in internal/gamevanyagotchi/content.go. So
 * teaching a verb to leave something behind, naming the thing, or retuning the
 * ten minutes are all backend edits that this sentence follows on its own. A
 * hand-typed «остаётся на 10 минут» would be wrong the first afternoon somebody
 * shortened the lifetime, and nothing would ever compare the two.
 *
 * A KEY THE CATALOGUE DOES NOT DESCRIBE YIELDS NO NOTE, rather than a vague one.
 * A client older than the server can be told about a verb that leaves a kind it
 * has never heard of, and saying nothing is honest where «оставляет что-то»
 * would be a guess dressed up as a rule.
 */
function leavesNote(
  def: VanyagotchiAction,
  objectKinds: Map<string, VanyagotchiObjectKind>,
): string | null {
  if (!def.leaves) return null;
  const kind = objectKinds.get(def.leaves);
  if (!kind) return null;
  // Named only when the catalogue names it. A deposit is deliberately unnamed —
  // a caption over it would be one more thing to draw on a small screen — so the
  // noun is the one part of this sentence the config cannot supply, and it is
  // vague on purpose rather than by omission.
  const what = kind.label ? `«${kind.label}»` : 'кое-что';
  return `оставляет ${what} на земле: ${visibility(kind.lifetime_seconds)}`;
}

/**
 * How long the thing stays there, as the catalogue states it.
 *
 * Three answers rather than one, because the field carries three meanings: a
 * positive lifetime is seconds, ZERO is the catalogue's word for "forever" (such
 * a row is never filtered out on read), and anything else — absent, negative, a
 * NaN — is a catalogue this client cannot read, where the honest line claims no
 * duration at all rather than promising eternity by accident.
 */
function visibility(seconds: number): string {
  if (seconds === 0) return 'видно всем, и оно уже никуда не денется';
  if (!Number.isFinite(seconds) || seconds < 0) return 'видно всем';
  return `видно всем ${duration(seconds)}`;
}

/** «10 минут», «1 минуту», «30 секунд» — an accusative span, as «видно всем …» needs. */
function duration(seconds: number): string {
  if (seconds < 60) {
    // At least one, so a lifetime of half a second reads as a moment rather than
    // as «0 секунд», which a player would read as "it does not happen".
    const secs = Math.max(1, Math.round(seconds));
    return `${secs} ${plural(secs, 'секунду', 'секунды', 'секунд')}`;
  }
  const mins = Math.round(seconds / 60);
  return `${mins} ${plural(mins, 'минуту', 'минуты', 'минут')}`;
}

/**
 * Russian numeral agreement: three forms, and the one part of this sentence that
 * a naive `${n} минут` gets visibly wrong. «1 минут» and «2 минут» are both
 * broken, and a lifetime retuned to a minute or two is exactly the change most
 * likely to happen — the whole reason the line is derived is that such a retune
 * must not need an edit here. The teens are the exception the modulo carves out.
 */
function plural(n: number, one: string, few: string, many: string): string {
  const mod100 = n % 100;
  if (mod100 >= 11 && mod100 <= 14) return many;
  const mod10 = n % 10;
  if (mod10 === 1) return one;
  if (mod10 >= 2 && mod10 <= 4) return few;
  return many;
}

/**
 * The one warning an action row owes the player.
 *
 * Says «единственный» only when it is TRUE — counted off the catalogue rather
 * than asserted — because uniqueness is the rule that changed when reviving
 * stopped being a side effect of drinking, and it is the rule that decides which
 * button a player reaches for at the one moment the game is asking for a
 * particular one. Should a second way back ever be added, this softens by itself
 * rather than going quietly wrong.
 */
function reviveNote(def: VanyagotchiAction, revivers: number): string {
  if (!def.revives_fatal) return 'мёртвому нельзя';
  return revivers === 1 ? 'единственный способ поднять мёртвого' : 'поднимает мёртвого';
}

/** «пиво +40 · здоровье +15» — the ordinary case, one entry per delta. */
function effectsText(def: VanyagotchiAction, byKey: Map<string, VanyagotchiStat>): string {
  return (Array.isArray(def.effects) ? def.effects : [])
    .filter(
      (effect): effect is VanyagotchiStatDelta =>
        !!effect && typeof effect.stat_key === 'string' && Number.isFinite(effect.delta),
    )
    .map((effect) => effectText(effect, byKey.get(effect.stat_key)))
    .join(' · ');
}

/**
 * «всё заново: здоровье → 65 · пиво → 60 · мочевой пузырь → 0».
 *
 * DERIVED FROM THE CATALOGUE'S OWN `start` VALUES, and that is the whole reason
 * this function exists rather than a sentence: those numbers are already written
 * down once, in internal/gamevanyagotchi/content.go, and a second copy typed in
 * here would go wrong the first afternoon somebody decides дядя Ваня should come
 * back with less health. Counters are absent because a reset spares them — they
 * were filtered out before this was called, so there is no second opinion here
 * about what a counter is.
 */
function resetText(bars: VanyagotchiStat[]): string {
  const parts = bars
    .filter((stat) => Number.isFinite(stat.start))
    .map((stat) => `${stat.label || stat.key} → ${amount(stat.start)}`);
  return parts.length ? `всё заново: ${parts.join(' · ')}` : 'всё заново';
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
 *   4. who — and WHAT — else is in the yard: the sleepers (`sleeperLimit`), the
 *      NPC regulars, and the things people leave lying on the ground, none of
 *      which the client can tell apart from a player. The part that has to be
 *      said by hand is that the leavings are NOT people: `props` in world.go
 *      appends them after the «во дворе» head count is taken, so they are
 *      excluded from it, and that exclusion appears nowhere in the catalogue.
 *      What one of them is and how long it lasts is derived instead, one section
 *      up, off the verb that leaves it.
 *   5. WHERE THE PLACES ARE AND HOW YOU LEAVE ONE. How many there are and what
 *      they are called is derived, one section up; that they exist at all, that
 *      you see only the people standing in the same one, and that the head count
 *      in the corner of the plane is what you tap to move between them, are not.
 *      The first two are properties of the server's broadcast (one room, filtered
 *      by `loc`) and the third is a decision this SPA made about where to put a
 *      control — no catalogue field could carry either. If the travel control
 *      moves, this is the text that goes wrong.
 *   6. the key hunt, which is the one rule where the derived half is actively
 *      misleading on its own. The catalogue says the searching verb adds one to
 *      a tally and is refused on a corpse, and stops there. Two of the missing
 *      parts ARE derived now and are no longer in this block — that the verb is
 *      not a button, and how many places have something to search in and how many
 *      hiding places there are between them, both attached to the verb itself by
 *      `searchNotes` above. What is still missing is everything that makes it a
 *      RACE and a GAMBLE: that there is exactly ONE key in the WHOLE WORLD at a
 *      time and not one per place, so most places are empty and searching
 *      everywhere is the game (the singleton invariant is a partial unique index
 *      in migration 008, not a catalogue flag); that it is
 *      HIDDEN — the server picks a hiding place at spawn and simply never
 *      publishes which, so the key is not an entity on the roster at all and
 *      cannot be seen, followed, or read off a frame; that arriving at the wrong
 *      place is answered «тут пусто» and the walk was the whole cost; that the
 *      walk itself can end in «устал», which is what makes a far hiding place a
 *      risk rather than a longer wait; that the first to arrive at the right one
 *      takes it and the rest are refused; that losing costs nothing but a sad
 *      face for a few seconds — no stat moves, deliberately, so nothing about
 *      the loss can be derived from `effects` — and that a replacement is lost
 *      the instant the old one is found.
 *   7. the beer store, where the derived half stops one sentence short of the
 *      rule. `needs_near` and the crate's `stock` give «нужно стоять рядом» and
 *      «6 порций на всех» one section up, and read alone those describe a yard
 *      that runs dry — which is the opposite of what happens. What is missing is
 *      that the crate is REPLACED the instant it is drawn to nothing, in the
 *      same transaction as the draw that empties it, so a shortage lasts as long
 *      as it takes somebody to press again. That is a property of the server's
 *      own write (`DrawFromStock` plus the replacement insert in world.go), not
 *      a catalogue value, so nothing on the wire could ever say it. Nor could
 *      anything say how close «рядом» is in a way a player could act on:
 *      `arrive_within` is a distance in plane widths, and the honest instruction
 *      is «walk to the crate and watch the button light up».
 *
 * If one of those numbers or behaviours moves, this text is what goes wrong. Keep
 * it honest, keep it short, and resist adding anything here that the config
 * already carries — a line that could have been derived is a line that will
 * eventually contradict the bars two screens later.
 */
export const YARD_PROSE: readonly string[] = [
  'Время идёт без тебя: пока вкладка закрыта, всё продолжает капать. Вернулся утром — вернулся к последствиям.',
  'Тапни по земле — Ваня пойдёт туда пешком, примерно пятая часть площадки в секунду. С дальнего тапа может сесть на полпути и сообщить, что нога отваливается. Короткий шаг доходит всегда, а новый тап всегда отменяет старый, так что застрять нельзя.',
  'Стоит без дела — бормочет себе под нос.',
  'Мест несколько, и ходить между ними можно как угодно. В углу площадки написано, где ты сейчас и сколько там народу; тапни по этой надписи — и выберешь, куда перейти. Видишь только тех, кто стоит там же, где и ты, — остальные никуда не делись, просто они в другом месте.',
  'Остальные рядом — живые люди. Кто ушёл, тот лежит спит там, где стоял. А пара местных вообще ничьи.',
  'Не всё вокруг — люди. Что кто-то оставил на земле, то там и лежит: видно всем, но в счётчике народу не числится.',
  'Ключи одни на все места сразу, а не по штуке на каждое, и где они — не видно никому: их прячут в одном из укрытий, и на карте они не нарисованы. Тапни укрытие — Ваня пойдёт туда и обыщет его, когда дойдёт. Не дошёл — не обыскал, так что дальнее укрытие это риск.',
  'Обыскал не то — «тут пусто»: ключи всё ещё где-то, а ты уже сходил. Нашёл первым — твои; опоздавшему не будет ничего, только грустная морда на пару секунд. И новые ключи теряются сразу же, так что искать можно вечно.',
  'Пиво не берётся из воздуха: до ящика надо дойти ногами. Ящик один на всех, и разбирают его вместе — кто дошёл, тот и налил. Кончилось — тут же выкатывают новый, так что ждать долго не придётся.',
];
