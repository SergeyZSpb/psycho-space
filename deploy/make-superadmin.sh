#!/usr/bin/env bash
# Promote the account whose vk_user_ref hex starts with <handle> to
# superadmin + approved. The superadmin is the only role that can create other
# admins in-app. Installed at /usr/local/bin/make-superadmin by bootstrap.sh.
# Run as: sudo make-superadmin <handle>
set -euo pipefail
HANDLE="${1:?usage: make-superadmin <handle-hex-prefix (shown on the pending screen)>}"
case "$HANDLE" in
    *[!0-9a-fA-F]*) echo "handle must be hex" >&2; exit 1 ;;
esac
sudo -u postgres psql psychospace -v ON_ERROR_STOP=1 -c \
  "UPDATE accounts SET role='superadmin', status='approved', updated_at=now() \
   WHERE encode(vk_user_ref,'hex') LIKE '${HANDLE}%';"
echo "Done. Verify:"
sudo -u postgres psql psychospace -c \
  "SELECT left(encode(vk_user_ref,'hex'),8) AS handle, role, status FROM accounts WHERE encode(vk_user_ref,'hex') LIKE '${HANDLE}%';"
