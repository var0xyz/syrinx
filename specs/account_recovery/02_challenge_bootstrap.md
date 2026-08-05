# Account recovery 02 — Challenge + bootstrap API

## Status

Implemented.

## Depends on

[00](00_design.md)

## Context

The account already exists on a healthy server. The client proves it holds
the **active** private key, then receives everything the server can give
without peers: profile, following ids, tip id, own-reed catalog. This is
**not** `RECOVERY_MODE` claim (no nested key chain rebuild).

## Scope

- Main package: handlers in `handlers.go`, SQL in `services.go` (`DataService`).
- Routes registered **always** (not only when `RECOVERY_MODE` is on):

  | Method | Path | Auth |
  |--------|------|------|
  | `GET` | `/api/account-recovery/challenge` | none |
  | `POST` | `/api/account-recovery/bootstrap` | challenge + active-key sig |

- Bootstrap verifies active key, returns payload (profile, following, tip
  id, reed catalog). Client enqueues own-reed fetches via normal
  `REQUEST_REED` ([03](03_rehydration_relay.md)).
- **Profile is server-authoritative** — never taken from the identity export
  file. For root ([07](07_root_user_bootstrap.md)), bootstrap **creates** the
  countersigned profile on first successful proof if none exists yet.
- Device bind hook point (no-op until [06](06_device_takeover.md)).

## Non-goals

- Enqueuing peer relays ([03](03_rehydration_relay.md)).
- SPA ([04](04_spa_keys_only_restore.md)).
- Accepting revoked keys.
- Server recovery claim / `ongoing_recoveries`.

## Design

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
6. Build and return **200** bootstrap JSON.

Idempotent: repeat bootstrap with a valid challenge returns a fresh payload.

### Bootstrap response

```json
{
  "profile": { "...User wire..." },
  "following": ["userId1", "userId2"],
  "tipReedID": "<id or null>",
  "reedIDs": ["...", "..."]
}
```

Rules:

- `profile` — same shape as `GET /users/{id}/profile` (countersigned).
- `following` — all `user_following` targets for this user. If the list
  can be large, either return all in one shot for v1 or add
  `followingCursor` / pages of ≤100; document the choice in the handler.
  Prefer **single full list** until real scale demands paging.
- `tipReedID` — current tip id (newest non-removed by `signed_at`, `id`),
  or `null` for genesis (Approach B).
- `reedIDs` — own non-removed reed ids, tip first. Bodies and per-reed
  signatures come from peer relay; the client verifies after fetch.

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
- [ ] Genesis user → `tipReedID` null
- [ ] Removed reeds excluded from `reedIDs` / tip
