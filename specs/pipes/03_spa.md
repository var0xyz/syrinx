# Pipes 03 — SPA links + pipe page

## Status

Proposed.

## Depends on

[02](02_subscribe_fanout.md)

## Context

Markdown already emits provisional `web+syrinx://channel/…` links. The pipe
page must list local matches and subscribe for live updates.

## Scope

- Switch `formatChannelHref` / `internalPath` to `pipe` (update any tests).
- Route `/pipe/[tag]`: decode tag; normalize for queries; show `#tag`.
- Load: query IndexedDB for reeds whose tags include this tag; sort by
  server signature time descending.
- `onMount`: `SUBSCRIBE_PIPE`; `onDestroy`: `UNSUBSCRIBE_PIPE`.
- On live reed for this tag: verify, store, prepend if matching.
- Empty state when no local reeds yet; still listen while open.

## Non-goals

- Pinning pipes in the toolbar (optional later).
- Composer tag autocomplete.

## Work

1. Rename URI helpers; keep a short redirect from `/channel/[tag]` →
   `/pipe/[tag]` only if any bookmarks exist—prefer hard rename with no
   redirect if the provisional path never shipped to users (lock in impl).
2. Pipe page UI consistent with feeds/list density.
3. Tag match must use the same normalization as publish.
4. Tests: link click navigates; local reed with tag appears; subscribe
   called on enter.

## Acceptance

- `#tag` in a reed opens `/pipe/<tag>`.
- Re-opening the pipe shows previously received local reeds.
- Leaving the page unsubscribes.
