# Invites 02 — Lifecycle API (create / status / revoke / check)

## Status

Implemented (`invites.RegisterRoutes` signed create / status / revoke /
check; quota + closed-mode on create; `/api/invites/check` allowlisted).

## Depends on

[01](01_schema_and_store.md)

## Context

Schema and store exist. Authenticated users mint client-id invites with a
user signature over id + `createdAt`; the server verifies, stores the token
hash, and countersigns. There is **no list API** — clients keep invites
locally and poll status-by-id while pending.

## Scope

- `invites.RegisterRoutes` mounted from `main` under `/api`.
- Handlers: create (signed), status, revoke, check.
- Enforce `MAX_INVITES_PER_USER` on create.
- Enforce `SIGNUP_MODE=closed` → create returns 403 (status/revoke remain).
- Allowlist `GET /api/invites/check` in signature-auth middleware.
- Identity helpers: `BuildInviteUserPayload` / `BuildInviteServerPayload`.

## Non-goals

- Consuming invites at signup (03).
- SPA (04–05).
- Returning inviter identity from `check`.

## Design

### Routes

| Method   | Path                 | Auth     |
|----------|----------------------|----------|
| `POST`   | `/api/invites`       | Required |
| `GET`    | `/api/invites/{id}`  | Required |
| `DELETE` | `/api/invites/{id}`  | Required |
| `GET`    | `/api/invites/check` | None     |

### `POST /api/invites`

Request body:

```json
{
  "id": "<clientInviteID>",
  "token": "<rawToken>",
  "createdAt": "<RFC3339 seconds>",
  "userSignature": { "fingerprint": "...", "armor": "..." }
}
```

1. Resolve caller `userID` from auth context.
2. If mode `closed` → **403** `"Signups are closed on this server"`.
3. Validate id alphabet/length; `createdAt` within skew of server now.
4. If max ≠ −1 and `CountByCreator >= max` → **403** `"Invite limit reached"`.
5. Rebuild `invite-user` payload; verify `userSignature` against caller key.
6. `Insert` `(created_by, id, token_hash, created_at)`; duplicate → **409**.
7. Countersign `invite-server`; **201**:

```json
{
  "id": "...",
  "token": "...",
  "createdAt": "...",
  "userSignature": { "fingerprint": "...", "armor": "..." },
  "serverSignature": { "serverID": "...", "fingerprint": "...", "armor": "...", "timestamp": "..." }
}
```

### `GET /api/invites/{id}`

Caller-owned only (composite key). **200** status wire (no token, no sigs).
**404** if missing or not owned.

### `DELETE /api/invites/{id}`

Same revoke semantics as before (404 / 409 claimed / 204 revoked).

### `GET /api/invites/check?token=`

Unchanged: `{ valid: true }` only when pending.

## Test plan

- [x] Create while `open` / `invite` → 201 with nested sigs
- [x] Create at quota → 403; `max=-1` never hits quota
- [x] Create while `closed` → 403; status still 200
- [x] Duplicate id for same user → 409
- [x] Status shows `claimedBy` after `MarkClaimed`
- [x] Revoke pending → check `valid: false`; revoke again → 204
- [x] Revoke claimed → 409; other user’s id → 404
- [x] Check missing / unknown / pending variants
