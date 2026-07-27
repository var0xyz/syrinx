# Conversations 03 — Echo count + conversation section on reed detail

## Status

Proposed.

## Depends on

[02](02_index_and_api.md)

## Context

APIs expose `echoCount` and reply metadata. The reed detail page
([`+page.svelte`](../../spa/src/routes/reed/[userID]/[reedID]/+page.svelte))
must surface them without changing the peer-to-peer body distribution model.

## Scope

- Fetch and display `echoCount` on the Echo action.
- Conversation section: load direct replies, render preview cards, navigate on
  tap.
- Relay-fetch reply bodies not held locally.
- Refresh on `new_reed` when the open page is the parent.
- Local index helper (optional but recommended) for offline reply previews.

## Non-goals

- Echo list UI (count only).
- Inline nested thread tree.
- New notification types.
- Changes to feed cards / broadcast (may add reply counts later).

## Design

### API client

Extend [`apiService.getReed`](../../spa/src/lib/services/api.ts) return type
with `echoCount?: number`.

Add:

```ts
listReplies(userID: string, reedID: string, opts?: { limit?: number; before?: string })
  → { replies: ReplyMeta[]; hasMore: boolean }
```

`ReplyMeta`: `{ userID, reedID, signedAt, username? }`.

### Echo count

On reed load (same request as existence check or after local cache hit):

- If reed loaded from IndexedDB only, still hit `GET /reeds/{userID}/{reedID}`
  for fresh `echoCount` when online (metadata is cheap).
- Echo button label: `Echo` or `Echo · {n}` when `n > 0`.

### Conversation section

New component `ConversationSection.svelte` (or inline in page):

**Props:** `parentUserID`, `parentReedID`

**Load:**

1. `listReplies(parentUserID, parentReedID)`.
2. For each `ReplyMeta`, try `reedsService.getReed(author, id)`.
3. Missing → `serverConnection.requestReedContent(id, author, viewerId)`;
   on delivery, `storeReed` and update row.

**Render:**

- Header: `Conversation` + `· {count} replies` when count > 0.
- Rows: reuse feed/quote styling — avatar, `@username`, relative time,
  `MarkdownParser` preview (`preview={true}`).
- Row tap → `goto(/reed/${reply.userID}/${reply.reedID})`.
- `hasMore` → "Load more" button passing `before` cursor.

**Empty:** muted `No replies yet`.

**Errors:** banner + retry; do not block main reed render.

### Drill-down behaviour

No special route state. Navigating to a reply reed is a normal detail page
load; that reed's `ConversationSection` lists *its* children. The
replying-to quote at the top supplies upstream context.

Optional enhancement (non-blocking): breadcrumb `← Back to @alice's reed` using
`replying` ref — skip for v1 unless trivial.

### Realtime refresh

Subscribe while detail page mounted:

- On `new_reed` queue / `ServerEvent`, if incoming reed's `replying` parses to
  current `(parentUserID, parentReedID)`, prepend to list (after verify +
  store).
- If incoming `echoing` matches current reed, increment local `echoCount`.

Debounce burst traffic (e.g. 300ms) if multiple replies arrive at once.

### Local reply index (recommended)

Add IndexedDB index on `reeds` store: `replying` field (multi-entry).

Helper `getLocalDirectReplies(parentAuthorId, parentReedId)`:

- Scan `replying === `${parentAuthorId}!${parentReedId}``.
- Use for instant paint offline; merge with server list when online (server
  wins ordering; union by `reedID`).

Not required for MVP if relay-only path is acceptable offline-empty.

### Component structure

```
reed detail page
├── reed meta + body (existing)
├── action bar (echo count on Echo btn)
└── ConversationSection
    └── ReplyRow × N
```

Extract `ReplyRow` if it shares markup with feed cards.

## Work items

1. API types + `listReplies` client.
2. `echoCount` on `getReed` response + Echo button label.
3. `parseSocialRef` used consistently (from [01](01_publish_and_refs.md)).
4. `ConversationSection.svelte` + wire into detail page.
5. Relay + store loop for missing bodies.
6. `new_reed` refresh hook on detail page.
7. (Optional) IndexedDB `replying` index + offline merge.
8. Manual test plan:
   - Post reply → appears in parent conversation.
   - Tap reply → child conversation visible.
   - Echo count updates after echo publish.
   - Removed reply disappears from list on refresh.

## Testing

- Component test or Playwright: open reed with replies → rows visible →
  navigate to child.
- Unit test: `parseSocialRef` + merge logic if implemented.

## Risks

- **Double fetch** — list metadata then per-reed relay; acceptable for small
  threads; batch relay is a future optimization.
- **Stale offline list** — without server refresh, conversation may lag;
  `$isOnline` reactive retry matches existing reed page pattern.

## Parallelism

UI can be built against mocked `listReplies` before [02](02_index_and_api.md)
merges.
