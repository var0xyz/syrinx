# Depackaging 08 — `invites`

## Status

Proposed.

## Depends on

[00](00_encoding.md) (`encoding`), [03](03_crypto.md) (`crypto`),
[04](04_signing.md) (`signing`), [05](05_identity.md) (`identity`),
[06](06_roles.md) (`roles`)

## Context

`invites` (5 files: `store.go`, `handlers.go`, `mode.go`, `signup.go`,
`token.go`) manages invite creation/claiming/revocation. Touches
`identities`, `invites`, `servers`, `users`, `user_signatures`,
`server_signatures`, `pg_stat_activity` — 7 tables, root-owned. Imports
`crypto`, `encoding`, `identity`, `roles`, `signing` — all five merge in
earlier steps of this spec.

Already deeply integrated with root rather than a clean boundary: its
`RegisterRoutes(api, invites.Deps{...})` is called directly from
`main.go` with a `Deps` struct wired to root's own `DataService`/config;
`services.go` embeds `*invites.Store` as a field and re-exports several
of its methods (`GetPendingInvite`, `MarkClaimed` wiring). This isn't a
package with a clean API surface being called from outside — it's root
code that happens to live in a different directory.

## Collisions

**Same name, different shape — needs a rename, not dedup.**
`invites.Invite` is a full state-tracking struct:

```go
type Invite struct {
    ID            string
    CreatedBy     string
    CreatedAt     time.Time
    GrantedRole   string
    ClaimedAt     *time.Time
    ClaimedBy     *string
    RevokedAt     *time.Time
    UserSignature signing.UserSignature
}
```

Root's `db.go` `Invite` is a minimal wire struct:

```go
type Invite struct {
    ID       string `json:"id"`
    UserID   string `json:"userID"`
    Username string `json:"username"`
}
```

Different purpose entirely (DB-backed lifecycle record vs a wire-facing
reference). Rename `invites.Invite` on merge — e.g. `inviteRecord` — since
both are needed and neither should be deleted. Note this also depends on
how step 04's `signing.UserSignature` rename lands, since
`invites.Invite.UserSignature` references it directly — update that field
type to match whatever `signing.UserSignature` gets renamed to.

No other type/func collisions found.

## Move plan

1. Move `store.go`, `handlers.go`, `mode.go`, `signup.go`, `token.go`'s
   contents into root. Given the existing tight coupling (Step Context
   above), this is less a "move" and more an unwrapping: `Deps`/
   `RegisterRoutes` likely collapse into direct calls from `main.go`'s
   existing route-registration code rather than staying a separate
   struct-and-registration-function pair — decide the exact shape at
   implementation time, but don't preserve the `Deps` indirection purely
   out of inertia if `main.go`'s other route registration doesn't use
   that pattern.
2. Rename `Invite` → `inviteRecord` (or similar) per the Collisions
   section; update the `UserSignature` field type to match step 04's
   renamed `signing.UserSignature`.
3. Add section headers per destination file (likely `handlers.go` for
   the HTTP handlers, `services.go` or `db.go` for the store/DB logic —
   split by content type the same way other steps do, not dumped as one
   block):
   ```go
   // =========== //
   //   invites   //
   // =========== //
   ```
4. Update `main.go`, `services.go`, `handlers.go` to drop the `invites.`
   prefix and import, folding the `Deps`-based wiring into direct calls
   per step 1.
5. Delete the `invites/` directory (port `invites/*_test.go` cases to
   root test files first, resolving the `newTestDatabase`/`testDSN`
   duplication noted in step 07 the same way).

## Verification

`go build ./...`, `go vet ./...`, `go test ./...` pass, including the
invite-flow tests currently in `invites/handlers_test.go`/
`invites/signup_test.go`/`invites/store_test.go` — these exercise real
HTTP behavior (create/claim/revoke), so don't let coverage silently drop
during the port.
