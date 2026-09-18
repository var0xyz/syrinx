# Depackaging 08 — `invites`

## Status

Implemented and fully complete — `invites/` directory **deleted**, unlike
every prior step in this spec. `invites` had zero remaining external
importers once its own call sites moved (the spec's importer list —
`deletion/store.go`, `identity/identity.go`, `recovery/*` — was already
stale by the time this step landed; those packages import `signing`/
`identity`/`roles` but never `invites` itself), so there was no reason to
defer directory deletion the way every schema-coupled step before this
one had to.

**Two duplicate helpers deleted outright, not renamed.** `invites.noop`
and `invites.writeJSON` were exact duplicates of root's own
`(h *Handlers) noop` and `writeResponse` — found by checking `main.go`'s
existing OPTIONS-route registrations (every other route already used
`h.noop`) before assuming these needed porting at all. Confirms this
spec's core thesis directly: `invites` didn't need a "clean API," it
needed root's own helpers, which it couldn't reach without an import
cycle.

**The `Deps`/`RegisterRoutes` struct-of-closures pattern is gone, not
preserved.** `Create`/`Status`/`RevokeInvite`/`Check` are now
`h.CreateInvite`/`h.InviteStatus`/`h.DeleteInvite`/`h.CheckInvite` —
plain `*Handlers` methods reading `h.services.db`/`h.services.crypto`/
`h.cfg`/`h.signingKey` directly, registered via `api.HandleFunc(...)` in
`main.go` exactly like every other route. The `GetPublicKeyArmor`/
`GetUserRole`/`VerifySignature`/`Countersign` injectable closures are
gone — those are direct calls now (`h.services.db.GetPublicKey`,
`h.services.db.GetUserRole`, `h.services.crypto.verifySignature`,
`h.countersign`, which already returns root's own `ServerSignature`,
eliminating the `invites.ServerSignatureWire` conversion `main.go` used
to do by hand).

**`DataService.invites *invites.Store` field deleted entirely**, along
with the two `s.invites.ServerID = id` sync points in `InitServer` and
`setServerIDForTest` — `Store{DB, ServerID}` was pure duplication of
`DataService{db, serverID}` once the invite functions became
`DataService` methods / package-level functions taking `s.db` directly.

**Real bugs found and fixed while rewriting the test suite** (see the
"18 tests rewritten" note below) — not present in production, only
surfaced because the tests moved from fake to real signature
verification:
- The test helper needed `UserSignature.ID` in **canonical**
  (`fingerprint@serverID`) form, matching how `public_keys.id` is
  actually stored (confirmed against `services.go`'s `Signup` insert and
  the *original* `invites/handlers_test.go`'s fixture, which already
  used the canonical form — the bug was in translating that convention
  to the new test file, not in production code).
  `GetPublicKeyArmor(ctx, userID, fingerprint string)` (both old and new)
  discards `userID` and looks up `fingerprint` as-is — this only works
  when the caller already passes it canonical.
- `CreateInvite` normalizes an empty `grantedRole` to `roleUser` *before*
  building the signed payload — a test signing with the raw (possibly
  empty) role produces a payload that doesn't match what the server
  reconstructs, failing signature verification. Both of these are
  pre-existing behaviors in the original `invites` package, not new bugs
  introduced by this merge — they were only ever exercised by the
  original tests' fake `VerifySignature: func(...) error { return nil }`,
  which could never have caught either issue.

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
reference). Renamed `invites.Invite` → `inviteRecord` per this section's
original plan. `UserSignature`'s field type became `userSignatureRow`
(step 04's actual rename, resolving the note below).

**Also found and deleted on merge, not ported**: `invites.noop` and
`invites.writeJSON` — exact duplicates of root's `(h *Handlers) noop` and
`writeResponse` (see Status).

No other type/func collisions found.

## Move plan

1. Moved `store.go`, `mode.go`, `signup.go`, `token.go`'s DB/logic
   contents into `services.go` (as `DataService` methods and
   package-level functions, following the `deletion`/`signing` pattern —
   not `db.go`, which stayed pure DDL through every step of this spec).
   Moved `handlers.go`'s HTTP layer into `handlers.go` at root, as plain
   `*Handlers` methods — `Deps`/`RegisterRoutes` were deleted entirely,
   not preserved in any form (see Status).
2. Renamed `Invite` → `inviteRecord`; its `UserSignature` field now types
   as `userSignatureRow` (step 04's actual naming).
3. Added section headers:
   ```go
   // =========== //
   //   invites   //
   // =========== //
   ```
   in both `services.go` (store/logic section) and `handlers.go` (HTTP
   handlers section).
4. Updated `main.go` to drop the whole `invites.RegisterRoutes(api,
   invites.Deps{...})` block, replaced with direct `api.HandleFunc(...)`
   registrations matching every other route (including reusing `h.noop`
   for the OPTIONS variants, not a ported duplicate). Updated
   `services.go`/`handlers.go` to drop the `invites.` prefix and import.
5. Ported all 18 tests (`invites/signup_test.go`, `invites/store_test.go`,
   `invites/handlers_test.go`) into three new root files
   (`invites_signup_test.go`, `invites_store_test.go`,
   `invites_handlers_test.go`), reusing root's existing
   `newTestDatabase`/`testDSN` (`testdb_test.go`) rather than porting
   `invites`' own copies — resolves the duplication this spec flagged.
   The 12 HTTP-handler tests required a real rewrite, not a mechanical
   port: `Deps`'s injectable `VerifySignature`/`Countersign`/
   `GetPublicKeyArmor` seams no longer exist, so tests now go through a
   real `*Handlers` (reusing `newSignupGateHandlers`/`signedUpUser` from
   `handlers_signup_gate_test.go`, already in this package) with real
   keypairs and real signature verification — this is what surfaced the
   two pre-existing bugs noted in Status. Dropped the two
   `RegisterRoutes`-through-a-real-router tests (`RegisterRoutes` no
   longer exists as a separable function; their coverage is subsumed by
   the direct-handler tests).
6. Deleted the `invites/` directory outright — zero remaining external
   importers (see Status), unlike every prior step in this spec.

## Verification

`go build ./...`, `go vet ./...`, `go test ./...`, plus `go build -tags
ops` and `go build -tags ripplescleanup`, all pass. All 18 original
tests pass in their ported form, with real (not faked) signature
verification exercising the full create/claim/revoke/check flow.
`invites/` directory is gone — `git rm -r invites/` — confirmed via
`go list ./...` no longer showing `syrinx/invites`.
