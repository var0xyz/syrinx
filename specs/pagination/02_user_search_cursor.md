# Pagination 02 — User search cursor pagination

## Status

Proposed.

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
