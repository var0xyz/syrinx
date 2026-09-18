# Depackaging 06 — `roles`

## Status

Proposed.

## Depends on

[05](05_identity.md) (`identity` — `roles/roles.go` imports it)

## Context

`roles/roles.go` is 224 lines, one file: role-string helpers (`IsAdmin`,
`IsRoot`, `CanGrantAdmin`, `RoleForSignup`, `RoleFromInviteGrant`,
`SignupRole`, `RequireAdmin`, `ValidateProfileRole`) plus constants like
`RootUserID`, `RoleRoot`. No DB coupling. Used directly from root
(`mailbox.go`, `root.go`, `main.go`) and imports `identity` for
`identity.CanonicalID` — hence depends on step 05 landing first.

Once `deletion`, `invites`, `recovery`, `realtime` also merge (later
steps), `roles` has no remaining reason to be a package: it's a handful of
string-comparison helpers around one canonical root-user constant, already
called directly from root today.

## Collisions

None — no root-level `func`/`type`/`const` name conflicts for anything in
`roles/`.

## Move plan

1. Move `roles/roles.go`'s contents into root — given its focus (role
   checks + the `RootUserID`/`RoleRoot` constants used throughout root
   boot/signup/recovery logic), a new `roles.go` at the repo root is the
   natural home; small enough (224 lines) that folding into an existing
   file is also reasonable if one fits better at implementation time
   (e.g. alongside signup-related code in `root.go`, check what's there).
2. If merging into an existing file, add the section header:
   ```go
   // ========= //
   //   roles   //
   // ========= //
   ```
   If landing as a new standalone `roles.go`, no header needed (same
   reasoning as `secret`/`crypto`/`identity`).
3. Update call sites (`mailbox.go`, `root.go`, `main.go`) to drop the
   `roles.` prefix and import. Leave `deletion`, `invites`, `recovery`,
   `realtime` importing `"syrinx/roles"` until their own later steps.
4. Delete the `roles/` directory only once `deletion` (07), `invites`
   (08), `recovery` (09), and `realtime` (10) have all landed.

## Verification

`go build ./...`, `go vet ./...`, `go test ./...` pass. `roles/`
directory deletion deferred until every later step in this spec clears
its remaining importers.
