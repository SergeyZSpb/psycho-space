// Shared API types — mirror the Go backend's JSON contract.

export type Role = 'user' | 'admin' | 'superadmin';
export type AccountStatus = 'pending' | 'approved' | 'blocked';
export type WishlistSort = 'top' | 'new';

// The public account shape returned by /api/auth/me and the login result.
// `handle` (first 8 hex of the blind index) is shown on the pending screen.
export interface Account {
  id: string;
  display_name: string;
  avatar_url: string;
  vk_url: string;
  role: Role;
  status: AccountStatus;
  handle: string;
}

// The richer shape the admin console lists (adds handle + created_at).
export interface AdminAccount {
  id: string;
  handle: string;
  display_name: string;
  avatar_url: string;
  vk_url: string;
  role: Role;
  status: AccountStatus;
  created_at: string;
}

export interface ItemAuthor {
  display_name: string;
  avatar_url: string;
  vk_url: string;
}

export interface WishlistItem {
  id: string;
  title: string;
  body: string;
  votes: number;
  voted_by_me: boolean;
  created_at: string;
  author: ItemAuthor;
  mine: boolean;
  comment_count: number;
}

// A comment on a wishlist item — itself upvotable, same shape of vote fields.
export interface WishlistComment {
  id: string;
  item_id: string;
  body: string;
  votes: number;
  voted_by_me: boolean;
  created_at: string;
  author: ItemAuthor;
  mine: boolean;
}

// Admin settings.
export interface AdminSettings {
  open_registration: boolean;
}

// /api/auth/vk/callback now ALWAYS returns the account (and sets a session
// cookie) regardless of status; the client routes by account.status.
export interface LoginResult {
  status: AccountStatus;
  account: Account;
}

// --- Game (mini-games section) ----------------------------------------------
// A character dialogue judged by an LLM. The SPA fetches config (character +
// art catalog), then each turn sends the transcript + the player's chosen line;
// the backend replies in character, judges progress, picks an art, and returns
// the next options. Persona prompts stay server-side; options + art are
// LLM-generated. Assets resolve from the backend art catalog (no client update
// to add arts).

// GameKhimkiArt is one showable asset with its render descriptor. `image` (a URL)
// wins when present; otherwise render `emoji` over `gradient`.
export interface GameKhimkiArt {
  key: string;
  emoji: string;
  gradient: string;
  image?: string;
}

export interface GameKhimkiCharacter {
  key: string;
  name: string;
  goal: string; // high-level, user-facing (no spoilers)
  greeting: string; // static opening line
  opening_options: string[]; // static first answer options
  arts: GameKhimkiArt[]; // asset catalog the judge chooses from
}

export interface GameKhimkiConfig {
  game_key: string;
  title: string;
  intro: string;
  default_character: string;
  /**
   * Tension scale, defined by the backend so the client never hardcodes it.
   * The bar fills to `anger_lose_at` — reaching the end of it is the punch.
   */
  max_anger: number;
  anger_lose_at: number;
  start_anger: number;
  characters: GameKhimkiCharacter[];
}

// One completed turn in the conversation, sent back as context each turn.
export interface GameKhimkiExchange {
  choice: string;
  reply: string;
  /**
   * The options the judge offered after this reply, and the tension it left the
   * turn at. Both are sent back so the judge sees its own past state inside the
   * history — once, as part of an append-only prefix — instead of us re-deriving
   * and re-sending a summary every turn. Forgotten together with the rest of the
   * exchange when the context window drops it.
   */
  options: string[];
  /**
   * The rest of what the judge returned that turn. All of it goes back so the
   * backend can replay the turn to the model as the JSON it actually produced —
   * an incomplete example would teach it to omit that field.
   */
  art: string;
  anger: number;
  themes_done: string[];
}

