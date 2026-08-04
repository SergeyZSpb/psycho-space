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
  FintechAdminFloor,
  FintechAdminLayout,
  FintechAdminRebuild,
  FintechConfig,
  FintechLayoutDraft,
  FintechLayoutProblem,
  FintechShift,
  FintechShiftRow,
  FintechTopBoards,
  LoginResult,
  VanyadumConfig,
  VanyadumVisitRow,
  VanyadumWorld,
  VanyagotchiConfig,
  VanyagotchiState,
  WishlistComment,
  WishlistItem,
  WishlistSort,
} from './types';

/**
 * The body both providers' callbacks take.
 *
 * `device_id` is VK's alone and is simply absent from a Yandex request — hence
 * optional here rather than an empty string, so the field never appears on a
 * Yandex payload at all (the backend ignores it either way, but a field that
 * means nothing to the provider has no business being sent).
 */
export interface OAuthCallbackBody {
  code: string;
  device_id?: string;
  state: string;
  code_verifier: string;
  consent_version: string;
}

/**
 * What Yandex's state endpoint answers with.
 *
 * It returns the whole authorize URL, unlike VK's, because the backend builds
 * it: the SPA never learns the Yandex client id or redirect URI, so those two
 * values live in one place instead of three. See internal/httpapi/auth_yandex.go.
 */
export interface YandexStart {
  state: string;
  authorize_url: string;
}

