# Depackaging 02 — `coverage`

## Status

Implemented (root's own call sites only), with two deviations from the
original move plan below. `coverage/` still exists and stays exported —
`deletion`, `recovery`, `realtime` haven't merged yet.

**Deviation 1 — destination file.** `db.go` turned out to be entirely
`InitDB`'s DDL string constants (1,282 lines, one function) — there's no
home there for executable query-helper functions. Moved into
`services.go` instead, where the actual `DataService` query methods live
and where `coverage.BumpActiveUsers`/`ActiveUsers` were already being
called from.

**Deviation 2 — `ActiveUsersTx` dropped, not moved.** Found to have zero
call sites anywhere in the repo (not just root) during this step — the
original spec draft only checked `crypto` for dead code, not `coverage`.
Deleted from root's copy rather than relocated; `coverage/stats.go`
itself is untouched (still has `ActiveUsersTx`, since deleting dead code
from a package still serving other importers is a separate concern from
this move).

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
`recovery/upsert.go`, `realtime/db.go` — `deletion`, `recovery`, and
`realtime` merge later in this spec (steps 07, 09, 10), so by the time
this step lands, `coverage` only needs to keep working for root's own
direct callers plus those three.

## Collisions

None. `Percent`, `BumpActiveUsers`, `ActiveUsers`, `ActiveUsersTx` have no
root-level name conflicts.

## Move plan

1. Moved `coverage/stats.go`'s `Percent`, `BumpActiveUsers`,
   `ActiveUsers` into `services.go` (see Status deviation 1 for why not
   `db.go`), renamed unexported: `coveragePercent`, `bumpActiveUsers`,
   `getActiveUsers`. `ActiveUsersTx` dropped (see Status deviation 2).
2. Added the section header in `services.go`:
   ```go
   // ============ //
   //   coverage   //
   // ============ //
   ```
3. Dropped the `coverage.` prefix and `"syrinx/coverage"` import at
   direct root call sites (`services.go`, `handlers.go`). Left
   `deletion/account.go` and `recovery/upsert.go` importing
   `"syrinx/coverage"` until those packages merge in later steps —
   `realtime/db.go`'s import similarly stays until step 10.
4. `coverage/` directory deletion deferred — still needed by `deletion`
   (step 07), `recovery` (step 09), `realtime` (step 10).

## Verification

`go build ./...`, `go vet ./...`, `go test ./...` all pass; `coverage/`
directory no longer exists once every dependent package listed above has
also merged (this step alone only removes root's own two direct call
sites' qualifiers — full directory deletion happens after step 10).
