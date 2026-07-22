# Recovery 09 — User status probe (`POST /api/users/status`)

## Status

Implemented.

## Depends on

[03](03_bookkeeping_and_gates.md), [04](04_own_identity_claim.md) (claimed vs
`unclaimed_accounts` / `ongoing_recoveries` semantics).

## Context

After a hostile takeover the community moves to a **new origin**. Browser storage
does not follow. The user brings an encrypted backup from the old app, decrypts
it on the new origin, and the client must learn whether this server already
knows that account — without trusting a client-supplied public key and without
asking the user to sign anything yet.

The client sends the **countersigned profile** from the backup. The server
verifies its own countersignature and reports claim / recovery progress state.
SPA branching on the response is [10](10_spa_unified_restore.md).

## Scope

- Register **`POST /api/users/status`** always (not only under `RECOVERY_MODE`).
  Lives in the main API (not `syrinx/recovery` route registration), because the
  import path needs it when recovery mode is off.
- **Unauthenticated** (no signature-auth). Body is the full `User` wire profile
  including the `server` countersignature block.
- Server steps:
  1. Reject malformed body → **400**.
  2. Require `profile.server.id ==` this server’s ID → else **400**.
  3. Verify the **server countersignature** over the profile → else **400**.
  4. Look up `profile.id` in `users`.
  5. If missing, **or** the row is still in `unclaimed_accounts` → **404**
     `{ "status": "unknown" }` (peer-seeded ≠ claimed).
  6. If a claimed row exists and the submitted profile’s server timestamp is
     **older** than the DB’s `server_signed_at` → **400** (stale backup; would
     fork the identity / key chain). A **newer** submitted profile is allowed.
     A countersigned profile either matches the stored record or is a different
     countersigned record — no partial match.
  7. If `profile.id` is in `ongoing_recoveries` → **409**
     `{ "status": "ongoing" }`.
  8. Otherwise (claimed, recovery finished) → **200**
     `{ "status": "complete" }`.

### Response body

```json
{ "status": "complete" | "unknown" | "ongoing" }
```

| HTTP | `status`   | Meaning                                      |
|------|------------|----------------------------------------------|
| 200  | `complete` | Claimed account; not in `ongoing_recoveries` |
| 404  | `unknown`  | Not present, or peer-seeded / unclaimed only |
| 409  | `ongoing`  | Claimed and mid-recovery (`ongoing_recoveries`) |
| 400  | —          | Bad body, wrong `serverID`, bad countersig, or stale (older) profile |

## Non-goals

- SPA backup UX or branching ([10](10_spa_unified_restore.md)).
- Writing user rows or advancing recovery (claim / peer / reeds stay 04–06).
- Device binding ([17](17_device_binding.md)).

## Design

Do **not** require or trust a client-presented public key at this step. Proof that
the profile belonged on this server is the restored server key verifying the
countersignature. Ownership proof for an unknown account remains the claim
challenge in [04](04_own_identity_claim.md).

## Test plan

- [ ] Valid countersigned profile, claimed, not ongoing → 200 `complete`
- [ ] User absent → 404 `unknown`
- [ ] User only in `unclaimed_accounts` → 404 `unknown`
- [ ] Claimed + `ongoing_recoveries` → 409 `ongoing`
- [ ] Wrong `server.id` or bad countersig → 400
- [ ] Submitted profile older than DB → 400
- [ ] Submitted profile newer than DB, claimed, not ongoing → 200
- [ ] Endpoint available with `RECOVERY_MODE` off
