# Deletion 09 — SPA account removal (author + peers)

## Status

Implemented (online-only author delete + peer apply / tombstone note).

## Depends on

[08](08_account_api_fanout.md), [06](06_spa_reed_holders.md)

## Context

Author deletes their account **while online** (no offline pending queue).
Peers apply `type: "account"` certs from fanout or 410 responses and purge
per [07](07_account_schema.md).

## Scope

### Author

- Require network connectivity before signing / DELETE.
- Sign → DELETE with optional `note` → verify countersig → wipe local
  device session. **No** `pendingAccountRemoval` / retry-later path —
  failure leaves the account intact for the user to try again while online.
- Verify countersig before wiping the local device.

### Peers

- On cert `type: "account"` (fanout or 410 on profile/reed GET): verify both
  signatures.
- Purge local data per [07](07_account_schema.md); **keep public keys**.
- Persist cert so tombstone profile can render `note` under the profile
  chrome when visiting a gone user.
- Do not apply account certs through the reed-only path (switch on `type`).

## Non-goals

- Offline-first author queue (reed removal only).
- Recovery re-submit of account certs.
- Re-registering the same user id after deletion (clean-slate product
  rules stay elsewhere).

## Design

Tombstone UI: profile route receives 410 → verify → show note (if any),
empty content, keys still resolvable for historical verification UX if
exposed.

## Test plan

- [ ] Offline: delete control disabled / API path rejects; no pending row
- [ ] Online success: cert accepted; local session wiped
- [ ] Peer fanout + 410 profile + 410 reed share one account-apply helper
- [ ] Valid cert → reeds/profile/follows gone; keys remain; note visible
- [ ] Invalid sig → no purge
- [ ] `type: "reed"` cert does not run account purge
