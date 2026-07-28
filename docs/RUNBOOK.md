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
- **naming:** game 1 is `GameKhimki` everywhere — package `internal/gamekhimki/`, table `game_khimki_runs` (art stays in the shared `game_assets` — ADR-031), routes `/api/game-khimki/*`, view `GameKhimkiView.vue` at `/app/game-khimki`. It was generic `game`/`game_runs`/`game_assets`/`/api/game/*` until `migrations/007_game_khimki_rename.sql`, so **anything older than that — a log line, a saved query, a bookmark — uses the old names.** `game_key` values are unchanged (`smalltalk_khimki`). Rule: `ARCHITECTURE.md` → ADR-030.
- **siblings:** `ARCHITECTURE.md` (the shape of the system — logical/runtime/data/deployment views — plus §8, one paragraph per decision record saying why it is that shape, each rewritten in place when the decision moves), `../CLAUDE.md` (working rules and gates).
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

### Get into the gated app without VK

VK ID is IP-allowlisted to prod and its redirect URI is the prod domain, so the real login can't run locally. Seed an approved account + session instead:

```bash
./dev.sh seed                          # superadmin "Локальный Разработчик"
./dev.sh seed -role user -name Гость   # a plain approved user
```

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

### Tests

```bash
./dev.sh test          # Go unit (incl. internal/gamekhimki)
./dev.sh integration   # testcontainers (incl. test/integration/game_test.go)
./dev.sh web           # frontend type-check + vitest
./dev.sh e2e           # Playwright, mobile viewports, /api stubbed in the browser
./dev.sh e2e-stack     # Playwright against the REAL binary + Postgres (needs Docker)
./dev.sh cover         # coverage: Go unit + Go integration + web
./dev.sh pre-commit    # everything (the git hook runs this) — never bypassed
```

**The two Playwright suites do different jobs.** `web/e2e/` stubs `/api` with route
interception and checks responsive layout at 360/390/768/1440 px. The desktop
project is the newest and was added because its absence hid a real bug: the yard
drew at a different apparent scale above tablet width and nothing had ever opened
it there. The width-gated mobile rules (tap targets, the 44 px floor) skip at
1440 by design — that project covers the ungated half. `web/e2e-stack/` runs
`scripts/e2e-stack.sh` — throwaway Postgres on port 55433 (tmpfs, force-recreated
per run), the SPA built and the server compiled and started on :8081, then accounts
seeded straight into the database (`cmd/dev-seed -json`) so tests can be "logged
in" without VK. Its assertions are all "reload, or ask the API, and it is still
true", which only holds if the SQL ran. Videos are recorded for **every** test in
both suites (`web/test-results/`).

First run needs the browser once: `(cd web && npx playwright install chromium)`.

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
# Correlate a specific account without exposing PII (we log vk_user_ref hex, never names):
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
  \"SELECT left(encode(vk_user_ref,'hex'),8) AS handle, role, status, created_at FROM accounts WHERE status='pending' ORDER BY created_at;\""

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

## Superadmin bootstrap (first login)

The **superadmin** is created once via script; only the superadmin can promote other users to **admin** in-app (admins can approve/revoke but not mint admins).

1. Owner logs in via VK once → sees a **pending** screen with a short code (the first 8 hex of their `vk_user_ref`).
2. Promote that account to superadmin + approved:

```bash
ssh psycho 'sudo /usr/local/bin/make-superadmin <handle>'   # deployed helper, or the SQL directly:
ssh psycho "sudo -u postgres psql psychospace -c \
  \"UPDATE accounts SET role='superadmin', status='approved', updated_at=now() \
    WHERE encode(vk_user_ref,'hex') LIKE '<handle>%';\""
```

3. Reload the app — the owner now has the admin page to approve people and promote admins.

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
