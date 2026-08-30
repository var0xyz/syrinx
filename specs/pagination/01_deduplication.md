# 01 — Unify duplicated pagination logic

**Status: Implemented.**

Pure duplication-removal refactor: no behavior, wire format, or SQL
changes. Every extracted backend block was byte-for-byte identical before
this step; the frontend adopted a shared store only where it was a clean
fit.

## Backend — `pagination.go` (repo root, `package main`, no build tag)

`handlers.go`/`services.go` carry `//go:build !ops && !ripplescleanup`,
but sibling files like `utils.go`/`constants.go` carry no tag — this file
follows that untagged pattern since nothing in it depends on
ops/ripplescleanup-gated code.

```go
func parseLimitParam(r *http.Request) (limit int, err error)
func parseBeforeTimeParam(r *http.Request) (before *time.Time, err error)
func clampLimit(limit int) int
func paginationTrimCount(fetched, limit int) (keep int, hasMore bool)
func paginateRows[T any](items []T, limit int) (trimmed []T, hasMore bool)
```

`parseLimitParam`/`parseBeforeTimeParam` return a plain `error`
(`errInvalidLimit`/`errInvalidBefore`) rather than writing an HTTP
response themselves — each handler writes its own 400 using the error's
message, keeping this file free of any `http.ResponseWriter` dependency
so the parsers stay pure and independently testable.

`parseBeforeTimeParam` is not used by `GetRipples` — its cursor is an
opaque base64-JSON string validated by `decodeRippleCursor`, a
fundamentally different shape not worth forcing into the RFC3339 helper.

`ListRipples` trims two parallel slices (`items`, `threadCreatedAts`) in
lockstep before deriving `nextCursor` from the last kept element, so it
uses `paginationTrimCount` directly rather than the single-slice
`paginateRows`:

```go
keep, hasMore := paginationTrimCount(len(items), limit)
items = items[:keep]
threadCreatedAts = threadCreatedAts[:keep]
```

Applied to all 5 handlers (`GetReedChorus`, `GetReedReplies`,
`GetUserFollowing`, `GetUserFollowers`, `GetRipples`) and 4 service
functions (`ListReplies`, `listFollowEdge`, `GetReedChorus`,
`ListRipples`). SQL text, response struct definitions/JSON tags, and
`rippleCursor`/`encodeRippleCursor`/`decodeRippleCursor` were untouched.

Covered by `pagination_test.go` plus the existing handler/service test
suites (unmodified, all passing).

## Frontend — `spa/src/lib/stores/pagination.ts`

Classic `writable`-based store factory, matching the codebase's one
existing stateful-store precedent (`spa/src/lib/stores/notifications.ts`).
No runes — the codebase runs Svelte 5 in legacy `export let`/`$:` mode
throughout, and introducing runes here would have been an unrelated
stylistic change out of scope for this refactor.

```ts
export interface PaginationState<T> {
  rows: T[];
  loading: boolean;
  loadingMore: boolean;
  hasMore: boolean;
  error: string;
}

export interface PaginationOptions<T, Page> {
  direction?: 'append' | 'prepend';
  fetchPage: (cursor: string | undefined) => Promise<Page>;
  getItems: (page: Page) => T[] | Promise<T[]>;
  getHasMore: (page: Page) => boolean;
  getNextCursor: (page: Page, rowsSoFar: T[]) => string | undefined;
}

export function createPaginationStore<T, Page>(opts: PaginationOptions<T, Page>): PaginationStore<T>;
```

There is no frontend equivalent of the backend's trim/clamp logic —
pages arrive already trimmed with a `hasMore` boolean, so the store's job
is state shape and load/loadMore orchestration only.

### Adopted: `FollowListModal.svelte`, `ChorusSection.svelte`

Both mapped cleanly onto `createPaginationStore` — no template changes,
same fetch calls and cursor derivation as before.

### Documented exceptions: `ConversationSection.svelte`, `RipplesSection.svelte`

Both were evaluated and left un-adopted, with a comment at each
component's load function explaining why:

- **ConversationSection** interleaves pagination with cache side-effects
  that must happen in a specific order relative to each fetch
  (`reedRepliesRepository.syncFromServerList`, then a conditional
  `pruneStale` gated on that same page's `hasMore`, before hydration),
  has a cache-first-then-network double-load path for page 1 with no
  analog elsewhere, and `loadMore()` prepends older pages at the head
  with an explicit dedup set — opposite direction and shape from the
  other adopters.
- **RipplesSection** has its own expiry short-circuit before touching
  `ripples` at all, filters fetched items through a
  `ripplesRepository.storeRipple` step (kept items can be a strict subset
  of the page), and live-arrived ripples route through three separate
  buffers (`liveExtras`, `topLevelLiveExtras`, `pendingRipples`) before
  being spliced into `ripples` by a tree-insertion index — all outside of
  `loadPage`. Adopting the shared store's `update()` escape hatch would
  have been in near-constant use, relocating complexity rather than
  removing it.

Websocket handling, templates, and `api.ts` signatures were untouched
everywhere in this step.

## Verification performed

- `go build ./...`, `go vet ./...`, `go test .` (full suite, all
  packages) — all passing.
- `pagination_test.go` added for the new Go helpers.
- `npm run check` (svelte-check) — 0 errors.
- `npm run build` — succeeds, `pagination.js` chunk present.
