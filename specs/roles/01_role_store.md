# Roles 01 — `users.role` column + code helpers

## Status

Proposed.

## Depends on

[00](00_design.md)

## Context

No role column exists today. Root is identifiable only by `id = "1"`. Admin
checks would otherwise scatter string comparisons.

## Scope

- DDL: `users.role` with check constraint.
- Set `root` on mint and for reserved id `"1"`.
- Default `user` for all other signups until 02 applies invite-granted admin.
- Small API: read role, test admin/root, optional middleware helper.

## Non-goals

- Invite `grantedRole` (02).
- SPA exposure of role (optional hint later; not required here).

## Design

### Schema

Blank-slate addition to `users`:

```sql
ALTER TABLE users ADD COLUMN role VARCHAR(16) NOT NULL DEFAULT 'user'
  CHECK (role IN ('root', 'admin', 'user'));
```

On `InitDB` for fresh DB, only `CREATE TABLE` with column included (no ALTER).

Backfill rule on boot (blank-slate recreate): `UPDATE users SET role = 'root'
WHERE id = '1'`.

### Signup defaults

| Path | `users.role` |
|------|----------------|
| Root mint (`root.go`) | `root` |
| Open signup | `user` |
| Invite redeem (until 02) | `user` |
| Invite redeem with admin grant (02) | `admin` |

### Code surface

Package **`roles`** (or helpers on `DataService` if preferred to avoid
sprawl):

```go
const (
    RoleRoot  = "root"
    RoleAdmin = "admin"
    RoleUser  = "user"
)

func IsAdmin(role string) bool   // root or admin
func IsRoot(userID, role string) bool
func CanGrantAdmin(role string) bool // IsAdmin
```

Signup and invite handlers consult these; no new HTTP routes in this step.

### Tests

- Root mint → `role = root`.
- Normal signup → `user`.
- `IsAdmin` true for root/admin, false for user.
