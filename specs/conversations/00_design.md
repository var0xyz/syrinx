# Conversations 00 — Design + UX model

## Status

Implemented (design locked; reflected in 01+).

## Depends on

—

## Context

Today a reed may carry optional social headers in its signed markdown:

| Header | Meaning | Current client shape |
|--------|---------|----------------------|
| `echoing` | Repost of another reed, with optional commentary | `userID@serverID/reedID` |
| `replying` | Response to another reed | `userID@serverID/reedID` |

The SPA already renders quotes for both ([`Quote.svelte`](../../spa/src/lib/components/Quote.svelte))
and exposes Echo / Reply actions on the reed detail page. Nothing aggregates
echoes or lists replies.

The server `reeds` table stores only `(id, user_id, signed_at, …)` — no
social graph. `POST /reeds` accepts a detached user signature but not the
markdown it signs, so the server cannot verify the author attestation or
extract headers at publish time.

## Scope

- Lock the product model for echo counts and threaded reply browsing.
- Lock the **one-level-at-a-time** conversation UX.
- Document non-goals and deletion behaviour.

## Non-goals

- Ephemeral comments ([planned](../../docs/planned.md)) — different feature.
- Full recursive thread tree on one screen (Reddit-style collapse tree).
- Server storage of reed bodies for replies.
- Cross-instance reply discovery before federation ships.
- Likes / reaction counts (separate [planned](../../docs/planned.md) item).
- Notifications ("X replied to your reed") — may reuse
  [proposal 11](../11_user_notifications.md) later; out of scope here.

## Design

### Terms

- **Target reed** — the reed being echoed or replied to.
- **Echo** — a new reed whose `echoing` header points at a target.
- **Reply** — a new reed whose `replying` header points at a target.
- **Direct reply** — a reply whose `replying` ref resolves to the reed
  currently being viewed (not a reply-to-a-reply of an ancestor).
- **Thread** — the full chain of replies-of-replies rooted at one reed,
  identified by that root's ref (`threadId`); see
  [05](05_thread_reply_counts.md).
  - **Thread total** — count of every reply in a thread; the same number
    regardless of which reed in the thread you're viewing.
  - **Subtree count** — count of every reply descending from a *specific*
    reed; different per reed, and always ≤ the thread total.

### Echo count

On the reed detail page, the Echo action shows how many **non-removed** echo
reeds point at this reed:

```
echoing: <targetAuthorId>!<targetReedId>
```

Count is server-authoritative (indexed at publish). Display examples:

- `Echo` when count is 0
- `Echo · 3` when count is 3

Tapping Echo still opens the compose modal (unchanged). The count is
informational, not a separate list in v1. (A future step may add an echo
list drawer; not required for this proposal set.)

### Reply count — two numbers, two places

Unlike the echo count, replies get **two** separate numbers, because "how
big is this conversation" and "how many replies come from this reed" are
different questions:

- **Thread total** — shown in the stats line next to echo count / coverage.
  Same number on every reed in the thread (root or nested reply), because
  it describes the whole conversation, not the specific reed being viewed.
- **Subtree count** — shown as `Replies (N)` in the section below the reed
  body (see "Conversation section" below). Specific to the reed being
  viewed: how many replies (including replies-to-replies) descend from
  *this* reed, not the whole thread.

Both count replies-to-replies transitively — that's what makes them
"recursive" — but they answer different scopes, and neither is derived from
the other. See [05](05_thread_reply_counts.md) for the `threadId` header,
the `reed_threads` identity row, graph queries over `reed_replies`, and the live-update
wiring.

### Conversation section

Below the action bar on the reed detail page, add a **Replies** section.

**Initial view (on reed R):**

1. Section title: `Replies (N)` where `N` is `R`'s **subtree count** (all
   descendants, not just direct children — see [05](05_thread_reply_counts.md)).
   Hide the whole section when `N` is 0 (or absent).
2. List **direct replies** to `R` only — reeds where
   `replying === R.userID@R.serverID/R.id` (normalized format from
   [01](01_publish_and_refs.md)). The list body is unaffected by the
   subtree-count change above: still direct children only.
3. Sort **oldest first** (`signed_at ASC`) so reading top-to-bottom matches
   chronological dialogue.
4. Each row is a compact preview card: author avatar + username, relative
   time, content preview (reuse `Quote` / feed card styling). Tapping a row
   navigates to `/reed/{replyAuthorId}/{replyReedId}`.

**Drill-down (on reply reed Q):**

The user opened `Q` from the conversation list. The page shows:

