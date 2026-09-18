# Depackaging 02 — `coverage`

## Status

Proposed.

## Depends on

—

## Context

`coverage/stats.go` is 39 lines, one file: `Percent` (pure function, no
DB), and three DB-touching functions (`BumpActiveUsers`, `ActiveUsers`,
`ActiveUsersTx`) that read/write `network_stats.active_users`. The
`network_stats` table's DDL (`CREATE TABLE`, seed `INSERT`, and a view
joining it) lives in root's `db.go`, not in this package — `coverage`
doesn't own the table it queries, root does. This is the clearest example
in the repo of the pattern this whole spec is about: a package that looks
independent but is actually one query group split out of the schema owner
for no structural reason.

Importers: `services.go`, `handlers.go`, `deletion/account.go`,
`recovery/upsert.go`, `realtime/db.go` — the latter two are themselves
merging into root later in this spec (steps 06, 07), so by the time this
step lands, `coverage` only needs to keep working for root's own direct
callers plus `deletion` (step 04, after this one).

## Collisions

None. `Percent`, `BumpActiveUsers`, `ActiveUsers`, `ActiveUsersTx` have no
root-level name conflicts.

## Move plan

1. Move `coverage/stats.go`'s four functions into root — `db.go` is the
   natural destination since it's adjacent to the `network_stats` DDL
   these functions already depend on.
2. Add the section header:
   ```go
   // ============ //
   //   coverage   //
   // ============ //
   ```
3. Drop the `coverage.` prefix and `"syrinx/coverage"` import at all
   direct root call sites (`services.go`, `handlers.go`). Leave
   `deletion/account.go` and `recovery/upsert.go` importing
   `"syrinx/coverage"` until those packages merge in later steps —
   `realtime/db.go`'s import similarly stays until step 07.
4. Delete the `coverage/` directory once empty (port `coverage/*_test.go`
   cases to a root test file first).

## Verification

`go build ./...`, `go vet ./...`, `go test ./...` all pass; `coverage/`
directory no longer exists once every dependent package listed above has
also merged (this step alone only removes root's own two direct call
sites' qualifiers — full directory deletion happens after step 07).
