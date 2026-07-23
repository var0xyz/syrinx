# Deletion 09 — SPA account removal (author + peers)

## Status

Proposed.

## Depends on

[08](08_account_api_fanout.md), [06](06_spa_reed_holders.md)

## Context

Author queues account removal offline-first; peers apply `type: "account"`
certs from fanout or 410 responses and purge per [07](07_account_schema.md).

## Scope

### Author

- `pendingAccountRemoval` (serverID, userID, note, user signature).
- DELETE → store local tombstone/cert → purge local account data per purge
  set (keep own keys as needed for the device) → clear pending.
- Flush pending on startup/online; idempotent DELETEs.
- Verify countersig before committing local purge.

### Peers

- On cert `type: "account"` (fanout or 410 on profile/reed GET): verify both
  signatures.
- Purge local data per [07](07_account_schema.md); **keep public keys**.
- Persist cert so tombstone profile can render `note` under the profile
  chrome when visiting a gone user.
- Do not apply account certs through the reed-only path (switch on `type`).

## Non-goals

- Recovery re-submit of account certs.
- Re-registering the same user id after deletion (clean-slate product
  rules stay elsewhere).

## Design

Tombstone UI: profile route receives 410 → verify → show note (if any),
empty content, keys still resolvable for historical verification UX if
exposed.

## Test plan

- [ ] Author offline queue survives reload; flush converges
- [ ] Peer fanout + 410 profile + 410 reed share one account-apply helper
- [ ] Valid cert → reeds/profile/follows gone; keys remain; note visible
- [ ] Invalid sig → no purge
- [ ] `type: "reed"` cert does not run account purge
