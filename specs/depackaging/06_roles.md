# Depackaging 06 — `roles`

## Status

Implemented (root's own call sites only). `roles/` still exists in full
and stays exported — `deletion`, `invites`, `recovery`, `realtime`
haven't merged yet.

**Context correction — two more importers than listed.** `services.go`
and `handlers.go` also import `roles` (for `signupRole`/`requireAdmin`/
`isRoot`/`rootUserID`), not just `mailbox.go`/`root.go`/`main.go` as the
original Context section said — 5 root files total, not 3.

**Real shadowing bug caught before it shipped.** Unexporting
`IsRoot`→`isRoot` created a same-name collision with
`validateProfileRole`'s existing local variable `isRoot := ...` —
legal Go (inner scope shadows the outer function), but the local
variable would have silently hidden the real `isRoot` function for
the rest of that function body. Renamed the local to `rootMatch`.

**Build-tag split, same lesson as step 03 (`crypto`).** `roles.go`
initially got the `!ops && !ripplescleanup` tag (matching most of its
callers), but `mailbox.go` (no build tag, compiles into all three
variants) needs the bare `rootUserID` constant. Since the rest of
`roles.go` calls `canonicalID` (from `identity_id.go`, itself tagged
`!ops && !ripplescleanup` — `ops`/`ripplescleanup` builds don't need
identity-ID parsing), the constant alone was split out into
`constants.go` (already untagged, already holds other small root-wide
constants) — `roles.go` keeps its tag for everything else. Caught by
building all three variants explicitly before running `go test`, not
after — applying the previous step's lesson.

**7 test files needed the same treatment as production code**:
`root_test.go`, `federation_test.go`, `handlers_signup_gate_test.go`,
`key_rotation_test.go`, `account_removal_test.go`,
`federation_handshake_test.go`, `signup_invite_test.go`. Lower risk
than step 05's find (`roles.RootUserID` etc. are string constants
compared by value, not `error` sentinels compared by reference), but
fixed for consistency and to eventually allow `roles/` deletion.

## Depends on

[05](05_identity.md) (`identity` — `roles/roles.go` imports it)

## Context

`roles/roles.go` is 224 lines, one file: role-string helpers (`IsAdmin`,
`IsRoot`, `CanGrantAdmin`, `RoleForSignup`, `RoleFromInviteGrant`,
`SignupRole`, `RequireAdmin`, `ValidateProfileRole`) plus constants like
`RootUserID`, `RoleRoot`. No DB coupling. Used directly from root
(`services.go`, `handlers.go`, `mailbox.go`, `root.go`, `main.go` — see
Status for the 2 files missed in this original count) and imports
`identity` for `identity.CanonicalID` — hence depends on step 05 landing
first.

Once `deletion`, `invites`, `recovery`, `realtime` also merge (later
steps), `roles` has no remaining reason to be a package: it's a handful of
string-comparison helpers around one canonical root-user constant, already
called directly from root today.

## Collisions

None — no root-level `func`/`type`/`const` name conflicts for anything in
`roles/`.

## Move plan

1. Moved `roles/roles.go`'s contents into a new standalone root
   `roles.go` (no section header — same reasoning as `secret`/`crypto`/
   `identity`). Every exported symbol unexported and PascalCase →
   camelCase: `IsAdmin`→`isAdmin`, `IsRoot`→`isRoot`,
   `CanGrantAdmin`→`canGrantAdmin`, `RoleForSignup`→`roleForSignup`,
   `RoleFromInviteGrant`→`roleFromInviteGrant`, `SignupRole`→
   `signupRole`, `RequireAdmin`→`requireAdmin`,
   `ValidateProfileRole`→`validateProfileRole`,
   `RoleRoot`/`RoleAdmin`/`RoleUser`→`roleRoot`/`roleAdmin`/`roleUser`,
   `ErrAdminRequired`/`ErrInvalidRole`→`errAdminRequired`/
   `errInvalidRole`. `RootUserID`→`rootUserID` split out into
   `constants.go` instead of staying in `roles.go` — see Status
   (build-tag reasons).
2. Fixed a local-variable/function-name shadow this rename introduced
   (`isRoot` the function vs. `isRoot :=` the local in
   `validateProfileRole`) — see Status.
3. Updated all 5 root production call sites (`services.go`,
   `handlers.go`, `mailbox.go`, `main.go`, `root.go` — corrected from the
   3 originally listed, see Status) and 7 test files (see Status) to the
   new unexported names; dropped `"syrinx/roles"` from all 12. Left
   `deletion`, `invites`, `recovery`, `realtime` importing
   `"syrinx/roles"` unchanged.
4. `roles/` directory deletion deferred — `deletion` (07), `invites`
   (08), `recovery` (09), and `realtime` (10) still import it.

## Verification

`go build ./...`, `go vet ./...`, `go test ./...`, plus `go build -tags
ops` and `go build -tags ripplescleanup`, all pass — the tagged-variant
builds caught the `rootUserID`/`mailbox.go` build-tag issue (see Status)
before `go test` ran, applying step 05's lesson to check variants early
rather than relying on the default build alone. `roles/` directory
deletion deferred until every later step in this spec clears its
remaining importers.
