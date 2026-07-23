# psycho-space — Ops / Debugging Runbook

## LLM Continuation Context

_Machine-oriented recap for an LLM continuing this work. Written for agents, not humans — optimise for hand-off. Keep current with the doc._

- **topic:** operational runbook for the psycho-space production box (SSH, logs, DB, nginx, TLS, admin bootstrap).
- **status:** written during P0/P1. Server is provisioned by `scripts/bootstrap.sh`; deploys via `.github/workflows/deploy.yml`.
- **host/port:** intentionally NOT recorded here — this repo is public. The real host and hardened SSH port live only in the GitHub `prod` environment secrets (`DEPLOY_SSH_HOST`, `DEPLOY_SSH_PORT`) and in the operator's local `~/.ssh/config` (+ the local living doc `~/Desktop/psycho-space.md`). Use the `psycho` ssh alias below.
- **app:** systemd unit `psycho-space` under user `psychospace`; binary `/opt/psycho-space/psycho-space`; env `/etc/psycho-space/app.env`; logs `/var/log/psycho-space/app.log`.
- **code:** service in `cmd/psycho-space` + `internal/*`; deploy assets in `deploy/`; provisioning in `scripts/bootstrap.sh`.
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

## Admin bootstrap (first login)

1. Owner logs in via VK once → sees a **pending** screen with a short code (the first 8 hex of their `vk_user_ref`).
2. Promote that account to admin + approved:

```bash
ssh psycho 'sudo /usr/local/bin/make-admin <handle>'     # deployed helper, or the SQL directly:
ssh psycho "sudo -u postgres psql psychospace -c \
  \"UPDATE accounts SET role='admin', status='approved', updated_at=now() \
    WHERE encode(vk_user_ref,'hex') LIKE '<handle>%';\""
```

3. Reload the app — the owner now has the admin page to approve everyone else.

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
