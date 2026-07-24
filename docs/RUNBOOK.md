# psycho-space — Ops / Debugging Runbook

## LLM Continuation Context

_Machine-oriented recap for an LLM continuing this work. Written for agents, not humans — optimise for hand-off. Keep current with the doc._

- **topic:** operational runbook for the psycho-space production box (SSH, logs, DB, nginx, TLS, admin bootstrap).
- **status:** written during P0/P1. Server is provisioned by `scripts/bootstrap.sh`; deploys via `.github/workflows/deploy.yml`.
- **host/port:** intentionally NOT recorded here — this repo is public. The real host and hardened SSH port live only in the GitHub `prod` environment secrets (`DEPLOY_SSH_HOST`, `DEPLOY_SSH_PORT`) and in the operator's local `~/.ssh/config` (+ the local living doc `~/Desktop/psycho-space.md`). Use the `psycho` ssh alias below.
- **app:** systemd unit `psycho-space` under user `psychospace`; binary `/opt/psycho-space/psycho-space`; env `/etc/psycho-space/app.env`; logs `/var/log/psycho-space/app.log`.
- **code:** service in `cmd/psycho-space` + `internal/*`; deploy assets in `deploy/`; provisioning in `scripts/bootstrap.sh`.
- **local-dev:** see "Local development (game / backend)" below — `docker-compose.yml` (Postgres), `./dev.sh db-up|run|seed`, Vite on :5173. `cmd/dev-seed` mints a local approved session (VK can't run locally). Game section: LLM-judged (`internal/game/llm.go`, OpenAI-compatible), content/persona in `content.go`; requires `PSYCHOSPACE_LLM_*` env to play (else `/attempt` → 503).
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

It's an **LLM-judged** character dialogue: convince дядя Ваня (a strange, on-edge drunk who won't let you pass) to let you into your own entrance. To win you must genuinely work through **all three** of his deep themes — longing for a woman/children, his friendship with Тунг Тунг Сахур, and his drinking — before he warms up and lets you through. Each turn the LLM replies in character, judges whether the goal is genuinely reached, picks an **art** to show (his changing mood, or a story/location art with no character — e.g. the passage into the entrance), and generates the **next answer options** (always 4 while playing). The game **requires an LLM** — no mock; unconfigured → `/attempt` returns 503.

> **The third theme is alcohol on purpose — do not make it drugs.** It used to be substance use, and YandexGPT's content filter answered those turns with plain Russian prose instead of the JSON we ask for; the parse failed and the player got an error. The tell in the log is a **`game llm reply not json`** line **~100 ms** after its `game llm request` (real generations take 1–2.5 s). `TestContentAvoidsDrugFlavouredPrompts` guards the prompt material against regressing.
>
> **Three outcomes when the reply isn't clean JSON — check the status code first:**
>
> | Symptom | Status / code | Meaning |
> |---|---|---|
> | Reply was *nearly* valid JSON — raw newline inside a string, options array closed early with the rest under a junk key | **200**, `game llm reply salvaged` at Warn | Recovered: the turn goes through. YandexGPT 5 Lite does this a few percent of the time — see *Salvaging* below. |
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

