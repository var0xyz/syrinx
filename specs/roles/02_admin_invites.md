# Roles 02 — Admin-only admin invites (create + signup)

## Status

Proposed.

## Depends on

[01](01_role_store.md), [invites 02](../invites/02_lifecycle_api.md),
[invites 03](../invites/03_signup_consume.md)

## Context

Invites are signed user attestations countersigned by the server
(`identity.BuildInviteUserPayload` / `BuildInviteServerPayload`). The payload
today binds `serverID`, issuer `userID`, invite `id`, `tokenHash`, and
`createdAt`. Signup consumes the invite and creates the account with
`invited_by`; role is always implicit **`user`**.

## Scope

- Extend invite **user** signed payload with optional **`grantedRole`**
  (`user` \| `admin`; default `user`).
- `POST /api/invites`: admins may set `grantedRole: admin`; users cannot.
- Verify signature over extended payload; countersign server block unchanged
  except binding includes granted role in user bytes-to-sign.
- Signup: persist `users.role` from invite row or verified payload.
- Store `granted_role` on `invites` row at insert for redeem lookup.

## Non-goals

- SPA role picker UI polish (minimal: admin sees toggle or dropdown on invite
  create — can be a checkbox “Invite as admin” in 02 SPA follow-up or bundled
  here if trivial).
- Inviting **`root`** (forbidden).
- Changing role of existing users.

## Design

### Signed payload (`identity`)

Add to invite user headers when non-default:

```
grantedRole: admin
```

Omit line when `user` (same empty-value rule as `BytesToSign` elsewhere).
Update `BuildInviteUserPayload` / verifier; **SPA + Go parity test** required.

### `invites` table

```sql
granted_role VARCHAR(16) NOT NULL DEFAULT 'user'
  CHECK (granted_role IN ('admin', 'user'))
```

Persist at create from validated request body (after role check).

### `POST /api/invites`

Optional body field:

```json
{
  "id": "...",
  "token": "...",
  "createdAt": "...",
  "grantedRole": "admin",
  "userSignature": { ... }
}
```

Rules:

1. Caller `CanGrantAdmin(callerRole)` required to accept `grantedRole: admin`.
2. Caller role **`user`** → force `grantedRole = user` (ignore or 403 if
   `admin` requested — prefer **403** `"Cannot grant admin role"`).
3. **`root`** and **`admin`** may send `admin` or omit/`user`.
4. Rebuild user payload including `grantedRole`; verify signature; insert
   row with `granted_role`.

### Signup consume

When invite mode resolves a pending invite:

1. Read `granted_role` from `invites` row (or re-derive from stored attestation).
2. Set `users.role` to that value on insert (`user` or `admin` only).
3. Never set `root` from invite.

Open mode unchanged (`user`).

### SPA (minimal)

On invite create screen ([`invites/05`](../invites/05_spa_invite_management.md)):

- If current user is admin (server returns role on `/users/{id}/info` or
  session hint — **unsigned hint OK** for UI gating only; server enforces):
  show **“Invite as admin”** checkbox.
- Include `grantedRole` in create body and client-signed payload when checked.

Server remains authoritative; tampering without admin role → 403.

### Tests / checklist

- User creates invite with `grantedRole: admin` → 403.
- Admin creates admin invite → signup → `users.role = admin`.
- Admin creates normal invite → signup → `user`.
- Signature parity Go/SPA for invite with `grantedRole: admin`.
- Redeem after revoke/claim unchanged.
