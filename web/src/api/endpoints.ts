// Thin, typed wrappers around the backend routes. Keeping them in one place
// documents the full contract the SPA depends on.

import { apiFetch } from './client';
import type {
  Account,
  AdminAccount,
  AccountStatus,
  AdminSettings,
  GameConfig,
  GameExchange,
  GameLeaderboardEntry,
  GameRun,
  GameStats,
  GameTurnResult,
  LoginResult,
  WishlistComment,
  WishlistItem,
  WishlistSort,
} from './types';

export interface VkCallbackBody {
  code: string;
  device_id: string;
  state: string;
  code_verifier: string;
  consent_version: string;
}

export const authApi = {
  // Mints + sets the CSRF state cookie and returns the value to echo to VK.
  vkState: () => apiFetch<{ state: string }>('/api/auth/vk/state'),

  // Confidential backend code exchange; issues a session on approval.
  vkCallback: (body: VkCallbackBody) =>
    apiFetch<LoginResult>('/api/auth/vk/callback', { method: 'POST', body }),

  // Current account, or throws ApiError(status 401) when not logged in.
  me: () => apiFetch<{ account: Account }>('/api/auth/me'),

  logout: () => apiFetch<void>('/api/auth/logout', { method: 'POST' }),
};

export const wishlistApi = {
  list: (sort: WishlistSort) =>
    apiFetch<{ items: WishlistItem[] }>(`/api/wishlist/items?sort=${sort}`),

  // Returns the single created Item (not wrapped).
  create: (title: string, body: string) =>
    apiFetch<WishlistItem>('/api/wishlist/items', { method: 'POST', body: { title, body } }),

  vote: (id: string) => apiFetch<void>(`/api/wishlist/items/${id}/vote`, { method: 'POST' }),

  unvote: (id: string) => apiFetch<void>(`/api/wishlist/items/${id}/vote`, { method: 'DELETE' }),

  // Delete an idea — 204 | 403 forbidden | 404 not_found. Author or admin.
  deleteItem: (id: string) => apiFetch<void>(`/api/wishlist/items/${id}`, { method: 'DELETE' }),

  // Delete a comment — same semantics as deleteItem.
  deleteComment: (id: string) =>
    apiFetch<void>(`/api/wishlist/comments/${id}`, { method: 'DELETE' }),

  // Comments — pre-sorted top-voted first by the backend.
  comments: (itemId: string) =>
    apiFetch<{ comments: WishlistComment[] }>(`/api/wishlist/items/${itemId}/comments`),

  // Returns the single created Comment (not wrapped).
  createComment: (itemId: string, body: string) =>
    apiFetch<WishlistComment>(`/api/wishlist/items/${itemId}/comments`, {
      method: 'POST',
      body: { body },
    }),

  voteComment: (commentId: string) =>
    apiFetch<void>(`/api/wishlist/comments/${commentId}/vote`, { method: 'POST' }),

  unvoteComment: (commentId: string) =>
    apiFetch<void>(`/api/wishlist/comments/${commentId}/vote`, { method: 'DELETE' }),
};

export const gameApi = {
  // Backend-served game config (characters, options, assets). No persona prompts
  // or answer keys — those stay server-side.
  config: (game: string) => apiFetch<GameConfig>(`/api/game/config?game=${game}`),

  // Judge one dialogue turn via the LLM. `transcript` is the conversation so
  // far; `choice` is the player's latest line ("" on the opening turn).
  attempt: (game: string, character: string, transcript: GameExchange[], choice: string) =>
    apiFetch<GameTurnResult>('/api/game/attempt', {
      method: 'POST',
      body: { game_key: game, character_key: character, transcript, choice },
    }),

  // Record a finished play-through (goal reached or step budget spent).
  submitRun: (game: string, character: string, success: boolean, steps: number) =>
    apiFetch<GameRun>('/api/game/runs', {
      method: 'POST',
      body: { game_key: game, character_key: character, success, steps },
    }),

  leaderboard: (game: string, limit = 20) =>
    apiFetch<{ entries: GameLeaderboardEntry[] }>(
      `/api/game/runs/leaderboard?game=${game}&limit=${limit}`,
    ),

  stats: (game: string) => apiFetch<GameStats>(`/api/game/runs/me?game=${game}`),
};

export const adminApi = {
  list: (status: AccountStatus) =>
    apiFetch<{ accounts: AdminAccount[] }>(`/api/admin/accounts?status=${status}`),

  approve: (id: string) => apiFetch<void>(`/api/admin/accounts/${id}/approve`, { method: 'POST' }),

  block: (id: string) => apiFetch<void>(`/api/admin/accounts/${id}/block`, { method: 'POST' }),

  // superadmin-only; 403 otherwise.
  promote: (id: string) => apiFetch<void>(`/api/admin/accounts/${id}/promote`, { method: 'POST' }),

  // Any admin may read the settings.
  settings: () => apiFetch<AdminSettings>('/api/admin/settings'),

  // superadmin-only; 403 otherwise.
  demote: (id: string) => apiFetch<void>(`/api/admin/accounts/${id}/demote`, { method: 'POST' }),

  // superadmin-only; 403 otherwise. Returns the applied state.
  setOpenRegistration: (enabled: boolean) =>
    apiFetch<AdminSettings>('/api/admin/settings/open-registration', {
      method: 'PUT',
      body: { enabled },
    }),
};
