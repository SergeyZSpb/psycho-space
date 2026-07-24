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

// GameArt is one showable asset with its render descriptor. `image` (a URL)
// wins when present; otherwise render `emoji` over `gradient`.
export interface GameArt {
  key: string;
  emoji: string;
  gradient: string;
  image?: string;
}

export interface GameCharacter {
  key: string;
  name: string;
  goal: string; // high-level, user-facing (no spoilers)
  greeting: string; // static opening line
  opening_options: string[]; // static first answer options
  arts: GameArt[]; // asset catalog the judge chooses from
}

export interface GameConfig {
  game_key: string;
  title: string;
  intro: string;
  default_character: string;
  /** Tension scale, defined by the backend so the client never hardcodes it. */
  max_anger: number;
  start_anger: number;
  characters: GameCharacter[];
}

// One completed turn in the conversation, sent back as context each turn.
export interface GameExchange {
  choice: string;
  reply: string;
}

// Result of one dialogue turn, judged by the LLM. `art` is a key into the
// character's art catalog. `options` are the next answer choices (labels) —
// always 4 while playing; empty ends the dialogue, either won (`achieved`) or
// lost (`game_over` — the character snapped and threw a punch).
export interface GameTurnResult {
  reply: string;
  art: string;
  achieved: boolean;
  game_over: boolean;
  /** Tension after this turn, 0..max_anger. At the max the run is lost. */
  anger: number;
  options: string[];
}

export interface GameRun {
  id: string;
  game_key: string;
  character_key: string;
  success: boolean;
  steps: number;
  created_at: string;
}

export interface GamePlayer {
  display_name: string;
  avatar_url: string;
  vk_url: string;
}

// The leaderboard is four record boards, each ranking players by their best (or
// worst) SINGLE run rather than by an aggregate. A player only appears on a
// board they hold a record on — no wins yet means no place on either win board.
export type GameBoardKey = 'longest_win' | 'shortest_win' | 'longest_loss' | 'shortest_loss';

export interface GameRecordEntry {
  player: GamePlayer;
  /** Steps of the single run that earned this place on this board. */
  steps: number;
  /** The player's overall tally for the game, shown alongside the record. */
  plays: number;
  wins: number;
  losses: number;
  mine: boolean;
}

export type GameBoards = Record<GameBoardKey, GameRecordEntry[]>;

export interface GameStats {
  successes: number;
  plays: number;
  best_steps: number;
}
