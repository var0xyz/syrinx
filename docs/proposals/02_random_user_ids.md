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

The counter must remain available as a **display-only** number of registered
users; it is no longer an ID source.

## Scope

- Replace the Sqids ID generation in `CreateUser` with a random string.
- Keep `user_count` as a display-only counter (kept incremented for backwards
  compatibility with existing admin/UI consumers).
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
for attempt := 0; attempt < 5; attempt++ {
    id := generateUserID(12)
    err := tx.QueryRow(`
        INSERT INTO users (id, username)
        VALUES ($1, $2)
        ON CONFLICT (id) DO NOTHING
        RETURNING id, username, avatar_url, bio, created_at
    `, id, username).Scan(...)
    if err == sql.ErrNoRows {
        continue // extremely rare collision
    }
    // ...
}
```

Cap the retry loop; log a warning on retry. Five attempts is theatre at 68
bits but cheap.

### `user_count`

Keep the `UPDATE user_count SET count = count + 1, active = active + 1`
statement in `CreateUser` (and the mirror decrement path already in
`services.go` around line 496). It is now purely a display counter. Add a code
comment stating "display only — not an ID source" at both sites.

If we want to be tidy we can also drop `active` in a follow-up; leave it for
now to minimise blast radius of this PR.

## Work items

1. Introduce `generateUserID(n int) (string, error)` in `services.go` (or
   `utils.go`), backed by `crypto/rand`.
2. Rewrite the ID generation block in `CreateUser` to loop + `ON CONFLICT DO
   NOTHING`.
3. Delete the Sqids import from `services.go`.
4. Remove `sqids-go` from `go.mod`/`go.sum` if no other file uses it (`rg
   sqids` after step 3).
5. Add comments at the two `user_count` update sites clarifying it is
   display-only.
6. Update tests in `services_test.go` that assert ID shape/prefix (if any).

## Testing

- Unit test: `generateUserID(12)` produces distinct values across 1000 calls,
  all matching the expected alphabet.
- Integration: create 1000 users in a loop; assert all IDs are unique and
  `user_count.count` equals 1000.
- Regression: existing signup end-to-end test still passes.

## Risks

- **Anything that parses user IDs**. Sqids IDs happen to be alphanumeric and
  variable-length; random IDs in the same alphabet should be a drop-in shape.
  Grep the SPA and Go code for hardcoded ID length assumptions before landing.
- **Log/analytics dashboards** keyed on the counter: no change — the counter
  still increments.
- **`user_count.active`** decrement/consistency: unchanged; still tracked the
  same way.

## Dependencies

None. Can land in parallel with Proposals 01, 03–07.

## Parallelism

Fully independent. Proposals 04/06 (signed identity records, signed
revocations) will embed the new user ID format automatically once landed —
they do not need Proposal 02 to be complete first, but starting Proposal 04
after Proposal 02 avoids a late reformat.