// Result of one dialogue turn, judged by the LLM. `art` is a key into the
// character's art catalog. `options` are the next answer choices (labels) —
// always 4 while playing; empty ends the dialogue, either won (`achieved`) or
// lost (`game_over` — the character snapped and threw a punch).
export interface GameKhimkiTurnResult {
  reply: string;
  art: string;
  achieved: boolean;
  game_over: boolean;
  /** Tension after this turn. At `anger_lose_at` the run is lost. */
  anger: number;
  /**
   * Which of the character's deep themes the judge counts as opened. Carried back
   * next turn so it can steer toward what is still closed — deliberately NOT
   * rendered: showing the checklist would play the game for the player.
   */
  themes_done: string[];
  options: string[];
}

export interface GameKhimkiRun {
  id: string;
  game_key: string;
  character_key: string;
  success: boolean;
  steps: number;
  created_at: string;
}

export interface GameKhimkiPlayer {
  display_name: string;
  avatar_url: string;
  vk_url: string;
}

// The leaderboard is four record boards, each ranking players by their best (or
// worst) SINGLE run rather than by an aggregate. A player only appears on a
// board they hold a record on — no wins yet means no place on either win board.
export type GameKhimkiBoardKey = 'longest_win' | 'shortest_win' | 'longest_loss' | 'shortest_loss';

export interface GameKhimkiRecordEntry {
  player: GameKhimkiPlayer;
  /** Steps of the single run that earned this place on this board. */
  steps: number;
  /** The player's overall tally for the game, shown alongside the record. */
  plays: number;
  wins: number;
  losses: number;
  mine: boolean;
}

export type GameKhimkiBoards = Record<GameKhimkiBoardKey, GameKhimkiRecordEntry[]>;

export interface GameKhimkiStats {
  successes: number;
  plays: number;
  best_steps: number;
}

// ---------------------------------------------------------------------------
// «Ванягоччи» — the pet.
//
// Mirrored from internal/gamevanyagotchi/{content,pet}.go. Everything the screen
// draws or labels comes from the config below, so nothing about a stat, an
// action, a skin or a location is spelled out in the SPA: adding one is a
// backend change and this file does not move.
// ---------------------------------------------------------------------------

/**
 * Extra drain one stat suffers while ANOTHER sits in a bad range.
 *
 * This is the coupling that makes health a consequence of two needs rather than
 * a chore of its own: an empty beer and a full bladder are what actually kill
 * him. It is on the wire because it is content — a number somebody will want to
 * move by feel — and NOT because the client evaluates it. The effective rate a
 * bar creeps at arrives already computed, as `rate_per_hour` below.
 */
export interface VanyagotchiPenalty {
  /** The driving stat. */
  when_key: string;
  /** The condition: at or above `threshold` when `above`, at or below it otherwise. */
  threshold: number;
  above: boolean;
  /** Added to the penalised stat's drain while it applies. */
  rate_per_hour: number;
}

/** One thing about a pet that changes with time on its own. */
export interface VanyagotchiStat {
  key: string;
  label: string;
  emoji: string;
  min: number;
  max: number;
  start: number;
  /**
   * Signed: positive drains towards `min`, negative fills towards `max`, zero is
   * a lifetime counter. This is the stat's UNCOUPLED rate — what it loses with
   * every need met — so it is the fallback the bar interpolates at, not the
   * first choice; see `rate_per_hour` on the value.
   */
  decay_per_hour: number;
  /** Which end of the scale is the happy one. */
  good_high: boolean;
  /** Where the stat starts reading as trouble: below when `good_high`, above otherwise. */
  warn_at: number;
  /** Reaching `min` kills him. */
  fatal: boolean;
  /**
   * What this stat loses on top of `decay_per_hour` while its drivers are in
   * trouble. Absent for a stat nothing drives, which is most of them.
   */
  penalties?: VanyagotchiPenalty[];
}

/**
 * One stat moved by one amount, before clamping.
 *
 * A separate shape because an action stopped being able to move a single stat:
 * drinking tops him up, cheers him up AND fills his bladder, which is what makes
 * the second verb have anything to do.
 */
export interface VanyagotchiStatDelta {
  stat_key: string;
  delta: number;
}

