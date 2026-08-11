# Likes 04 — `REED_LIKES` subscribe snapshot + live updates

## Status

Proposed.

## Depends on

[03](03_api.md)

## Context

Reed detail already carries a per-reed WS subscription
(`SUBSCRIBE_REED` / `UNSUBSCRIBE_REED`) delivering an initial `REED_STATS`
snapshot plus independent `REED_ECHOES` / `REED_COVERAGE` /
`REED_REPLIES` deltas ([coverage 02](../coverage/02_reed_subscription.md)).
Likes extend this same channel rather than opening a new one — same
audience (anyone who can view the reed detail page), same lifecycle
(subscribe on enter, unsubscribe on leave), same "count only, no
identities" privacy posture as coverage.

## Scope

- Add `likes` to the `REED_STATS` snapshot payload.
- New `REED_LIKES` delta event, likes-count-only, mirroring
  `REED_ECHOES`'s shape exactly.
- Server: `BroadcastType.LikeCountChanged` (Go internal enum, alongside
  existing `EchoCountChanged` / `ReplyCountChanged`), fired from the
  like/unlike handlers ([03](03_api.md)) onto the existing
  `broadcastChan`; a `notifyReedLikes` function in `realtime/service.go`
  mirroring `notifyReedEchoes`'s exact shape; a `CountLikes` helper in
  `realtime/db.go` mirroring `CountEchoes`.
- SPA: extend the existing `ReedStatsSubscription.svelte` /
  `ServerEvent` handling already used for echoes/replies/coverage.

## Non-goals

- New subscribe/unsubscribe verbs — reuses `SUBSCRIBE_REED`.
- A separate "who liked this" channel (no identities on the wire; see
  [00 non-goals](00_design.md#non-goals)).

## Design

### Wire (client → server)

No change. Existing:

```json
{ "type": "SUBSCRIBE_REED", "userID": "<authorID>", "reedID": "<reedID>" }
```

### Wire (server → client)

**Snapshot** — `REED_STATS` gains a fourth field:

```json
{
  "type": "REED_STATS",
  "userID": "<authorID>",
  "reedID": "<reedID>",
  "echoes": 3,
  "replies": 1,
  "coveragePercent": 12,
  "likes": 7
}
```

| Field | Source |
|-------|--------|
| `likes` | `CountLikes(ctx, authorUserID, reedID)` — `SELECT COUNT(*) FROM reeds_liked WHERE author_user_id=$1 AND reed_id=$2`, or read `reeds.like_count` directly (same denormalized-counter choice as `coveragePercent`'s `allocation_count` read — pick whichever the implementation of `GetReedStatsSnapshot` already prefers for the other three fields, for consistency) |

**Like-count update** — only when the like count for this reed changes:

```json
{
  "type": "REED_LIKES",
  "userID": "<authorID>",
  "reedID": "<reedID>",
  "likes": 8
}
```

No other fields on this message — mirrors `REED_ECHOES` carrying only
`echoes`. Sent on **both** directions of change: a like fires it with the
incremented count, an unlike fires it with the decremented count — the
event is "the count changed," not "someone liked." Every current
subscriber receives whichever number is now correct and updates their
displayed **count** with it — nothing else.

`REED_LIKES` never carries `likerID` and never implies anything about
*whose* like button should change. The like button's filled/outlined
state (`isLiked`) is **not** derived from this broadcast at all — it is
a purely local fact about the signed-in user's own `likedReeds` cache
(see [05](05_spa_pending_and_button.md#reed-detail-like-button)). A
viewer who is not the liker never sees the button change; only the
count they're looking at changes. The liker's own device updates its
button locally, synchronously, from its own `pendingLikes`/`pendingUnlike`
flow — not from receiving this broadcast (they may not even be
subscribed to the reed in the tab that triggered the action, e.g. from
the liked-reeds feed — see [06](06_spa_liked_feed.md)).

### When to emit

| Trigger | Emit |
|---------|------|
| Successful `SUBSCRIBE_REED` | `REED_STATS` once, now including `likes` |
| Like inserted (first time for `(likerID, authorID, reedID)`) | `REED_LIKES` to subscribers of the **liked** reed |
| Like replay (idempotent, no row change) | nothing — no count change, no event |
| Unlike (row existed, deleted) | `REED_LIKES` |
| Unlike replay (nothing to unlike) | nothing |

### Server sketch

- `handlers.go` like/unlike handlers ([03](03_api.md)) push
  `realtime.BroadcastMessage{Type: realtime.LikeCountChanged, UserID: authorID, ReedID: reedID}`
  onto `broadcastChan` only on an actual count change (mirrors how
  `EchoCountChanged` is only fired when an echo row is actually
  inserted, not on every publish attempt).
- `handleBroadcasts` (`realtime/service.go`) gains a
  `LikeCountChanged` branch calling `rs.notifyReedLikes(userID, reedID)`.
- `notifyReedLikes` re-reads the count (`CountLikes` or `reeds.like_count`)
  and calls `rs.connManager.SendToReedSubscribers(authorUserID, reedID, ReedLikesMsg{...})`
  — identical shape to `notifyReedEchoes`.
- `ReedLikesMsg` added to `realtime/wire.go` alongside `ReedEchoesMsg` /
  `ReedCoverageMsg` / `ReedRepliesMsg`.
- `GetReedStatsSnapshot` (`realtime/db.go`) extended to also return
  `likes`, folded into the existing four-value snapshot tuple/struct.

### SPA

On reed detail for **published** reeds (unchanged subscribe lifecycle
from [coverage 02](../coverage/02_reed_subscription.md)):

1. `SUBSCRIBE_REED` with page author / reed id (already happens).
2. On `REED_STATS` → also set `likeCount` from the new field.
3. On `REED_LIKES` → update `likeCount` only.
4. Leave / destroy → `UNSUBSCRIBE_REED` (unchanged).
5. Pending unsigned reed: no subscribe, no stats line (unchanged) — a
   reed that hasn't been countersigned yet cannot be liked either (see
   [05](05_spa_pending_and_button.md), Like button hidden while pending).

`ServerEvent.ReedLikes = 'REED_LIKES'` added to
`spa/src/lib/services/serverConnection.ts`'s enum, string value matching
the Go wire `Type` literal exactly (existing convention — every other
`ServerEvent` entry is a 1:1 mirror of a Go wire-message `Type` string).

Handler shape mirrors the existing `handleReedEchoes`
(`+page.svelte`):

```js
function handleReedLikes(msg) {
  if (msg?.userID === userID && msg?.reedID === reedID) {
    if (typeof msg.likes === 'number') likeCount = msg.likes;
  }
}
```

Registered/unregistered in the same `onMount`/`onDestroy` block as
`handleReedStats` / `handleReedEchoes` / `handleReedReplies` /
`handleReedCoverage`.

### Tests / checklist

- Subscribe → exactly one `REED_STATS` with `likes` included alongside
  the existing three fields; no extra HTTP call required for the UI.
- Like of subscribed reed → `REED_LIKES` only (no other stats fields).
- Idempotent replay like/unlike → no event emitted, no count drift.
- Unlike → `REED_LIKES` with decremented count.
- Unsubscribe → no further like events for that reed.
- Two tabs viewing the same reed: liking from tab A updates the count
  live in tab B (own-like button state in B is a separate, local concern
  — see [05](05_spa_pending_and_button.md); the *count* still updates in
  both regardless of who's viewing).
