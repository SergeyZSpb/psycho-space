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
  VanyadumConfig,
  VanyadumRun,
  VanyadumRunRow,
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

  // There is no `act` here on purpose: a verb travels over the SOCKET now, as
  // one frame carrying a list, and the server answers with state rather than a
  // response body. See GameVanyagotchiView's act().
};

// «ВАНЯДУМ». Its own block, like every game — and deliberately only the EDGES
// of a run: nothing a player touches while playing is here, because input and
// the world both travel on the socket at twenty frames a second.
export const gameVanyadumApi = {
  // The catalogue: the player's dimensions, the pickups, the surfaces the client
  // generates textures from, and the rates it has to match. The splash screen's
  // rules cheatsheet is built from this, so retuning a constant on the server
  // changes what the player is told with no frontend deploy.
  config: () => apiFetch<VanyadumConfig>('/api/game-vanyadum/config'),

  // Starts a run and returns the whole level. 409 `run_in_progress` when one is
  // already going — a refusal rather than a silent replacement, because
  // dropping the old arena would throw away a run open on another tab.
  start: () => apiFetch<VanyadumRun>('/api/game-vanyadum/runs', { method: 'POST' }),

  // The run already in progress, or 404 `no_run`. This is what a page reload
  // needs: after a refresh the browser has a session, a socket and no geometry.
  current: () => apiFetch<VanyadumRun>('/api/game-vanyadum/runs/current'),

  // Gives up. Nothing is written — a run somebody walked out of is not a result.
  abandon: () => apiFetch<void>('/api/game-vanyadum/runs/current', { method: 'DELETE' }),

  // The caller's own recent runs. It exists so that "the run was written" is
  // something a person can check without opening a database.
  myRuns: () => apiFetch<{ runs: VanyadumRunRow[] }>('/api/game-vanyadum/runs/me'),
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
