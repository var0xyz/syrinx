# Deletion 08 — Account-removal API, 410 bodies, fanout

## Status

Proposed.

## Depends on

[07](07_account_schema.md), [02](02_reed_payload.md) (countersign conventions),
[04](04_reed_fanout.md) (realtime + catch-up pattern)

## Context

Submit account removal (idempotent countersign-once), serve **410** tombstones
with `type: "account"`, distribute certs with the **same realtime / sync
machinery** as reed removal and new reeds. See
[README examples](README.md#example--account).

## Scope

- Author-authenticated account-removal submit via **DELETE** (same verb as
  reed removal) with body: user signature + optional `note` ≤140.
- Canonical account payload (`type: account`, serverID, userID, note in
  content or headers — match reed style; note is signed).
- Countersign once; persist; replay same cert on retry (**200** + cert).
- `GET` profile (and equivalent): if account-removal exists → **410** +
  account cert (not 404). Unknown user → **404**.
- `GET /reeds/{userID}/{reedID}`: if account-removal exists for `userID` →
  **410** + account cert (**before** reed-removal check).
- **Live fanout** on first accept only: `pending_events` / dispatch to
  online followers (and other new-reed audiences as appropriate) with
  event type e.g. `account_removed` and the full account cert as payload.
- **Catch-up on `SYNC_REQUEST`:** peers who still **follow** the user **or**
  still have **allocations** for that author’s reeds receive the account
  cert once; after apply, local purge per 07 clears the need to re-deliver.
- Disable further authenticated actions as the deleted user (product rule:
  session ends; keys remain for verify-only paths as needed).

## Non-goals

- SPA (09).
- Minting per-reed removals.
- A separate deletion-notification table.

## Design

### Wire

JSON as in README: `type`, `serverID`, `userID`, `note`, `signature`,
`server` block.

### Decision order on GET reed

Account cert → reed cert → 200 → 404 (see README).

### Distribution

Same pattern as [04](04_reed_fanout.md): live path for online recipients;
sync catch-up recomputes who still needs the cert; GET 410 is the safety
net. Account cert alone authorizes reed purge — no per-reed events required
for account deletion.

## Test plan

- [ ] First account removal → stored cert; online peers get one fanout
- [ ] Retry → identical server signature; no second fanout
- [ ] Offline through removal → SYNC_REQUEST delivers account cert once
- [ ] GET profile → 410 `type: "account"` with note
- [ ] GET reed under deleted author → 410 account cert (not reed, not 404)
- [ ] Note >140 → 400
- [ ] Unknown user profile → 404 (no body cert)
