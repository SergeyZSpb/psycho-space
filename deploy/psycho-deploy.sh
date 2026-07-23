#!/usr/bin/env bash
# Privileged deploy step, installed at /usr/local/bin/psycho-deploy by bootstrap.sh
# and granted to the `deploy` user via a NOPASSWD sudoers entry. The CI deploy job
# stages files into /tmp/psycho-deploy/ then runs `sudo psycho-deploy`.
#
# Expected staged files in /tmp/psycho-deploy/:
#   psycho-space          (the binary, required)
#   app.env               (rendered env file, required)
#   psycho-space.service  (systemd unit, optional — installed if present)
#   psycho-space.conf     (nginx site, optional — installed if present)
set -euo pipefail
STAGE=/tmp/psycho-deploy

[ -f "$STAGE/psycho-space" ] || { echo "missing $STAGE/psycho-space" >&2; exit 1; }
[ -f "$STAGE/app.env" ]      || { echo "missing $STAGE/app.env" >&2; exit 1; }

install -o root -g root -m 0755 "$STAGE/psycho-space" /opt/psycho-space/psycho-space
install -o psychospace -g psychospace -m 0600 "$STAGE/app.env" /etc/psycho-space/app.env

if [ -f "$STAGE/psycho-space.service" ]; then
    install -m 0644 "$STAGE/psycho-space.service" /etc/systemd/system/psycho-space.service
    systemctl daemon-reload
fi

if [ -f "$STAGE/psycho-space.conf" ]; then
    install -m 0644 "$STAGE/psycho-space.conf" /etc/nginx/sites-available/psycho-space.conf
    ln -sf /etc/nginx/sites-available/psycho-space.conf /etc/nginx/sites-enabled/psycho-space.conf
    if nginx -t; then
        systemctl reload nginx
    else
        echo "nginx config test failed — leaving previous config running" >&2
    fi
fi

systemctl enable --now psycho-space >/dev/null 2>&1 || true
systemctl restart psycho-space

# Health gate: the service must answer locally within ~20s.
for _ in $(seq 1 20); do
    if curl -fsS http://127.0.0.1:8080/healthz >/dev/null 2>&1; then
        echo "psycho-deploy: healthz OK"
        rm -rf "$STAGE"
        exit 0
    fi
    sleep 1
done

echo "psycho-deploy: healthz FAILED after restart" >&2
journalctl -u psycho-space -n 50 --no-pager >&2 || true
exit 1
