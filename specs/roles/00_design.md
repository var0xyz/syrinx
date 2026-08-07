# Roles 00 — Design + locked model

## Status

Proposed.

## Depends on

—

## Context

Today every authenticated user is equivalent for invite minting: any user
can create single-use signup links ([`invites/`](../invites/README.md)), and
redeeming always yields a normal account. The reserved root identity
(`id = "1"`) exists for operator bootstrap
([`account_recovery/07`](../account_recovery/07_root_user_bootstrap.md)) but
there is no general **admin** tier in code.

Federation ([`federation/`](../federation/README.md)) will need instances to
act as identity providers for their own users and as clients verifying
foreign users. Roles define who may perform operator-level actions **on this
instance** before any cross-instance wire exists.

## Scope

- Lock three role tiers and their meaning.
- Lock the first admin-only capability: **invite with `grantedRole: admin`**.
- Document non-goals (no admin UI, no moderation).

## Non-goals

- Admin dashboard or role assignment UI.
- Kick, ban, suspend, or force logout.
- Promote/demote users after signup (future track).
- Cross-instance role import (federation track).
- Changing who may revoke invites, set `SIGNUP_MODE`, or run `ops` — future
  operator docs; this track only adds **`users.role`** and admin invites.

## Design

### Role tiers

| Role | Who | Capabilities (this track) |
|------|-----|---------------------------|
| **`root`** | `users.id = "1"` only | Same as admin for invites; reserved forever; minted at startup, not via invite |
| **`admin`** | Assigned at signup from admin invite, or held by root | May create invites with `grantedRole: admin` or omit/`user` for normal users |
| **`user`** | Default for all other accounts | May create invites that always grant **`user`** (cannot elevate) |

**Root is not created through invites.** Signup rejects `userID == "1"` today;
that stays. Root role is set atomically at one-shot mint.

### Admin invite (only new product behavior)

When an **admin** or **root** creates an invite, the signed invite payload
may include **`grantedRole`**:

- Omitted or `"user"` → redeemer becomes a normal user (today’s behavior).
- `"admin"` → redeemer becomes **`admin`**.

A **`user`** creating an invite must not set `grantedRole: admin`; server
rejects. A **`user`** invite always grants **`user`**, even if the client
sends another value.

Open-mode signup (no invite) always creates **`user`**. Only invite redeem
with an admin-granting invite (or root mint) creates **`admin`**.

### Where roles live

- **Durable:** `users.role` column (`root` \| `admin` \| `user`).
- **Signed:** `role` header on the profile server countersignature
  ([03](03_profile_role.md)) — users never sign role; peers verify via
  `serverSignature`.
- **Hint:** `/users/{id}/info` still exposes role for lightweight UI gating
  (same value as the signed profile).
- **Helpers:** e.g. `IsAdmin(userID)`, `RequireAdmin(ctx)` in a small
  `roles` package or `invites`/`services` guard — implementation in 01.

### Relationship to root bootstrap

[`account_recovery/07`](../account_recovery/07_root_user_bootstrap.md) mints
`id = "1"`. Step **01** sets `role = root` on that row at mint and on any
existing `"1"` row at schema create (single-row backfill for blank-slate only).

### Future (not this track)

- Admin UI, user list, kick/ban.
- Admin-only HTTP routes (peer management, server config).
- Audit log of admin actions.
