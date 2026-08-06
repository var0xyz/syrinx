# Conversations 03 — Echo count + conversation section on reed detail

## Status

Implemented.

## Depends on

[02](02_index_and_api.md)

## Context

APIs expose echo count and reply metadata. The reed detail page
([`+page.svelte`](../../spa/src/routes/reed/[userID]/[reedID]/+page.svelte))
must surface them without changing the peer-to-peer body distribution model.

## Scope

- Display echo count in the **stats line** (existing pattern).
- Conversation section: load direct replies, render preview cards, navigate on
  tap.
- Relay-fetch reply bodies not held locally.
- Refresh on `FOLLOW_REED` when the open page is the parent.
- Local **`reed_replies`** + **`reed_threads`** IndexedDB stores for offline
  reply metadata.

## Non-goals

- Echo count on the Echo action button (stats line only).
- `echoCount` embedded on `GET /reeds`.
- Echo list UI (count only).
- Inline nested thread tree.
- New notification types.
- Changes to feed cards / broadcast (may add reply counts later).

## Design

### API client

Add:

```ts
listReplies(userID: string, reedID: string, opts?: { limit?: number; before?: string })
  → { replies: ReplyMeta[]; hasMore: boolean }
```

`ReplyMeta`: `{ userID, reedID }`.

Echo count: [`getReedEchoCount`](../../spa/src/lib/services/api.ts) →
`GET /reeds/{userID}/{reedID}/echoes`; cached in `echo_counts`; displayed in
stats line via [`echoCountsRepository`](../../spa/src/lib/repositories/echoCounts.ts)
+ WS `REED_STATS` / `REED_ECHOES`.

Reply count (subtree): `REED_STATS.replies` + `REED_REPLIES`; cached in `reply_counts`;
shown in stats line (`reply-16.png`) and as `Replies (N)` on the conversation section.

### Echo count

- Stats line shows echo count (unchanged).
- Echo action button label stays plain **`Echo`**.

### Local IndexedDB stores

Snake_case store names (match `echo_counts`, `unsigned_reeds`).

**`reed_threads`** — thread root lookup:

| Field | Notes |
|-------|-------|
| `id` (keyPath) | `threadId` wire ref |
| `userID` | Root author |
| `reedID` | Root reed id |

**`reed_replies`** — one row per reply reed:

| Field | Notes |
|-------|-------|
| `reedID` (keyPath) | This reply's id |
| `userID` | Reply author |
| `parent` | `{ userID, reedID }` direct parent |
| `parentUserID`, `parentReedID`, `parentKey` | Denormalized for index |
| `threadId` | Thread wire ref |

Index `parentKey` = `` `${parentUserID}/${parentReedID}` `` for direct-children
queries.

### Conversation section

New component `ConversationSection.svelte`:

**Props:** `parentUserID`, `parentReedID`, `threadId?`

**Load:**

1. `reedRepliesRepository.listByParent(...)` for instant offline paint.
2. When online, `listReplies(...)` → upsert `reed_replies` rows + `reed_threads`.
3. For **each** reply, `serverConnection.requestReedContent` (REQUEST_REED relay);
   update the row when content arrives.

**Render:**

- Header: `Conversation` + `· {count} replies` (section only rendered when count > 0).
- Rows: avatar, username, relative time, `MarkdownParser` preview.
- Row tap → child reed page.
- `hasMore` → load-more with `before` cursor.

**Empty:** hide the conversation section entirely.

### Realtime refresh

On `followReedQueue` / `FOLLOW_REED`, if incoming reed's `replying` matches
current parent → upsert `reed_replies` + `reed_threads`, refresh section.

If incoming `echoing` matches current reed → bump stats echo count (existing).

## Work items

1. API types + `listReplies` client.
2. `reed_threads` + `reed_replies` stores + repositories.
3. `ConversationSection.svelte` + wire into detail page.
4. Relay + store loop for missing bodies.
5. `FOLLOW_REED` refresh hook on detail page.
6. Playwright: reply → parent conversation → drill-down.

## Testing

- Playwright: open reed with replies → rows visible → navigate to child.

## Risks

- **Double fetch** — list metadata then per-reed relay; acceptable for small
  threads.
- **Stale offline list** — `$isOnline` reactive retry on conversation section.