1. The existing **replying-to quote** at the top of the body (already
   implemented) — context for what `Q` is answering. If the parent has been
   removed, this renders as a tombstone rather than a live quote — but see
   [05](05_thread_reply_counts.md) for what happens if the reader navigates
   *into* that removed parent instead of just seeing its quote.
2. A **Replies** section listing direct replies to `Q` (same rules as
   above), titled with `Q`'s own subtree count.

There is no infinite inline nesting. Depth is explored by **navigation** —
each reed page is responsible for its own children. This matches "only direct
replies; click one to see responses to that."

**Empty state:** `No replies yet` with subtle hint that Reply is available in
the action bar.

**Loading:** Skeleton rows while metadata is fetched; per-row "Fetching…"
while relay retrieves a body not held locally.

### Reference format (locked)

Both `echoing` and `replying` use:

```
<userID>@<serverID>/<reedID>
```

Parsed by `ParseReedRef` / `parseReedRef`. On this instance, `serverID` must
match the local server id when validating publish targets.

Producers set both fields via `formatReedRef` (never bare reed ids).

### Trust and content

- Reply list API returns **metadata only** (`authorId`, `reedId`, `signedAt`,
  optional `username` from server cache).
- Bodies are fetched via the existing relay path; every stored reed still
  passes [`verifyReed`](../../spa/src/lib/verifiers/index.ts) before IndexedDB
  write.
- A reply whose user signature fails verification is never shown (relay
  miss / invalid → row stays in loading or is dropped with a warning log).

### Deletion

| Event | Behaviour |
|-------|-----------|
| Target reed removed | Quote shows "Original reed unavailable"; conversation may still list replies that pointed at it. Navigating *into* the removed reed's own permalink is a separate, currently-unresolved case — see [05](05_thread_reply_counts.md#viewing-a-removed-reed-and-its-replies-spa-gap) |
| Reply reed removed | Row omitted from conversation list and echo count; its own `Replies` section (if it had live descendants) is still reachable by navigating into it directly, pending [05](05_thread_reply_counts.md)'s open question on removed-reed rendering |
| Reply author account removed | Row omitted (account tombstone); existing 410 handling applies if user navigates directly (same open question as above) |

Index rows for removed reeds are **not** deleted — queries filter them out via
`NOT EXISTS` on `reed_removals` / `account_removals`. Orphan index rows are
acceptable; no GC in v1.

### Realtime (v1)

The **direct reply list** has no new WebSocket event type. When `FOLLOW_REED`
arrives and the open reed detail page is the parent, re-check the reply list
(debounced) or append if the incoming reed's `replying` ref matches.

The **thread total** ([05](05_thread_reply_counts.md)) is the exception: it
gets a dedicated live path, `REED_STATS`/`REED_REPLIES`, mirroring the
existing echo-count WS pattern — a reply anywhere in a thread pushes an
updated total to every currently-subscribed viewer of that thread, not just
the reed being replied to. Whether the **subtree count** (`Replies (N)`)
also needs a live path, or a snapshot on load/resubscribe is enough for v1,
is an open question in [05](05_thread_reply_counts.md).

A dedicated `new_reply` event (parent ref in payload) for the direct list is
a future optimization if list polling proves noisy.

## UX sketch

```
┌─────────────────────────────────────┐
│ @alice · 2h ago                     │
│ [replying-to quote if any]          │
│ Main reed body…                     │
│ [echo quote if any]                 │
├─────────────────────────────────────┤
│ 📢 Echo · 2    ↩ Reply    🔗 Share  │
├─────────────────────────────────────┤
│ Replies (2)                         │
│ ┌─────────────────────────────────┐ │
│ │ @bob · 1h ago                   │ │
│ │ Sounds good, let's do it.       │ │
│ └─────────────────────────────────┘ │
│ ┌─────────────────────────────────┐ │
│ │ @carol · 45m ago                │ │
│ │ +1 — I'll bring snacks.         │ │
│ └─────────────────────────────────┘ │
└─────────────────────────────────────┘
```

Tap Carol's row → navigate to Carol's reply reed → its Replies section
shows direct replies to *that* reed only, titled with *its own* subtree
count (not the thread total from the sketch above).

## Risks

- **Publish API change** — clients send form fields (`content`, optional
  `echoing` / `replying`) on `POST /reeds`; server rebuilds canonical markdown
  for signature verification. Coordinate SPA + server deploy (pre-launch
  acceptable).
- **Incomplete local cache** — conversation list may show rows whose bodies
  are not yet relayed; UI must tolerate loading / missing content gracefully.
- **`replying` format change** — existing dev reeds with bare `reedId` will
  not match indexes; dev DBs recreate or republish.

## Dependencies

None. Complements [deletion](../deletion/README.md) (filter removed rows) and
future federation routing.
