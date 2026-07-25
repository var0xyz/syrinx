# Recovery 13 — SPA peer identity report-back

## Status

Implemented.

## Depends on

[12](12_spa_own_identity_claim.md)

## Context

After own claim, the client reports peer identities the device holds, one user per
request, using signature-auth. See [05](05_peer_identity_report.md).

## Scope

- Execute **peer identity** progress items sequentially:
  `POST /api/recovery/identity` with `{ profile, key }` (full nested chain).
- Build nest per peer from IndexedDB; if a full chain cannot be assembled,
  **skip** that peer (already reflected in [11](11_spa_recovery_progress.md))
  without failing the run.
- Update persisted progress (`peer:<userId>` start/end or skipped) after each
  success or skip.
- Continue on individual peer failure (log + optional retry UI); do not abort the
  whole recovery for one bad peer.

## Non-goals

- Reeds, follows, `complete` (14).
- Playwright coverage (deferred).

## Design

Reuse the key-chain builder from 12 with a different subject user. Caller must
be the claimant (authenticated after 12).

## Test plan

- [ ] Peer with full nest → 200, ledger advances
- [ ] Peer without full nest → skipped
- [ ] Own user ID never sent on this endpoint (claim only)
