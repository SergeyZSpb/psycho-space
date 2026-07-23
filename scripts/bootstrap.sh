#!/usr/bin/env bash
# One-time provisioning for the psycho-space production box (fresh Ubuntu 24.04).
# Idempotent — safe to re-run. Run as root over your EXISTING access, e.g.:
#
#   ssh <your-root-alias> "PSYCHO_SSH_PORT=<port> bash -s" < scripts/bootstrap.sh
#
# Required:
#   PSYCHO_SSH_PORT   the hardened SSH port to move to (pick a high, non-standard
#                     port and keep it secret — it is NEVER committed; store it as
#                     the DEPLOY_SSH_PORT GitHub secret and in your ~/.ssh/config).
# Optional:
#   PSYCHO_DOMAIN     default: psycho-space.ru
#   PSYCHO_LE_EMAIL   default: sck.spb@yandex.ru   (Let's Encrypt contact)
#   PSYCHO_DB_PASSWORD  default: generated and printed
#
# This script does NOT close port 22 or disable root — that is a separate,
# verify-first step (scripts/harden-finalize.sh) so you cannot lock yourself out.
set -euo pipefail

REPO_RAW="https://raw.githubusercontent.com/SergeyZSpb/psycho-space/main"
DOMAIN="${PSYCHO_DOMAIN:-psycho-space.ru}"
LE_EMAIL="${PSYCHO_LE_EMAIL:-sck.spb@yandex.ru}"
SSH_PORT="${PSYCHO_SSH_PORT:?set PSYCHO_SSH_PORT to your chosen 65xxx port}"
DB_PW="${PSYCHO_DB_PASSWORD:-$(openssl rand -base64 24 | tr -d '/+=' | head -c 32)}"

case "$SSH_PORT" in
    ''|*[!0-9]*) echo "PSYCHO_SSH_PORT must be numeric" >&2; exit 1 ;;
esac
[ "$SSH_PORT" -ge 1024 ] && [ "$SSH_PORT" -le 65535 ] || { echo "PSYCHO_SSH_PORT out of range" >&2; exit 1; }
[ "$(id -u)" -eq 0 ] || { echo "run as root" >&2; exit 1; }

echo "== psycho-space bootstrap: domain=$DOMAIN ssh_port=$SSH_PORT =="

# --- packages ---------------------------------------------------------------
export DEBIAN_FRONTEND=noninteractive
apt-get update -qq
apt-get install -y -qq postgresql nginx certbot fail2ban ufw curl ca-certificates openssl

# --- users ------------------------------------------------------------------
id psychospace >/dev/null 2>&1 || useradd --system --no-create-home --shell /usr/sbin/nologin psychospace
id deploy      >/dev/null 2>&1 || useradd --create-home --shell /bin/bash deploy

# deploy gets passwordless sudo (single-admin box; the CI key is a protected
# GitHub secret, the operator uses their own key). Documented trade-off.
cat > /etc/sudoers.d/psycho-deploy <<'EOF'
deploy ALL=(ALL) NOPASSWD: ALL
EOF
chmod 440 /etc/sudoers.d/psycho-deploy

# --- ssh keys ---------------------------------------------------------------
install -d -m 700 -o deploy -g deploy /home/deploy/.ssh
DEPLOY_AK=/home/deploy/.ssh/authorized_keys
touch "$DEPLOY_AK"

# Let the operator keep access as deploy@<port> using their existing key.
if [ -f /root/.ssh/authorized_keys ]; then
    while IFS= read -r k; do
        [ -n "$k" ] || continue
        grep -qxF "$k" "$DEPLOY_AK" || echo "$k" >> "$DEPLOY_AK"
    done < /root/.ssh/authorized_keys
fi

# Dedicated CI deploy keypair (printed once for the DEPLOY_SSH_KEY secret).
CI_KEY=/root/psycho-ci-key
if [ ! -f "$CI_KEY" ]; then
    ssh-keygen -t ed25519 -N '' -C 'psycho-space-ci' -f "$CI_KEY" >/dev/null
fi
CI_PUB="$(cat "$CI_KEY.pub")"
grep -qxF "$CI_PUB" "$DEPLOY_AK" || echo "$CI_PUB" >> "$DEPLOY_AK"
chown deploy:deploy "$DEPLOY_AK"
chmod 600 "$DEPLOY_AK"

# --- directories ------------------------------------------------------------
install -d -m 755 -o root       -g root       /opt/psycho-space
install -d -m 750 -o root       -g psychospace /etc/psycho-space
install -d -m 750 -o psychospace -g psychospace /var/log/psycho-space
install -d -m 755 -o root       -g root       /var/www/certbot

# --- privileged helpers (from the public repo) ------------------------------
curl -fsSL "$REPO_RAW/deploy/psycho-deploy.sh"   -o /usr/local/bin/psycho-deploy
curl -fsSL "$REPO_RAW/deploy/make-superadmin.sh" -o /usr/local/bin/make-superadmin
chmod 755 /usr/local/bin/psycho-deploy /usr/local/bin/make-superadmin

