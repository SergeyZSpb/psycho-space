// Thin, typed wrappers around the backend routes. Keeping them in one place
// documents the full contract the SPA depends on.

import { apiFetch } from './client';
import type {
  Account,
  AdminAccount,
  AccountStatus,
  AdminSettings,
  GameKhimkiBoards,
  GameKhimkiConfig,
  GameKhimkiExchange,
  GameKhimkiRun,
  GameKhimkiStats,
  GameKhimkiTurnResult,
  LoginResult,
  VanyagotchiConfig,
  VanyagotchiState,
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

export const gameKhimkiApi = {
  // Backend-served game config (characters, options, assets). No persona prompts
  // or answer keys — those stay server-side.
  config: (game: string) => apiFetch<GameKhimkiConfig>(`/api/game-khimki/config?game=${game}`),

  // Judge one dialogue turn via the LLM. `transcript` is the conversation so
  // far; `choice` is the player's latest line ("" on the opening turn); `anger`
  // is the tension carried over from the previous turn, and `themesDone` the
  // theme progress (both are cross-turn state the client holds).
  attempt: (
    game: string,
    character: string,
    transcript: GameKhimkiExchange[],
    choice: string,
    anger: number,
    themesDone: string[],
  ) =>
    apiFetch<GameKhimkiTurnResult>('/api/game-khimki/attempt', {
      method: 'POST',
      body: {
        game_key: game,
        character_key: character,
        transcript,
        choice,
        anger,
        themes_done: themesDone,
      },
    }),

  // Record a finished play-through (goal reached or step budget spent).
  submitRun: (game: string, character: string, success: boolean, steps: number) =>
    apiFetch<GameKhimkiRun>('/api/game-khimki/runs', {
      method: 'POST',
      body: { game_key: game, character_key: character, success, steps },
    }),

  leaderboard: (game: string, limit = 20) =>
    apiFetch<{ boards: GameKhimkiBoards }>(`/api/game-khimki/runs/leaderboard?game=${game}&limit=${limit}`),

  stats: (game: string) => apiFetch<GameKhimkiStats>(`/api/game-khimki/runs/me?game=${game}`),
};

// «Ванягоччи». A separate object from the game above and deliberately so: games
// share no client code either, so deleting one is deleting its own block.
export const gameVanyagotchiApi = {
  // The content catalogue — stats and their rates, the actions, the skins, the
  // locations. Everything the screen labels or draws comes from here, so a new
  // stat or a retitled action needs no frontend deploy.
  config: () => apiFetch<VanyagotchiConfig>('/api/game-vanyagotchi/config'),

  // The caller's pet, with every stat decayed to this instant. Creates the pet
  // on first sight and records a death the first time one is observed, which is
  // why a plain GET is allowed to change things.
  state: () => apiFetch<VanyagotchiState>('/api/game-vanyagotchi/state'),

  // Apply one catalogue action. The verb goes in the path and the body is empty:
  // the client says "heal", never "set hp to 80", so there is no number in the
  // request to forge. The answer is the server's own recomputed state, which is
  // also what corrects any drift in the bar the screen has been interpolating.
  act: (action: string) =>
    apiFetch<VanyagotchiState>(`/api/game-vanyagotchi/actions/${encodeURIComponent(action)}`, {
      method: 'POST',
    }),
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
