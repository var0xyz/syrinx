# Invites 02 — Lifecycle API (create / list / revoke / check)

## Status

Implemented (`invites.RegisterRoutes` create/list/revoke/check; quota +
closed-mode on create; `/api/invites/check` allowlisted).

## Depends on

[01](01_schema_and_store.md)

## Context

Schema and store exist. Authenticated users need to mint, inspect, and revoke
invites; the SPA needs a cheap unauthenticated validity check before running
the full PGP signup flow.

## Scope

- `invites.RegisterRoutes` mounted from `main` under `/api`.
- Handlers: create, list, revoke, check.
- Enforce `MAX_INVITES_PER_USER` on create.
- Enforce `SIGNUP_MODE=closed` → create returns 403 (list/revoke remain).
- Allowlist `GET /api/invites/check` in signature-auth middleware.
- Wire shapes exactly as in the [README](README.md) API catalog.

## Non-goals

- Consuming invites at signup (03).
- SPA (04–05).
- Returning inviter identity from `check` (invitee learns inviter via
  `user.invitedBy` after signup).

## Design

### Routes

| Method   | Path                 | Auth     |
|----------|----------------------|----------|
| `POST`   | `/api/invites`       | Required |
| `GET`    | `/api/invites`       | Required |
| `DELETE` | `/api/invites/{id}`  | Required |
| `GET`    | `/api/invites/check` | None     |

Register from `invites.RegisterRoutes(r *mux.Router, …)` analogous to
recovery's pattern. Main only calls Register when the server boots (always —
routes exist in all modes; handlers enforce mode/quota).

### `POST /api/invites`

1. Resolve caller `userID` from auth context.
2. If mode `closed` → **403** `"Signups are closed on this server"` (same
   string as signup closed gate).
3. If max ≠ −1 and `CountByCreator >= max` → **403**
   `"Invite limit reached"`.
4. Generate id + token; `Insert` hash.
5. **201** body:

```json
{
  "id": "<inviteID>",
  "token": "<rawToken>",
  "createdAt": "<RFC3339>"
}
```

Client builds `shareURL = origin + "/signup?invite=" + token`. Do not have
the server invent absolute URLs (origin varies; `ALLOWED_ORIGIN` is not
always the public SPA URL).

### `GET /api/invites`

Returns **200** `{ "invites": [ … ] }` ordered by `created_at DESC`.

Each element:

```json
{
  "id": "...",
  "createdAt": "...",
  "status": "pending" | "claimed" | "revoked",
  "claimedAt": null,
  "claimedBy": null,
  "revokedAt": null
}
```

When claimed, populate:

```json
"claimedAt": "...",
"claimedBy": { "id": "...", "username": "..." }
```

Join `users` for `claimed_by` username. If the redeeming user was later
deleted (not possible today — no hard-delete of other users in normal
flow; account delete is self), tolerate null username by still returning
id.

Never include `token` or `token_hash`.

### `DELETE /api/invites/{id}`

1. Caller must own the invite (`created_by = caller`).
2. If not found or not owner → **404** (do not leak cross-user existence).
3. If already claimed → **409** `"Invite already claimed"`.
4. If already revoked → **200** idempotent (or 204); pick **204** with no
   body for both fresh revoke and already-revoked.
5. Else set `revoked_at = now()` → **204**.

### `GET /api/invites/check?token=`

1. Missing/empty token → **400**.
2. Hash token; lookup.
3. **200** `{ "valid": true }` only if row exists and is pending.
4. Otherwise **200** `{ "valid": false }` — do not distinguish
   unknown / claimed / revoked (avoid oracle detail for attackers probing
   tokens). Still return 200 so the SPA can branch on `valid` without
   treating 404 as a network failure.

Allowlist exact path `/api/invites/check` in
[`middlewares.go`](../../../middlewares.go) `excludePaths`.

### Errors

Use the existing `writeResponse` JSON string / object patterns. Prefer
stable short strings the SPA can match if needed; primary UX can also key
off status codes alone.

## Test plan

- [x] Create while `open` / `invite` → 201 with token; second create increments count
- [x] Create at quota → 403; `max=-1` never hits quota
- [x] Create while `closed` → 403; list still 200
- [x] List shows `claimedBy` after a manual `MarkClaimed` in test
- [x] Revoke pending → check becomes `valid: false`; revoke again → 204
- [x] Revoke claimed → 409
- [x] Revoke other user's id → 404
- [x] Check missing token → 400; unknown token → `{valid:false}`; pending → `{valid:true}`
- [x] Unauthenticated create → 401/403 from existing auth middleware
- [x] Unauthenticated check → allowed
