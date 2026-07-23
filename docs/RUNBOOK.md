# psycho-space — Ops / Debugging Runbook

## LLM Continuation Context

_Machine-oriented recap for an LLM continuing this work. Written for agents, not humans — optimise for hand-off. Keep current with the doc._

- **topic:** operational runbook for the psycho-space production box (SSH, logs, DB, nginx, TLS, admin bootstrap).
- **status:** written during P0/P1. Server is provisioned by `scripts/bootstrap.sh`; deploys via `.github/workflows/deploy.yml`.
- **host:** `psycho-space.ru` / `185.70.105.77`, Ubuntu 24.04. SSH hardened to port **2222**, user `deploy` (admin), no root login, no passwords. App runs as systemd unit `psycho-space` under user `psychospace`.
- **code:** service in `cmd/psycho-space` + `internal/*`; deploy assets in `deploy/`; provisioning in `scripts/bootstrap.sh`.
- **next:** keep this current as ops procedures are exercised; add a new section whenever you work out a procedure not captured here (read-before / write-after).
- **constraints:** never paste real personal data or secrets into shared places; the app log is PII-free by design, but the DB and nginx access log are not — treat their contents as confidential.

---

All commands assume the SSH alias below. The dev team's access is for observability/debugging; production changes go through CI.

```
# ~/.ssh/config
Host psycho
    HostName 185.70.105.77
    User deploy
    Port 2222
    IdentityFile ~/.ssh/id_ed25519_psycho
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

## Admin bootstrap (first login)

1. Owner logs in via VK once → sees a **pending** screen with a short code (the first 8 hex of their `vk_user_ref`).
2. Promote that account to admin + approved:

```bash
ssh psycho './make-admin.sh <handle>'     # deployed helper, or the SQL directly:
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
ssh psycho 'sudo ss -tlnp | grep -E ":2222|:443|:80"'
```
