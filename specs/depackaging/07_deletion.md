# Depackaging 07 — `deletion`

## Status

Implemented (root's own call sites only). `deletion/` still exists in
full and stays exported — `realtime` (step 10) still imports it.

**Context correction — `recovery/upsert.go` no longer imports
`deletion`.** The spec's original importer list included it; re-checked
at implementation time and it doesn't (code must have changed since the
audit). Current importers besides root: only `realtime/db.go` and
`realtime/wire.go`.

**`db.go` was wrong again — third time this pattern's shown up** (after
`coverage` in step 02, `signing`'s `store.go` in step 04). `store.go`
and `account.go` moved into `services.go`, not `db.go` — `db.go` is
still pure DDL.

**Needed two new raw-row signing helpers not yet in root.**
`getServerSignatureRow` (mirroring step 04's `getUserSignatureRow`) —
`deletion`'s cert-assembly code needs the raw `PrivateKeyID`/`SignedAt`
fields, not the wire-shaped conversion `getServerSignatureWire`
produces.

**A second legacy-bridge case, this time for `realtime`.**
`realtime.NewReedRemovalWire`/`NewAccountRemovalWire` (still in the
unmerged `realtime` package) take literal `*deletion.Cert`/
`*deletion.AccountCert` parameters — same shape of problem as step 03's
`crypto`/`realtime.NewService` finding, but for a *type conversion*
this time rather than an alternate service instance. Added
`toLegacyDeletionCert`/`toLegacyDeletionAccountCert` (field-copying
converters, `services.go`) and re-added the `"syrinx/deletion"` import
to `services.go` just for this bridge. The 4 call sites
(`handlers.go` ×2, `federation_relay.go` ×2) build root's own cert,
then convert to the legacy type only right before the `realtime.New*Wire`
call. Drops once `realtime` merges (step 10) — `realtime.RippleWire` and
`MailboxRow` (already found to be true duplicates of root's own types in
the original audit) suggest `NewReedRemovalWire`/`NewAccountRemovalWire`
themselves may turn out to be redundant with direct field construction
once `realtime` is inside root too; worth checking at that step rather
than assuming these survive as-is.

**Renamed the exported types on merge, not just unexported them** (per
Collisions): `Cert`→`reedRemovalCert`, `AccountCert`→
`accountRemovalCert` — bare `Cert`/`AccountCert` would be ambiguous in a
5000+ line file already containing an unrelated `LikeCert`; the more
descriptive names make each cert's purpose clear without a comment.

**Test-porting deferred, as usual** — `deletion/store_test.go` (despite
the name, covers both `store.go` and `account.go`) stays with the
package until final deletion after step 10. Fixed two stale comments in
`ripples_test.go`/`federation_test.go` that named the old
`deletion.GetCert`/`deletion.GetAccountCert` functions (harmless —
comments only, not compiled code — but corrected for accuracy).

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

Importers today: `services.go`, `handlers.go`, `federation_relay.go`
(root, direct), plus `realtime/db.go` and `realtime/wire.go` (step 10 —
see Status for the `recovery/upsert.go` correction).

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

1. Moved `store.go` and `account.go`'s contents into `services.go`, not
   `db.go` (see Status). Added `getServerSignatureRow` first (see
   Status) since it didn't exist yet.
2. Added the section header in `services.go`:
   ```go
   // ============ //
   //   deletion   //
   // ============ //
   ```
3. Renamed every exported symbol, unexported and more descriptive than a
   mechanical case-change alone (see Status): `Cert`→`reedRemovalCert`,
   `AccountCert`→`accountRemovalCert`, `ErrConflict`→
   `errRemovalConflict`, `ValidateAccountNote`→`validateAccountNote`,
   `GetCert`→`getReedRemovalCert`, `InsertCert`→`insertReedRemovalCert`,
   `GetAccountCert`→`getAccountRemovalCert`, `InsertAccountCert`→
   `insertAccountRemovalCert`, `InsertForeignAccountCert`→
   `insertForeignAccountRemovalCert`, `HasAccountRemoval`→
   `hasAccountRemoval`, `MaxAccountNoteLen`→`maxAccountNoteLen`.
4. Dropped the `deletion.` prefix and `"syrinx/deletion"` import at
   root's direct call sites (`services.go`, `handlers.go`,
   `federation_relay.go`) — then re-added the import to `services.go`
   alone for the `realtime` legacy bridge (see Status).
5. Deferred the `newTestDatabase`/`testDSN` duplication (per Collisions)
   to whenever `deletion/store_test.go` actually gets ported — that's
   step 10's problem now, same as every other step's test-porting.
6. `deletion/` directory deletion deferred — only `realtime` (step 10)
   still imports it.

## Verification

`go build ./...`, `go vet ./...`, `go test ./...`, plus `go build -tags
ops` and `go build -tags ripplescleanup`, all pass. `deletion/`
directory deletion deferred until step 10 (`realtime`) clears its last
importer.
