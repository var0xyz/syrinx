# Pagination 05 — `ReedsList` (profile feed) client-side cursor

## Status

Proposed.

## Depends on

[00](00_audit.md) (convention), [01](01_deduplication.md) (shared frontend store, as a state-shape reference only — see below)

## Context

[00](00_audit.md) flagged `spa/src/lib/components/ReedsList.svelte` (an
author's own reed list, shown on their profile) as **the highest-risk
unbounded list in the app**: `getReedsByAuthor()`
(`spa/src/lib/repositories/reeds.ts:278-286`) calls
`dbService.getAllByIndex('reeds', 'userID', authorId)` with no limit at
all, and the component renders the full result with no windowing. Unlike
the follow/broadcast feeds (which cap at `FOLLOW_FEED_LIMIT`/
`BROADCAST_LIMIT = 50` via a session-storage ref list), this list has no
cap of any kind — a prolific author's profile has unbounded DOM nodes.

This is structurally different from every other pagination gap in this
track: there is no `GET /reeds` REST endpoint (see [00](00_audit.md) —
reeds are delivered exclusively via the websocket relay, never listed
over REST), and reeds live in IndexedDB on-device. So this is a
**client-side IndexedDB windowing problem**, not a server round-trip —
the shared backend helpers from [01](01_deduplication.md) don't apply,
and the shared frontend store's `fetchPage`/`getItems` shape (built
around an HTTP call) doesn't fit as-is either, though its `rows`/
`hasMore`/`cursor` **state shape** is still the right one to mirror for
consistency.

## Scope

- Add a cursor-capable read path to `spa/src/lib/services/db.ts`.
  `getLatestFromIndex` (`db.ts:241-267`) already takes a `limit` and a
  filter predicate but always starts its `openCursor(null, 'prev')` from
  the end — it needs an optional "continue after this key" parameter so
  a second page can resume where the first left off, instead of
  re-scanning from the top.
- Wire `ReedsList.svelte` to page through this new cursor read instead of
  `getAllByIndex`, capped to a limit (e.g. matching `FOLLOW_FEED_LIMIT`'s
  50, or its own constant).
- Scroll behavior: manual **"Load more"** button, per the [00](00_audit.md)
  content-list rule (reeds are the definitional "content-bearing" case).
- Preserve the existing scrolled-down-reader UX seam: `ReedsList`
  already distinguishes "reload the list" (when the viewer is scrolled to
  top) from "show a new-reeds banner" (when scrolled away,
  `showNewReedBanner`) in reaction to live websocket arrivals
  (`profileReedQueue`/`followReedQueue`, `ReedsList.svelte:94-104`).
  Pagination must not regress this — a live new reed arriving while the
  viewer has paged several screens down should still surface as a banner,
  not silently reflow or reset pagination.

## Non-goals

- `LikedReedsList.svelte` and the reed-by-tag view
  (`reeds.ts:302-334`, `getReedsByTag`) have the same unbounded-IndexedDB-read
  shape but are lower risk (bounded by realistic user behavior/quota) —
  not covered by this step. If `db.ts` gains a general cursor-read
  primitive here, reusing it for those is a natural, cheap follow-up but
  not required by this step.
- Any server-side `GET /reeds` endpoint — reed delivery stays
  websocket-relay-only, consistent with the project's client-side
  content-verification direction.
