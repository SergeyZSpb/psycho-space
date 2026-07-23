#!/usr/bin/env bash
# Finalize SSH hardening AFTER you have verified you can log in on the new port.
# Closes port 22, disables root login and passwords, keeps key auth + TCP
# forwarding (needed for the DB tunnel). Idempotent.
#
#   ssh -p <port> deploy@<host> "PSYCHO_SSH_PORT=<port> sudo -E bash -s" < scripts/harden-finalize.sh
#
# Required: PSYCHO_SSH_PORT (the port you already verified works).
set -euo pipefail

SSH_PORT="${PSYCHO_SSH_PORT:?set PSYCHO_SSH_PORT to the verified port}"
case "$SSH_PORT" in ''|*[!0-9]*) echo "PSYCHO_SSH_PORT must be numeric" >&2; exit 1 ;; esac
[ "$(id -u)" -eq 0 ] || { echo "run with sudo" >&2; exit 1; }

# Safety: confirm the current SSH session is actually on the hardened port, so we
# don't cut off the only way in.
if [ -n "${SSH_CONNECTION:-}" ]; then
    conn_port="$(awk '{print $4}' <<<"$SSH_CONNECTION")"
    if [ "$conn_port" != "$SSH_PORT" ]; then
        echo "REFUSING: this session is on port $conn_port, not $SSH_PORT." >&2
        echo "Reconnect on $SSH_PORT first, then re-run." >&2
        exit 1
    fi
fi

cat > /etc/ssh/sshd_config.d/99-psycho.conf <<EOF
Port $SSH_PORT
PermitRootLogin no
PasswordAuthentication no
PubkeyAuthentication yes
AllowTcpForwarding yes
EOF

sshd -t
# Keep socket activation disabled so the Port directive takes effect (Ubuntu 24.04).
systemctl disable --now ssh.socket 2>/dev/null || true
systemctl restart ssh.service 2>/dev/null || systemctl restart ssh || systemctl restart sshd
sleep 1
if ! ss -tlnp 2>/dev/null | grep -q ":$SSH_PORT "; then
    echo "ERROR: sshd not listening on $SSH_PORT after restart — NOT closing port 22." >&2
    exit 1
fi

ufw delete allow 22/tcp >/dev/null 2>&1 || true

echo "Hardening finalized: SSH only on $SSH_PORT, no root, no passwords, port 22 closed."
echo "Confirm a fresh connection still works: ssh -p $SSH_PORT deploy@<host>"
