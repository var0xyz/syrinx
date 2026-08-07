# Roles 03 — Role on profile countersignature

## Status

Implemented.

## Depends on

[01](01_role_store.md), [02](02_admin_invites.md)

## Context

Roles were persisted in `users.role` and granted at signup from signed
invites, but the profile countersignature did not bind role. Clients could
not verify who assigned a tier; recovery rebuilt every account as `user`
(except root).

## Scope

- Add **`role`** (`root` | `admin` | `user`) to `identity-server` profile
  headers (server countersignature only — never in `identity-user`).
- Include role when countersigning at signup, profile update, and root mint.
- Return `role` on `GET /users/{id}/profile` (signed wire).
- Recovery: persist `users.role` from submitted profile; verify role in nest
  validation.
- Go/SPA parity for `BuildProfilePayload` / `buildProfilePayload`.

## Non-goals

- Promote/demote after signup (role is immutable; re-bind on profile update
  from the existing row).
- Removing `role` from `/users/{id}/info` (kept as a lightweight cache hint).

## Design

### Signed payload

```
role: admin
```

Always present on `identity-server` profile payloads (unlike `invitedBy`,
which omits when empty).

Assignment at signup still flows from invite `grantedRole` → `SignupRole` →
profile countersignature. Open signup → `user`; root mint → `root`.

### Wire

`User.role` on profile responses. Verifiers rebuild the server payload with
`user.role` when checking `serverSignature`.

### Recovery

`insertUser` / `updateUserIfNewer` write `profile.role` after
`roles.ValidateProfileRole` (root only on id `"1"`).

### Tests

- Canonical shape + roundtrip in `identity/identity_test.go`.
- `TestTamperedRoleBreaksServerSignature`.
- `roles.ValidateProfileRole` unit tests.
- Shared vector in `signing/testvectors.json`; SPA `test:signing`.
