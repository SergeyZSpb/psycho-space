# ADR-027 · The client IP comes from `X-Real-IP`, trusted only from a loopback peer

## LLM Continuation Context

_Machine-oriented recap for an LLM continuing this work. Written for agents, not humans — optimise for hand-off, not prose. Keep current with the doc._

- **topic:** The client IP comes from X-Real-IP, trusted only from a loopback peer
- **status:** Accepted · 2026-07-25
- **summary:** one paragraph in [ARCHITECTURE.md §8.7](../ARCHITECTURE.md#adr-027--the-client-ip-comes-from-x-real-ip-trusted) — this file is the detail behind it.

---

`clientIP` supplies the key for every per-IP rate limit. It reads `X-Real-IP`, and **only** when the request's own TCP peer is a loopback address; a request that arrived any other way, or one whose `X-Real-IP` is missing or unparsable, is keyed by that peer address instead. `X-Forwarded-For` is never consulted, and chi's `middleware.RealIP` is deliberately not installed.

_Reasoning:_ nginx passes `X-Forwarded-For: $proxy_add_x_forwarded_for`, which *appends* the peer to whatever the client already sent, so the header's leftmost entry is attacker-controlled. `middleware.RealIP` trusted exactly that entry and overwrote `r.RemoteAddr` with it — which made every per-IP limit forgeable by varying one header per request, the login limiter and the limiter guarding the paid LLM endpoint included. `X-Real-IP` is safe in the same position for two reasons that have to hold together: nginx sets it from `$remote_addr`, overwriting whatever the client sent, and the loopback check means a value that reached the app by any other route is not believed.

_Consequence:_ the limits are only meaningful while the app sits behind that proxy, which is already the deployment (it listens on loopback). Both halves are pinned by tests, because the failure is silent: `TestClientIPTrustsProxyHeaderOnlyFromLoopback` covers the trust rule, and `TestRateLimitNotBypassableByForwardedHeader` drives a client rotating `X-Forwarded-For` and requires it to still be counted as one client.
