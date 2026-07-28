#!/usr/bin/env bash
# Promote the account whose identity_ref hex starts with <handle> to
# superadmin + approved. The superadmin is the only role that can create other
# admins in-app. Installed at /usr/local/bin/make-superadmin by bootstrap.sh,
# and reinstalled by every deploy, so it moves with the schema.
#
# The handle is a prefix of the blind index and says nothing about which login
# provider the account came through, so the verify query prints the provider
# alongside — if two rows come back, that is which is which.
# Run as: sudo make-superadmin <handle>
set -euo pipefail
HANDLE="${1:?usage: make-superadmin <handle-hex-prefix (shown on the pending screen)>}"
case "$HANDLE" in
    *[!0-9a-fA-F]*) echo "handle must be hex" >&2; exit 1 ;;
esac
sudo -u postgres psql psychospace -v ON_ERROR_STOP=1 -c \
  "UPDATE accounts SET role='superadmin', status='approved', updated_at=now() \
   WHERE encode(identity_ref,'hex') LIKE '${HANDLE}%';"
echo "Done. Verify:"
sudo -u postgres psql psychospace -c \
  "SELECT left(encode(identity_ref,'hex'),8) AS handle, provider, role, status FROM accounts WHERE encode(identity_ref,'hex') LIKE '${HANDLE}%';"
