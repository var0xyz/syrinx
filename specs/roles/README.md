# Roles (root, admin, user)

This directory specifies **role tiers** on a Syrinx instance: root, admin, and
normal users. Roles are enforced in **server code only** for now — no admin UI,
no moderation actions (kick, ban, revoke access), no role management screens.

The first concrete capability: **admins may invite other admins** via the
existing invite flow. Normal users continue to mint invites that yield normal
users only.

**Blank slate — no migration, no backwards compatibility.** Recreate the DB
when schema changes.

| # | Title | Depends on |
|---|-------|------------|
| [00](00_design.md) | Design + locked model | — |
| [01](01_role_store.md) | `users.role` column + code helpers | 00 |
| [02](02_admin_invites.md) | Admin-only admin invites at create + signup | 01; [invites 02](../invites/02_lifecycle_api.md) |
| [03](03_profile_role.md) | Role bound on profile countersignature | 02 |

Related: root mint at startup
[`account_recovery/07`](../account_recovery/07_root_user_bootstrap.md);
federation builds on roles + `serverID`
[`federation/`](../federation/README.md).

---

## Status

**Implemented** (00–03).

| Step | Status |
|------|--------|
| 00 | Proposed (design locked) |
| 01 | **Implemented** |
| 02 | **Implemented** |
| 03 | **Implemented** |

## Locked decisions

| Topic | Decision |
|-------|----------|
| Tiers | **`root`**, **`admin`**, **`user`** (normal) |
| Root | Reserved `users.id = "1"`; minted via `ROOT_KEY_EXPORT_PASSPHRASE` ([07](../account_recovery/07_root_user_bootstrap.md)); role **`root`** |
| Default role | New signups (open mode or normal invite) → **`user`** |
| Admin invite | Only **`admin`** or **`root`** may set `grantedRole: admin` on invite create; signup applies role from invite |
| Enforcement | Code checks only — no admin UI in this track |
| Profile wire | **`role`** on server countersignature (`identity-server`); verifiable + recoverable ([03](03_profile_role.md)) |
| Non-goals (now) | Kick/ban, role UI, demote/promote after signup, federation trust |

## Motivation

Closed communities need more than “first signup is open”: some members must be
able to bootstrap other operators without opening registration. Federation will
require knowing which local identities are authoritative on this instance;
roles are the local foundation before cross-instance trust ships.
