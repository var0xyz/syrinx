# Coverage 01 — Counters + `GET …/stats`

## Status

Proposed.

## Depends on

[00](00_design.md)

## Context

Echo count is already indexed (`reed_echoes`) and exposed as
`GET /reeds/{userID}/{reedID}/echoes` (bare JSON number). Coverage needs
holder and active-user totals without scanning tables on every allocate.
Reed detail should fetch **one** snapshot that includes echoes and coverage.

## Scope

- DDL for per-reed `allocation_count` and network `active_users`.
- Same-TX bumps on every allocate / deallocate / signup / account-removal path.
- `GET /reeds/{userID}/{reedID}/stats`.
- Relationship to existing `/echoes`.

## Non-goals

- DB views or triggers as the primary write path (app bumps in the same TX
  as existing helpers so call sites stay obvious).
- WS subscribe (see [02](02_reed_subscription.md)).
- Backfill / migrations (blank slate).

## Design

### Schema

Prefer a column on `reeds` for holder count (one tip row per reed already):

```sql
CREATE TABLE IF NOT EXISTS reeds (
    id VARCHAR(255) UNIQUE NOT NULL,
    user_id VARCHAR(255) NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    private_key_fingerprint VARCHAR(255) NOT NULL REFERENCES private_keys(fingerprint),
    signed_at TIMESTAMP NOT NULL,
    user_signature_id INT NOT NULL REFERENCES user_signatures(id),
    server_signature_id INT NOT NULL REFERENCES server_signatures(id),
    allocation_count INT NOT NULL DEFAULT 0,
    PRIMARY KEY (user_id, id)
);
```

Network-wide active users — single-row table (or equivalent singleton):

```sql
CREATE TABLE IF NOT EXISTS network_stats (
    id BOOLEAN PRIMARY KEY DEFAULT TRUE CHECK (id),
    active_users INT NOT NULL DEFAULT 0
);
```

Seed `network_stats` with one row at init (`active_users = 0`). Signup
increments; account removal decrements (not below zero).

Invariant: `reeds.allocation_count` equals
`COUNT(*) FROM reed_allocations WHERE reed_id = reeds.id` after every
successful TX that touches allocations for that reed.

### Same-TX bump sites

| Event | Allocation rows | Counter action |
|-------|-----------------|----------------|
| Author publish (`CreateReedWithEcho`) | Insert author allocation | `allocation_count = 1` (or `+1` if insert succeeds) |
| `AllocateReed` (`ON CONFLICT DO NOTHING`) | Insert holder | `+1` **only if** a row was inserted (`RowsAffected` / `INSERT … RETURNING`) |
| Recovery `SaveReed` allocation for reporter | `ON CONFLICT DO NOTHING` | `+1` only if inserted |
| `DeleteReedAllocation` | Delete one holder | `-1` only if a row was deleted |
| `ClearPeerStateForRemovedAccount` | Delete all of viewer’s allocations for removed author | Subtract `RowsAffected` (or per-reed decrements in the same TX) |
| Reed hard-delete / tip cascade | Allocations cascade away | Row gone — no separate counter maintain |
| Signup (user create) | — | `network_stats.active_users += 1` |
| Account removal insert | — | `network_stats.active_users -= 1` (floor 0) |

**Idempotent allocate:** conflict → no increment (critical — ACK retries must
not inflate coverage).

**Bulk clear:** when deleting many allocations for one viewer against one
author, decrement each affected reed’s `allocation_count` by the number of
rows removed for that `reed_id` (group by `reed_id` in the TX, or
`UPDATE reeds SET allocation_count = allocation_count - sub.count FROM …`).

Helpers today live in `realtime/db.go` (`AllocateReed`,
`DeleteReedAllocation`, `ClearPeerStateForRemovedAccount`) and
`CreateReedWithEcho` / recovery `SaveReed`. Refactor allocate/deallocate to
accept a `*sql.Tx` (or wrap begin/commit) so counter updates cannot drift
from the row change.

After commit of allocate/deallocate, realtime notifies reed subscribers
([02](02_reed_subscription.md)) using the new `allocation_count` and current
`active_users` — no `COUNT(*)`.

### `GET /reeds/{userID}/{reedID}/stats`

Authenticated. Path author + reed id.

**Behaviour**

- Tip missing → 404 (same class as other reed GETs).
- Reed or author removed (removal cert) → 410 with existing cert body
  semantics where applicable; no stats body required.
- Otherwise 200 JSON:

```json
{
  "echoes": 3,
  "holders": 12,
  "activeUsers": 100,
  "coveragePercent": 12
}
```

| Field | Source |
|-------|--------|
| `echoes` | `COUNT` / existing echo index for this target (same predicate as today’s `/echoes` — non-removed echoes) |
| `holders` | `reeds.allocation_count` |
| `activeUsers` | `network_stats.active_users` |
| `coveragePercent` | `floor(100 * holders / activeUsers)` or `0` if `activeUsers == 0`; clamp to `100` max |

### Relationship to `/echoes`

- Reed detail **uses `/stats`** for the initial snapshot (echoes + coverage).
- Keep `GET …/echoes` as a thin endpoint returning the bare echo number for
  any other callers, implemented by sharing the echo-count query with
  `/stats`, **or** document it as deprecated in favor of `/stats.echoes`.
  Prefer: keep `/echoes` working; detail page stops calling it.

### Tests / checklist

- Publish → `allocation_count == 1`, stats `holders: 1`.
- Second allocate (new holder) → `holders: 2`; duplicate ACK → still `2`.
- Deallocate → `holders` decrements; coverage WS/REST agree.
- Signup increments `activeUsers`; account removal decrements.
- `/stats` returns echoes consistent with `/echoes` for the same reed.
- Removed reed → no 200 stats body.
