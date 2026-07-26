# ADR-002 · Provisioning is a one-time manual script; only the app deploys from CI

## LLM Continuation Context

_Machine-oriented recap for an LLM continuing this work. Written for agents, not humans — optimise for hand-off, not prose. Keep current with the doc._

- **topic:** Provisioning is a one-time manual script; only the app deploys from CI
- **status:** Accepted · 2026-07-25
- **summary:** one paragraph in [ARCHITECTURE.md §8.1](../ARCHITECTURE.md#adr-002--provisioning-is-a-one-time-manual-script-only) — this file is the detail behind it.
- **code:** `scripts/bootstrap.sh` · `scripts/harden-finalize.sh`

---

`scripts/bootstrap.sh` installs Postgres, nginx, certbot, systemd units, the `deploy` user, and the CI key, then hardens SSH. It is run once, by hand, over the existing root access — and it deliberately leaves SSH listening on **both** the old and the new port so a mistake cannot lock the operator out. `scripts/harden-finalize.sh` closes the old port afterwards, once the new one is proven from a second terminal.

_Reasoning:_ the lockout-sensitive part of provisioning is exactly the part that should not run unattended from a pipeline.
