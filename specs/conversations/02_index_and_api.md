# Conversations 02 — Echo/reply index tables + list/count APIs

## Status

Proposed.

## Depends on

[01](01_publish_and_refs.md)

## Context

With publish-time ref parsing in place, the server can maintain durable
**metadata indexes** without storing reed bodies. Clients need:

1. `echoCount` when opening a reed.
2. A paginated list of direct reply metadata for the conversation section.

## Scope

- DDL for `reed_echoes` and `reed_replies`.
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
-- echoing_* = who/what is doing the echo; echoed_* = who/what is being echoed.
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

-- One row per reply reed. reply_reed_id is UNIQUE: a reed replies to at most one parent.
CREATE TABLE IF NOT EXISTS reed_replies (
    parent_user_id VARCHAR(255) NOT NULL,
    parent_reed_id VARCHAR(255) NOT NULL,
    reply_user_id  VARCHAR(255) NOT NULL REFERENCES users(id),
    reply_reed_id  VARCHAR(255) NOT NULL UNIQUE,
    signed_at      TIMESTAMP NOT NULL,
    PRIMARY KEY (reply_user_id, reply_reed_id)
);

CREATE INDEX IF NOT EXISTS idx_reed_replies_parent_signed
    ON reed_replies (parent_user_id, parent_reed_id, signed_at);
```

No FK to `reeds` — parents may be hard-deleted after removal while index rows
remain; queries filter via `reed_removals` / `account_removals`.

Register DDL in `InitDB` ([`db.go`](../../db.go)).

### Insert on publish

Inside the `SignReed` transaction (after tip-check lock if
[recovery 16](../recovery/16_reed_tip_check.md) has landed):

| Parsed ref | Insert |
|------------|--------|
| `echoing → (Tuser, Tid)` | `reed_echoes(echoing_user_id=author, echoing_reed_id=newId, echoed=T…, signed_at)` |
| `replying → (Puser, Pid)` | `reed_replies(parent=…, reply_user_id=author, reply_reed_id=newId, signed_at)` |

`ON CONFLICT (echoing_user_id, echoing_reed_id)` /
`ON CONFLICT (reply_user_id, reply_reed_id)` `DO NOTHING` for idempotent
retries (same reed republication must not duplicate).

Store helpers in `DataService` methods.

### Visibility predicate (shared SQL fragment)

A row is **visible** when:

- `reply_reed_id` / `echoing_reed_id` has no row in `reed_removals` for that
  `(user_id, reed_id)`.
- `reply_user_id` / `echoing_user_id` has no row in `account_removals`.

Apply to counts and lists.

### `GET /reeds/{userID}/{reedID}` (extend)

Existing JSON gains:

```json
{
  "id": "...",
  "userID": "...",
  "fingerprint": "...",
  "timestamp": "...",
  "echoCount": 3
}
```

`echoCount` = visible rows in `reed_echoes` for
`(echoed_user_id, echoed_reed_id) = (userID, reedID)`.

Auth: same as today (public metadata endpoint for existence checks).

### `GET /reeds/{userID}/{reedID}/replies`

List **direct replies** to the parent reed.

Query params:

| Param | Default | Description |
|-------|---------|-------------|
| `limit` | 50 | Max 100 |
| `before` | — | Cursor: ISO8601 `signed_at` of oldest item already shown (exclusive) |

Response `200`:

```json
{
  "replies": [
    {
      "userID": "replyAuthorId",
      "reedID": "replyReedId",
      "signedAt": "2026-07-27T12:00:00Z",
      "username": "bob"
    }
  ],
  "hasMore": false
}
```

- Order: `signed_at ASC`, `reply_reed_id ASC` tie-break.
- `username` from `users` table when available; omit if unknown.
- Parent removed → still 200 with replies (children are independent reeds).
- Parent not found (never existed) → 404.
- Parent removed and caller uses existence check → existing 410 from
  `GetReed` path; replies endpoint returns 410 when parent has removal cert
  (consistent with `GetReed`).

Auth: public (metadata only). Bodies require relay + signature auth as today.

### Routes

In [`main.go`](../../main.go):

```
GET /reeds/{userID}/{reedID}/replies
```

OPTIONS noop alongside existing reed routes.

## Work items

1. DDL + indexes in `InitDB`.
2. `InsertEcho` / `InsertReply` store helpers.
3. Wire into `SignReed` transaction.
4. `CountEchoes(echoedUser, echoedReed)`.
5. `ListReplies(parentUser, parentReed, limit, before)`.
6. Extend `GetReed` handler response.
7. `ListReplies` handler + route.
8. Tests:
   - Publish echo → count increments.
   - Publish reply → appears in list.
   - Remove echo/reply reed → excluded from count/list.
   - Account-remove reply author → excluded.
   - Pagination `before` cursor.
   - Idempotent republish does not double-count.

## Risks

- **Hot threads** — very large reply lists need pagination; default 50 is
  enough for v1 UI with "Load more" in SPA.
- **Username denormalization** — `username` in list is convenience; may be
  stale after profile update. Acceptable for preview cards; detail page
  fetches fresh profile.

## Parallelism

[03](03_spa_reed_detail.md) can mock API responses while this step is in
flight.
