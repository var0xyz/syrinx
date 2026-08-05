# Account recovery 05 — SPA rehydration + tip publish

## Status

Implemented.

## Depends on

[03](03_rehydration_relay.md), [04](04_spa_keys_only_restore.md)

## Context

After keys-only bootstrap the user can browse and publish immediately (with
Approach B server tip id). Own reed bodies arrive through the normal relay
path in the **background** — not server recovery; no waiting, no progress
banner.

## Scope

- Background rehydration via IndexedDB `reedRequests` ([03](03_rehydration_relay.md)).
- On relayed own reed: verify → `storeReed` → `DATA_ACK`; delete
  `reedRequests` row.
- Publish / compose: `previousID` = stored bootstrap `publishTipReedID` (omit if
  null genesis). After first successful post-recovery create, clear
  bootstrap tip override and use normal local tip.
- `REED_NOT_HELD` / `REED_NOT_FOUND` → delete `reedRequests` row (no endless
  retry for that id).

## Non-goals

- Progress UI, dismiss/complete flows, or localStorage progress ledgers.
- Blocking compose or the app shell on rehydration.
- Server recovery progress UI.
- Changing SignReed HTTP beyond sending `previousID` when tip-check
  ([recovery 16](../recovery/16_reed_tip_check.md)) is implemented.
- `POST /account-recovery/complete` (client bookkeeping only via IndexedDB).
- Restoring peer content.

## Design

### Publish tip (Approach B)

```text
function previousIDForPublish(): string | undefined {
  if (bootstrapTipOverride != null) return bootstrapTipOverride;
  return newestLocalOwnReedId(); // normal path
}
```

- `tipReedID === null` from server → genesis → omit `previousID`.
- Else always send bootstrap tip id until override cleared.
- On `reed_fork`, refresh tip from server (existing tip-check client
  behavior once 16 lands); update override if server tip moved.

### Integration with reedsService

- When creating a reed, read `previousID` from `previousIDForPublish()`.
- After countersign + `storeReed` success, clear `publishTipReedID`
  override.

## Test plan

- [x] Compose enabled immediately after bootstrap with tipReedID set
- [x] Genesis (null tip) → publish with empty previousID
- [x] Relayed own reed lands in IndexedDB; `reedRequests` row deleted
- [x] First new create clears tip override
- [x] No progress UI; user can use app while reeds fetch in background