# --- systemd unit -----------------------------------------------------------
curl -fsSL "$REPO_RAW/deploy/systemd/psycho-space.service" -o /etc/systemd/system/psycho-space.service
systemctl daemon-reload
systemctl enable psycho-space >/dev/null 2>&1 || true   # started by first deploy

# --- postgres ---------------------------------------------------------------
systemctl enable --now postgresql
sudo -u postgres psql -tAc "SELECT 1 FROM pg_roles WHERE rolname='psychospace'" | grep -q 1 \
    || sudo -u postgres psql -qc "CREATE ROLE psychospace LOGIN PASSWORD '$DB_PW'"
sudo -u postgres psql -qc "ALTER ROLE psychospace PASSWORD '$DB_PW'"
sudo -u postgres psql -tAc "SELECT 1 FROM pg_database WHERE datname='psychospace'" | grep -q 1 \
    || sudo -u postgres createdb -O psychospace psychospace

# --- nginx: bootstrap http-only site (ACME + proxy) -------------------------
# The first CI deploy replaces this with the full TLS config (deploy/nginx).
rm -f /etc/nginx/sites-enabled/default
cat > /etc/nginx/sites-available/psycho-space.conf <<EOF
server {
    listen 80;
    listen [::]:80;
    server_name $DOMAIN;
    location /.well-known/acme-challenge/ { root /var/www/certbot; }
    location / {
        proxy_pass http://127.0.0.1:8080;
        proxy_set_header Host \$host;
        proxy_set_header X-Forwarded-For \$proxy_add_x_forwarded_for;
        proxy_set_header X-Forwarded-Proto \$scheme;
    }
}
EOF
ln -sf /etc/nginx/sites-available/psycho-space.conf /etc/nginx/sites-enabled/psycho-space.conf
nginx -t && systemctl reload nginx

# --- TLS cert (webroot; non-fatal if DNS isn't ready yet) -------------------
install -d -m 755 /etc/letsencrypt/renewal-hooks/deploy
cat > /etc/letsencrypt/renewal-hooks/deploy/reload-nginx.sh <<'EOF'
#!/usr/bin/env bash
systemctl reload nginx
EOF
chmod 755 /etc/letsencrypt/renewal-hooks/deploy/reload-nginx.sh

if certbot certonly --webroot -w /var/www/certbot -d "$DOMAIN" \
        --agree-tos -m "$LE_EMAIL" --non-interactive --keep-until-expiring; then
    echo "TLS: certificate for $DOMAIN is ready"
else
    echo "WARN: certbot failed (DNS not propagated?). Re-run after DNS resolves:" >&2
    echo "  sudo certbot certonly --webroot -w /var/www/certbot -d $DOMAIN --agree-tos -m $LE_EMAIL -n" >&2
fi

# --- fail2ban ---------------------------------------------------------------
cat > /etc/fail2ban/jail.local <<EOF
[sshd]
enabled  = true
port     = $SSH_PORT
backend  = systemd
maxretry = 5
bantime  = 1h
EOF
systemctl enable --now fail2ban
systemctl restart fail2ban

# --- sshd: add the new port (KEEP 22 for now), keys-only -------------------
cat > /etc/ssh/sshd_config.d/99-psycho.conf <<EOF
Port 22
Port $SSH_PORT
PasswordAuthentication no
PubkeyAuthentication yes
EOF
# validate before applying
sshd -t
systemctl restart ssh || systemctl restart sshd

# --- firewall ---------------------------------------------------------------
ufw allow 22/tcp        >/dev/null
ufw allow "$SSH_PORT"/tcp >/dev/null
ufw allow 80/tcp        >/dev/null
ufw allow 443/tcp       >/dev/null
ufw --force enable

cat <<EOF

============================================================================
 psycho-space bootstrap complete.

 1) DB password (store as the POSTGRES_PASSWORD GitHub secret):
      $DB_PW

 2) CI deploy PRIVATE key (store as the DEPLOY_SSH_KEY GitHub secret) — copy
    everything between the BEGIN/END lines:
$(sed 's/^/      /' "$CI_KEY")

 3) Also set these GitHub 'prod' environment secrets:
      DEPLOY_SSH_HOST = <this server's IP or $DOMAIN>
      DEPLOY_SSH_PORT = $SSH_PORT
      DEPLOY_SSH_USER = deploy
      VK_APP_ID, VK_SERVICE_TOKEN, VK_REDIRECT_URI
      APP_ENC_KEY, APP_HMAC_KEY, APP_SESSION_KEY   (openssl rand -base64 32 each)

 4) VERIFY new SSH access in a NEW terminal BEFORE finalizing hardening:
      ssh -p $SSH_PORT deploy@<server-ip>
    Only once that works, close port 22 + disable root:
      ssh -p $SSH_PORT deploy@<server-ip> \\
        "PSYCHO_SSH_PORT=$SSH_PORT sudo -E bash -s" < scripts/harden-finalize.sh

 SSH currently listens on BOTH 22 and $SSH_PORT so you are not locked out.
============================================================================
EOF
