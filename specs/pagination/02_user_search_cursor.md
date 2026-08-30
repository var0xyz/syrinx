# Pagination 02 — User search cursor pagination

## Status

Implemented.

## Depends on

[00](00_audit.md) (convention), [01](01_deduplication.md) (shared helpers)

## Context

`GET /users/search` (`handlers.go:836`, `SearchUsers`) is the most
user-facing pagination gap found in [00](00_audit.md): it takes a flat
`limit` (default 20, max 100 enforced service-side in
`DataService.SearchUsers`, `services.go:2268-2273`) with no cursor of any
kind. A caller can never see results past the first page. The SQL
backing it (`services.go:2278-2289`) already has `ORDER BY u.username
ASC LIMIT $2` — the same shape chorus/replies/following/followers had
before they got a keyset predicate, so adding one here is mechanical.

The complicating factor is that local results are fanned out to peers and
merged: `fanoutUserSearchToPeers` (`handlers.go:859`) and
`mergeUserSearchResults` combine local + foreign results into one
response, reordering as they merge. That merge has no concept of a
cross-server "page 2" today.

## Scope

- Add `before` (opaque cursor, following the [00](00_audit.md) convention
  — likely `(username)` as the ordering key, or `(username, user_id)` if
  usernames aren't unique) to the **local** query only.
- Return `hasMore` alongside the existing result array.
- Federation fanout (`fanoutUserSearchToPeers`) and the merge step stay
  **single-page** — do not attempt cross-server cursor pagination in this
  step. A merged response's `hasMore` should reflect only the local
  page's `hasMore`; foreign results are not paginated at all until
  federation search gets its own design (out of scope here, tracked
  informally in [`federation/README.md`](../federation/README.md) if it
  becomes relevant there).
- Frontend: `MentionPicker.svelte`'s typeahead use of `searchUsers` stays
  as-is (hardcoded `limit: 20`, no "load more" — appropriate for a
  typeahead, not a target for this step). If a full "search results page"
  UI exists or is added elsewhere, it should adopt the shared
  `createPaginationStore` ([01](01_deduplication.md)).

## Non-goals

- Federated/cross-server merged pagination — the fanout+merge path stays
  single-page.
- Any change to `MentionPicker`'s typeahead behavior.

## Implementation

`DataService.SearchUsers` (`services.go`) now takes `(ctx, query, limit,
before string)` and returns `(*UserSearchResponse, error)`, where
`UserSearchResponse{Users, HasMore, NextCursor}` mirrors the
`FollowListResponse`/`EchoerListResponse` shape.

Usernames aren't globally unique (only unique per-server), so a bare
`(username)` cursor wasn't enough — the keyset needed a tiebreak. Rather
than an RFC3339-timestamp `before` (there's no timestamp in this
ordering), the cursor follows the ripples pattern instead: an opaque
base64-JSON `userSearchCursor{Username, ID}`, minted server-side and
returned as `NextCursor`, decoded via `decodeUserSearchCursor`. The SQL
keyset predicate is `(u.username, u.id) > ($from cursor)`, `ORDER BY
u.username ASC, u.id ASC` — same `limit+1`/`paginateRows` trim as every
other endpoint from [01](01_deduplication.md).

`SearchUsers` (`handlers.go`) passes the incoming `before` query param
straight through to the service and echoes back `HasMore`/`NextCursor`
from the **local** response only, exactly as scoped — the merge with
`fanoutUserSearchToPeers`'s foreign results happens after, and doesn't
affect either field. `SearchUsersFromPeer` (`federation_relay.go`, the
federation relay leg) calls `SearchUsers` with `before = ""` always,
keeping the peer-to-peer path single-page as planned.

`MentionPicker.svelte`'s call site was updated only for the new
`(query, { limit, before? })` options-object signature on
`apiService.searchUsers` — its behavior (hardcoded `limit: 20`, no
pagination) is unchanged. `spa/src/lib/types/api.ts` gained
`UserSearchResult`/`UserSearchResponse` (previously the response was an
inline anonymous type).

Covered by `TestSearchUsers` (unchanged behavior) and the new
`TestSearchUsersPagination` in `mentions_integration_test.go`: pages
through 5 seeded users at `limit=2`, asserts no duplicate/missing rows
across pages, and asserts a malformed cursor errors.
