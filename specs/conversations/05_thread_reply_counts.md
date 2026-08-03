# Conversations 05 — Recursive thread reply counts

## Status

Proposed.

## Depends on

[02](02_index_and_api.md)

## Context

[00](00_design.md) and [02](02_index_and_api.md) give the reed detail page a
**direct**-reply-only count and list (`reed_replies`, one level at a time).
That matches the locked conversation UX, but it does not answer "how many
replies does this reed have in total, including replies to replies" — the
same kind of stat the echo count already gives for echoes
([`REED_STATS`/`REED_ECHOES`](../../realtime/service.go)).

Echoes are cheap to total live: an echo points at exactly one target, so
`COUNT(*)` on `reed_echoes` for that target is the whole answer, and a new
echo only ever changes one target's count. Replies don't have that property
— a reply three levels deep is also, transitively, a reply to the root and
to every reed in between. Naively supporting "total replies including
nested ones" would mean either a recursive query on every read or walking
every ancestor to fan out a live update on every write.

This step avoids both by flattening every reply chain to a single
**thread**, identified by the ref of the topmost reed in the chain, and
maintaining one denormalized counter per thread — the same "same-TX bump"
pattern already used for `reeds.allocation_count` / `network_stats.active_users`
([`specs/coverage/01_counts.md`](../coverage/01_counts.md)), just keyed by
thread root instead of by reed.

## Scope

- New `threadId` signed-markdown header, alongside `echoing`/`replying`.
- `reed_threads` counter table; same-TX bump on publish, decrement on
  removal (single reed and account-removal cascade).
- Extend the WS reed-stats pattern ([`specs/coverage/02_reed_subscription.md`](../coverage/02_reed_subscription.md))
  with a `replies` field on `REED_STATS` and a new `REED_REPLIES` live-delta
  message, mirroring `REED_ECHOES`.
- SPA: third stat icon on the reed detail page, reusing the already-present
  [`reply-16.png`](../../spa/static/icons/reply-16.png) asset.

## Non-goals

- Changing the direct-reply list/UI from [00](00_design.md)/[03](03_spa_reed_detail.md)
  — this is an additional aggregate **count**, not a replacement for the
  one-level-at-a-time conversation section.
- A distinct per-node subtree count (see "Locked semantic" below) — every
  reed in a thread reports the thread's total, not its own descendant count.
- Cross-instance thread resolution before federation ships (same limitation
  as `echoing`/`replying` today).

## Design

### Locked semantic: per-thread, not per-node

The count shown next to any reed in a thread is the **thread's total live
reply count** — the same number for the root and for every reply beneath
it, no matter how deep. It is *recursive* in the sense the product needs
(replies of replies count toward the total), but it is not a distinct
"descendants of this exact node" count. This is what keeps maintenance
O(1) per write instead of a recursive walk on every read or write.

### `threadId` header

Third optional social header, alongside `echoing` and `replying`, using the
same wire format locked in [01](01_publish_and_refs.md):

```
threadId: <userID>@<serverID>/<reedID>
```

- Empty for root reeds and for reeds that only echo (no `replying`).
- Resolved **server-side** at publish time with a single lookup, not a walk:
  - Reed replies to parent `P`.
  - If `P` has a `reed_replies` row (i.e. `P` is itself a reply), the new
    reed's `threadId` = `P`'s own `thread_user_id`/`thread_reed_id`
    (inherit — "if the thread id exists already, we replace it").
  - Otherwise `P` is the root: the new reed's `threadId` = ref to `P`.
- Producers still set the header via `formatReedRef` (never a bare id), and
  it's rebuilt server-side the same way `echoing`/`replying` are rebuilt in
  `ReedAsMarkdown` for signature verification ([01](01_publish_and_refs.md)).

### `reed_threads` schema