export const authApi = {
  // Mints + sets the CSRF state cookie and returns the value to echo to VK.
  vkState: () => apiFetch<{ state: string }>('/api/auth/vk/state'),

  // Confidential backend code exchange; issues a session on approval.
  vkCallback: (body: OAuthCallbackBody) =>
    apiFetch<LoginResult>('/api/auth/vk/callback', { method: 'POST', body }),

  // Mints + sets the Yandex CSRF state cookie AND builds the authorize URL to
  // navigate to. The PKCE challenge is public by design (it is the verifier
  // that is secret), so it rides the query string.
  yandexState: (codeChallenge: string) =>
    apiFetch<YandexStart>(
      `/api/auth/yandex/state?code_challenge=${encodeURIComponent(codeChallenge)}`,
    ),

  // Same confidential exchange as VK's, minus the device id Yandex has no
  // notion of.
  yandexCallback: (body: OAuthCallbackBody) =>
    apiFetch<LoginResult>('/api/auth/yandex/callback', { method: 'POST', body }),

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

// «ВАНЯДУМ». Its own block, like every game — and now three READS and nothing
// else. There is no start endpoint and no abandon endpoint, because there is
// nothing to start: the заброшка runs continuously, opening the socket and
// saying hello is walking in, and closing it is walking out. A door that could
// also be opened over HTTP would be a second door for the two to disagree about.
export const gameVanyadumApi = {
  // The catalogue: the player's dimensions, the pickups, the surfaces the client
  // generates textures from, the rates it has to match, and the building's own
  // rules. The splash screen's cheatsheet is built from this, so retuning a
  // constant on the server changes what the player is told with no deploy here.
  config: () => apiFetch<VanyadumConfig>('/api/game-vanyadum/config'),

  // The building everybody is in, with the whole level in it. Never 404s —
  // there is always one to walk into, and one is generated if nobody has been
  // here yet. Fetched once per building: the level is a few kilobytes, and the
  // `world_id` on the ready frame is what says the cached copy has gone stale.
  world: () => apiFetch<VanyadumWorld>('/api/game-vanyadum/world'),

  // The caller's own recent visits. It exists so that "the visit was written" is
  // something a person can check without opening a database.
  myVisits: () => apiFetch<{ visits: VanyadumVisitRow[] }>('/api/game-vanyadum/visits/me'),
};

// «СИМУЛЯТОР ФИНТЕХА». Its own block, like every game, and — like «ВАНЯДУМ»'s —
// deliberately only the EDGES of a shift: standing still, walking and dashing
// all travel on the socket, because they happen ten times a second.
export const gameFintechApi = {
  // The catalogue: the office's whole layout — everything solid in it and every
  // pane of glazing — plus the money ramp, the dash, the boss and both endings.
  // The splash's rules cheatsheet is generated from this, so retuning a constant
  // on the server changes what the player is told with no frontend deploy.
  //
  // FETCHED ONCE, BUT NO LONGER FOR EVER. The layout is stored and can be rebuilt
  // while a tab sits on the splash, so `office.id` is a cache key and every shift
  // response names the layout it belongs to; a disagreement means fetch this
  // again before entering play. See `adoptOffice` in GameFintechView.
  config: () => apiFetch<FintechConfig>('/api/game-fintech/config'),

  // Clocks in. 409 `shift_in_progress` when one is already going — a refusal
  // rather than a silent replacement, because dropping the old occupant would
  // throw away a shift open on another tab. 503 `office_full` when the office
  // has reached its occupant cap.
  //
  // No level comes back and none is needed: the geometry is already in the
  // catalogue, and what does come back is the ID of the layout this shift is
  // standing in — sixteen bytes rather than a room. Nothing is written to
  // Postgres here either: a shift is one row, written once, when it ends.
  start: () => apiFetch<FintechShift>('/api/game-fintech/shifts', { method: 'POST' }),

  // The shift already in progress, or 404 `no_shift`. This is the reload path:
  // after a refresh the browser has a session, a socket and no shift id.
  current: () => apiFetch<FintechShift>('/api/game-fintech/shifts/current'),

  // Walks out. Unlike «ВАНЯДУМ», this DOES write the shift — leaving is an
  // ending («ТЫ ПРОСТО УШЁЛ»), not an abandonment.
  leave: () => apiFetch<void>('/api/game-fintech/shifts/current', { method: 'DELETE' }),

  // Your own recent shifts. It exists so that "the shift was written" is
  // something a person can check without opening a database.
  myShifts: (limit = 10) =>
    apiFetch<{ shifts: FintechShiftRow[] }>(`/api/game-fintech/shifts/me?limit=${limit}`),

  // BOTH boards: the best shift per account by money, and the longest shift per
  // account. Per account rather than per row, or one good shift fills the whole
  // thing — and both in one response, because the splash draws them side by side
  // and a screen that needed two requests to render would be exactly the
  // chattiness this project's client–server rule forbids.
  //
  // Five by default rather than ten: it is a top five on a phone, above the guide,
  // and a board long enough to scroll past is a board nobody reads.
  topShifts: (limit = 5) =>
    apiFetch<FintechTopBoards>(`/api/game-fintech/shifts/top?limit=${limit}`),
};

// «АДМИН ФИНТЕХА» — the same game's control room, admin-only.
//
// ITS OWN OBJECT, AND UNDER THE GAME'S OWN ROUTE GROUP rather than beside
// `adminApi` below. A game owns everything about itself (ADR-028), so deleting
// this one stays «remove its package, its migration, its routes and its views» —
// which a floor plan filed under the account-administration surface would not be.
// The gate is the generic admin middleware, which knows nothing about any game.
export const gameFintechAdminApi = {
  // The floor in force: the room's bounds, the layout with everything standing on
  // it, where that layout came from, when it arrived, how many people are on it
  // right now, the kind taxonomy with its Russian labels, and every fixed point
  // furniture has to keep off.
  //
  // ONE REQUEST FOR ONE SCREEN. The page draws a plan, a status card and a
  // legend, and none of the three is worth a round trip of its own — a screen
  // that needed three would be exactly the chattiness this project's
  // client–server rule forbids.
  //
  // 403 `forbidden` for an approved non-admin (the router's guard turns them back
  // first, so this is the second lock rather than the first), 401 `unauthorized`
  // with no session, 503 `game_unavailable` when the game is unwired.
  layout: () => apiFetch<FintechAdminFloor>('/api/game-fintech/admin/layout'),

  // Draws a new floor, installs it, and ends every shift standing on the old one
  // with the «РЕМОНТ» ending — the salary counts and the row is written, so this
  // is destructive to somebody's shift rather than to their work. The page puts a
  // confirmation naming the live occupant count in front of it.
  //
  // NO BODY, deliberately: there is nothing to say about a floor drawn at random,
  // and a request that carried one would be a second way to install geometry.
  //
  // IT ANSWERS THE READ'S OWN PAYLOAD plus `ended`, which is what lets the page
  // redraw from the response rather than fetching again — see FintechAdminRebuild.
  //
  // 403 `forbidden` for an approved non-admin, 503 `game_unavailable` when the
  // game is unwired, 500 `internal` when the new floor could not be stored — and
  // then nothing moved, so the page keeps drawing the floor it already has.
  reroll: () =>
    apiFetch<FintechAdminRebuild>('/api/game-fintech/admin/layout/reroll', { method: 'POST' }),

  // Draws a floor and INSTALLS NOTHING — the editor's «СЛУЧАЙНЫЙ». Pressing it
  // three times costs three draws and no evictions, which is what makes trying
  // an office before keeping it a thing somebody can actually do. The response
  // carries a bare layout; the server marks it `no-store`, so a browser cannot
  // reuse the first draw and make the button appear to stop working.
  proposal: () =>
    apiFetch<{ layout: FintechAdminLayout }>('/api/game-fintech/admin/layout/proposal'),

  // What is wrong with a draft, and it changes nothing.
  //
  // 200 WITH PROBLEMS RATHER THAN A 4xx, deliberately: «this floor is not legal
  // yet» is the ordinary state of a layout somebody is in the middle of
  // dragging, so it is an answer and not a failed request. An empty array means
  // the draft would be accepted; the editor keeps «ПРИМЕНИТЬ» disabled until the
  // answer is both empty AND current, because the window in which nothing has
  // been asked yet is exactly the window a destructive button must not be live.
  //
  // `problems` IS `null` ON A CLEAN FLOOR, not `[]`. The validator answers a nil
  // slice when it has nothing to say and Go marshals that as `null`, so «no
  // problems» arrives as an absent list rather than an empty one — which reads as
  // a shape error to anything that assumes an array and would crash a `.length`.
  // Typed honestly here rather than papered over at the call site, because a
  // client that only ever saw the refusal path would never find out.
  check: (layout: FintechLayoutDraft) =>
    apiFetch<{ problems: FintechLayoutProblem[] | null }>('/api/game-fintech/admin/layout/check', {
      method: 'POST',
      body: layout,
    }),

  // Installs a floor somebody drew. The same install path the rebuild button
  // uses, so a hand-drawn office is held to exactly the rules a generated one is
  // and ends exactly the same shifts — which is why the page puts the same
  // confirmation, naming the live occupant count, in front of it.
  //
  // It answers the READ's payload plus `ended`, exactly as the rebuild does, so
  // the page redraws from the response rather than fetching again.
  //
  // 422 `layout_invalid` when the floor is refused — and then NOTHING MOVED, so
  // the page keeps drawing the floor it already has. The problems ride the error
  // body (see ApiError.body), because a code alone cannot say which desk.
  save: (layout: FintechLayoutDraft) =>
    apiFetch<FintechAdminRebuild>('/api/game-fintech/admin/layout', {
      method: 'PUT',
      body: layout,
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

  // superadmin-only; 403 otherwise. IRREVERSIBLE.
  //
  // Anonymises the person and keeps what they wrote: their ideas, the comments
  // other people replied to, their votes and their leaderboard times all stay,
  // authored by an anonymous `psycho-…` that links nowhere. The same VK account
  // logging in again becomes a brand-new pending account, because the blind
  // index that used to match it is gone. 409 `already_forgotten` if it has
  // already happened.
  forget: (id: string) => apiFetch<void>(`/api/admin/accounts/${id}/forget`, { method: 'POST' }),

  // superadmin-only; 403 otherwise. Returns the applied state.
  setOpenRegistration: (enabled: boolean) =>
    apiFetch<AdminSettings>('/api/admin/settings/open-registration', {
      method: 'PUT',
      body: { enabled },
    }),
};