/** A verb that moves one or more stats by fixed amounts. */
export interface VanyagotchiAction {
  key: string;
  label: string;
  emoji: string;
  /**
   * What it moves, each clamped against its own stat's bounds — a delta larger
   * than the whole scale is the catalogue's idiom for "reset".
   *
   * The client never applies these: it posts the verb and redraws from the state
   * the server answers with, so there is no local sum that could disagree. They
   * are here because they are part of the contract and a screen may yet want to
   * say what a button will do.
   */
  effects: VanyagotchiStatDelta[];
  /** Shown for a moment after it lands. */
  done: string;
  /**
   * Allowed on, and undoes, a death. Deliberately not true of every action — a
   * dead Ваня cannot go to the toilet — which is what makes the 409 refusal a
   * path the screen has to handle rather than a theoretical one.
   */
  revives_fatal: boolean;
}

/** One look for a pet. `image` (a URL) wins when set; otherwise emoji over gradient. */
export interface VanyagotchiSkin {
  key: string;
  label: string;
  emoji: string;
  gradient: string;
  image?: string;
}

/**
 * Somebody in the yard who is not a player.
 *
 * Three fields and no fourth: `art` is a catalogue key resolved exactly as a
 * pet's skin is, which is why an NPC costs the browser nothing — to the client
 * it is one more entity in the roster, with a face it already knows how to draw.
 * HOW an NPC moves is deliberately absent from the wire (`json:"-"` on the Go
 * side): where it stands arrives in the roster like everybody else's position,
 * and publishing the pattern would invite a second implementation of it here.
 *
 * The screen does not currently read this list at all — the roster already
 * carries each NPC's art key, so nothing has to be looked up by character. It is
 * typed because it is part of the contract, and because a surface that wants to
 * name the cast (a credits line, say) should not have to invent the shape.
 */
export interface VanyagotchiNPC {
  key: string;
  label: string;
  /** A catalogue art key, resolved exactly as a pet's skin is. */
  art: string;
}

export interface VanyagotchiLocation {
  key: string;
  label: string;
  /** Where a pet stands on arriving, in the plane's normalised 0..1 coordinates. */
  entry: { x: number; y: number };
}

export interface VanyagotchiConfig {
  game_key: string;
  title: string;
  stats: VanyagotchiStat[];
  actions: VanyagotchiAction[];
  skins: VanyagotchiSkin[];
  /**
   * The cast. Optional for the same reason `rate_per_hour` is: a server that
   * predates the field — or a fixture that omits it — must still draw a yard,
   * and it can, because the roster is self-describing.
   */
  npcs?: VanyagotchiNPC[];
  locations: VanyagotchiLocation[];
  default_skin: string;
  default_location: string;
}

export interface VanyagotchiPet {
  id: string;
  name: string | null;
  skin_key: string;
  location_key: string;
  /** The moment hp reached zero. `null` means alive. */
  died_at: string | null;
  created_at: string;
}

/** A stat decayed to the instant of the read, with the pair it was decayed from. */
export interface VanyagotchiStatValue {
  key: string;
  value: number;
  as_of: string;
  /**
   * The drain this stat is suffering RIGHT NOW, penalties included — generally
   * NOT the catalogue's own `decay_per_hour`, because health falls faster while
   * a need is unmet.
   *
   * Sent so the bar can keep creeping without the browser owning a second copy
   * of the coupling: reproducing it here would mean the thresholds, the onset
   * arithmetic and every driver's trajectory, i.e. a transliteration of decay.go
   * kept honest by nothing.
   *
   * Optional because a response that predates it — or a fixture that omits it —
   * must still draw a bar; the client falls back to the catalogue rate then.
   */
  rate_per_hour?: number;
}

export interface VanyagotchiState {
  pet: VanyagotchiPet;
  stats: VanyagotchiStatValue[];
  alive: boolean;
  /**
   * The clock everything above was computed against. The bar interpolation
   * measures elapsed time from HERE rather than from the device's own clock, so
   * a phone that is minutes out does not draw a wrong value.
   */
  server_now: string;
}
