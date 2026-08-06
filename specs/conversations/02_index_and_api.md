# Conversations 02 — Echo/reply index tables + list/count APIs

## Status

Implemented — **`reed_echoes`** DDL, insert on publish, `CountEchoes`, and
`GET /reeds/{userID}/{reedID}/echoes`; **`reed_threads`** + **`reed_replies`**,
reply insert on publish (`InsertReply`), `threadId` header resolution, and
`GET /reeds/{userID}/{reedID}/replies`. `echoCount` on `GET …/reeds` (spec
shape) remains open.

## Depends on

[01](01_publish_and_refs.md)

## Context

With publish-time ref parsing in place, the server can maintain durable
**metadata indexes** without storing reed bodies. Clients need:

1. `echoCount` when opening a reed.
2. A paginated list of direct reply metadata for the conversation section.

## Scope

- DDL for `reed_echoes`, `reed_threads`, and `reed_replies`.
- Insert rows in the same transaction as `CreateReed` when social refs are
  present.
- Extend `GET /reeds/{userID}/{reedID}` with `echoCount`.
- New `GET /reeds/{userID}/{reedID}/replies` endpoint.
- Filter out removed reeds and account-removed authors in all queries.

## Non-goals

- Storing or serving reed markdown from these tables.
- Echo list endpoint (count only in v1).
- Cross-instance indexes.
- Realtime event types (see [00](00_design.md)).
- Index compaction / GC when targets are deleted.

## Design

### Schema

```sql
-- One row per echo reed. echoing_reed_id is UNIQUE: a reed echoes at most one target.
CREATE TABLE IF NOT EXISTS reed_echoes (
    echoing_user_id VARCHAR(255) NOT NULL REFERENCES users(id),
    echoing_reed_id VARCHAR(255) NOT NULL UNIQUE,
    echoed_user_id  VARCHAR(255) NOT NULL,
    echoed_reed_id  VARCHAR(255) NOT NULL,
    signed_at       TIMESTAMP NOT NULL,
    PRIMARY KEY (echoing_user_id, echoing_reed_id)
);

CREATE INDEX IF NOT EXISTS idx_reed_echoes_echoed_signed
    ON reed_echoes (echoed_user_id, echoed_reed_id, signed_at);

-- One row per conversation thread. Created on the first reply to a root reed.
-- id is the root reed ref (user@server/reed), same wire form as threadId header.
CREATE TABLE IF NOT EXISTS reed_threads (
    id             VARCHAR(255) PRIMARY KEY,
    root_user_id   VARCHAR(255) NOT NULL,
    root_reed_id   VARCHAR(255) NOT NULL,
    reply_count    INT NOT NULL DEFAULT 1,
    FOREIGN KEY (root_user_id, root_reed_id) REFERENCES reeds(user_id, id)
);

-- One row per reply reed.
CREATE TABLE IF NOT EXISTS reed_replies (
    thread_id        VARCHAR(255) NOT NULL REFERENCES reed_threads(id),
    user_id          VARCHAR(255) NOT NULL,
    reed_id          VARCHAR(255) NOT NULL UNIQUE,
    parent_user_id   VARCHAR(255) NOT NULL,
    parent_reed_id   VARCHAR(255) NOT NULL,
    timestamp        TIMESTAMP NOT NULL,
    PRIMARY KEY (user_id, reed_id),
    FOREIGN KEY (user_id, reed_id) REFERENCES reeds(user_id, id),
    FOREIGN KEY (parent_user_id, parent_reed_id) REFERENCES reeds(user_id, id)
);

CREATE INDEX IF NOT EXISTS idx_reed_replies_parent_timestamp
    ON reed_replies (parent_user_id, parent_reed_id, timestamp);

CREATE INDEX IF NOT EXISTS idx_reed_replies_thread
    ON reed_replies (thread_id, timestamp);
```

No FK to `reeds` — parents may be hard-deleted after removal while index rows
remain; queries filter via `reed_removals` / `account_removals`. Reply rows are
**never deleted** when the reply itself is removed (needed to render the
reply as a tombstone in the conversation list).

### Thread resolution at publish

Reply to parent `P`:

1. If `P` has a `reed_replies` row → inherit `thread_id` from it; load
   `root_user_id`/`root_reed_id` from `reed_threads`.
2. Otherwise `P` is the root → canonical `threadId` = ref to `P` (thread row
   is created on first reply).

The signed `threadId` header must match the server-computed value (signature
verification). Wrong `threadId` (wrong root, reed not in thread, mid-node
instead of root ref) → 400. Missing `threadId` when `replying` is set → 400.

All reeds in a thread share the same `thread_id`; listing `reed_replies` by
`thread_id` yields every reply reed in the conversation for validation.

### Insert on publish

Inside `SignReed` (after reed creation):

| Parsed ref | Insert |
|------------|--------|
| `echoing → (Tuser, Tid)` | `reed_echoes(...)` |
| `replying → (Puser, Pid)` | `reed_replies(...)` + upsert `reed_threads` (`reply_count` bump gated on new reply row) |

`ON CONFLICT (user_id, reed_id) DO NOTHING` on replies for idempotent retries.

### Visibility predicate (shared SQL fragment)

A row is **visible** when:

- `reed_id` has no row in `reed_removals` for that `(user_id, reed_id)`.
- `user_id` has no row in `account_removals`.

Apply to counts and lists.

### `GET /reeds/{userID}/{reedID}/replies`

List **direct replies** to the parent reed (one level only).

Query params:

| Param | Default | Description |
|-------|---------|-------------|
| `limit` | 50 | Max 100 |
| `before` | — | Cursor: ISO8601 `timestamp` of oldest item already shown (exclusive) |

Response `200`:

```json
{
  "replies": [
    {
      "userID": "replyAuthorId",
      "reedID": "replyReedId"
    }
  ],
  "hasMore": false
}
```

- Order: `timestamp ASC`, `reed_id ASC` tie-break.
- Parent removed → still 200 with replies.
- Parent not found → 404.
- Parent removed → 410 (consistent with `GetReed`).

## Work items

1. DDL + indexes in `InitDB`.
2. `InsertReply` store helper (reply row + thread upsert).
3. Wire into `SignReed`.
4. `ListReplies(parentUser, parentReed, limit, before)`.
5. `ListReplies` handler + route.
6. Tests + SPA `threadId` on publish.

## Risks

- **Hot threads** — pagination default 50; drill-down navigation for nested replies.
- **Username denormalization** — convenience field; may be stale after profile update.

## Parallelism

[03](03_spa_reed_detail.md) can consume the replies API for the conversation section.
