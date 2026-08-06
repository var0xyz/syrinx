# Conversations 05 — Recursive thread reply counts

## Status

Proposed.

## Depends on

[02](02_index_and_api.md)

## Context

[00](00_design.md) and [02](02_index_and_api.md) give the reed detail page a
**direct**-reply-only count and list (`reed_replies`, one level at a time).
That matches the locked conversation UX, but it doesn't answer two related
questions the product also wants:

1. **"How big is the whole conversation?"** — a single total for the thread,
   shown as a stat next to echo count / coverage, the same kind of number on
   every reed in the thread.
2. **"How many replies stem from the reed I'm looking at right now?"** — a
   number specific to *this* reed, including replies-to-replies beneath it
   (not just its direct children), shown as the header of a **Replies**
   section under the reed body.

These are genuinely different numbers with different scopes, and this step
covers both, plus what happens when browsing into a reed that's since been
removed but still has live replies underneath it.

Echoes are cheap to total live: an echo points at exactly one target, so
`COUNT(*)` on `reed_echoes` for that target is the whole answer, and a new
echo only ever changes one target's count. Replies don't have that property
— a reply three levels deep is also, transitively, a reply to the root and
to every reed in between. This step avoids a recursive query on every read
by maintaining two separate denormalized counters, described below.

## Scope

- New `threadId` signed-markdown header, alongside `echoing`/`replying`.
- `reed_threads` counter table — one row per thread, the **total** count,
  same-TX bump on publish / decrement on removal.
- `reed_reply_counts` counter table — one row per reed that has at least one
  descendant, the **subtree** count for that specific reed, maintained by
  walking the ancestor chain on publish/removal.
- Extend the WS reed-stats pattern ([`specs/coverage/02_reed_subscription.md`](../coverage/02_reed_subscription.md))
  with a `replies` field on `REED_STATS` (thread total only) and a new
  `REED_REPLIES` live-delta message, mirroring `REED_ECHOES`.
- SPA: third stat icon on the reed detail page (thread total), reusing the
  already-present [`reply-16.png`](../../spa/static/icons/reply-16.png)
  asset, **and** a `Replies (N)` section header (subtree count) on the
  existing Conversation section from [00](00_design.md).
- Define what happens when a reed with live replies is itself removed and
  someone navigates to its permalink directly (today this 404s — see "Open
  questions").

## Non-goals

- Changing the direct-reply list body from [00](00_design.md)/[03](03_spa_reed_detail.md)
  — both counters here are aggregate **numbers**; the list underneath the
  `Replies (N)` header still shows direct children only, one level at a
  time.
- Cross-instance thread resolution before federation ships (same limitation
  as `echoing`/`replying` today).
- Live-updating the subtree count (`REED_REPLIES` beyond the thread total)
  in v1 — see "Open questions."

## Design

### Two numbers, two places

| | Thread total | Subtree count |
|---|---|---|
| Answers | "How big is this whole conversation?" | "How many replies stem from *this* reed?" |
| Same value across a thread? | Yes — every reed in the thread shows the same number | No — distinct per reed; a leaf reply shows a different (smaller, often 0) number than its parent |
| Displayed | Stats line, next to echo count / coverage (nerds corner) | `Replies (N)` section header, under the reed body (main content) |
| Table | `reed_threads`, keyed by thread root | `reed_reply_counts`, keyed by the reed itself |
| Maintenance cost | O(1) per write (single row bump/decrement) | O(depth) per write (walk + bump/decrement every ancestor) |

Earlier drafts of this step tried to serve both needs from the single
thread-total number (either showing it everywhere, or a combined `subtree /
total` display). Both were dropped: showing the same total on every reed in
a thread doesn't tell a reader anything about the specific reed they're
looking at, and combining the two numbers into one UI element muddles two
different questions. They're now fully independent — the thread total
**only** appears in the stats line; it does not appear in or influence the
`Replies (N)` section at all.

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
    reed's `threadId` = `P`'s own `thread_id` from its `reed_replies` row
    (inherit — load root from `reed_threads`).
  - Otherwise `P` is the root: the new reed's `threadId` = ref to `P` (first
    reply creates the `reed_threads` row).
