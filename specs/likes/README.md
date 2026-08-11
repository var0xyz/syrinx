# Reed likes

This directory is the **reed likes** feature proposal set. Numbered files
below are independently reviewable implementation steps. Land them in order
unless a step's "Depends on" says otherwise.

**Code organization (suggested):** canonical payload builders (like only —
unlike has none) live in `syrinx/identity` alongside the existing
`Build*Payload` helpers (`BuildReedLikeUserPayload` /
`BuildReedLikeServerPayload`). Server-side store/handler code can be
colocated with reed handlers or a thin `syrinx/likes` package if it
grows. Main wires DDL, routes, and realtime fanout. SPA owns two outbox
stores — `pendingLikes` (signed) and `pendingUnlike` (unsigned) — plus a
local `likedReeds` confirmed-state cache, following the same offline-
first shape as `pendingRemoval` / `removedReeds`
([deletion 05](../deletion/05_spa_reed_author.md)).

| #                                   | Title                                            | Depends on |
|-------------------------------------|---------------------------------------------------|------------|
| [00](00_design.md)                  | Design + locked model                              | —          |
| [01](01_schema.md)                  | `reeds_liked` schema + denormalized like count     | 00         |
| [02](02_payload.md)                 | Like canonical payload + countersign               | 00         |
| [03](03_api.md)                     | Like (signed) / unlike (unsigned) API (idempotent) | 01, 02     |
| [04](04_realtime.md)                | `REED_LIKES` subscribe snapshot + live updates     | 03         |
| [05](05_spa_pending_and_button.md)  | SPA `pendingLikes`/`pendingUnlike` + like button   | 03, 04     |
| [06](06_spa_liked_feed.md)          | SPA "Liked reeds" list (profile entry point)       | 05         |

## Status

**Proposed.** Nothing in this directory is implemented yet — this is a spec
only; no code changes accompany it.

## Motivation

Reed detail already surfaces echo count, reply count, and coverage percent
in a small stats bar ([coverage](../coverage/README.md),
[conversations](../conversations/README.md)). A "like" is a fourth signal:
a lightweight, one-tap acknowledgement, distinct from echo (rebroadcast)
and reply (conversation). Unlike echo/reply, a like has no content of its
own — it is purely a signed attestation "user X liked reed Y", so it is
the simplest signed resource in the app, and a good vehicle for exercising
the offline-first pending-queue pattern end to end once more.

Users also want to find reeds they've liked again later, the same way they
can already see who they follow — see [06](06_spa_liked_feed.md).

## Protocol sketch

1. Client creates a `pendingLikes` row locally: `serverID`, `authorID`,
   `reedID`, own `userSignature` (offline-first; survives app close).
2. `POST` the like to the server (body carries the user signature).
3. Server verifies, **countersigns once** (idempotent — liking twice
   returns the same stored cert, does not double-count), stores the cert,
   bumps the denormalized `reeds.like_count`, returns **200** with a
   `server` block.
4. Client stores the confirmed like in a local `likedReeds` cache, flips
   its **own** button to filled, then deletes the `pendingLikes` row.
5. Server fans out a live count update through the existing per-reed WS
   subscription channel ([04](04_realtime.md)), the same class of channel
   used for `REED_ECHOES` / `REED_COVERAGE`
   ([coverage 02](../coverage/02_reed_subscription.md)).
6. Other viewers of that reed detail page see the like count update live.

**Unliking is deliberately simpler, not a mirror of liking.** It is a
plain authenticated `DELETE` with **no signature** — the server hard-
deletes the `reeds_liked` row and decrements `like_count`, no cert
produced or stored. The client still queues it durably first (its own
`pendingUnlike` outbox entry, separate from `pendingLikes`) for the same
offline-first reason as everything else here, but there is no signing
step in that path at all. See [00 § Unlike is unsigned](00_design.md#unlike-is-unsigned),
[02](02_payload.md), and [03](03_api.md).

## Resolved

1. **Offline-first**, mirroring reed removal: durable pending row first,
   then submit, then reconcile, matching
   [deletion's README protocol sketch](../deletion/README.md#protocol-sketch-reeds).
2. **Liking is signed, server-countersigned once**, using the shared
   `user_signatures` / `server_signatures` FK model
   ([signatures 00](../signatures/00_design.md)) — no inline signature
   columns on `reeds_liked` (that pattern is deprecated; see
   [signatures README](../signatures/README.md)). **Unliking is unsigned**
   — a plain authenticated `DELETE`, hard row deletion, no cert. See
   [00 § Unlike is unsigned](00_design.md#unlike-is-unsigned).
3. **Counted, not just boolean-displayed**: `reeds.like_count` is a
   denormalized counter bumped in the same TX as insert/delete, exactly
   like [coverage 01](../coverage/01_counts.md)'s `allocation_count`. No
   `COUNT(*)` on read.
4. **v1 scope note (from the request):** for now we count *all* likes
   including a user liking their own reed — no self-like guard. Revisit
   if abuse shows up.
5. **Live updates** via the existing per-reed subscribe channel
   ([coverage 02](../coverage/02_reed_subscription.md)), extended with a
   fourth field/event rather than a new subscription type.
6. **Stats bar + modal**: like count joins echoes/replies/coverage in the
   reed-detail stats bar and its explainer modal
   (`ReedStatsInfoModal.svelte`).
7. **Liked-reeds feed**: a new SPA entry point (profile page) lists the
   current user's liked reeds, newest-liked first. See
   [06](06_spa_liked_feed.md).

## Non-goals

- Likes on echoes/replies-as-such beyond what already falls out of them
  being reeds (a reply *is* a reed and can be liked like any other; no
  special-casing needed).
- Notifying the author "X liked your reed" (belongs to the
  [notifications](../notifications/README.md) track if ever built).
- Public "who liked this" list (v1 shows a count only, not identities —
  consistent with echo/coverage today). May reuse
  `FollowListModal`-style UI later if requested.
- Undo/redo history of like state; only current state matters.

## Open questions

1. Exact route/entry-point for the liked-reeds feed
   ([06](06_spa_liked_feed.md) proposes `/profile` → "Liked" link, page at
   `/profile/liked`) — implementation may adjust based on nav real estate.
2. Whether self-likes should be excluded from the count later (see
   Resolved #4 — deliberately deferred, not decided against).
