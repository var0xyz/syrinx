# Depackaging 07 — `deletion`

## Status

Proposed.

## Depends on

[02](02_coverage.md) (`coverage`), [04](04_signing.md) (`signing`),
[05](05_identity.md) (`identity`)

## Context

`deletion` (2 files: `store.go`, `account.go`) stores and reads signed
reed- and account-removal certs. Touches `identities`, `users`,
`public_keys`, `user_signatures`, `server_signatures`, `reed_removals`,
`account_removals`, `network_stats`, `pg_stat_activity` — 9 tables, all
owned by root's `db.go`. Imports `coverage`, `identity`, `signing` — all
three merge in earlier steps of this spec, so by the time this step lands
those imports just become direct calls to root code instead of
cross-package calls.

Importers today: `services.go`, `handlers.go` (root, direct), plus
`federation_relay.go` (root), `recovery/upsert.go` (step 09, still a
package at this point).

## Collisions

Two test-helper function names shared with `invites` and possibly root's
own test files: `newTestDatabase`, `testDSN`. Not a real conflict inside
Go's package system (each package can have its own private test helpers
of the same name), but once `deletion`'s tests move into root's test
files, a name clash with an existing root test helper (or with `invites`'
copy, landing in step 08 right after) needs resolving — likely by keeping
one shared `newTestDatabase`/`testDSN` in a root test-helpers file instead
of two near-identical copies. Check root's existing test files for a
helper already serving this role before assuming these need to be added
fresh.

No type-level collisions: `Cert`, `AccountCert` have no existing
root-level equivalents by these names.

## Move plan

1. Move `store.go` and `account.go`'s contents into root — `db.go` is the
   natural destination (it already owns the DDL for every table these
   functions touch, same reasoning as `coverage` in step 02).
2. Add the section header:
   ```go
   // ============ //
   //   deletion   //
   // ============ //
   ```
3. Drop the `deletion.` prefix and `"syrinx/deletion"` import at root's
   direct call sites (`services.go`, `handlers.go`, `federation_relay.go`).
   Leave `recovery/upsert.go` importing `"syrinx/deletion"` until step 09.
4. Resolve the `newTestDatabase`/`testDSN` duplication per the Collisions
   section — consolidate to one shared helper rather than porting both
   copies verbatim.
5. Delete the `deletion/` directory once `recovery` (step 09) has also
   merged.

## Verification

`go build ./...`, `go vet ./...`, `go test ./...` pass. `deletion/`
directory deletion deferred until step 09 clears its last importer.
