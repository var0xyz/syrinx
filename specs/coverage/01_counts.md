# Coverage 01 — Denormalized counters

## Status

Implemented.

## Depends on

[00](00_design.md)

## Context

Coverage percent needs holder and active-user totals without scanning
tables on every allocate. Echo count stays on the existing `reed_echoes`
index (conversations). Live WS ([02](02_reed_subscription.md)) reads these
counters after commit.

## Scope

- DDL for per-reed `allocation_count` and network `active_users`.
- Same-TX bumps on every allocate / deallocate / signup / account-removal
  path.

## Non-goals

- HTTP snapshot endpoints for reed-detail stats.
- WS subscribe wire (02).
- Backfill / migrations (blank slate).

## Design

### Schema

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

CREATE TABLE IF NOT EXISTS network_stats (
    id BOOLEAN PRIMARY KEY DEFAULT TRUE CHECK (id),
    active_users INT NOT NULL DEFAULT 0
);
```

Seed `network_stats` with one row at init (`active_users = 0`).

Invariant: `reeds.allocation_count` equals
`COUNT(*) FROM reed_allocations WHERE reed_id = reeds.id` after every
successful TX that touches allocations for that reed.

### Same-TX bump sites

| Event | Counter action |
|-------|----------------|
| Author publish | `allocation_count = 1` (or `+1` if insert succeeds) |
| `AllocateReed` (`ON CONFLICT DO NOTHING`) | `+1` **only if** a row was inserted |
| Recovery `SaveReed` allocation | `+1` only if inserted |
| `DeleteReedAllocation` | `-1` only if a row was deleted |
| `ClearPeerStateForRemovedAccount` | Subtract rows removed per reed in the same TX |
| Reed tip cascade | Row gone — no separate counter maintain |
| Signup | `network_stats.active_users += 1` |
| Account removal | `network_stats.active_users -= 1` (floor 0) |

**Idempotent allocate:** conflict → no increment.

Helpers live in `realtime/db.go` and publish/recovery paths. Refactor
allocate/deallocate to share a TX with the counter update.

After commit: notify reed subscribers ([02](02_reed_subscription.md)).

### Coverage percent helper

```
coveragePercent = floor(100 * allocation_count / active_users)  // active_users > 0
coveragePercent = 0                                               // else
```

Clamp to `100`. Used for subscribe snapshot and `REED_COVERAGE` events.

### Tests / checklist

- Publish → `allocation_count == 1`.
- Second allocate → count rises; duplicate ACK → unchanged.
- Deallocate → count drops.
- Signup / account removal bump `active_users`.
