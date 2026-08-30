# 00 — Audit + locked convention

**Status: Implemented.** This is the audit that the locked convention in
[`README.md`](README.md) was derived from. Nothing to build here beyond
the audit itself; downstream steps act on its findings.

## Backend audit

Router: gorilla/mux, `main.go`. Handlers in `handlers.go`,
`federation_relay.go`, `services.go` (DB layer), plus `invites/handlers.go`
and `recovery/routes.go`.

Before [01](01_deduplication.md) landed, the `limit` parsing/clamping
boilerplate was copy-pasted five times (`handlers.go:2705, 2763, 2805,
2847, 5198`), and the keyset-fetch logic was copy-pasted four times in
`services.go` (`ListReplies`, `listFollowEdge`, `GetReedChorus`,
`ListRipples`) — see that step for the extraction.

| Resource | Endpoint | Paginated? | Mechanism |
|---|---|---|---|
| Reed chorus (echoers) | `GET /reeds/{u}/{r}/chorus` (`handlers.go:2677`) | Yes | `limit` + `before` (RFC3339) keyset, `hasMore` |
| Reed replies | `GET /reeds/{u}/{r}/replies` (`handlers.go:2735`) | Yes | `limit` + `before` (RFC3339) keyset, `hasMore` |
| Ripples | `QUERY /reeds/{u}/{r}/ripples` (`handlers.go:5164`) | Yes | `limit` + `before` (opaque cursor) keyset, `hasMore` + `nextCursor` |
| Following | `GET /users/{u}/following` (`handlers.go:2794`) | Yes | `limit` + `before` (RFC3339) keyset, `hasMore` |
| Followers | `GET /users/{u}/followers` (`handlers.go:2836`) | Yes | `limit` + `before` (RFC3339) keyset, `hasMore` |
| Reed feed / timeline | — | N/A | No such endpoint exists at all — reeds are delivered exclusively over the websocket relay, never listed via REST (see `docs/content.md`) |
| User search | `GET /users/search` (`handlers.go:836`) | **No** | Flat `limit` only (default 20, max 100), no cursor. Caller can never see past page one. Also fans out to peers and merges results, which complicates adding a cursor later. See [02](../pagination/README.md). |
| Federation invitations | `GET /federation/invitations` (`handlers.go:3744`) | **No** | Full result, `ORDER BY created_at DESC`, no LIMIT |
| Federation servers | `GET /federation/servers` (`handlers.go:3809`) | **No** | Full result, `ORDER BY created_at DESC`, no LIMIT |
| Federation attempts | via `GET /federation/list` (`handlers.go:4072`) | **No** | Full result, `ORDER BY created_at DESC`, no LIMIT |
| Federation server/attempt logs | `handlers.go:3887`, `4160` | **No** | Full result, `ORDER BY created_at ASC`, no LIMIT — unbounded growth risk for a long-lived peer connection. See [04](../pagination/README.md). |
| Federation relay sync (server-to-server) | `federation_relay.go`, ~25 routes | N/A | Per-event push/notify protocol, not a batch list API. No "sync everything since X" endpoint exists. |
| WS pending events on reconnect | `realtime/db.go:883, 919` | **No** | Unbounded queries returning a user's full backlog on reconnect; not exposed as a client-paginable API |

Notifications, mailbox, and blocked-users are not endpoints — those
features don't exist in this codebase.

**Gaps ranked by impact:**

1. **User search** — the most user-facing gap. No way to page past the
   first `limit` results.
2. **Federation admin lists/logs** — admin-only so lower urgency, but the
   log endpoints (`GetFederationServerLogs`, `GetFederationAttemptLogs`)
   are genuinely unbounded and could grow large for a long-lived peer.
3. Everything else already follows the working convention.

## Frontend audit

SvelteKit app under `spa/`. State is plain local component state
everywhere — no Redux/Zustand/global store holds any of these lists. The
only relevant global stores are one-shot websocket event channels
(`profileReedQueue`, `followReedQueue`, etc. in
`spa/src/lib/repositories/reeds.ts`) that components react to
imperatively; they never hold arrays themselves.

**Reeds have no REST list endpoint to paginate against.** The feed is
built entirely from IndexedDB + websocket push (`follow_reed`,
`broadcast_reed`, `pipe_reed`, `reed_reply` events landing in
`dispatchReedToQueue`, `spa/src/lib/repositories/reeds.ts:53-70`).
Pagination for reed lists is therefore a client-side IndexedDB windowing
problem, not a server round-trip — see [05](../pagination/README.md).

| List | Component | Fetch | Current behavior |
|---|---|---|---|
| Follow feed | `spa/src/routes/feed/follow/+page.svelte` | `getFollowReeds()` reads `sessionStorage`-backed ref list, capped at 50 (`FOLLOW_FEED_LIMIT`, `reeds.ts:369`) | Renders full (already-capped) array, no "load more" |
| Broadcast feed | `spa/src/routes/feed/broadcast/+page.svelte` | Same pattern, `BROADCAST_LIMIT = 50` | Renders full (already-capped) array |
| **Author's reed list (profile)** | `spa/src/lib/components/ReedsList.svelte` | `getReedsByAuthor()` → `dbService.getAllByIndex`, **no limit at all** | Renders full array, no windowing — **highest-risk unbounded list in the app**. See [05](../pagination/README.md). |
| Liked reeds | `spa/src/lib/components/LikedReedsList.svelte` | `likedReedsRepository.getAll()`, unbounded | Renders full array |
| Followers / Following | `spa/src/lib/components/FollowListModal.svelte` | `apiService.listFollowing`/`listFollowers` — **already paginated** | Manual "Load more" button, appends to `rows`. Adopted the shared `createPaginationStore` in [01](01_deduplication.md). |
| Replies (Conversation tab) | `spa/src/lib/components/ConversationSection.svelte` | Local cache read (unbounded, instant paint) then `apiService.listReplies` — **already paginated** | Manual "Load more", plus live WS append-to-tail/remove-in-place that must not collide with prepend-on-page. Left as a documented exception in [01](01_deduplication.md). |
| Chorus (echoers) | `spa/src/lib/components/ChorusSection.svelte` | `apiService.listEchoers` — **already paginated** | Manual "Load more"; WS event triggers full reload (resets pagination — only acceptable because the payload carries no per-item identity). Adopted the shared `createPaginationStore` in [01](01_deduplication.md). |
| Ripples | `spa/src/lib/components/RipplesSection.svelte` | `apiService.listRipples` — **already paginated**, explicit `nextCursor` | Manual "Load more"; most sophisticated live-merge logic in the app (`routeIncomingRipple`). Left as a documented exception in [01](01_deduplication.md). |
| Invites | `spa/src/routes/invites/+page.svelte` | `invitesRepository.getAll()`, unbounded | Renders full array — low risk, bounded by invite quota |
| Mesh (federation admin) | `spa/src/routes/mesh/+page.svelte` | `apiService.listFederation()`, no pagination params | Renders full arrays — admin-only, low risk |
| Mention/user search (typeahead) | `spa/src/lib/components/MentionPicker.svelte` | `apiService.searchUsers(query, 20)`, no cursor | Bounded by hardcoded limit 20 — fine as-is for a typeahead |

## Out of scope

- **Federation relay sync** (server-to-server) has no batch/pull API at
  all today — it's pure per-event push. Building a paginated "sync
  everything since X" protocol for peer catch-up is a separate,
  federation-sync design problem, not a REST pagination gap.
- **Notifications, mailbox, blocked users** — none of these features
  exist in the codebase; nothing to paginate.