- Producers still set the header via `formatReedRef` (never a bare id), and
  it's rebuilt server-side the same way `echoing`/`replying` are rebuilt in
  `ReedAsMarkdown` for signature verification ([01](01_publish_and_refs.md)).
- Used **only** for the thread total below — the subtree count doesn't need
  it (it walks `reed_replies.parent_*` directly).

### `reed_threads` schema (thread total)

Implemented in [02](02_index_and_api.md) — one row per thread, keyed by
`id` (the root reed ref). `reply_count` defaults to 1 on create and is
incremented on subsequent replies; decremented on removal (this step).

**Same-TX bump on publish** — inside the `SignReed` transaction, after
inserting the `reed_replies` row ([02](02_index_and_api.md#insert-on-publish)):

```sql
INSERT INTO reed_threads (id, root_user_id, root_reed_id, reply_count)
VALUES ($1, $2, $3, 1)
ON CONFLICT (id)
DO UPDATE SET reply_count = reed_threads.reply_count + 1;
```

On first reply to a root reed, insert `reed_threads` with `reply_count = 1`
(default). On subsequent replies to the same thread, increment `reply_count`
after the new `reed_replies` row lands (gated on insert success).

**Removal — decrement by exactly one:** when a reed is removed
([`Handlers.DeleteReed`](../../handlers.go), which today calls
`DeleteEchoIndexForReed` for echoes):

1. Look up `reed_replies` by `(user_id, reed_id)` = the removed
   reed. If no row exists, the removed reed wasn't a reply — nothing to do
   here (this also covers removing a thread root: a root has no
   `reed_replies` row for itself, so the thread total is untouched).
2. If a row exists, decrement `reed_threads.reply_count` by 1 for that row's
   `thread_id` (FK to `reed_threads.id`), floored at 0, in the same
   transaction as the `reed_removals` insert.
3. The `reed_replies` row itself is **not deleted** — needed to render the
   reply as a tombstone and to keep resolving `threadId`/ancestor chains for
   later replies. Removing a reed does **not** cascade to rows that point at
   it: its own descendants keep counting toward the thread total.

Account removal cascades the same decrement across every reply that account
authored, mirroring `DeleteEchoesByAuthor`'s enumerate-then-clean shape
([`services.go:1394-1423`](../../services.go)): for each `reed_replies` row
where `user_id` = the removed account, decrement its thread's counter
by 1 (same "don't delete the row, just decrement" rule as above).

### `reed_reply_counts` schema (per-node subtree count)

```sql
CREATE TABLE IF NOT EXISTS reed_reply_counts (
    user_id       VARCHAR(255) NOT NULL,
    reed_id       VARCHAR(255) NOT NULL,
    subtree_count INT NOT NULL DEFAULT 0,
    PRIMARY KEY (user_id, reed_id)
);
```

One row per reed that has at least one live descendant (direct or nested).
No FK to `reeds` — a reed can be removed and keep a nonzero `subtree_count`
(its still-live descendants). `subtree_count` for reed `X` = count of every
live, non-removed reply reachable by following `reed_replies.parent_* → X`
transitively, **excluding** removed reeds from the count but **not** from
the walk (a removed intermediate reed doesn't sever the chain — see
"Removal" below).

**Ancestor-chain bump on publish:** after inserting the new reply's
`reed_replies` row (parent `P`), walk `P`'s own ancestor chain by following
`parent_user_id`/`parent_reed_id` one hop at a time (each hop is itself a
`reed_replies` row) until reaching a reed with no parent row (the thread
root). For **every** reed visited on that walk (`P`, `P`'s parent,
grandparent, … up to and including the root), upsert-and-increment:

```sql
INSERT INTO reed_reply_counts (user_id, reed_id, subtree_count)
VALUES ($1, $2, 1)
ON CONFLICT (user_id, reed_id)
DO UPDATE SET subtree_count = reed_reply_counts.subtree_count + 1;
```

This is the direct cost of true per-node counts: **O(depth)** writes per
publish instead of the thread total's O(1) — see "Open questions" for
whether/how to bound this.

**Ancestor-chain decrement on removal:** when a reed `R` is removed, walk
`R`'s own ancestor chain the same way (starting from `R`'s parent, **not**
including `R` itself — `R`'s own `subtree_count` row, if any, describes
*its* descendants and is untouched by `R`'s own removal) and decrement each
visited ancestor's `subtree_count` by 1, floored at 0, in the same
transaction as the `reed_removals` insert. This mirrors the thread-total
decrement rule, just applied to every ancestor instead of only the root:
removing `R` costs each ancestor exactly one unit (for `R` itself); `R`'s
own descendants are unaffected and keep counting for those same ancestors,
since they were bumped independently when *they* were created.

Account removal cascades the same ancestor-chain decrement across every
reply that account authored — for a heavy poster with many deep replies
across many threads, this means one ancestor-chain walk+decrement per
authored reply, not just one operation total (see "Open questions" on
whether this should be synchronous).

### Removed reeds don't sever the chain

Because `reed_replies` rows are never deleted, a removed reed `R`'s
children keep a valid `parent_user_id`/`parent_reed_id` pointing at `R`
even though `R` itself is gone. This means:

- Walks (ancestor chain for bump/decrement, or a future descendant walk for
  listing) pass straight through a removed reed — it just doesn't itself
  contribute to any count.
- `R`'s own `subtree_count` row (if `R` has replies) stays accurate and
  queryable — visiting `R`'s own permalink can still show `Replies (N)` for
  whatever live descendants remain, even though `R`'s own body renders as a
  tombstone. This is the mechanism behind "display the tombstoned reed
  alongside all of its replies" — but see the next section for a real gap
  this exposes in the current SPA.

### Viewing a removed reed and its replies (SPA gap)

Today, [`+page.svelte`](../../spa/src/routes/reed/[userID]/[reedID]/+page.svelte)'s
`loadReedFromNetwork` treats a `gone` result from `getReedOrRemoval`
identically to a `not_found` one: it sets `reedNotFound = true`, which
renders a generic "Reed not found... this reed doesn't exist or has been
deleted" state ([`+page.svelte:325-331`](../../spa/src/routes/reed/[userID]/[reedID]/+page.svelte))
with **no** reed body, no stats, and no `Replies` section. That's fine for
"never existed," but it means a reader who follows a link into a removed
reed (e.g. tapping the "replying-to" quote on a still-live child) currently
hits a dead end even if that removed reed has plenty of live replies
underneath it.

Making "display the tombstoned reed alongside all of its replies" work
requires the detail page to distinguish these two cases and, for the
`removed` one, render a tombstone stub (author, "this reed was removed",
removal reason if available — reusing the existing removal-cert data
already fetched via `verifyAndCommitReedRemoval`) **plus** the `Replies (N)`
section for that reed, instead of the current blanket "not found" state.
This is a real, currently-missing piece of scope — see "Open questions."

### WS live stats — thread total only (mirrors echoes)

Extends the pattern in [`specs/coverage/02_reed_subscription.md`](../coverage/02_reed_subscription.md).
This section covers **only** the thread total; the subtree count's
live-update story is an open question below.

- `SUBSCRIBE_REED {userID, reedID}` — before building the snapshot, resolve
  the *viewed* reed to its thread root: look up `reed_replies` by
  `(user_id, reed_id)`; if found, use its `thread_id` to read
  `reed_threads.reply_count` for that thread id — `REED_STATS` gains a
  `replies` field alongside `echoes` and `coveragePercent`.
- Reed-subscriber registration for the thread-total stat is keyed by the
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

Two independent surfaces:

1. **Stats line (thread total)** — same pattern as `echoCount`/`coveragePercent`
   in [`+page.svelte`](../../spa/src/routes/reed/[userID]/[reedID]/+page.svelte):
   a `replyCount` var seeded from the `REED_STATS` snapshot, updated live by
   a new `handleReedReplies` handler on `REED_REPLIES`, cached locally the
   same way `echoCountsRepository` caches `echoCount`. Displayed as a third
   icon in the `reed-stats` line, reusing the already-present but currently
   unused [`reply-16.png`](../../spa/static/icons/reply-16.png) icon with
   the same `mask-image` + `currentColor` styling as
   `.reed-stat-icon.echoes`/`.coverage`.
2. **`Replies (N)` section (subtree count)** — the existing Conversation
   section from [00](00_design.md), retitled: header becomes `Replies (N)`
   where `N` = this reed's `subtree_count` (omit the `(N)` / hide the whole
   section when `N` is 0 or absent, same "only if there are replies" gating
   as today). The list body underneath is unchanged — still direct replies
   only, oldest first, drill-down navigation. `N` comes from a fetched
   `reed_reply_counts` row (extend `GET /reeds/{userID}/{reedID}` or the
   replies-list endpoint from [02](02_index_and_api.md), whichever ends up
   cheaper) — see "Open questions" on whether this needs a live-update path
   too.

## Work items

1. DDL: `reed_reply_counts`; `reed_threads`/`reed_replies` per
   [02](02_index_and_api.md#schema).
2. `threadId` header: parse/validate/rebuild in `ReedAsMarkdown` alongside
   `echoing`/`replying`; resolve via single parent lookup at publish time.
3. Bump `reed_threads` (O(1)) and walk-and-bump `reed_reply_counts` (O(depth))
   in the `SignReed` transaction, both gated on the `reed_replies` insert
   actually affecting a row.
4. Decrement `reed_threads` (single lookup) and walk-and-decrement
   `reed_reply_counts` (ancestor chain) on single-reed removal (`DeleteReed`
   handler) and account removal, mirroring the echo cleanup call sites.
5. `ReplyCountChanged` broadcast type; `notifyReedReplies`; extend
   `GetReedStatsSnapshot`/`REED_STATS`; new `REED_REPLIES` message — thread
   total only.
6. Thread-root resolution on `SUBSCRIBE_REED`; thread-keyed subscriber
   registration for the thread-total stat only.
7. Expose `reed_reply_counts.subtree_count` for a given reed (new field on
   an existing endpoint, or a new one — TBD, see "Open questions").
8. SPA: `replyCount` state + `REED_REPLIES` handler + stat icon
   (`reply-16.png`) for the thread total; retitle the Conversation section
   to `Replies (N)` using the subtree count; handle removed-reed permalinks
   as a tombstone stub + `Replies` section instead of blanket "not found."
9. Tests:
   - Reply to a root → thread total = 1, root's subtree_count = 1.
   - Reply to a reply (3 levels) → thread total = 3; each ancestor's
     subtree_count includes the new leaf (root = 3, mid = 2, leaf's own
     parent = 1); the leaf itself has no subtree_count row (0 replies).
   - Idempotent republish does not double-count either counter.
   - Remove a leaf reply → thread total -1; every ancestor's subtree_count
     -1; no other rows affected.
   - Remove a mid-thread reply → thread total -1; every ancestor above it
     (not below) -1 by exactly one; replies further down keep counting
     toward all of *their* ancestors, including ones above the removed node.
   - Remove the thread root → thread total unchanged; root's own
     subtree_count (its descendants) unchanged and still queryable.
   - Account removal → decrements once per reply that account authored,
     across however many threads/ancestor chains they touched.
   - Visiting a removed reed's permalink directly still returns its
     `subtree_count` / replies list for still-live descendants.

## Risks

- **O(depth) writes, twice** — both publish (bump) and removal (decrement)
  walk the full ancestor chain for `reed_reply_counts`, on top of the O(1)
  thread-total maintenance. Acceptable for expected reply depths, but a
  pathological very-deep thread makes both publish and removal
  proportionally slower. No depth cap is proposed yet — see "Open questions."
- **Two counters can drift** — a bug in one write path (e.g. an early
  return after bumping `reed_threads` but before walking
  `reed_reply_counts`) leaves the two numbers inconsistent with each other.
  They're independent by design, but should be updated in the same
  transaction to avoid partial-write drift.
- **SPA removed-reed rendering is a real gap, not just documentation** —
  today's `reedNotFound` handling actively prevents the "tombstone + live
  replies" UX from working at all; this needs actual product/engineering
  work, not just a data-layer change.
- **Ancestor-chain resolution cost** — repeated single-hop `reed_replies`
  lookups per write (both bump and decrement) mean N sequential queries for
  a depth-N chain unless resolved via a bulk/recursive query — see "Open
  questions."

## Open questions

- **Removed-reed permalink rendering.** Should visiting a removed reed's own
  URL render a tombstone stub + `Replies (N)` section (needed for the
  requested "browse into a tombstoned parent" UX), replacing today's
  blanket "Reed not found" treatment? If so, does `getReedOrRemoval`'s
  `gone` result need to carry enough to still fetch/display the replies
  list and subtree count for that (now-removed) `(userID, reedID)`, since
  today the SPA treats `gone` as terminal?
- **Live updates for the subtree count.** The thread total gets a live WS
  path (`REED_REPLIES`); should `Replies (N)`'s subtree count also update
  live while a reed detail page is open, or is a snapshot on load/resubscribe
  acceptable for v1? Doing it live means, on every publish, notifying every
  ancestor's subscribers individually (the exact ancestor-fan-out cost this
  design otherwise avoids for the thread total) — worth deciding explicitly
  rather than defaulting into it.
- **Ancestor walk implementation.** Repeated single-hop `SELECT`s up the
  chain (simple, N round trips for depth N) vs. a single recursive CTE
  (`WITH RECURSIVE`) to fetch the whole ancestor list in one query vs.
  materializing a closure table (`reed_reply_ancestors(descendant, ancestor,
  depth)`, one row per ancestor per reply, populated at publish time) to
  make removal-time decrement a single bulk `UPDATE ... WHERE ancestor IN
  (...)` instead of N sequential updates. Trade-off is write complexity now
  vs. write cost later; no decision made yet.
- **Depth cap.** Is there a reasonable maximum thread depth worth enforcing
  to bound the O(depth) cost, or is unbounded depth acceptable given
  expected usage patterns?
- **All-descendants-removed edge case.** If every reply under a reed is
  itself removed, `subtree_count` naturally decrements to 0 and the
  `Replies` section disappears (per the "only if there are replies" rule).
  Is that the right call, or should a reed with only-tombstoned descendants
  still show a `Replies` section explaining "all replies were removed,"
  which would mean gating section visibility on something other than a
  plain `subtree_count > 0` check?
- **Account-removal cascade cost.** For an account with many deep replies
  across many different threads, removal now triggers one ancestor-chain
  walk+decrement per authored reply (on top of the existing echo cleanup).
  Should this run synchronously inside the account-removal request, or be
  deferred/batched given it could touch a large number of rows for a prolific
  account?
- **Where does `subtree_count` get served from?** A new field on
  `GET /reeds/{userID}/{reedID}`, folded into the
  `GET /reeds/{userID}/{reedID}/replies` response from [02](02_index_and_api.md),
  or a dedicated endpoint? Not decided.

## Parallelism

Independent of [03](03_spa_reed_detail.md)/[04](04_mentions.md) for the
data-layer/counter work, but the SPA removed-reed rendering change and the
`Replies (N)` retitling touch the same reed-detail template as
[03](03_spa_reed_detail.md) — sequence with or after it rather than in
parallel. Can land any time after [02](02_index_and_api.md)'s schema exists.