- **Three endings.** `achieved` → he lets you in (`hallway_pass`). **`game_over`** → you pushed him too far, he throws a punch and the run is lost (`vanya_game_over_hits_us`, forced server-side; the SPA shows an `error` alert titled «Game over»). Neither → the dialogue simply ran out of options. `achieved` always wins over `game_over` on the same turn.
- **The tension scale is the balance mechanism** (`MaxAnger` 100, `StartAnger` 40 — he opens hostile, and both are published in `GET /api/game/config` as `max_anger`/`start_anger` so the client never hardcodes them). It is the game's only cross-turn state besides the transcript: the SPA **sends** `anger` with each `/attempt` and the judge **returns** the new value, which the prompt tells it to raise 10–25 for rudeness/pressure/treading water and lower 5–15 for genuine warmth. **At `MaxAnger` the backend ends the run itself**, whatever the model said about `game_over` — a judge left to its own devices almost never pulls the trigger, which is why the game used to be unloseable. Three server-side guarantees: the incoming value is clamped (a tampering client can neither disable the scale nor pre-lose), an **omitted** `anger` drifts **up by 5** rather than stalling (a stalled scale is an unloseable run), and a win still beats a full scale on the same turn. Logged per turn as `anger_in` / `anger_out` / `anger_from_model`.
- **Mobile fit of the scale**: one 12px caption row plus a 5px bar ≈ **19px**, taken out of the art (`.stage > .portrait-pane` is `flex: 1 1 auto; min-height: 0`), never out of the options. Verified with headless Chrome at 320/360/390/414px — no scroll, all 4 options on screen.
- **Character profile is backend config**: `internal/game/content.go` — `Character` carries public bits (`name`, high-level `goal`, static `greeting` + `opening_options`, and the **`Arts` catalog**: each art's `emoji`/`gradient`/`image`) plus server-only judge material (`Objective` = the real win condition and `Failure` = what makes him snap, both kept off the client so they aren't spoiled; `GameOverArt`; `Motivation`/`Persona`/`TalkStyle`). **Opening is static** — the greeting + first options render with no LLM call; the judge takes over from the player's first pick (the greeting is seeded into the model's context). Subsequent options are LLM-generated. Edit + restart `./dev.sh run`; the SPA fetches `GET /api/game/config`.
- **Assets resolve from the backend catalog** — `Character.Arts`. The judge returns an art *key*; the SPA renders the matching descriptor. Adding/altering arts is backend-only; no client change.
- **Turns are judged by the LLM** in `internal/game/llm.go` (`openAIEvaluator`, OpenAI-compatible: Yandex Cloud / DeepSeek). `POST /api/game/attempt {game_key, character_key, transcript:[{choice,reply}], choice}` → `{reply, art, achieved, options[]}` (`choice:""` = opening turn). The full transcript is sent to the model, trimmed to a ~32k-token window (older exchanges forgotten — `maxContextTokens`).
- **Config** (start target: **YandexGPT 5 Lite**): `PSYCHOSPACE_LLM_BASE_URL=https://llm.api.cloud.yandex.net/v1`, `PSYCHOSPACE_LLM_API_KEY=<key>`, `PSYCHOSPACE_LLM_MODEL=gpt://<folder-id>/yandexgpt-5-lite` (full model URI, folder-specific). Set all three to activate; creds arrive via GH secrets. Context window 32768 (`modelContextTokens`), ~2k reserved for output.
- **Runs** (`{success, steps}`) are recorded via `POST /api/game/runs` and feed the leaderboard (`/runs/leaderboard`) and stats (`/runs/me`).
- **The leaderboard is four record boards over SINGLE runs**, not an aggregate: `longest_win`, `shortest_win`, `longest_loss`, `shortest_loss`. `GET /runs/leaderboard` returns `{boards: {<key>: [{player, steps, plays, wins, losses, mine}]}}`; one SQL query computes each account's `max/min(steps) FILTER (WHERE success / NOT success)` and the service ranks + caps in Go (`buildBoards`). A player appears only on boards they hold a record on — no wins yet means no row on either win board. The SPA renders them as four tabs (`web/src/lib/gameBoards.ts` owns the labels/captions/empty states) and opens on `longest_win` after a win, `longest_loss` after a loss.
- Files: LLM judge `internal/game/llm.go`; content `content.go`; UI `web/src/views/GameView.vue` (turn loop, portrait + landscape, art from catalog); `gameApi` in `web/src/api/endpoints.ts`; migration `migrations/005_game_runs.sql`.

### Game assets (generation & packaging)

Each art in the catalog needs an image. **Placeholders (emoji + gradient) render until real images land** — adding images is backend-only, no client change.

**Names — derive from the art catalog** (source of truth: `internal/game/content.go`, `Character.Arts`). The required filenames are exactly the art keys per game:

- from the API: `curl -s -b <cookie> 'http://localhost:8080/api/game/config?game=smalltalk_khimki' | jq -r '.characters[].arts[].key'`
- or read the `Arts: []Art{…}` block in `content.go`.

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

- Table `game_assets` (`game_key`, `art_key`, `content_type`, `bytes`), migration `006_game_assets.sql`. Kept out of git so the art doesn't bloat the repo/binary.
- Served by `GET /api/game/assets/{game}/{key}` — **public**, `Cache-Control: max-age=86400`; the client downloads each art on demand and caches it. No CDN.
- `GET /api/game/config` advertises an image URL **only for arts that have an uploaded blob**; arts without one keep the emoji placeholder, so partial uploads degrade gracefully. `Art.Image` in `content.go` stays empty — the config handler fills it per uploaded key.

**Upload (owner-only, over SSH for now — an admin UI may come later):**

`deploy/upload-game-assets.py` converts each image in a dir to WebP and prints
`INSERT … ON CONFLICT` SQL to stdout; pipe it to a psql. Requires Pillow
(`pip install pillow`).

```bash
# prod (hardened SSH alias `psycho`):
python3 deploy/upload-game-assets.py ~/Desktop/vanya_assets \
  | ssh psycho "sudo -u postgres psql psychospace"

# local dev DB:
python3 deploy/upload-game-assets.py ~/Desktop/vanya_assets \
  | psql "postgres://psychospace:psychospace@localhost:5432/psychospace"
```

- Art key = filename without extension; it **must** match a key in `content.go`. Re-running upserts. Remove one with `DELETE FROM game_assets WHERE game_key='…' AND art_key='…'`.
- After upload, reload the game — the config now serves the real images (`<img>` in `GameView.vue`; falls back to the emoji if a load fails).

### Tests

```bash
./dev.sh test          # Go unit (incl. internal/game)
./dev.sh integration   # testcontainers (incl. test/integration/game_test.go)
./dev.sh web           # frontend type-check + vitest
./dev.sh pre-commit    # everything (the git hook runs this)
```

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
