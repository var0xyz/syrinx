# Account recovery 02 — Challenge + bootstrap API

## Status

Proposed.

## Depends on

[00](00_design.md)

## Context

The account already exists on a healthy server. The client proves it holds
the **active** private key, then receives everything the server can give
without peers: profile, following ids, tip id, own-reed catalog. This is
**not** `RECOVERY_MODE` claim (no nested key chain rebuild).

## Scope

- Package `syrinx/accountrecovery` with handlers, wire types, store
  helpers.
- DDL: `account_rehydrations` (bookkeeping; blank slate in `InitDB`).
- Routes registered **always** (not only when `RECOVERY_MODE` is on):

  | Method | Path | Auth |
  |--------|------|------|
  | `GET` | `/api/account-recovery/challenge` | none |
  | `POST` | `/api/account-recovery/bootstrap` | challenge + active-key sig |

- Bootstrap verifies active key, upserts rehydration row, returns payload
  (profile, following page(s) or full list, tip id, reed catalog).
- Device bind hook point (no-op until [06](06_device_takeover.md)).

## Non-goals

- Enqueuing peer relays ([03](03_rehydration_relay.md)).
- SPA ([04](04_spa_keys_only_restore.md)).
- Accepting revoked keys.
- Server recovery claim / `ongoing_recoveries`.

## Design

### Schema — `account_rehydrations`

```sql
CREATE TABLE IF NOT EXISTS account_rehydrations (
	user_id    TEXT PRIMARY KEY REFERENCES users(id) ON DELETE CASCADE,
	started_at TIMESTAMPTZ NOT NULL DEFAULT CURRENT_TIMESTAMP,
	completed_at TIMESTAMPTZ
);
```

- Row present with `completed_at IS NULL` ⇒ rehydration in progress.
- Bootstrap inserts / refreshes `started_at`, clears `completed_at`
  (`ON CONFLICT` upsert).
- Complete ([03](03_rehydration_relay.md)) sets `completed_at`.
- **Not** an API gate like `ongoing_recoveries`: the user may use the app
  after bootstrap; this row drives server-side relay orchestration and
  client progress only.

### `GET /api/account-recovery/challenge`

Unauthenticated. Response:

```json
{ "challenge": 1710000000 }
```

`challenge` is unix seconds (server clock), same ≤60s freshness window as
recovery claim. Dedicated path (resolved open question): do **not** reuse
`/api/recovery/identity/claim` so account recovery stays off the
`RECOVERY_MODE` surface.

### `POST /api/account-recovery/bootstrap`

Body:

```json
{
  "challenge": 1710000000,
  "userID": "<user id>",
  "fingerprint": "<active key fingerprint>",
  "signature": "<base64 detached PGP sig over decimal challenge>"
}
```

Server steps:

1. Reject if challenge in the future or older than **60 seconds** → 400.
2. Load user by `userID`. Missing → **404** (“account not found” /
   plain English). Account-removed → reject (410/404 per deletion rules).
3. Confirm `fingerprint` is this user’s **active** key and not revoked →
   else **401** / 400.
4. Load public key armor; verify `signature` over the challenge string →
   else 401.
5. Optional: device revoke-all + bind ([06](06_device_takeover.md)) in the
   same TX when binding exists.
6. Upsert `account_rehydrations` (started, not completed).
7. Build and return **200** bootstrap JSON.

Idempotent: repeat bootstrap with a valid challenge refreshes the
rehydration row and returns a fresh payload.

### Bootstrap response

```json
{
  "profile": { "...User wire..." },
  "following": ["userId1", "userId2"],
  "tipReedID": "<id or null>",
  "reeds": [
    {
      "reedID": "...",
      "signedAt": "2026-07-31T12:00:00Z",
      "userSignature": { "...nested..." },
      "serverSignature": { "...nested..." },
      "holderUserIDs": ["...", "..."]
    }
  ]
}
```

Rules:

- `profile` — same shape as `GET /users/{id}` (countersigned).
- `following` — all `user_following` targets for this user. If the list
  can be large, either return all in one shot for v1 or add
  `followingCursor` / pages of ≤100; document the choice in the handler.
  Prefer **single full list** until real scale demands paging.
- `tipReedID` — current tip id (newest non-removed by `signed_at`, `id`),
  or `null` for genesis (Approach B).
- `reeds` — own tip rows excluding removal tombs; include signature blocks
  needed so the client can verify relayed bodies later; `holderUserIDs`
  = current allocations **excluding** the recovering user (self is not a
  body source). Empty `holderUserIDs` is allowed (tip may be unrestorable).

Public key material: either embed active (+ chain) public keys in the
response **or** require the client to `GET` existing key endpoints after
it can sign. Prefer embedding **active public key + any needed
revocations for local store** in bootstrap if that avoids a chicken-egg
before request signing; otherwise document “after local private key
write, fetch `/users/{id}/keys/...`”.

### Errors

| Case | HTTP |
|------|------|
| Bad body / stale challenge | 400 |
| Unknown user | 404 |
| Wrong / revoked / non-active fingerprint or bad sig | 401 |
| Account removed | 410 (account cert) or 404 |

## Test plan

- [ ] Challenge returns unix seconds
- [ ] Bootstrap with valid active key → 200 + tipReedID + following
- [ ] Revoked fingerprint → reject
- [ ] Unknown userID → 404
- [ ] Stale challenge → 400
- [ ] Upserts `account_rehydrations` with `completed_at` null
- [ ] Genesis user → `tipReedID` null
- [ ] Removed reeds excluded from `reeds` / tip
