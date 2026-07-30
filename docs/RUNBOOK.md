# psycho-space — Ops / Debugging Runbook

## LLM Continuation Context

_Machine-oriented recap for an LLM continuing this work. Written for agents, not humans — optimise for hand-off. Keep current with the doc._

- **topic:** operational runbook for the psycho-space production box (SSH, logs, DB, nginx, TLS, admin bootstrap).
- **status:** written during P0/P1. Server is provisioned by `scripts/bootstrap.sh`; deploys via `.github/workflows/deploy.yml`.
- **host/port:** intentionally NOT recorded here — this repo is public. The real host and hardened SSH port live only in the GitHub `prod` environment secrets (`DEPLOY_SSH_HOST`, `DEPLOY_SSH_PORT`) and in the operator's local `~/.ssh/config` (+ the local living doc `~/Desktop/psycho-space/psycho-space.md`). Use the `psycho` ssh alias below.
- **app:** systemd unit `psycho-space` under user `psychospace`; binary `/opt/psycho-space/psycho-space`; env `/etc/psycho-space/app.env`; logs `/var/log/psycho-space/app.log`.
- **code:** service in `cmd/psycho-space` + `internal/*`; deploy assets in `deploy/`; provisioning in `scripts/bootstrap.sh`.
- **local-dev:** see "Local development (game / backend)" below — `docker-compose.yml` (Postgres), `./dev.sh db-up|run|seed`, Vite on :5173. `cmd/dev-seed` mints a local approved session (VK can't run locally). Game section: LLM-judged (`internal/gamekhimki/llm.go`, OpenAI-compatible), content/persona in `content.go`; requires `PSYCHOSPACE_LLM_*` env to play (else `/attempt` → 503).
- **game 2 («Ванягоччи»):** package `internal/gamevanyagotchi/`, tables `game_vanyagotchi_pets` / `_pet_stats` / `_world_objects` (`migrations/008_*`), routes `/api/game-vanyagotchi/*` — **two reads (`config`, `state`) and nothing that writes**, because a verb travels over the socket as a `vanyagotchi_do` frame and cannot be curl'd (ADR-043) — view `GameVanyagotchiView.vue` at `/app/game-vanyagotchi`. **No LLM on any path** — it costs nothing to run. Debugging it is unlike game 1 in two ways: nothing runs on a timer, so a stat's stored `(value, as_of)` is *not* what the screen shows and moving `as_of` is how you fast-forward; and **health is a consequence, not a timer** — it drains 1/hour on its own and +6/hour for each unmet need (`beer` ≤ 20, `bladder` ≥ 80), so read those two before diagnosing a dying pet. Every hand-written stat `UPDATE` must touch **all** rows with one `as_of`, or the coupling loses damage (ADR-040). See "Working on «Ванягоччи» (the pet)" below. Rates, thresholds, labels, the cast and every phrase pool are in `content.go`, not the database — so retuning, renaming, adding a regular or adding a line is a backend deploy with no migration and no client change. The **balloons** over a Ваня's head come from **five disjoint pools** (a test enforces the disjointness, so a line can only ever mean one thing) and they say different things about him: `tiredSays` — he gave up on a walk; `idleSays` — he is just standing about; `shySays` — he lost his nerve and the verb did nothing; `reekSays` — **somebody else** relieved himself near him; `enviousSays` — **somebody else** found the keys. The last two are the only lines another player's action puts in your mouth, and the regulars get them too. Idle muttering is closed-form on 12-second slots from `worldEpoch` (no timer, nothing stored, per-process key), so it changes across a restart and is silent for anyone walking, dead or asleep; the two reactions are held in memory with an expiry and dropped by the tick that finds them stale.
- **game 4 («Симулятор финтеха»):** package `internal/gamefintech/`, table `game_fintech_shifts` (created by `migrations/013_game_karen.sql`, renamed by `014_game_fintech_rename.sql`), routes `/api/game-fintech/*`, realtime room `fintech`, view `GameFintechView.vue` at `/app/game-fintech`. **It was «СИМУЛЯТОР КАРЕНА» until 2026-07-30**, so anything older than that — a log line, a saved query, a bookmark — uses `gamekaren` / `game_karen_shifts` / `/api/game-karen/*` / room `karen` / the `karen_*` frame types. **013 keeps its old filename on purpose** (the migrator keys `schema_migrations` on it, so a renamed 013 re-runs and recreates an empty `game_karen_shifts`), and the `game_key` value is still `karen` because a `game_key` value is data, not a name. **No LLM on any path.** Debugging it is unlike every game above in one way that dominates: **almost nothing is stored.** The office, the positions, the boss, the streak and the salary live in process memory and are lost on any restart; Postgres gets **one summary row when a shift ends** and nothing else, so there is no table to look at while somebody is playing. A shift shorter than `MinShiftSeconds` is deliberately **dropped rather than written**, which is the first thing to check when somebody swears they played and there is no row. `cause` is `promoted` (the лысый reached you) or `left` (you walked out, or your socket was gone past the abandon grace) — plain `text`, so a later ending is not a migration. Every rule — the static office and its desks, the walk and dash speeds, the base rate, the ×1→×3 ramp, the grace window, the boss's speed and catch radius, the ending titles — is in `content.go`, **served** at `GET /api/game-fintech/config`, and the splash screen's cheatsheet is generated from that same payload, so the rules on screen cannot drift from the rules enforced. Playing happens entirely on the socket (`fintech_input` up, `fintech_snap` down at 10 Hz) and cannot be curl'd; the HTTP surface is only the edges of a shift. **AND TWO PEOPLE WHO ARE NOT PLAYING** — Серега and Тёма. They walk about at random, each carrying his own кальян with a cloud that never goes out, and they say what they think of your branch. That is all they do: no seeking, no arrival, no state. They are **not in the occupant map**, which is the whole of their safety — anything in that map becomes a chase target, a snapshot addressee and a slot against the three-player cap, so an NPC in there would let a lazy player be saved by scenery. They take no slot, neither man walks at them, they buff and debuff nobody, and they **never touch the office's кальян**: an earlier version had them walking to it, which was interference, because the prop is a first-taker and an NPC on his way to it was competing with a player for the strongest effect in the game. Their positions are stepped on the SERVER and ride `np` (never omitted); their cloud is not on the wire at all, because it is permanent. **They have no navigation on purpose** — they amble, and sliding along a desk is what an amble looks like — so the thing that keeps them moving is `NPCGiveUpSeconds`: a target they cannot reach is abandoned after fourteen seconds. Without it they walk at it forever, because the resolver holds them off the furniture and they never get close enough to choose again. Both of them were permanently stuck in production for exactly that reason, and the draw now also rejects a spot inside a desk. **If one of them stops moving**, that pair of mechanisms is where to look, and `TestTheNonPlayersDoNotGetStuck` is the test that measures it — distinct places visited over ninety seconds, because a man grinding along a desk edge is moving and still stuck. **If they are missing from the office**, check the served catalogue first — `GET /api/game-fintech/config` must carry a non-empty `npcs` array, and the client draws nothing at all without it. That failed in production once: the assignment never reached `BuildConfig`'s struct literal, the JSON said `"npcs":null`, and the test that was supposed to catch it only checked that the KEY appeared. **THERE ARE TWO MEN ON THE FLOOR.** The лысый ends your shift; **Claude Code** slows you down — `SlowFactor` of your walk for `SlowSeconds` — and never ends anything. He walks at exactly the лысый's speed (deliberately: `TestASlowedWalkStillOutrunsThem` pins both that and the fact a slowed walk, 5.12 m/s, still beats their 4.0), he is **not redirectable** by the verb, and the slow **does not stack** — assigned, never accumulated, because two applications would leave 4.096 against their 4.0 and three would make the game unwinnable. His frame field `cl` is the one thing in this game that is **never omitted**, since he is always on the floor; the slow rides `sl` on your own frame and `pr[].sl` on a colleague's. **`SlowLeft` is the only effect on `Player` rather than on the Occupant** ([ADR-058](adrs/)), because it multiplies the walk and therefore has to be predicted — which is why this is the one iteration that regenerated `testdata/step_vectors.json`. If a figure starts stuttering after a change here, the first suspect is the reconcile spread in `fintechPredict.ts`: a predicted timer only advances when a command is emitted, and a still player emits nothing. **There is a КАЛЬЯН as well as a bottle, and they do opposite things.** The bottle acts on the лысый (he drinks, goes green and staggers); the кальян acts on YOU — walk onto it and you are behind a cloud for `InvincibleSeconds`, during which he **cannot see you and cannot catch you**. Mechanically that is an *exclusion from his target list*, not a shield: excluding him from walking at you is what makes the reprieve buy distance, since he stops on arrival and a mere catch-guard would leave him standing on you when the cloud cleared. Two consequences worth knowing when debugging: with the only other occupant hidden he **switches to whoever is left** and will catch an idle colleague within a few seconds, and **being uncatchable is not being immortal** — the abandon path still records a shift whose tab was closed, because that guard is on the caught case alone. Neither the cloud nor the кальян is stored; both are in-memory like everything else here, and the frame carries `iv` (yours), `pr[].iv` (a colleague's — a buff only its owner can see is unfinished), `hk` and `hs`. **A shift has a PERSONA** — an index into the catalogue's `personas` (`Карен`, `Андрюха`, `Саня`, `Даша`), drawn with `crypto/rand` when the shift starts. It lives on the in-memory `Occupant` and **never on `Player`**, because `Player` is pinned to its TypeScript port by golden vectors and a field the simulation does not read would force that artefact regenerated for nothing. It rides the two shift responses and the `fintech_ready` frame, **never a snapshot** — it cannot change during a shift — and it is **not stored**: `game_fintech_shifts` has no persona column, so a finished shift does not remember who worked it. All a persona changes is the line a figure says when it introduces itself; `Карен` is index 0 and must stay there, because an omitted index, `introLine` and `player_lines[0]` all mean him. **Faces come from two different places on purpose**, which is the first thing to know if one of them is missing: a COLLEAGUE's face is fetched by his pseudonym from `GET /api/game-fintech/avatar/{peer}`, which 302s to the provider CDN and answers **404 for anybody with no picture** — the ordinary reply, drawn as a plain figure — while YOUR OWN comes straight from `avatar_url` in the session (`GET /api/auth/me`), because the server never sends you your own handle and there is nothing to withhold from you about yourself. So "I can see everyone's face but mine" means the account has no `avatar_url` (every Яндекс account, and every forgotten one), and "I can see mine but not theirs" means the redirector or the CDN, not the game. Neither ever rides a frame. **Every defect it has had so far has been the client and the server disagreeing about how far something moved**, and none of them was found by reading the code — see "«It teleports / stutters / doesn't move» — measuring a netcode complaint" for the two procedures that did find them (drive the real stack from `web/e2e-stack/` and record the socket frames beside the drawn CSS position; or track the figure in the owner's phone recording, calibrating metres per pixel off the desks). See "Working on «Симулятор финтеха» (the office)" below.
- **naming:** game 1 is `GameKhimki` everywhere — package `internal/gamekhimki/`, table `game_khimki_runs` (art stays in the shared `game_assets` — ADR-031), routes `/api/game-khimki/*`, view `GameKhimkiView.vue` at `/app/game-khimki`. It was generic `game`/`game_runs`/`game_assets`/`/api/game/*` until `migrations/007_game_khimki_rename.sql`, so **anything older than that — a log line, a saved query, a bookmark — uses the old names.** `game_key` values are unchanged (`smalltalk_khimki`). Rule: `ARCHITECTURE.md` → ADR-030.
- **siblings:** `ARCHITECTURE.md` (the shape of the system — logical/runtime/data/deployment views — plus §8, one paragraph per decision record saying why it is that shape, each rewritten in place when the decision moves), `../CLAUDE.md` (working rules and gates).
- **login:** two providers, VK ID and Яндекс ID, sharing everything after the exchange. Redirect URLs are SPA **pages**, never API endpoints: `/auth/redirect` (VK) and `/auth/yandex/redirect` (Yandex). VK needs **three** copies of its URL to match byte for byte (SPA `VK_REDIRECT_PATH`, `PSYCHOSPACE_VK_REDIRECT_URI`, the VK app list); Yandex needs only **two** because the server builds its authorize URL (ADR-055). A **405 on either `/api/auth/*/callback`** means something points at the API again. See "Login — the redirect URL, and what a 405 means".
- **webgl:** `./dev.sh` runs both Playwright suites with `DISPLAY` unset (`playwright_`), because Chromium's ANGLE picks Vulkan/XCB from a set-but-unreachable `DISPLAY` and then exits the GPU process instead of falling back to SwiftShader — which silently removes WebGL, sends «ВАНЯДУМ» down its no-3D production path and times out every spec that clicks `vanyadum-start`. The first test in `web/e2e/gamevanyadum.spec.ts` asserts 3D is present, so this fails by name rather than as twenty unrelated timeouts. Details + the ten-second diagnosis: "The layout suite fails only on «ВАНЯДУМ»".
- **flaky-tests:** see "A test that passes on its own and fails in CI". Every flake found so far was a defect in the *test*, of exactly four kinds — a loop bounded by an attempt count rather than a deadline; two round trips reading one 4-second speech balloon; an implicit 5 s `expect` shorter than that balloon plus a round trip; and a fixture that *plays the game* to reach its starting position (walking to a randomly-placed crate ate most of a 120 s budget, and surfaced as a bare `Test timeout exceeded` — the same kind bites again when a caller's budget contradicts the helper it calls, which is what broke CI on 2026-07-29). Reproduce by saturating the CPU (`nproc − 1` spinners) and running the one spec; CI's only difference is that it is slower. **Never** fix one with `retries`, and never with an env flag that disables a game's random rolls — that is test-only machinery in a production path. Determinism comes from direct DB setup (`web/e2e-stack/vanyagotchi-db.ts`).
- **next:** keep this current as ops procedures are exercised; add a section whenever you work out a new procedure (read-before / write-after).
- **constraints:** never commit the host/IP/port or any secret; never paste real personal data into shared places. The app log is PII-free by design; the DB and nginx access log are not — treat their contents as confidential.

---

**Do not put the host or SSH port in this file (public repo).** Configure a local `psycho` alias once; every command below uses it.

```
# ~/.ssh/config  (LOCAL, not committed) — fill from your prod secrets / living doc:
Host psycho
    HostName <server-ip-or-psycho-space.ru>
    User deploy
    Port <your-hardened-ssh-port>
    IdentityFile ~/.ssh/id_ed25519_psycho
```

The dev/admin access below is for observability/debugging; production changes go through CI.

## Local development (game / backend)

Full local loop: Postgres in Docker, the Go server, and the Vite dev server with hot reload.

```bash
# one-time
mise install
cp .env.example .env         # then fill the 3 keys: openssl rand -base64 32

# every session
./dev.sh db-up               # local Postgres via docker compose (data persists in a volume)
./dev.sh run                 # Go server on :8080 (API + embedded SPA; auto-migrates on boot)
# second terminal — hot-reloading frontend:
cd web && mise exec -- npm run dev   # Vite on :5173, proxies /api + /healthz to :8080
```

Open <http://localhost:5173>.

### Get into the gated app without a real login

Neither provider can be driven from a workstation: VK ID is IP-allowlisted to prod, and both redirect URIs are the prod domain. Seed an approved account + session instead:

```bash
./dev.sh seed                          # superadmin "Локальный Разработчик"
./dev.sh seed -role user -name Гость   # a plain approved user
./dev.sh seed -provider yandex -provider-id 42   # a Yandex identity, for the two-provider paths
```

The identity is `(provider, provider-id)`, so seeding the same `-provider-id` under each provider gives you two distinct accounts — which is the state worth having locally when touching anything in the account package.

It prints a `psycho_session` cookie value. In the browser (DevTools → Application → Cookies) add `psycho_session=<value>` for the origin you use (`http://localhost:5173`) and reload — you land in `/app`. Or hit the API directly: `curl -b 'psycho_session=<value>' http://localhost:8080/api/auth/me`.

`dev-seed` reuses the real `crypto`/`account`/`session` packages (so hashing + encryption match prod exactly), refuses to run unless `PSYCHOSPACE_ENV=dev`, and is never built into the server binary or deployed.

### Working on the game («Смолтолк в Химках»)

The model choice itself — DeepSeek V4 Flash, and why `reasoning_effort` is off — is recorded in [ARCHITECTURE.md → ADR-029](ARCHITECTURE.md#adr-029--the-judge-runs-on-deepseek-v4-flash). The measurements, prices and per-turn economics below are this file's to own and to re-measure.

It's an **LLM-judged** character dialogue: convince дядя Ваня (a strange, on-edge drunk who won't let you pass) to let you into your own entrance. To win you must genuinely work through **all three** of his deep themes — longing for a woman/children, his friendship with Тунг Тунг Сахур, and his drinking — before he warms up and lets you through. Each turn the LLM replies in character, judges whether the goal is genuinely reached, picks an **art** to show (his changing mood, or a story/location art with no character — e.g. the passage into the entrance), and generates the **next answer options** (always 4 while playing). The game **requires an LLM** — no mock; unconfigured → `/attempt` returns 503.

> **The third theme is alcohol on purpose — do not make it drugs.** It used to be substance use, and YandexGPT's content filter answered those turns with plain Russian prose instead of the JSON we ask for; the parse failed and the player got an error. The tell in the log is a **`game llm reply not json`** line **~100 ms** after its `game llm request` (real generations take 1–2.5 s). `TestContentAvoidsDrugFlavouredPrompts` guards the prompt material against regressing.
>
> **Three outcomes when the reply isn't clean JSON — check the status code first:**
>
> | Symptom | Status / code | Meaning |
> |---|---|---|
> | Reply was *nearly* valid JSON — raw newline inside a string, options array closed early with the rest under a junk key | **200**, `game llm reply salvaged` at Warn | Recovered: the turn goes through. YandexGPT 5 Lite did this a few percent of the time; DeepSeek rarely does, but the salvage path stays because a bad turn costs a player their move — see *Salvaging* below. |
> | Reply is unrecoverable — prose (filter refusal), empty `reply`, truncated | **422 `llm_unparsable`** | Not an outage. The same line fails again, so the SPA explains it in Russian and asks the player to pick a different option. |
> | Transport error, non-200, or no choices at all | **502 `llm_error`** | The provider is down / misconfigured. |
>
> The 422 path logs everything at Error in one line — `game llm reply not json` with `content`, `raw_response`, `finish_reason`, `parse_err`, token usage, latency, `choice`, `account_id`, `trace_id` (both bodies clamped to 2000 runes). Find one by trace id — **every** log line carries it, not just `http_request`:
>
> ```bash
> ssh psycho 'sudo grep <trace-id> /var/log/psycho-space/app.log' | jq .
> ```
>
> **Salvaging** (`salvageJudgeReply` in `llm.go`): raw control characters inside string literals get escaped, unknown keys are ignored, and answer options parked under junk keys are recovered up to `optionCount` (junk keys visited in sorted order, so recovery is deterministic). A salvaged turn logs `salvaged=true` on its `game llm response` line, so `sudo grep '"salvaged":true' /var/log/psycho-space/app.log | wc -l` gives the rate. If that rate climbs, the answer is a better prompt or a bigger model — not more salvaging.

- **Дядя Ваня answers in RAP** — 2-8 short rhymed bars, one per line, under ~45 characters each so a bar does not wrap on a phone. `TalkStyle` demands rhyme but subordinates it to meaning, and the static `Greeting` is already in verse so the first thing a player sees matches everything after it. Profanity is deliberately NOT restrained: the content-filter refusals were a YandexGPT behaviour and this runs on DeepSeek, so he swears in character.
- **Verse survives the round trip, two guards.** The SPA renders with `white-space: pre-line`, and `normalizeVerse` converts literal escape TEXT into real newlines. The second guard is not theoretical: shown the escape sequence in the prompt, the model typed it as two characters, which rendered visibly and wrapped four bars into six lines. The prompt no longer shows it the escape, and the normaliser handles it regardless.
- **A verse too tall for its box ROLLS like post-credits** (`web/src/lib/gameKhimkiCredits.ts` + `.verse-reel` in `GameKhimkiView.vue`). He raps up to 8 bars and the box is deliberately short - the play screen must never scroll on a phone - so instead of clipping (the old `-webkit-line-clamp: 3` silently ate everything past three lines) or demanding a scroll gesture mid-game, the verse loops upward continuously. The text is rendered **twice** and the reel is translated by exactly **-50%**, so the second copy lands where the first began and the loop has no seam; `creditsDuration` holds a constant ~22 px/s reading speed whatever the length. It engages only on real overflow (>2px, since sub-pixel line heights leave noise), and `prefers-reduced-motion` falls back to a scrollable box. Verified in a real browser at 320/360px: rolling, two copies, transform actually moving, page not scrolling.
- **The box cap is `min(8.5em, 15dvh)`** - the `dvh` term is load-bearing: a fixed `8.5em` overflowed 320x568 by 27px and pushed the OPTIONS off-screen, a worse bug than the one being fixed.
- **Three endings.** `achieved` → he lets you in (`hallway_pass`). **`game_over`** → you pushed him too far, he throws a punch and the run is lost (`vanya_game_over_hits_us`, forced server-side; the SPA shows an `error` alert titled «Game over»). Neither → the dialogue simply ran out of options. `achieved` always wins over `game_over` on the same turn.
- **The tension scale is the balance mechanism** (`MaxAnger` 100 = the scale the judge is given, **`AngerLoseAt` 90 = the kill line**, `StartAnger` 40 — he opens hostile; all published in `GET /api/game-khimki/config` as `max_anger`/`anger_lose_at`/`start_anger`, and the SPA fills the bar to `anger_lose_at` so a full bar is the punch). Cross-turn state the client carries: the SPA **sends** `anger` with each `/attempt`, the judge **returns** the new value, raising it 10–25 for rudeness/pressure/treading water and lowering 5–15 for genuine warmth. **At `AngerLoseAt` the backend ends the run itself**, whatever the model said about `game_over`. The kill line is 90, not 100, because measured against the real model the scale crawled 85 → 90 → 95 → **95** and stalled below its own ceiling, so the run never ended. Server guarantees: incoming value clamped, an **omitted** `anger` drifts **up by 5** rather than stalling, a win beats the kill line on the same turn. Logged as `anger_in`/`anger_out`/`anger_from_model`.
- **Options are asked for by ROLE, not "four varied options"** — slot 1 opens a still-closed theme, 2 is warm, 3 is rude (so losing is always reachable), 4 is neutral/unexpected. Measured cause: asking for variety produced four lexically different ways to say «давай поговорим» (pairwise word similarity 0.04 — "diverse" by any metric, one move in practice).
- **Theme coverage is tracked, never shown.** `Character.Themes` (server-only keys + labels) and `themes_done`, carried by the client like `anger`: the judge reports which deep subjects are genuinely open, and the prompt names the ones still closed so slot 1 has somewhere to steer. Measured cause: without it, alcohol appeared in **1 of 40** offered options and the three-theme win was practically unreachable. Deliberately **not rendered** — a visible checklist would play the game for the player — and deliberately **not** the win condition, so a tampering client cannot award itself the ending (`achieved` stays the judge's read of the dialogue). Logged as `themes_done`/`themes_from_model`.
- **His own recent lines go back too** (`recentReplies`, 6 newest, clamped) with "don't reuse these openings". Measured cause: «Ты меня не знаешь…» ×4 and «С чего ты взял, что…» ×4 in a single eight-turn run.
- **Quoted numbers and bools are accepted** (`flexInt`, `flexBool`): a complete turn was lost in prod to `"anger": "35"`. 42, "42", 42.0 and true, "true" all parse; real junk still errors so the salvage path can see it.
- **Model + prompt economics** (`internal/gamekhimki/llm.go`). Model names come from the endpoint itself: `curl -H "Authorization: Bearer $KEY" $BASE/models` lists e.g. `gpt://<folder>/deepseek-v4-flash/latest`. Prices (Yandex AI Studio, per 1k tokens): **yandexgpt-5-lite 0.2 in / 0.2 out**, **deepseek-v4-flash 0.3 in / 0.5 out / 0.075 cached in**. `modelPrices` keeps input and output separate — a blended rate misreports every turn when output costs 1.67x input — and an unlisted model logs NO `est_cost_rub` rather than a wrong one.
- **Context window is measured, not assumed**: an oversized request answers `This model's maximum context length is 1048576 tokens`. Cyrillic tokenises at **~1 token per character** on this model (1,200,000 chars -> 1,200,004 tokens), so `estTokens` counts runes; the old runes/2 estimate under-counted Russian 2x. **Careful probing this:** an oversized *Cyrillic* request is rejected free, but Latin compresses ~8:1, so a 3M-character Latin probe fits under the limit and gets billed (375k tokens, ~112 RUB — a real mistake, don't repeat it).
- **Prompt caching works, and the prompt is laid out for it.** Same API, same key, no flag: `usage.prompt_tokens_details.cached_tokens` reports it, and cached input bills at a quarter of the rate. Requirements: (1) the cacheable part must be a **prefix**, and (2) it must be **append-only** between turns. So the layout is **static system prompt -> history -> one volatile message last**. Anything per-turn in the system prompt invalidates everything after it, including the whole transcript — the tension value used to sit there.
- **History is replayed as the JSON the judge returned** (`judgeReplayJSON`), so the conversation reads to the model as a series of correctly-formatted examples of its own output. `Exchange` therefore carries `art`, `anger`, `options` and `themes_done` — an example missing a field teaches the model to omit that field. This replaced an earlier bracketed footer (`[напряжение: 55; предлагал: …]`), and the reason is worth keeping: **the model imitates whatever format it sees.** Shown prose history plus that footer, it answered in prose with the footer and no JSON at all (trace `c771c3ed23c4fba6f2a0b439f3862a90`, turn 11 of a good run, `finish_reason: stop`); before that it answered in pure roleplay with stage directions, ignoring `response_format` entirely. Restating the JSON contract in the tail — right where the model acts — fixed it. JSON replay costs ~0.08 RUB/turn more than prose and is worth it: each unparsable reply wastes a whole turn's tokens *and* the player's move.
- **Cost control**: history capped at `historyExchanges` 12 / `historyTokens` 6000 (was effectively ~114 exchanges), so a long game's turns cost the same as turn 12 instead of growing; `maxCompletionTokens` **1500** caps the answer, which matters most because output is the dearest rate. One turn is **~0.59 RUB** on deepseek-v4-flash after the prompt trim and reasoning being turned off (vs ~0.36 on lite) — DeepSeek costs more, bought with visibly better play (it produced the first winning run in any audit). The `/attempt` limit is **5/min per IP** (halved from 10 when the model moved to DeepSeek, since a turn costs ~2x there); there is no per-account cap, so one determined player is still the real cost exposure.
- **Theme steering, and the loop it used to create.** The first option slot aims at a still-closed theme, which in prod produced a **runaway**: once two of three themes were marked, the slot pushed the last one every turn, the conversation became that one subject, the judge never marked it — and **15 of 20 option sets had all four options on the same topic**, with the run unwinnable. Five interlocking guards now:
  1. **`Theme.Keywords` + `themeEngagement`** measure, over the WHOLE run (not the context window), how many exchanges actually engaged each theme.
  2. **`autoMarkThemes`** opens a theme the conversation engaged `themeAutoDoneTurns` (4) times regardless of what the judge says — this is what breaks the loop. It only ever *releases* steering; the win stays the judge's call, so keyword-spamming cannot win a run.
  3. **`confirmThemes`** rejects a claim with fewer than `themeConfirmTurns` (2) engaged turns behind it — the judge marked `sahur` open on turn one before the friendship was mentioned. Already-open themes stay open.
  4. **`steerTheme`** aims the first slot at the **least-engaged** open theme, and returns false once every remaining theme is already in play, which drops the requirement instead of hammering it.
  5. **Slots carry subjects, not just tones**: slot 2 is the current topic, slot 3 may leave it, **slot 4 must be a different topic** (the tail names the current one via `currentTopic`), plus a hard rule of **≤2 options per topic, ≥3 distinct topics of 4**.
  Logged as `theme_engagement` on every `game llm response`. Re-measured after: **2–3 distinct topics per set** where prod had 1.
- **Reasoning is OFF (`reasoning_effort: "none"`).** deepseek-v4-flash is a reasoning model: it emits `reasoning_content` billed as **output**, the dearest rate. Twice it spent the entire `max_tokens` budget reasoning and returned **empty content** (`finish_reason: length`, 1500 completion tokens, 0 characters of reply) — a lost turn, billed in full. Turning it off removed that failure class and cut output from ~400–570 to ~220 tokens/turn. `thinking` and `enable_thinking` are rejected by this endpoint; `reasoning_effort` is the supported knob. The judging task is rule-following, not puzzle-solving, and a measured playthrough with it off was the best of the night: all three themes marked by turn 4, avg 2.2 distinct topics per option set, no unparsable replies.
- **The system prompt is kept terse on purpose** — it is the cached prefix *and* the biggest fixed cost of every turn. Trimmed 6283 → 4463 characters (−29%) by compressing wording and removing the theme enumeration that `Character.Themes` already supplies. Every rule in it was earned by a measured failure: compress the wording, never drop a rule.
- **Cost trajectory this matters for** (same model, same game): 1.04 RUB/turn → 0.85 (cache layout + JSON replay) → ~1.1 (theme steering grew the prompt) → **0.59** (prompt trim + reasoning off).
- **`maxCompletionTokens` is 1500, not 900.** Four role-differentiated options make the model write much more than a bare reply did, and 900 truncated it mid-JSON (`finish_reason: length`, whole turn wasted and billed). A truncation is now reported as such — otherwise the log reads `unexpected end of JSON input` and the next reader hunts a formatting bug that isn't there.
- **Auditing the option quality**: `options_text` + `already_offered` + `themes_done` are on every `game llm response` line. To replay a judged conversation against the real model, run the app locally with `.env` (the real LLM key) and drive `/api/game-khimki/attempt` — the throwaway driver pattern is in the living doc's analysis notes. Beware the **5/min per IP** limit on `/attempt`.
- **Mobile fit of the scale**: one 12px caption row plus a 5px bar ≈ **19px**, taken out of the art (`.stage > .portrait-pane` is `flex: 1 1 auto; min-height: 0`), never out of the options. Verified with headless Chrome at 320/360/390/414px — no scroll, all 4 options on screen.
- **Character profile is backend config**: `internal/gamekhimki/content.go` — `Character` carries public bits (`name`, high-level `goal`, static `greeting` + `opening_options`, and the **`Arts` catalog**: each art's `emoji`/`gradient`/`image`) plus server-only judge material (`Objective` = the real win condition and `Failure` = what makes him snap, both kept off the client so they aren't spoiled; `GameOverArt`; `Motivation`/`Persona`/`TalkStyle`). **Opening is static** — the greeting + first options render with no LLM call; the judge takes over from the player's first pick (the greeting is seeded into the model's context). Subsequent options are LLM-generated. Edit + restart `./dev.sh run`; the SPA fetches `GET /api/game-khimki/config`.
- **Assets resolve from the backend catalog** — `Character.Arts`. The judge returns an art *key*; the SPA renders the matching descriptor. Adding/altering arts is backend-only; no client change.
- **Turns are judged by the LLM** in `internal/gamekhimki/llm.go` (`openAIEvaluator`, OpenAI-compatible: Yandex Cloud / DeepSeek). `POST /api/game-khimki/attempt {game_key, character_key, transcript:[{choice,reply,options}], choice, anger}` → `{reply, art, achieved, game_over, anger, options[]}` (`choice:""` = opening turn). The transcript is trimmed to the **history budget** — `historyTokens` 6000 / `historyExchanges` 12, whichever binds first — and older exchanges are forgotten. (`modelContextTokens` minus `outputReserveTokens` is the outer bound, but at a 1M-token window it never binds.)
- **Each transcript entry carries the options that were offered after it**, and the judge is shown that list ("эти варианты ты УЖЕ предлагал… не повторяй и не перефразируй") so it stops recycling the same four lines. Details: newest-first, de-duplicated case-insensitively, capped at `maxRecentlyOffered` (6 turns' worth); an option is **forgotten exactly when its exchange is** — `exchangeTokens` prices options into the window, so they can't quietly overflow the context. The one exception is the character's **static `OpeningOptions`, always listed**: they never grow, and re-offering them on turn six is precisely the repetition being avoided. With the 12-exchange cap, a run longer than twelve turns trims on every turn — which is the point: turn 30 then costs what turn 12 cost, instead of a long game growing quadratically more expensive.
- **Config** (current model: **DeepSeek V4 Flash**): `PSYCHOSPACE_LLM_BASE_URL=https://llm.api.cloud.yandex.net/v1`, `PSYCHOSPACE_LLM_API_KEY=<key>`, `PSYCHOSPACE_LLM_MODEL=gpt://<folder-id>/deepseek-v4-flash/latest` (full model URI, folder-specific). Set all three to activate; creds arrive via GH secrets. Context window **1048576** (`modelContextTokens`, measured against the endpoint — not from docs), ~2k reserved for output. Our own `historyTokens`/`historyExchanges` budget binds long before the model's window does.
- **Runs** (`{success, steps}`) are recorded via `POST /api/game-khimki/runs` into `game_khimki_runs`, and feed the leaderboard (`/runs/leaderboard`) and stats (`/runs/me`).
- **The leaderboard is four record boards over SINGLE runs**, not an aggregate: `longest_win`, `shortest_win`, `longest_loss`, `shortest_loss`. `GET /runs/leaderboard` returns `{boards: {<key>: [{player, steps, plays, wins, losses, mine}]}}`; one SQL query computes each account's `max/min(steps) FILTER (WHERE success / NOT success)` and the service ranks + caps in Go (`buildBoards`). A player appears only on boards they hold a record on — no wins yet means no row on either win board. The SPA renders them as four tabs (`web/src/lib/gameKhimkiBoards.ts` owns the labels/captions/empty states) and opens on `longest_win` after a win, `longest_loss` after a loss.
- Files: LLM judge `internal/gamekhimki/llm.go`; content `content.go`; UI `web/src/views/GameKhimkiView.vue` (turn loop, portrait + landscape, art from catalog) at route `/app/game-khimki`; `gameKhimkiApi` in `web/src/api/endpoints.ts`; migrations `migrations/005_game_runs.sql` (created the table, then named `game_runs`) + `007_game_khimki_rename.sql` (renamed it to `game_khimki_runs`).

### Game assets (generation & packaging)

Each art in the catalog needs an image. **Placeholders (emoji + gradient) render until real images land** — adding images is backend-only, no client change.

**Names — derive from the art catalog, never from a list kept here.** The required filename is exactly the art key, and each game's catalogue is the source of truth:

- **game 1** — `internal/gamekhimki/content.go`, `Character.Arts`; from the API, `curl -s -b <cookie> 'http://localhost:8080/api/game-khimki/config?game=smalltalk_khimki' | jq -r '.characters[].arts[].key'`.
- **game 2** — `internal/gamevanyagotchi/content.go`, where art keys live on **four** different things rather than one: `Skins[].Art` (the pet and every regular), `ObjectKinds[].Art` (what is lying about — a deposit, the crate) and `Locations[].Art` (the backdrop behind the plane). `grep -n 'Art:' internal/gamevanyagotchi/content.go` lists the lot.
- A key with no blob is not an error and never logs one: the config simply advertises no image and the client draws the emoji placeholder. **So a misspelt filename is silent** — it uploads cleanly and nothing ever asks for it.

Current game `smalltalk_khimki` — 10 arts (file name = `<key>.webp`):

| key | what |
|-----|------|
| `entrance_far_angry` | подъезд издалека, злой дядя Ваня (establishing) |
| `vanya_angry` | дядя Ваня — злой, крупно |
| `vanya_suspicious` | подозрительный |
| `vanya_neutral` | нейтральный |
| `vanya_warming` | теплеет |
| `vanya_deep` | раскрывается глубина |
| `vanya_sahur` | Ваня со своим другом Тунг Тунг Сахуром (ключевая тема — раскрыть, чтобы победить) |
| `memory_children` | сюжетный арт-воспоминание, без персонажа |
| `hallway_pass` | проход в подъезд, без дяди Вани (финал — победа) |
| `vanya_game_over_hits_us` | Ваня сорвался и бьёт игрока (финал — проигрыш) |

Two kinds: **character-mood** (`vanya_*`) — the same дядя Ваня, changing expression; **story/location** (`entrance_far_angry`, `memory_children`, `hallway_pass`) — scene, no character in focus.

`vanya_game_over_hits_us` is not the judge's to pick: when it returns `game_over: true` the backend **forces** that art (`Character.GameOverArt`), so the beating always looks the same.

**Size & format:**

- **1024×1024 px** square is the default (rendered `object-fit: contain`, so it never crops — letterboxes in wide/short panes). Location arts may be **1280×768** landscape if you prefer full-bleed scenes.
- **WebP** (preferred) or PNG; keep each **≤ ~250 KB** — mobile downloads them on demand.
- Keep the character consistent across a game's `*_*` arts; gritty tragicomic RU-двор tone.

**Where they live — Postgres blob store (NOT the repo/binary):**

- Table `game_assets` (`game_key`, `art_key`, `content_type`, `bytes`), migration `006_game_assets.sql`. **Shared across games** and deliberately not renamed by `007_game_khimki_rename.sql` — the blob store is infrastructure, scoped per game by `game_key` (ADR-031), and served from the game-agnostic `/api/game-assets/{game}/{key}`. Kept out of git so the art doesn't bloat the repo/binary.
- Served by `GET /api/game-assets/{game}/{key}` — **public**, `Cache-Control: max-age=86400`; the client downloads each art on demand and caches it. No CDN. The `{game}` segment is still the `game_key` value `smalltalk_khimki`, which the rename deliberately left alone.
- `GET /api/game-khimki/config` advertises an image URL **only for arts that have an uploaded blob**; arts without one keep the emoji placeholder, so partial uploads degrade gracefully. `Art.Image` in `content.go` stays empty — the config handler fills it per uploaded key.

**Upload (owner-only, over SSH for now — an admin UI may come later):**

`deploy/upload-game-assets.py` converts each image in a dir to WebP and prints
`INSERT … ON CONFLICT` SQL to stdout; pipe it to a psql. Requires Pillow
(`pip install pillow`). The blob store is shared infrastructure scoped by
`game_key` (ADR-031), so **one uploader serves every game** — it takes the
directory and the game key as arguments: `upload-game-assets.py [dir] [game_key]`,
defaulting to «Смолтолк в Химках».

```bash
# prod (hardened SSH alias `psycho`) — game 1:
python3 deploy/upload-game-assets.py ~/Desktop/psycho-space/vanya_assets \
  | ssh psycho "sudo -u postgres psql psychospace"

# prod — game 2, whose key is `vanyagotchi`:
python3 deploy/upload-game-assets.py ~/Desktop/psycho-space/vanyagotchi_assets vanyagotchi \
  | ssh psycho "sudo -u postgres psql psychospace"

# local dev DB:
python3 deploy/upload-game-assets.py ~/Desktop/psycho-space/vanya_assets \
  | psql "postgres://psychospace:psychospace@localhost:5432/psychospace"
```

**Stage the files you actually mean to upload, in their own directory.** Every
image in the directory becomes an art key named after its filename, so a contact
sheet, a source photograph or a scratch render sitting beside the sprites becomes
a blob nothing ever asks for. Copy the ones you want into a clean directory and
point the uploader at that.

**Checking it landed** — there is no cache to bust and no deploy to run:

```bash
curl -sS -o /dev/null -w '%{http_code} %{content_type} %{size_download}B\n' \
  https://psycho-space.ru/api/game-assets/vanyagotchi/<key>
```

A 200 with `image/webp` is the whole contract. `GET /api/game-<name>/config` then
carries `"image": "/api/game-assets/<game>/<key>"` on exactly the entries that
have a blob, and every other entry keeps its emoji placeholder.

- Art key = filename without extension; it **must** match a key in `content.go`. Re-running upserts. Remove one with `DELETE FROM game_assets WHERE game_key='…' AND art_key='…'`.
- After upload, reload the game — the config now serves the real images (`<img>` in `GameKhimkiView.vue`; falls back to the emoji if a load fails).

### Working on «Ванягоччи» (the pet)

Nothing about a pet is ever computed on a timer, which makes it unusually easy to
debug and unusually easy to misread: a stat's row holds `(value, as_of)` and the
value you see in the app is `clamp(value − rate × hoursSince(as_of))` worked out
at the moment of the request. **So the number in the database is not the number on
the screen**, and moving `as_of` is how you travel in time.

```bash
# Somebody's pet and what it is actually storing.
ssh psycho "sudo -u postgres psql psychospace -c \"
  SELECT p.id, p.skin_key, p.location_key, p.died_at, s.stat_key, s.value, s.as_of
    FROM game_vanyagotchi_pets p
    JOIN game_vanyagotchi_pet_stats s ON s.pet_id = p.id
   WHERE p.account_id = '<uuid>' AND p.deleted_at IS NULL\""
```

**Health is a consequence, not a timer, so read the other two bars first.** `hp`
drains only 1/hour on its own; what kills him is **+6/hour while `beer` ≤ 20** and
**+6/hour while `bladder` ≥ 80**. So a Ваня losing health fast is a Ваня who is
dry, bursting, or both — and the arithmetic is worked out from the stored pairs
at read time, never simulated. `beer` drains 4/hour from 60; `bladder` fills
5/hour from 0 **and gains 25 every drink**.

**To age a pet without waiting** — the only way to see decay, a death, or the
revive path on demand — push `as_of` backwards. Move **every** row by the same
amount, or the coupling has a window it cannot reconstruct and the damage will
not appear:

```sql
UPDATE game_vanyagotchi_pet_stats
   SET as_of = now() - interval '20 hours'
 WHERE pet_id = '<uuid>';
```

At twenty hours a fresh pet is dry after ten (beer 60 at 4/hour), so it takes ten
hours of 1/hour and ten of 7/hour: 65 − 10 − 70, i.e. comfortably dead.

Then load the game (or `curl` `/api/game-vanyagotchi/state`). That request is what
writes `died_at`, and it writes the **derived** instant — the moment health
actually reached zero, worked out across the rate change when the beer ran dry,
not "now". A `died_at` equal to the moment you looked means something is wrong
with the derivation rather than with your test.

**To bring somebody back**, open the app: a dead Ваня is a **death screen** over
the yard carrying his lifetime totals and one button, «восстать из мертвых»,
which is the only verb that revives. There is no way to press anything else while
he is dead — the screen covers every other control, deliberately — so the server's
refusal of an ordinary verb on a corpse («он не встаёт», in the balloon over his
head rather than as an error) is now reachable only from a client that has not
re-read since he died.

**A verb cannot be curl'd, and that is deliberate** — do not go looking for the
endpoint. It travels over the socket as a `vanyagotchi_do` frame and is answered
with state rather than a reply ([ADR-043](adrs/ADR-043-a-verb-travels-over-the-socket-and-is.md)),
so the only two ways to apply one are the app and the `psql` equivalent below.
The two reads (`/api/game-vanyagotchi/config` and `/state`) are still ordinary
HTTP and still curl-able, which is what the ageing recipe above uses.

Over `psql` the equivalent of a revive is refilling *and* clearing,
because a pet whose `died_at` is NULL with health still at zero simply dies again
on the next read — and every row must carry the same `as_of`:

```sql
UPDATE game_vanyagotchi_pet_stats
   SET value = CASE stat_key WHEN 'hp' THEN 65 WHEN 'beer' THEN 60 ELSE 0 END,
       as_of = now(), updated_at = now()
 WHERE pet_id = '<uuid>';
UPDATE game_vanyagotchi_pets SET died_at = NULL, updated_at = now() WHERE id = '<uuid>';
```

**To see what is lying about in the yard**, and why a deposit will not go away:

```sql
SELECT id, kind, location_key, x, y, singleton, expires_at, exhausted_at
  FROM game_vanyagotchi_world_objects
 WHERE deleted_at IS NULL AND (expires_at IS NULL OR expires_at > now())
 ORDER BY created_at DESC;
```

Deposits are **filtered on read, never swept** — nothing deletes them, so expired
rows accumulate and are simply ignored, and a growing count is normal rather
than a leak. The server also holds a cached copy that is refreshed only when
somebody says hello or leaves something behind, so a row you delete by hand
stays on the plane until the next hello: the 5 Hz tick deliberately reads
nothing. Restart the service, or reload a client, to see a hand-edited world.

**Never write one stat row on its own.** Health is integrated from its own
`as_of` against the other stats' trajectories, so all the pairs have to share one
instant. Re-stamping the bladder alone would erase whatever damage a full bladder
had already done — nothing errors, the number is just quietly wrong. The app
enforces this (there is no single-stat write path); a hand-written `UPDATE` does
not, so include every row.

**Rates, thresholds, penalties and labels are not in the database.** They live in
`internal/gamevanyagotchi/content.go` and ship with the binary, so retuning how
fast he dries out, retitling a button, changing what a drink does, or adding a
whole stat is a backend deploy with **no migration and no frontend change** —
and existing pets pick a newly-added stat up on their next read. Nothing
backfills, because nothing needs to.

**Positions live in memory and are written down on the way out.** A Ваня's place
in the yard is in-process state, held for `PositionGrace` (2 minutes) after the
last socket closes so that reloading the page keeps your place — and written to
`pets.x` / `pets.y` / `pets.last_seen_at` **once**, when that last connection
goes away, or for everybody at once when the service shuts down. So a deploy no
longer returns the yard to the middle. A *crash* still does, for whoever had not
been written yet; that is accepted rather than fixed.

```sql
-- where everyone was last seen standing
SELECT account_id, round(x::numeric, 2) AS x, round(y::numeric, 2) AS y, last_seen_at
  FROM game_vanyagotchi_pets WHERE deleted_at IS NULL ORDER BY last_seen_at DESC NULLS LAST;
```

**A NULL x/y is normal** — it means that pet has never been in the yard, and he
starts at his location's entry point from the catalogue.

**Not everything in the yard is a person.** A roster frame carries three kinds of
entity and the client cannot tell them apart on purpose: connected players, the
**NPCs** (three of them, evaluated closed-form on the tick from `content.go` — no
rows, no accounts, nothing stored), and **sleepers** — players whose owner is
away, drawn lying where they last stood. Only the first kind is counted, and the
frame carries that count explicitly as `here`. So «во дворе: 1» beside eight
figures on screen is correct, not a bug. The cast is a catalogue list: `curl` the
config to see who is currently in it rather than trusting a number written here.

**A Ваня who will not move where you tapped is probably tired.** A tap is a
*walk* now, not a teleport: about a fifth of the plane per second, and for a long
tap the server may decide at accept time that he gives up part way and complains
— «нога отваливается», «спина болит», one of ten lines. That decision is made
once, server-side, and broadcast, so everybody sees him sit down in the same spot
saying the same thing. Re-tapping starts a new walk from where he sat, so nothing
is ever stuck. **Short hops never fail** — under `tiredFrom` (0.45 of the plane)
he always arrives, which is also how a test moves somebody reliably. Giving up is
common on purpose: `tiredChance` is 0.7 across the whole yard.

**Balloons come from two pools and mean two different things.** A line from
`tiredSays` means he gave up on a walk. A line from `idleSays` — «где ключи»,
«пивка бы» — means he is merely standing about, and is ambient noise rather than
feedback on anything. The pools are disjoint and a unit test keeps them that way,
so the line alone tells you which happened. Neither pool is served to the client:
if you see one in a browser, the server put it there.

**Idle muttering has no timer, so there is nothing to inspect.** Time is cut into
12-second slots from `worldEpoch`, hashing (account, slot) decides whether that
slot produces a remark and which, and it is shown for the first 4 seconds. So a
given Ваня speaks roughly once a minute, every client computes the identical
answer, and nothing is stored. Two consequences when debugging: **the phrase key
is per-process**, so a restart changes who says what and when — the same property
the tiredness roll has — and a Ваня who is walking, dead or asleep is silent by
design, so silence in those states is not a fault.

**The yard's appearance comes from an in-memory cache, not from a query.** The
5 Hz broadcast never touches Postgres: each account's skin, name and stat pairs
are read when its client says hello and refreshed whenever it acts over HTTP
(ADR-041). Consequences when debugging: a pet renamed or re-skinned straight in
the database **will not change on screen until that client reconnects or acts**,
and that is by design rather than a bug. The *pose* is not cached — it is derived
from the cached pairs on every tick, so a Ваня visibly deteriorates without
anything being refreshed.

### Working on «Симулятор финтеха» (the office)

**Almost nothing about this game is in the database, and that is the property to
hold in your head before debugging it.** The office, where everybody is standing,
the boss, the streak, the multiplier and the salary you are watching climb all
live in process memory and survive nothing — not a restart, not a deploy. What
reaches Postgres is **one summary row per shift, written once, when the shift
ends**. So there is no table to inspect while somebody is playing, and a shift in
flight during a deploy is simply lost, exactly as an in-flight «ВАНЯДУМ» run is.

```bash
# Every shift somebody has finished, most recent first.
ssh psycho "sudo -u postgres psql psychospace -c \"
  SELECT id, account_id, cause, round(salary::numeric) AS pay,
         round(seconds::numeric, 1) AS secs, created_at
    FROM game_fintech_shifts WHERE deleted_at IS NULL
   ORDER BY created_at DESC LIMIT 20\""
```

**`cause` tells you how it ended**, and iteration 1 has exactly two: `promoted`
(лысый reached you — the loss) and `left` (you pressed УЙТИ, or your socket was
gone long enough to be given up on). It is `text` rather than an enum on purpose,
so a later iteration's third ending is a catalogue change and not a migration.

**A very short shift is not a bug — it was deliberately dropped.** Anything under
`MinShiftSeconds` is discarded instead of written, so a stray tap on НАЧАТЬ СМЕНУ
never becomes a leaderboard row. If somebody swears they played and there is no
row, that is the first thing to check.

**The leaderboard is best-shift-per-account, not best rows**, so one very good
shift does not fill the whole board. That is in the SQL, not in the client:

```sql
-- what /shifts/top is actually computing
SELECT DISTINCT ON (account_id) account_id, salary, seconds, cause
  FROM game_fintech_shifts WHERE deleted_at IS NULL
 ORDER BY account_id, salary DESC;
```

**Every rule of the game is in `internal/gamefintech/content.go`, not in the
database and not in the client.** The office layout, the desks, the walk and dash
speeds, the base rate, the ramp, the grace window, the boss's speed and catch
radius, and the ending titles all ship with the binary and are **served** at
`GET /api/game-fintech/config` — which is also what the splash screen's rules
cheatsheet is generated from. So retuning the game is a backend deploy with no
migration and no frontend change, and the rules on the splash screen cannot drift
from the rules the server enforces. If the cheatsheet says something the game does
not do, the bug is in `fintechRules.ts` deriving it, never in a number typed twice.

**To see what a player is told:**

```bash
curl -fsS -b "psycho_session=<token>" https://psycho-space.ru/api/game-fintech/config | jq .
```

**The office is static.** There is no generator, no seed and no per-shift level —
unlike «ВАНЯДУМ», every shift is the same room, and the geometry is in the
catalogue above rather than on any frame or in any start response. So "the desks
moved" is not a thing that can happen.

**Nothing about a shift can be curl'd while it is running.** Movement travels over
the socket as `fintech_input` frames and the world comes back as `fintech_snap` ten
times a second — the HTTP surface is only the *edges* of a shift (start, resume,
walk out) plus the two reads. That is deliberate and matches the yard's rule that
a verb is not an endpoint.

**A player stuck at «смена уже идёт» (409) has a shift the office still thinks is
live.** The honest fix is the one the client already does — `GET /shifts/current`
and reconnect — and the blunt one is to wait out the abandon grace, after which
the occupant is ended as `left`, written, and dropped. Restarting the service
clears every office instantly and loses every shift in flight.

#### «It teleports / stutters / doesn't move» — measuring a netcode complaint

Every defect this game has had so far has been *the client and the server
disagreeing about how far something moved*, and every one of them was found by
printing metres rather than by reading code that looked right. The trap is that
the game is allowlist-gated, so you usually cannot reproduce it yourself and are
working from a description and, if you are lucky, a phone recording. Two
procedures, and they answer different halves of the question.

**1. Drive the real stack and record both ends.** This is the strong one, and it
needs nothing test-only in the app: Playwright can read the WebSocket frames, and
the drawn position is a CSS custom property, so a throwaway spec in
`web/e2e-stack/` sees exactly what the client sent, what the server answered, and
where the figure was actually painted on every animation frame.

```ts
// what the client sent and what the server answered
page.on('websocket', (s) => {
  s.on('framesent', (f) => log('OUT', f.payload));      // fintech_input: q, dt, mx, my, d
  s.on('framereceived', (f) => log('IN', f.payload));   // fintech_snap:  k, ack, x, y, dc
});
// where the figure was actually drawn, every frame — --x/--y are fractions of the office
await page.evaluate(() => {
  const el = document.querySelector('[data-testid="fintech-me"]') as HTMLElement;
  const tick = (now: number) => {
    (window as any).__samples.push([now,
      parseFloat(el.style.getPropertyValue('--x')), parseFloat(el.style.getPropertyValue('--y'))]);
    requestAnimationFrame(tick);
  };
  requestAnimationFrame(tick);
});
```

Then print, per frame, the position in metres and the step since the last frame,
and summarise **the largest single step and the number of direction reversals**.
Those two numbers are the complaint, quantified: a healthy dash is one monotone
burst of ~0.33 m per frame at 60 Hz, and the defect that shipped on 2026-07-29
read 2.767 m and fifteen reversals.

**Choose the lane deliberately, or the probe will lie to you.** Spawn is (8, 4);
desks occupy x 2.8–5.4 and 10.6–13.2 at y 3–4, 7–8, 11–12, 15–16; the floor clamp
is the player's 0.35 m radius. A dash is 5.5 m, so walking "somewhere" and dashing
"somewhere" mostly measures the wall clamp — the third time this project has been
fooled by exactly that. Walk down to y ≈ 5.6 and work rightwards, where 7.65 m of
clear floor holds a whole dash. And **the лысый is closing the whole time** (4.0
m/s from y = 20.5, so contact in under four seconds): keep the probe short, and
never walk *towards* him, or the occupant is deleted mid-run and the trace looks
like a deadlock.

**2. Track the figure in the phone recording.** When all you have is a video, the
positions are still in it. There is no `ffmpeg` on the workstation but
`gst-launch-1.0` is present, and PIL plus numpy do the rest:

```bash
gst-launch-1.0 filesrc location=clip.mp4 ! qtdemux ! h264parse ! avdec_h264 \
  ! videoconvert ! pngenc ! multifilesink location=f%05d.png
```

Then mask the figure by the colour of its shirt, take the largest connected
component **inside the plane only** (the controls below and the HUD above match
the same colours and will drag the centroid across the screen), and calibrate
metres per pixel off the **desks**, whose world coordinates are in `content.go` —
the two desk columns give the x scale and the four rows give the y scale, and they
must agree. Two warnings, both of which cost a wrong diagnosis on 2026-07-29:
**identify the figures before trusting a trace** (you have hair and a blue shirt,
the лысый is bald with a purple one and a grin; tracking the wrong one gives a
smooth 4 m/s walk that hides everything), and **re-extract at the native frame
rate** — resampling to 30 fps duplicates frames and manufactures a stutter that
is not there.

The dash cooldown readout is the clock for all of this: it comes from the
snapshot's `dc`, so the frame where «РЫВОК ГОТОВ» becomes a countdown is the frame
the *server* granted the dash, ± one snapshot period.

### Tests

```bash
./dev.sh test          # Go unit (incl. internal/gamekhimki)
./dev.sh integration   # testcontainers (incl. test/integration/game_test.go)
./dev.sh web           # frontend type-check + vitest
./dev.sh e2e           # Playwright: 360px in full, desktop for @wide, /api stubbed
./dev.sh e2e-stack     # Playwright against the REAL binary + Postgres (needs Docker)
./dev.sh cover         # coverage: Go unit + Go integration + web
./dev.sh pre-commit    # everything (the git hook runs this) — never bypassed
```

**The two Playwright suites do different jobs.** `web/e2e/` stubs `/api` with route
interception and checks layout at **360 px** — the whole suite, every test. A
second project at **1440 px** re-runs only the tests tagged **`@wide`** (~22 of
them): the ones whose claim is about width, not merely visible at one. That
project exists because its absence hid a real bug — the yard drew at a different
apparent scale above tablet width and nothing had ever opened it there — but it
is a regression guard rather than a target, so it does not replay assertions that
skip themselves above 600 px anyway (tap targets, the 44 px floor). It used to be
four full projects and took 16 minutes in CI; it is now ~167 tests. `web/e2e-stack/` runs
`scripts/e2e-stack.sh` — throwaway Postgres on port 55433 (tmpfs, force-recreated
per run), the SPA built and the server compiled and started on :8081, then accounts
seeded straight into the database (`cmd/dev-seed -json`) so tests can be "logged
in" without VK. Its assertions are all "reload, or ask the API, and it is still
true", which only holds if the SQL ran. Videos are recorded for **every** test in
both suites (`web/test-results/`).

First run needs the browser once: `(cd web && npx playwright install chromium)`.

### A test that passes on its own and fails in CI

This has happened twice, and both times the test was wrong rather than the code. CI runs on
a **4-vCPU** runner hosting Docker, Postgres, the Go server, Chromium and Node at once, so
everything is slower there than on a workstation — and *slower* is the only difference.

**Reproduce it before changing anything.** Saturate the machine and run the one spec:

```bash
for i in $(seq $(( $(nproc) - 1 ))); do (while :; do :; done) & done
./dev.sh e2e-stack gamevanyagotchi-pet.spec.ts     # or: ./dev.sh e2e <spec>
kill $(jobs -p)
```

**Then look for one of these five, in this order.** Every flake found so far was one of them —
and note that the first four are defects in the *test* while the fifth is not, so a test that
keeps insisting may be right:

1. **A loop bounded by an attempt count instead of a deadline.** `for (let i = 0; i < 15; …)`
   measures how hard you are willing to try; load changes how long each try *takes*, not how
   many you need. The count runs out mid-convergence and the failure lies about why. Every
   such loop in `web/e2e-stack/` is now `while (Date.now() < deadline)` — keep it that way,
   and give a hot path a `waitForTimeout` so a deadline loop cannot spin.
2. **Two round trips reading one short-lived thing.** «Ванягоччи»'s speech balloon lives
   exactly **4 s** (`sayFor`, `service.go`). Waiting for it to appear and *then* reading its
   text lets it expire in the gap — you conclude the verb was refused when it landed. Assert
   the **outcome** (the stat moved, the row changed), never the announcement.
3. **An implicit 5 s `expect`.** Playwright's default was shorter than that same 4 s balloon
   plus a round trip. `playwright.stack.config.ts` now sets `expect: { timeout: 15_000 }`.
4. **A fixture that plays the game to reach its starting position.** The crate hides in a
   random one of five locations, and walking is 0.2 plane-widths a second through a give-up
   roll — so "get to the crate" cost most of a 120 s budget before the test under it had done
   anything. It is a `Test timeout exceeded` with no assertion named, which reads like a hang
   and is not. Move the fixture to the player instead (`standTheCrateBesideHim` writes
   `location_key`/`x`/`y`), and leave the walking to the one test whose subject *is* walking.

   **The same kind bites a second way: a caller whose budget contradicts the helper it
   calls.** `walkToTheCrate` is bounded by a 60 s deadline, and its own comment says it is
   sized against callers with **120 s**. One caller — «Ванягоччи»'s *two accounts have two
   Ваняs* — asked for 90 s while being the heaviest of the set (two browser contexts, two
   logins, two pages, two yard entries and a reload, all outside the walk). It passed for
   weeks and then failed in CI on 2026-07-29, and the artefacts said "hang": a bare timeout,
   a trace whose last unfinished call is `click [data-test="shop"]`, and a screenshot of a
   perfectly healthy yard. Nothing was slow and nothing was racy — the arithmetic simply did
   not fit. **When a helper states the budget it needs, check the caller actually gives it
   one**, and read the trace's unfinished call before believing the word "timeout":

   ```bash
   gh run download <run-id> -D artifacts        # test-failed-*.png + error-context.md + trace.zip
   unzip -q trace.zip -d tr && ls tr/*.trace    # the last `before` with no `after` is where it hung
   ```

5. **…or the test was right and the product was racy.** This is the fifth kind and it broke
   the rule the other four established, so check the code before assuming the test is at
   fault. `TestRealtimeDrainsOnHubShutdown` failed ~7% of the time under saturation (14 in
   200) and once in CI, and it was correct every time: on shutdown a share of clients really
   did get a bare 1006 instead of the `bye` frame [ADR-018](adrs/ADR-018-the-close-reason-travels-as-a-frame-not-as-a.md)
   exists to guarantee, so a browser could not tell a deploy from a tunnel dropping and
   backed off instead of reconnecting. **The cause is worth knowing because it is invisible
   by reading:** coder/websocket hangs a `context.AfterFunc` on the context handed to
   `Write` and **closes the whole connection** if it fires. The write pump's per-message
   context descended from the hub's, so cancelling the hub did not merely stop the writes —
   it destroyed the socket, and whenever a frame happened to be queued at that instant it
   did so before the `bye` could be written. `readPump` already carried a comment about
   exactly this trap on `Read`; the write path did not. Both message and ping writes now use
   `context.WithoutCancel`, and every non-close exit from the pump goes through `windUp`,
   which gives a queued reason its one chance. 600 saturated runs, zero failures.

   The diagnostic that settled it, when reasoning had run out: instrument the suspect paths
   (`writeBye`, `hardClose`, both pump entries and exits) with temporary `slog.Error` lines,
   run `-count=60` under spinners, and read the ordering around a failure. `readpump exit
   … use of closed network connection` appearing *before* `hardClose` was the whole answer.

**What not to do.** Do not add `retries` — a retry makes a broken test green and deletes the
evidence. Do not add an env flag that disables a game's random rolls (the tiredness give-up,
the relieve fail chance): that is test-only machinery in a production path, which `CLAUDE.md`
forbids outright. Determinism comes from **direct DB setup** instead — `vanyagotchi-db.ts`
already writes stats and crate stock that way, and that is the sanctioned seam.

**A suite can also fail because it is testing an old build — and the layout suite no longer
can.** `playwright.config.ts` now sets `reuseExistingServer: false`, and that is the fix for a
whole class rather than a preference. The reasoning, because it cost four commit attempts:

The layout suite serves the built SPA out of the same directory the pre-commit gate's `web`
step rebuilds moments earlier. With reuse on, a server left alive by an earlier invocation
went on serving that directory **while it was being rewritten underneath it** — so a chunk
404s during boot and the app never starts. What that looks like is not a slow page:

* a **sixty-second wait for a button**, not an assertion failure, and
* a screenshot of a **completely blank page**, and
* **a different test every time**, because the casualty is whichever lazily-routed view the
  run happened to reach first.

That last property is the trap. Four failures on four different tests, each passing alone and
in full saturated re-runs, reads exactly like marginal timing and is not — so it invites
raising timeouts, which does nothing, because nothing is slow. **Read the artefact before
believing the pattern**: `web/test-results/<test>/error-context.md` names the wait and
`test-failed-1.png` shows the blank page, and either one identifies it in seconds.

Always starting fresh costs nothing — the `webServer` command rebuilds on every invocation
anyway, so reuse only ever saved a process spawn — and a leftover server now fails loudly on
the port instead of quietly serving yesterday's bundle. `playwright.stack.config.ts` keeps
reuse: it runs its own script and embeds the SPA into the Go binary, so it has no directory to
race. If you are ever chasing this by hand:

```bash
ss -ltnp | grep -E '4173|8081'      # 4173 = vite preview, 8081 = the full-stack server
pkill -f 'vite preview'
```

**Also note the full-stack suite has no isolation but seriality.** One database, one server,
five shared seeded accounts, `beforeEach` deleting every world object. `workers: 1` is what
makes it safe, and `./dev.sh e2e-stack` now refuses a `--workers` override rather than
letting it produce failures that look like product bugs. Two Playwright runs against the same
stack at once (`reuseExistingServer` is on locally) will do the same damage — don't.

## Service

```bash
ssh psycho 'systemctl status psycho-space'
ssh psycho 'sudo systemctl restart psycho-space'      # rarely needed; CI restarts on deploy
ssh psycho 'journalctl -u psycho-space -n 200 --no-pager'
ssh psycho 'journalctl -u psycho-space -f'            # live
```

## Logs (host files, rotated)

The app writes structured JSON to `/var/log/psycho-space/app.log` (rotated by size, 7 backups, 30 days) in addition to journald.

```bash
ssh psycho 'tail -f /var/log/psycho-space/app.log' | jq .
ssh psycho 'grep http_request /var/log/psycho-space/app.log | tail -50' | jq .
# Correlate a specific account without exposing PII (we log identity_ref hex, never names):
ssh psycho 'grep <ref-hex-prefix> /var/log/psycho-space/app.log'
```

## Database

```bash
# Interactive shell (as the DB superuser on the box):
ssh psycho 'sudo -u postgres psql psychospace'

# One-off query:
ssh psycho "sudo -u postgres psql psychospace -c 'SELECT status, count(*) FROM accounts GROUP BY status;'"

# Pending accounts (to approve) — note the short handle shown to the user on the pending screen:
ssh psycho "sudo -u postgres psql psychospace -c \
  \"SELECT left(encode(identity_ref,'hex'),8) AS handle, role, status, created_at FROM accounts WHERE status='pending' ORDER BY created_at;\""

# Wishlist vote counts:
ssh psycho "sudo -u postgres psql psychospace -c \
  \"SELECT i.title, count(v.*) AS votes FROM wishlist_items i \
    LEFT JOIN wishlist_votes v ON v.item_id=i.id AND v.deleted_at IS NULL \
    WHERE i.deleted_at IS NULL GROUP BY i.id ORDER BY votes DESC;\""
```

Profile fields are stored encrypted (`*_enc` bytea) and are **not** readable from SQL — that's by design (152-ФЗ). `\x` on a row shows only ciphertext.

## DB access from a local GUI (JetBrains DataGrip / DB plugin)

Postgres listens only on the server's `127.0.0.1:5432`; reach it through an SSH tunnel — nothing on the server needs changing (the `deploy` user can forward, and TCP forwarding stays enabled after hardening).

**JetBrains (DataGrip / IDEA Database tool):** New Data Source → PostgreSQL, then:

- **SSH/SSL tab → Use SSH tunnel:** Host = the server IP/domain, Port = the hardened SSH port, User = `deploy`, Auth = Key pair → your `~/.ssh/id_ed25519_psycho`.
- **General tab:** Host = `127.0.0.1`, Port = `5432`, Database = `psychospace`, User = `psychospace`, Password = the `POSTGRES_PASSWORD` value. (The IDE resolves `127.0.0.1` on the *server side* of the tunnel.)

**Plain CLI equivalent** (local port 5433 → server's 5432):

```bash
ssh -p <hardened-port> -N -L 5433:127.0.0.1:5432 deploy@<server-ip>   # leave running
psql "postgres://psychospace:<POSTGRES_PASSWORD>@127.0.0.1:5433/psychospace?sslmode=disable"
```

Treat everything you pull this way as confidential; profile columns are ciphertext regardless.

## «Ой, ошибка / unexpected» on the landing page in Firefox

**Not a bug, and not our CSP.** Diagnosed 2026-07-28 from a production report.

Symptoms: in Firefox, ticking the consent box shows the error modal with code
`unexpected` and an **empty trace id**, and the console logs

```
Content-Security-Policy: The page's settings blocked the loading of a resource
(frame-ancestors) at <unknown> because it violates the following directive:
"frame-ancestors 'self' https://vk.com https://*.vk.com https://vk.ru https://*.vk.ru"
```

In Chrome the same page works and the VK button reads «Продолжить как Сергей»;
in Firefox it reads the generic «Войти с VK ID». **Login works in both** — the
button opens VK top-level in a new tab and comes back fine.

What is actually happening: the OneTap widget personalises its button by reading
your VK session from an iframe on VK's origin. **Firefox's Total Cookie
Protection partitions third-party storage per top-level site**, so on
psycho-space.ru that iframe gets an empty cookie jar keyed to our domain, sees
no session, and reports failure. (The console shows `Cookie warnings` beside the
CSP line — that is the same thing.) Chrome still passes VK's real cookies to the
iframe. Expect Chrome to behave like Firefox eventually.

**The CSP line is not ours.** Verify rather than assume:

```bash
curl -sI https://psycho-space.ru/ | grep -i content-security-policy
```

Our policy has **no `frame-ancestors` directive at all** — there is exactly one
`add_header Content-Security-Policy` in `deploy/nginx/psycho-space.conf`, and
nothing in the Go binary or `index.html` adds another. The quoted policy is the
CSP of the document being framed, i.e. a VK page whose own allowlist does not
include us. Its resemblance to ours is because both list the VK domain family.
A second, decisive check: `frame-ancestors` is enforced identically by Chrome
and Firefox, so if it were the cause Chrome would fail too.

**What was ours** was the modal: the widget's `ERROR` event went to
`errorStore.report`, which renders anything that is not an `ApiError` as code
`unexpected` with an empty trace id and asks the user to send it to Sergei — a
meaningless code, no id, and nothing wrong. `mountOneTap` now takes separate
`onWidgetError` / `onExchangeError` callbacks: the widget's is a console
warning, and a backend exchange failure (a real `ApiError` with a real trace id)
keeps the modal. Pinned by `web/src/__tests__/vkLoginErrors.spec.ts`, with the
SDK mocked — the Playwright layout suite cannot make the real widget fail on
demand, and a test written there passed just as happily with the bug in place.

## The layout suite fails only on «ВАНЯДУМ», and the page says «браузер не умеет 3D»

**Symptom.** `./dev.sh e2e` goes red across most of `gamevanyadum.spec.ts` — often twenty-odd tests at once, several timing out at a minute — while every other spec passes. The error-context snapshot shows the splash rendered with:

> Твой браузер не умеет 3D (WebGL выключен или не тянет).

**`./dev.sh` now prevents this, so seeing it at all is information.** `dev.sh`'s `playwright_` helper runs both Playwright suites with `DISPLAY` unset, which is what makes the browser take the same path a CI runner takes. So if the symptom appears, one of two things is true: the suite was **not** run through `./dev.sh` (a bare `npx playwright test` from `web/` inherits the shell's broken `DISPLAY`), or something new is breaking GL and the rest of this section is how to find out which. `web/e2e/gamevanyadum.spec.ts` opens with a test — *«can actually do 3D, or every test that starts a run below is a lie»* — that fails first and by name, so the twenty red specs below it are no longer the diagnosis.

**It is the workstation, not the code.** «ВАНЯДУМ» is the one game that needs WebGL ([ADR-047](adrs/)); the other three are DOM and CSS and cannot notice. So a suite that fails on exactly this spec and nothing else is telling you the browser has no GL context, and CI passing the same commit is the confirmation.

**The cause, and it is almost always this one: `DISPLAY` points at an X server the shell cannot reach.** Chromium's ANGLE chooses its EGL backend from the environment: with `DISPLAY` set it selects the Vulkan/XCB display, and when `xcb_connect()` fails it does **not** fall back — the GPU process exits and even the software renderer never initialises. Two situations produce it. A session that restarted its graphics stack under a long-running terminal leaves the shell holding a `DISPLAY=:0` that no longer answers; and on a Wayland session, a shell that cannot use the Xwayland cookie in `$XAUTHORITY` — an agent, a service, an `ssh` login — has the same `DISPLAY=:0` and the same dead connection while the desktop itself is perfectly healthy.

**The fix, if you are running Playwright by hand, is one word — unset it:**

```bash
env -u DISPLAY npx playwright test          # from web/, when not going through dev.sh
```

Headless Chromium wants no display at all; given one it cannot reach, it fails closed. **`DISPLAY` alone is enough** — `WAYLAND_DISPLAY` is deliberately left set, both here and in `dev.sh`, so `--headed` can still open a real window when a human wants to watch.

**Confirm the diagnosis in ten seconds** by running the browser directly and reading its own errors, which Playwright otherwise swallows:

```bash
CHROME=$(node -e "console.log(require('@playwright/test').chromium.executablePath())")
"$CHROME" --headless=new --no-sandbox --dump-dom \
  "data:text/html,<script>document.title='GL='+!!document.createElement('canvas').getContext('webgl2')</script>" \
  2>&1 | grep -iE "GL=|xcb|egl|angle"
```

A broken box prints the chain that gives the game away:

```
DisplayVkXcb.cpp:62 (initialize): xcb_connect() failed, error 1
Display.cpp:1097 (initialize): ANGLE Display::initialize error 0: Not initialized.
eglInitialize SwANGLE failed with error EGL_NOT_INITIALIZED
Initialization of all (1) EGL display types failed.
```

Re-run the same command under `env -u DISPLAY` and it prints `GL=true`, and the renderer it reports is `ANGLE (Google, Vulkan 1.3.0 (SwiftShader Device (Subzero)), SwiftShader driver)` — software, which is all this suite has ever needed.

**Two things that look like the cause and are not**, so nobody spends the time again. **`/dev/dri`**: `card0` and `renderD128` are both present and both accessible through an ACL (`getfacl /dev/dri/renderD128` shows `user:<you>:rw-`), so a missing render node is the wrong tree — and `ls -la /dev/dri | head -5` **cuts `renderD128` off the listing**, which is how that wrong diagnosis got made in the first place. And **Chromium flags**: none of `--disable-gpu`, `--use-gl=swiftshader`, `--use-gl=angle --use-angle=swiftshader`, `--enable-unsafe-swiftshader` or `--disable-gpu-sandbox` helps in any combination, because the failure is upstream of renderer selection. `npx playwright install chromium` does not help either.

**Do not commit around it.** The pre-commit hook runs the layout suite and `--no-verify` is forbidden ([`CLAUDE.md`](../CLAUDE.md)) — but there is nothing to work around here, because the gate already unsets `DISPLAY` and passes honestly. `./dev.sh e2e --grep-invert "ВАНЯДУМ"` is useful for confirming a change is otherwise sound while you diagnose, and is a diagnostic rather than a substitute for the gate.

## Forgetting a user (irreversible)

**Админка → «Забыть»**, superadmin only. It anonymises the person and keeps
everything they contributed. Use it when somebody asks to be removed, or to get
a clean first-login flow out of an account you control.

What it does, in one statement: overwrites `identity_ref` with random bytes,
empties every encrypted profile field, clears the consent record, sets the row
to `blocked`/`user`, and stamps `forgotten_at`. What it does **not** do is
delete anything — their wishlist ideas, the comments other people left on them,
the votes and the leaderboard times all stay, now authored by an anonymous
`psycho-…` that links nowhere. Reasoning: [ADR-053](adrs/ADR-053-forgetting-a-person-is-anonymisation-not.md).

The consequence to know before pressing it: **that provider account logging in again
becomes a brand-new pending account** with a new id, which somebody then has to
approve. That is the point, not a side effect.

Check what you are about to erase:

```sql
-- who this is, without decrypting anything
SELECT id, left(encode(identity_ref, 'hex'), 8) AS handle, role, status,
       created_at, last_login_at
  FROM accounts WHERE id = '<uuid>';

-- what would survive it
SELECT (SELECT count(*) FROM wishlist_items    WHERE account_id = '<uuid>') AS ideas,
       (SELECT count(*) FROM wishlist_comments WHERE account_id = '<uuid>') AS comments,
       (SELECT count(*) FROM game_khimki_runs  WHERE account_id = '<uuid>') AS khimki_runs,
       (SELECT count(*) FROM game_vanyadum_runs WHERE account_id = '<uuid>') AS dum_runs,
       (SELECT count(*) FROM game_fintech_shifts  WHERE account_id = '<uuid>') AS fintech_shifts;
```

Confirm it worked:

```sql
SELECT forgotten_at, status, role,
       first_name_enc IS NULL AS name_gone,
       consent_version IS NULL AS consent_gone
  FROM accounts WHERE id = '<uuid>';
```

All three should be true and `forgotten_at` non-NULL. The account disappears
from every admin tab at the same moment — that is deliberate, an anonymous row
nobody can act on is noise on that screen — so the SQL above is how you look at
it afterwards.

**If you need the row genuinely gone** — a legal demand rather than somebody
leaving — this is not that operation. Nine of the eleven foreign keys to
`accounts` lack `ON DELETE CASCADE`, so a hard delete is an explicit
child-first transaction, and it removes other people's comments and votes along
with the ideas they were left on. Ask before doing it by hand.

## Superadmin bootstrap (first login)

The **superadmin** is created once via script; only the superadmin can promote (and only they can «forget» a user — see above) other users to **admin** in-app (admins can approve/revoke but not mint admins).

1. Owner logs in via VK **or Яндекс** once → sees a **pending** screen with a short code (the first 8 hex of their `identity_ref`).
2. Promote that account to superadmin + approved:

```bash
ssh psycho 'sudo /usr/local/bin/make-superadmin <handle>'   # deployed helper, or the SQL directly:
ssh psycho "sudo -u postgres psql psychospace -c \
  \"UPDATE accounts SET role='superadmin', status='approved', updated_at=now() \
    WHERE encode(identity_ref,'hex') LIKE '<handle>%';\""
```

3. Reload the app — the owner now has the admin page to approve people and promote admins.

## Login — the redirect URL, and what a 405 means

There are two providers and the trap is the same for both, but they have a
different number of copies to keep in step.

**VK — three copies of one string must agree exactly**, or logins fail in ways
that look unrelated to each other:

| Copy | Where |
|---|---|
| sent at authorize | `VK_REDIRECT_PATH` in `web/src/constants.ts` (baked into the SPA) |
| echoed at the token exchange | `PSYCHOSPACE_VK_REDIRECT_URI` ← GitHub `prod` secret `VK_REDIRECT_URI` |
| allowed by VK | the redirect URL list on the VK app (id 54691267) |

**Yandex — only two**, because the SPA never sees the value: the server builds
the whole authorize URL (ADR-055).

| Copy | Where |
|---|---|
| sent at authorize *and* echoed at the token exchange | `PSYCHOSPACE_YANDEX_REDIRECT_URI` ← GitHub `prod` secret `YANDEX_REDIRECT_URI` |
| allowed by Yandex | the Redirect URI list on the Yandex app |

Current Yandex value: `https://psycho-space.ru/auth/yandex/redirect`, and it must
be the **only** entry in that list. `https://psycho-space.ru/api/auth/yandex/callback`
was registered once and removed: it is POST-only, so a browser sent there gets the
same bare 405 described below. Unlike VK, Yandex has no widget, so the navigation
is the *only* path — a wrong entry there breaks every login rather than some.

Current value: `https://psycho-space.ru/auth/redirect` — a **page** of the SPA. It
must never be an API endpoint: VK navigates a browser there with GET whenever the
widget cannot finish in place (its in-app WebView, a blocked popup, partitioned
third-party storage, "войти другим способом"). While it pointed at the POST-only
`/api/auth/vk/callback`, exactly those people got a bare **405** and everyone else
was unaffected, which is what made it look like one person's broken browser.

Symptoms and what they mean:

- **405 on `/api/auth/<provider>/callback`** — something is using the API endpoint
  as a redirect URL again. Check the SPA constant and the provider's app list.
  Both callbacks are POST-only by design and both have a test pinning it
  (`TestVKRedirectTargetIsServedAsAPage`, `TestYandexCallbackRejectsGET`).
- **`oauth_exchange_failed` for everyone, right after a deploy** — a redirect URI
  the provider echoes back does not match what we sent. For VK that is the SPA
  copy and the secret disagreeing, and they are only ever changed together in one
  deploy. For Yandex there is no SPA copy, so it is the app dashboard.
- **`oauth_not_configured` (503)** — the provider's credentials are absent from
  `/etc/psycho-space/app.env`. Yandex is all-or-none: one or two of the three set
  fails at startup instead, so a 503 means all three are missing rather than
  half-supplied.
- **`bad_state` / a 400 after a redirect login** — the state cookie or the PKCE
  verifier did not survive the round trip; the verifier lives in `sessionStorage`,
  which is per-tab, so a login finished in a *different tab* cannot complete.

Ask VK which URLs it accepts (public app id, no secrets involved — a `302` means
registered, a `200` HTML page means rejected):

```bash
curl -sS -o /dev/null -w '%{http_code}\n' -G https://id.vk.com/authorize \
  --data-urlencode response_type=code --data-urlencode client_id=54691267 \
  --data-urlencode redirect_uri=https://psycho-space.ru/auth/redirect \
  --data-urlencode code_challenge=E9Melhoa2OwvFrEMTJguCHaoeK1t8URWbuGJSstw-cM \
  --data-urlencode code_challenge_method=S256 --data-urlencode state=probe1234567890
```

## nginx & TLS

```bash
ssh psycho 'sudo nginx -t && sudo systemctl reload nginx'
ssh psycho 'sudo tail -f /var/log/nginx/error.log'
ssh psycho 'sudo certbot certificates'          # cert status/expiry
ssh psycho 'sudo systemctl status certbot.timer' # auto-renewal
```

## Health

```bash
curl -fsS https://psycho-space.ru/healthz        # {"status":"ok"}
curl -fsS https://psycho-space.ru/api/ping        # {"message":"pong"}
```

## Fail2ban / SSH

```bash
ssh psycho 'sudo fail2ban-client status sshd'
ssh psycho 'sudo ss -tlnp'                        # confirm sshd is on the hardened port only
```

**Ubuntu 24.04 note:** sshd is run as the standalone `ssh.service` with `ssh.socket`
**disabled** — socket activation ignores the `Port` directive in `sshd_config`, so
the hardened port only works with the socket off. `bootstrap.sh`/`harden-finalize.sh`
handle this; if sshd ever reverts to listening only on 22, run
`sudo systemctl disable --now ssh.socket && sudo systemctl restart ssh.service`.

## SSH recovery / re-enabling root

Hardening disables root SSH **login** (`PermitRootLogin no`) and closes port 22 — it
does **not** remove or lock the root account. Recovery, in order of preference:

1. **`deploy` has full sudo — the normal path.** `ssh -p <port> deploy@<ip>` then
   `sudo -i` for a root shell. To re-enable root SSH login:
   ```bash
   sudo sed -i 's/^PermitRootLogin .*/PermitRootLogin yes/' /etc/ssh/sshd_config.d/99-psycho.conf
   sudo sshd -t && sudo systemctl restart ssh
   sudo ufw allow 22/tcp     # only if you also want port 22 reopened
   ```
2. **Provider console (VNC / serial / recovery mode)** in the hosting panel — the
   ultimate fallback if SSH is entirely unreachable; logs in as root locally,
   bypassing SSH. Use it to undo any sshd/ufw change that locked you out.

You rarely need root over SSH — `deploy` + sudo covers all admin.
