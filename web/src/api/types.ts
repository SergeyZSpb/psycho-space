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
// A character dialogue judged by an AI (mock now, LLM later). The SPA fetches
// the config, presents answer options, and each turn asks the backend to judge
// whether the goal is reached. Persona prompts + answer keys stay server-side.

export interface GameOption {
  id: string;
  label: string;
}

export interface GameCharacter {
  key: string;
  name: string;
  goal: string;
  background: string; // background asset key
  greeting: string;
  emotions: string[]; // emotion asset keys the judge may show
  max_steps: number; // dialogue-step budget
  options: GameOption[];
}

export interface GameConfig {
  game_key: string;
  title: string;
  intro: string;
  default_character: string;
  characters: GameCharacter[];
}

// Result of one dialogue turn, judged server-side.
export interface GameTurnResult {
  reply: string;
  emotion: string; // asset key to show
  achieved: boolean; // goal reached?
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

export interface GameLeaderboardEntry {
  player: GamePlayer;
  successes: number;
  plays: number;
  steps: number;
  mine: boolean;
}

export interface GameStats {
  successes: number;
  plays: number;
  best_steps: number;
}
