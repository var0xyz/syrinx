# Ripples 01 — Schema + expiry sweep

## Status

Proposed.

## Depends on

[00](00_design.md)

## Context

[00](00_design.md) locks the model: one thread per reed, flat ripple list,
whole-thread expiry at `last_activity_at + 7 days`, hard delete. This step
turns that into a schema and a background sweep — the first TTL/cron-style
mechanism in this codebase (confirmed via code audit: the only existing
background ticker is the realtime WS keepalive ping, unrelated to data
expiry).

## Scope

- `ripples` package: schema (`ripple_threads`, `ripples` tables).
- Expiry sweep goroutine, started from `main.go` at boot.
- Store methods needed by 02/03 (insert, list, delete-thread,
  bump-activity — all as one transaction where applicable).

## Non-goals

- The HTTP API surface (02).
- Realtime fanout (03).
- Rate limiting (02).

## Design

### Schema

```sql
-- One row per reed that has ever received a ripple. Created lazily on the
-- first ripple against a reed. No FK to reeds(id) — same soft-reference
-- pattern reed_echoes/reed_replies use, since a reed may be hard-deleted
-- (signed removal) while its (now also-expiring) ripple thread still exists
-- for the remainder of its own 7-day window. A removed parent reed's
-- thread is not force-expired early — see Design > Parent-reed removal.
CREATE TABLE IF NOT EXISTS ripple_threads (
    id               VARCHAR(255) PRIMARY KEY, -- ref(parent_user_id, parent_reed_id), same "user/reed" wire form used elsewhere
    parent_user_id   VARCHAR(255) NOT NULL,
    parent_reed_id   VARCHAR(255) NOT NULL,
    last_activity_at TIMESTAMP NOT NULL,
    expires_at       TIMESTAMP NOT NULL
);

CREATE INDEX IF NOT EXISTS idx_ripple_threads_expires
    ON ripple_threads (expires_at);

-- One row per ripple. No signature, no signed_at — posted_at is server-
-- generated wall-clock time, same as any other server-stamped column.
CREATE TABLE IF NOT EXISTS ripples (
    id                    VARCHAR(255) PRIMARY KEY,
    thread_id             VARCHAR(255) NOT NULL REFERENCES ripple_threads(id) ON DELETE CASCADE,
    user_id               VARCHAR(255) NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    content               VARCHAR(140) NOT NULL,
    in_reply_to_ripple_id VARCHAR(255) REFERENCES ripples(id) ON DELETE SET NULL,
    posted_at             TIMESTAMP NOT NULL
);

CREATE INDEX IF NOT EXISTS idx_ripples_thread_posted
    ON ripples (thread_id, posted_at);
```

`ripples.id` uses the same ID generator as every other entity in this
codebase (`crypto.Alphabet`/`crypto.Length` — see [conversations 04
mentions](../conversations/04_mentions.md) note on non-fixed ID lengths
across servers; irrelevant here since ripples never leave this server, but
consistency of ID shape is still worth keeping).

`ON DELETE CASCADE` on `ripples.user_id` — deleting a user (account
removal) deletes their own ripples outright rather than leaving orphaned
rows or tombstones; unlike reed replies, there's no "render as tombstone"
requirement for ripples (see Non-goals: no permanence story at all).

`ON DELETE SET NULL` on `in_reply_to_ripple_id` — if the referenced ripple
was deleted by its own author (self-delete, see [00](00_design.md)
Moderation), the addressing pointer just goes null and the SPA falls back
to the "replying to a deleted comment" rendering already specified in 00.

### Parent-reed removal

If the parent reed is later removed (signed deletion,
[`deletion/`](../deletion/README.md)) or the author's account is removed,
the ripple thread is **not** force-expired early. It continues to live out
its own `expires_at` and then the sweep removes it normally. Rationale:
ripples are never shown without their parent reed rendered (00's visibility
gate), so once the parent is gone, the SPA simply never fetches or renders
the orphaned thread — there's no user-facing inconsistency to fix by adding
early-cleanup logic, and adding it would mean hooking the sweep into two
more deletion code paths for no observable benefit. The sweep's normal
7-day pass reclaims the row eventually either way.

### Insert / activity-bump transaction

Posting a ripple (used by [02](02_post_and_list_api.md)) is one
transaction:

1. `INSERT INTO ripple_threads (id, parent_user_id, parent_reed_id,
   last_activity_at, expires_at) VALUES (...) ON CONFLICT (id) DO UPDATE
   SET last_activity_at = EXCLUDED.last_activity_at, expires_at =
   EXCLUDED.expires_at` — creates the thread on first ripple, bumps it on
   every subsequent one, in a single upsert.
2. `INSERT INTO ripples (...)`.

Both `last_activity_at` and `expires_at` are computed from the same
server-side `now()` reading passed into the transaction — no separate
read-then-write race, and no client-supplied timestamp anywhere in this
flow (consistent with "unsigned, session-authenticated, server clock only").

### Expiry sweep

New `ripples.StartExpirySweep(ctx context.Context, db *sql.DB, interval
time.Duration)` goroutine, started once from `main.go` at boot (alongside
the realtime hub's own startup) and stopped on the same shutdown context
everything else uses. Runs on a `time.Ticker` — first mechanism of its
kind in the codebase, kept intentionally simple rather than pulling in a
job-scheduling dependency for a single query:

```go
for {
    select {
    case <-ctx.Done():
        return
    case <-ticker.C:
        if _, err := db.ExecContext(ctx,
            `DELETE FROM ripple_threads WHERE expires_at <= NOW()`); err != nil {
            log.Printf("[ERROR] ripple expiry sweep: %v", err)
        }
    }
}
```

`ripples` cascade-delete via the `ON DELETE CASCADE` FK to
`ripple_threads` — no separate delete statement needed. Sweep interval:
**1 hour**. A week-long TTL doesn't need minute-level precision; hourly
keeps the worst-case overshoot (a thread living up to ~1h past its
`expires_at`) acceptably small without adding sweep load. No admin
override/config for the interval in v1 — hardcode it, revisit only if
operational experience says otherwise.

## Work items

1. `ripples/schema.go` (or equivalent) — DDL, wired into `InitDB`/whatever
   this codebase's schema-bootstrap entrypoint is (check `db.go` for the
   existing pattern other tables register through).
2. `ripples/store.go` — `PostRipple`, `ListThread`, `DeleteRipple`
   (self-delete only, per 00), the insert/bump transaction above.
3. `ripples/sweep.go` — `StartExpirySweep`.
4. `main.go` — call `StartExpirySweep` at boot with the app's shutdown
   context.
5. Tests: thread creation on first ripple, activity bump + expires_at
   extension on second ripple, sweep deletes expired thread + cascades
   ripples, sweep leaves non-expired thread alone, self-delete of a ripple
   nulls a dependent `in_reply_to_ripple_id` without deleting the thread.

## Risks

- **Sweep query cost at scale** — `DELETE ... WHERE expires_at <= NOW()`
  with the `idx_ripple_threads_expires` index should stay cheap even with
  many threads; revisit batching only if it becomes a measured problem.
- **Clock skew** — server-only timestamps throughout, no client clock ever
  trusted, so this is a non-issue by construction.

## Dependencies

None beyond stdlib `database/sql` + `time`.

## Parallelism

02 depends on the store methods here; can be scaffolded in parallel once
the schema and method signatures are agreed, actual implementation waits
on this landing.
