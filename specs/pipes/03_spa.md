# Pipes 03 — SPA links + pipe page

## Status

Implemented.

## Depends on

[02](02_subscribe_fanout.md)

## Context

Markdown emits `web+syrinx://pipe/…` links. The pipe page lists local
matches and subscribes for live updates.

## Scope

- `formatPipeHref` / `internalPath` → `pipe` (hard rename from provisional
  `channel`; no redirect).
- Route `/pipe/[tag]`: decode tag; normalize for queries; show `#tag`.
- Load: query IndexedDB for reeds whose tags include this tag; sort by
  server signature time descending.
- While mounted (and on tag change): `SUBSCRIBE_PIPE` / on destroy
  `UNSUBSCRIBE_PIPE`.
- On live reed for this tag: verify (layout store path), prepend if matching.
- Empty state when no local reeds yet; still listen while open.

## Non-goals

- Pinning pipes in the toolbar (optional later).
- Composer tag autocomplete.

## Work

1. Rename URI helpers (`formatPipeHref`).
2. Pipe page UI consistent with feeds/list density.
3. Tag match uses the same normalization as publish (`normalizePipeTag`).
4. `getReedsByTag` + `subscribePipe` / `unsubscribePipe` on the SPA.

## Acceptance

- `#tag` in a reed opens `/pipe/<tag>`.
- Re-opening the pipe shows previously received local reeds.
- Leaving the page (or switching tags) unsubscribes the previous pipe.