```sql
CREATE TABLE IF NOT EXISTS reed_threads (
    thread_user_id VARCHAR(255) NOT NULL,
    thread_reed_id VARCHAR(255) NOT NULL,
    reply_count    INT NOT NULL DEFAULT 0,
    PRIMARY KEY (thread_user_id, thread_reed_id)
);
```

No FK to `reeds` — the root may be removed while its thread lives on (see
"Removal" below); `reply_count` is the sum of live, non-tombstoned replies
sharing that thread, regardless of how deep they are.

Register DDL in `InitDB` ([`db.go`](../../db.go)), alongside the amended
`reed_replies` table from [02](02_index_and_api.md#schema) (which now also
carries `thread_user_id`/`thread_reed_id` per row).

### Same-TX bump on publish

Inside the `SignReed` transaction, after resolving `threadId` and inserting
the `reed_replies` row (as in [02](02_index_and_api.md#insert-on-publish)):

```sql
INSERT INTO reed_threads (thread_user_id, thread_reed_id, reply_count)
VALUES ($1, $2, 1)
ON CONFLICT (thread_user_id, thread_reed_id)
DO UPDATE SET reply_count = reed_threads.reply_count + 1;
```

Idempotent republish (same reed, same signature retried) must not
double-count — gate the bump on the `reed_replies` insert actually affecting
a row (`ON CONFLICT (reply_user_id, reply_reed_id) DO NOTHING`, mirroring
the echo insert's `echoIndexed` flag), same as `CreateReedWithEcho` does for
echoes today ([`services.go`](../../services.go)).

### Removal: tombstone in place, decrement by exactly one

When a reed is removed
([`Handlers.DeleteReed`](../../handlers.go), which today calls
`DeleteEchoIndexForReed` for echoes):

1. Look up `reed_replies` by `(reply_user_id, reply_reed_id)` = the removed
   reed. If no row exists, the removed reed wasn't a reply — nothing to do
   here.
2. If a row exists, decrement `reed_threads.reply_count` by 1 for that row's
   `(thread_user_id, thread_reed_id)`, floored at 0, in the same
   transaction as the `reed_removals` insert.
3. The `reed_replies` row itself is **not deleted** — the conversation list
   ([00](00_design.md)) needs it to render the reply as a tombstone
   ("this reply was removed") rather than silently disappearing.
4. Removing a reed does **not** cascade to rows that point at it. If the
   removed reed had its own replies (other `reed_replies` rows with
   `parent_reed_id` = the removed reed), those rows are untouched and keep
   counting toward the thread total — only the one removed reed's own
   contribution is decremented. This also means removing a **thread root**
   decrements nothing (a root has no `reed_replies` row for itself); the
   thread and its total live on, same as the conversation list still
   showing replies to an unavailable parent today.

Account removal cascades the same decrement across every reply that account
authored, mirroring `DeleteEchoesByAuthor`'s enumerate-then-clean shape
([`services.go:1394-1423`](../../services.go)): for each `reed_replies` row
where `reply_user_id` = the removed account, decrement its thread's counter
by 1 (same "don't delete the row, just decrement" rule as above).

### WS live stats (mirrors echoes)

Extends the pattern in [`specs/coverage/02_reed_subscription.md`](../coverage/02_reed_subscription.md):

- `SUBSCRIBE_REED {userID, reedID}` — before building the snapshot, resolve
  the *viewed* reed to its thread root: look up `reed_replies` by
  `(reply_user_id, reply_reed_id) = (userID, reedID)`; if found, use its
  `thread_user_id`/`thread_reed_id`, else the viewed reed is itself the
  root. `GetReedStatsSnapshot` reads `reed_threads.reply_count` for that
  resolved root (a single indexed row lookup — cheaper than echoes' live
  `COUNT(*)`) and `REED_STATS` gains a `replies` field alongside `echoes`
  and `coveragePercent`.
- Reed-subscriber registration for the replies stat is keyed by the
  **resolved thread root**, not the literal viewed reed — otherwise a
  client viewing a non-root reed in a thread would never receive live
  updates when a reply lands on a different branch of the same thread.
  Echo and coverage subscriptions are unaffected and stay keyed on the
  literal viewed reed.
- New broadcast type `ReplyCountChanged` (`UserID`/`ReedID` = thread root),
  parallel to `EchoCountChanged`: sent after the bump on publish and after
  the decrement on removal (single reed and account-removal paths).
- New `notifyReedReplies(threadUserID, threadReedID)` in
  [`realtime/service.go`](../../realtime/service.go), parallel to
  `notifyReedEchoes`: reads `reed_threads.reply_count` and pushes
  `REED_REPLIES {type, userID, reedID, replies}` to the thread's
  subscribers.

### SPA surface

Same pattern as `echoCount`/`coveragePercent` in
[`+page.svelte`](../../spa/src/routes/reed/[userID]/[reedID]/+page.svelte):
a `replyCount` var seeded from the `REED_STATS` snapshot, updated live by a
new `handleReedReplies` handler on `REED_REPLIES`, cached locally the same
way `echoCountsRepository` caches `echoCount` for optimistic initial
render. Displayed as a third icon in the `reed-stats` line, reusing the
already-present but currently unused
[`reply-16.png`](../../spa/static/icons/reply-16.png) icon with the same
`mask-image` + `currentColor` styling as `.reed-stat-icon.echoes`/`.coverage`.

## Work items

1. DDL: `reed_threads` table; amend `reed_replies` per [02](02_index_and_api.md#schema).
2. `threadId` header: parse/validate/rebuild in `ReedAsMarkdown` alongside
   `echoing`/`replying`; resolve via single parent lookup at publish time.
3. Bump `reed_threads` in the `SignReed` transaction, gated on the
   `reed_replies` insert affecting a row.
4. Decrement `reed_threads` on single-reed removal (`DeleteReed` handler)
   and account removal, mirroring the echo cleanup call sites.
5. `ReplyCountChanged` broadcast type; `notifyReedReplies`; extend
   `GetReedStatsSnapshot`/`REED_STATS`; new `REED_REPLIES` message.
6. Thread-root resolution on `SUBSCRIBE_REED`; thread-keyed subscriber
   registration for the replies stat only.
7. SPA: `replyCount` state, `REED_REPLIES` handler, stat icon using
   `reply-16.png`.
8. Tests:
   - Reply to a root → thread created, count = 1.
   - Reply to a reply → same thread, count increments; verify a third-level
     reply still lands in the root's thread (single-hop inheritance works
     transitively).
   - Idempotent republish does not double-count.
   - Remove a leaf reply → count decrements by 1; its (nonexistent) children
     unaffected.
   - Remove a mid-thread reply → count decrements by exactly 1; replies
     further down the chain keep counting.
   - Remove the thread root → thread total unchanged.
   - Account removal → decrements once per reply that account authored,
     across however many threads they touched.
   - Subscribing to a non-root reed in a thread receives live `REED_REPLIES`
     updates when a reply lands elsewhere in the same thread.

## Risks

- **Extra publish-time lookup** — resolving `threadId` costs one indexed
  `SELECT` against `reed_replies` per reply publish; negligible next to the
  existing echo/reply target-existence checks already done in `SignReed`.
- **Thread-root subscription key mismatch** — if the resolution step is
  skipped or cached stale, a client could silently stop receiving live
  reply-count updates while still receiving echo/coverage updates
  correctly (they're keyed differently); worth an explicit test.
- **Semantic surprise** — because the count is per-thread, two sibling
  replies deep in unrelated branches of the same thread both show the
  *whole thread's* total, not their own branch's size. This is the
  intended, locked trade-off (see "Locked semantic" above), but should be
  called out in the SPA copy/tooltip if it proves confusing in practice.

## Parallelism

Independent of [03](03_spa_reed_detail.md)/[04](04_mentions.md) — this adds
a stat, not a change to the conversation list or mention indexing. Can land
any time after [02](02_index_and_api.md)'s schema exists.
