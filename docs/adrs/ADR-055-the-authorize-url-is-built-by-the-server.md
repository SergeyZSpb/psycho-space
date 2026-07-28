# ADR-055 · The authorize URL is built by the server wherever the provider allows it

## LLM Continuation Context

_Machine-oriented recap for an LLM continuing this work. Written for agents, not humans — optimise for hand-off, not prose. Keep current with the doc._

- **topic:** why `GET /api/auth/yandex/state` returns a finished `authorize_url` rather than letting the SPA assemble one, and why VK cannot do the same.
- **status:** Accepted · 2026-07-28
- **summary:** one paragraph in [ARCHITECTURE.md §8.2](../ARCHITECTURE.md#82-identity-and-personal-data) — this file is the detail behind it.
- **related:** ADR-001 · ADR-054 · RUNBOOK → "the redirect URL, and what a 405 means"
- **code:** `internal/httpapi/auth_yandex.go` — `handleYandexState` and `validCodeChallenge` · `internal/yandex/client.go` — `AuthorizeURL` · `internal/config/config.go` — `Yandex`, which is where the client id lives and the only place it does
- **re-examine when:** a third provider arrives (does it need a browser SDK?), or somebody proposes putting the Yandex client id in `web/src/constants.ts` for symmetry with VK. Symmetry is the wrong goal here.

---

For Yandex, the SPA asks the backend for a login state and gets back both the `state` and the **entire authorize URL**, already carrying `client_id`, `redirect_uri`, `state`, `code_challenge` and `code_challenge_method`. It navigates to what it was given. The client id and the redirect URI exist in exactly one place: the server's environment.

_Reasoning._ The most expensive recurring failure this system has had is a string that must agree byte for byte in three places at once. VK's redirect URI lives in `web/src/constants.ts`, in `PSYCHOSPACE_VK_REDIRECT_URI`, and in the VK dashboard; when the three drifted, every login broke, and the failure was a bare `405` with no clue in it. `RUNBOOK.md` has a section about diagnosing exactly that. Yandex needs no browser SDK — the browser only has to be sent somewhere — so the copy in the SPA is not required, and a copy that is not required is a copy that will eventually be stale. Two must-agree strings instead of three, and the one that cannot be wrong is the one a cached SPA bundle would have carried.

_Why VK does not do this._ The VK ID SDK constructs its own authorize URL inside the browser and needs the app id and redirect path to do it. Forcing symmetry would mean either abandoning the OneTap widget — which is the good path for most users, and the one VK's own apps hit — or shadowing the SDK's URL with our own, which is two sources of truth wearing a disguise. So the asymmetry is deliberate and is documented at both ends: `handleVKState` says why it returns only a state, and `handleYandexState` says why it returns more.

_The challenge travels as a query parameter, and that is fine._ PKCE splits into a secret verifier and a public challenge; only the verifier must never leave the browser. The challenge is generated client-side and sent up so the server can put it in the URL. It is still validated before being interpolated — RFC 7636 shape, 43–128 unreserved characters — because it is being echoed into a URL handed to a browser, and "it is public" is not the same as "it is safe to echo unchecked".

_Consequence._ The Yandex state endpoint does two things rather than one, which is a small loss of symmetry with the VK endpoint beside it. That is the trade: a slightly less uniform pair of handlers, against removing a whole class of production outage from one of them. It also means the Yandex flow cannot be driven at all without the server — there is no way for a stale or hostile client to invent an authorize URL that this application would later accept a code from, because the `redirect_uri` at the token exchange is read from config and never from the request.
