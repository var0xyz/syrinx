# Proposal 02 — Random, server-scoped user IDs

## Status

Proposed. Prerequisite for recovery (see [`takeover_recovery.md`](../takeover_recovery.md),
"What must be built").

## Context

`CreateUser` in `services.go` today generates the user ID by incrementing
`user_count.count` and encoding `(count, randomNum)` with Sqids. The counter
is authoritative for the ID and also drives the "active users" gauge.

For recovery, IDs must be:

- **Random**, so they cannot be reproduced by re-running signup in ID order on
  a fresh DB, and so a live signup during recovery cannot collide with an
  as-yet-unrestored ID.
- **Server-scoped** and stable, since every reed, every follow edge, and every
  countersigned identity binding embeds `(serverID, userID)`.

The `user_count` table exists only to feed the ID generator and to keep an
`active` gauge that has no current consumer. Once the ID generator no longer
needs it, the table is dead weight and goes away with the same PR.

## Scope

- Replace the Sqids ID generation in `CreateUser` with a random string.
- Drop the `user_count` table entirely: remove the schema, the init insert,
  and both `UPDATE user_count ...` statements in `services.go`. No
  replacement counter — if a display of "registered users" is ever needed,
  it can be computed with `SELECT COUNT(*) FROM users` on demand.
- Remove the `sqids-go` dependency once it is no longer referenced.

## Non-goals

- No changes to the shape of the `id` column (`VARCHAR(255)`). Anything that
  fits is fine.
- No migration of existing IDs. Per the doc's blank-slate premise, there are
  no production IDs to preserve.
- No changes to countersignatures or identity records (those are Proposals
  03/04).

## Design

### ID format

Reuse the alphabet and generator used by `generateServerID` (already present
in `services.go`) for consistency. Target length: **12 characters** — with a
~57-character alphabet this gives ~68 bits of entropy, plenty for a
server-scoped namespace with `ON CONFLICT DO NOTHING` + retry.

Chosen alphabet: whatever `generateServerID` already uses. If that is Sqids-
adjacent (short, unambiguous, mixed-case) then reuse it verbatim; if a
distinct base58/base62 helper is preferable, introduce a small
`generateUserID(n int) string` alongside it. Either way: **one** helper,
crypto/rand-backed.

### Insert path

```go
id, err := generateUserID(12)
if err != nil {
    return nil, err
}

var exists bool
err = tx.QueryRow(`SELECT EXISTS (SELECT 1 FROM users WHERE id = $1)`, id).Scan(&exists)
if err != nil {
    return nil, err
}
if exists {
    return nil, fmt.Errorf("user ID collision, please retry")
}

err = tx.QueryRow(`
    INSERT INTO users (id, username)
    VALUES ($1, $2)
    RETURNING id, username, avatar_url, bio, created_at
`, id, username).Scan(...)
if err != nil {
    return nil, err
}
```

No server-side retry. At ~68 bits of entropy a collision is vanishingly
rare; if it happens we return an error and the client is expected to retry
the signup. This keeps `CreateUser` linear and avoids hiding pathological
states (e.g. a broken RNG) behind a silent loop.

We do a `SELECT EXISTS (...)` pre-check rather than inferring the collision
from a driver error (`ON CONFLICT DO NOTHING` + `sql.ErrNoRows`, or `pq.Error`
+ SQLSTATE `23505`). Reasons:

- No magic-number constant (`23505`) or constraint-name string (`users_pkey`)
  in application code.
- Nothing hits the DB error log for the common case where the pre-check
  catches it — the insert only fires when we know it will succeed.
- Any error from `INSERT` after that is genuinely unexpected and should
  bubble up unmodified.

There is a nominal race between the `SELECT` and the `INSERT`, but the
transaction wrapper + the near-zero collision probability make it moot; if
two callers do race, the second `INSERT` returns a normal error and callers
retry, same as if the pre-check had caught it.

### `user_count`

Deleted. Per the blank-slate premise there is no data to preserve, so the
schema is simply removed from `db.go` (both `CREATE TABLE` and the
`INSERT ... ON CONFLICT DO NOTHING` seed row) and the two `UPDATE
user_count ...` statements in `services.go` (increment in `CreateUser`,
decrement in the user-deletion path) go with it. No `DROP TABLE` migration
is added; environments that already have the table can drop it manually.

## Work items

1. Introduce `generateUserID(n int) (string, error)` in `services.go` (or
   `utils.go`), backed by `crypto/rand`.
2. Rewrite the ID generation block in `CreateUser` to: generate an ID,
   `SELECT EXISTS (SELECT 1 FROM users WHERE id = $1)`, and on `true` return a
   "please retry" error to the caller (no server-side retry). Otherwise do a
   plain `INSERT ... RETURNING ...` and let any error bubble up.
3. Delete the Sqids import from `services.go`.
4. Remove `sqids-go` from `go.mod`/`go.sum` if no other file uses it (`rg
   sqids` after step 3).
5. Drop the `user_count` table: remove `createUserCountTable` and
   `initUserCountTable` from `db.go`, and delete both `UPDATE user_count ...`
   statements from `services.go` (increment in `CreateUser`, decrement in the
   user-deletion path).
6. Update tests in `services_test.go` that assert ID shape/prefix (if any).

## Testing

- Unit test: `generateUserID(12)` produces distinct values across 1000 calls,
  all matching the expected alphabet.
- Integration: create 1000 users in a loop; assert all IDs are unique.
- Regression: existing signup end-to-end test still passes.

## Risks

- **Anything that parses user IDs**. Sqids IDs happen to be alphanumeric and
  variable-length; random IDs in the same alphabet should be a drop-in shape.
  Grep the SPA and Go code for hardcoded ID length assumptions before landing.
- **Consumers of `user_count`**: none in-tree today (`rg user_count` returns
  only the schema and the two update sites we are removing). Any external
  dashboard querying the table will break — acceptable given blank-slate.

## Dependencies

None. Can land in parallel with Proposals 01, 03–07.

## Parallelism

Fully independent. Proposals 04/06 (signed identity records, signed
revocations) will embed the new user ID format automatically once landed —
they do not need Proposal 02 to be complete first, but starting Proposal 04
after Proposal 02 avoids a late reformat.
