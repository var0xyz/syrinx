# Recovery 07 — SPA recover client

## Status

Proposed.

## Depends on

[04](04_own_identity_claim.md), [05](05_peer_identity_report.md),
[06](06_reeds_follows_complete.md)

## Context

Recovery is client-driven: the device holds the sync ledger and drives
claim → peers → reeds → follows → complete. See [README](README.md)
*Client responsibilities*.

## Scope

- Detect `recoveryMode` / `signupsEnabled` from `/server/info`.
- Unauthenticated allowlist includes claim GET/POST.
- Build nested key chain from IndexedDB (full chain or skip peer).
- Sync ledger module: claim (challenge + sign) → peer identities → reeds
  (one by one) → follows (chunks of 100) → `POST /complete`.
- `/recover` route + link from home; block normal app use while importing
  (mirror server gate).
- Do not offer recovery if the device already has a different live logged-in
  account (forfeit rule).

## Non-goals

- No Proposal 11 system notifications.
- No change to `.sxb` backup format beyond using existing local data as the
  source for report-back.
- Server API changes (those are steps 01–06).

## Design

Prefer a dedicated `recoveryLedger` (or equivalent) service. Progress via
existing ephemeral notification store. Keep recovery UI minimal.

## Test plan

- [ ] Against a recovery-mode server: import backup → recover → claim →
      complete → normal navigation works
- [ ] Mid-import request to a normal API returns 403
- [ ] Peer without full nest is skipped without failing the whole run
- [ ] Signups button hidden/disabled when `signupsEnabled` is false
