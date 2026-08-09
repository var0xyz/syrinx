# Ripples 04 — SPA Ripples section

## Status

Proposed.

## Depends on

[02](02_post_and_list_api.md), [03](03_realtime_fanout.md)

## Context

The reed detail page (`spa/src/routes/reed/[userID]/[reedID]/+page.svelte`)
already mounts `ConversationSection` gated on `!isPending`, where
`isPending = !!(reed && !reed.serverSignature)` (confirmed by direct code
read at ~line 579, with a separate tombstone-branch mount at ~478–484).
Ripples get their own section on the same page, gated on the same
condition, per [00](00_design.md)'s locked visibility rule.

## Scope

- `RipplesSection.svelte` — list + composer, mounted next to
  `ConversationSection`.
- Fetch on mount (via 02's `GET`), live-append via 03's WS event, optimistic
  local append on successful POST.
- Self-delete affordance (trash icon on own ripples only).
- Empty state, loading state, rate-limited error state.

## Non-goals

- Editing UI (no edit exists server-side either).
- Nested reply UI beyond the flat "replying to @X" display line (00's
  lock).
- Any reed-author moderation controls (00's lock — not built server-side,
  so nothing to wire client-side).

## Design

### Mount point

In `spa/src/routes/reed/[userID]/[reedID]/+page.svelte`, alongside the
existing `{#if !isPending}<ConversationSection .../>{/if}` block:

```svelte
{#if !isPending}
  <ConversationSection {reed} ... />
  <RipplesSection userID={reed.userID} reedID={reed.id} />
{/if}
```

Same condition, same branch — no new pending-state logic invented. In the
tombstone branch (reed removed), neither section mounts, consistent with
existing behavior for `ConversationSection`.

### Data flow

- On mount: `apiService.listRipples(userID, reedID)` → 02's `GET` endpoint.
  Reuses the pagination shape (`limit`/`before`) from
  [`conversations/02`](../conversations/02_index_and_api.md)'s existing
  replies-list client code as a template — same cursor pattern, don't
  invent a second one.
- WS: subscribe to the existing per-reed channel (already established by
  `ConversationSection`'s own subscription — check whether that
  subscription is page-scoped or component-scoped before adding a second
  one; reuse rather than duplicate the subscribe call if it's page-scoped).
  On `RIPPLE_POSTED`, append to the local list if not already present (by
  `id`, guards against the optimistic-append-then-echo double-add). On
  `RIPPLE_DELETED`, remove by `id`.
- Composer submit: optimistic local append (temp local state, not written
  to IndexedDB — ripples are not part of the offline-first reed model, see
  [README](README.md) Non-goals on offline queueing), `POST`, replace temp
  entry with server response (real `id`/`postedAt`) on success, remove and
  show an inline error on failure (including the 429 rate-limit case —
  surface the server's human-readable message directly, no custom
  client-side copy).

### Composer

Plain `<textarea>`, 140-char counter (reuse `MAX_REED_VISIBLE_CHARS` from
`spa/src/lib/utils/reedContent.ts` — same constant 00/02 reference
server-side, single source of truth split only by language runtime), no
markdown toolbar (00's lock: ripples are plain text). "Replying to
@username" chip appears above the textarea when the user taps "reply" on a
specific ripple (sets `inReplyToRippleID`), dismissible to go back to
top-level.

### Rendering a ripple row

Reuse `ReedAuthorHeader.svelte` (already extracted this session for exactly
this avatar+username+timestamp overflow problem — see
[`conversations/03_spa_reed_detail.md`](../conversations/03_spa_reed_detail.md)
and the pipe-page/reeds-list call sites) for the author line:
`<ReedAuthorHeader userID={ripple.userID} username={...} subtext={formatRelativeTime(ripple.postedAt)} avatarSize="32px" nameTag="span" />`,
followed by the plain-text content (rendered via `{ripple.content}` in a
`<p>` — no `MarkdownParser`, per 00's plain-text lock) and, if present, a
small "↳ replying to @X" line resolved from the local ripple list (or "a
deleted comment" if not found, per 00).

### Empty / loading / error states

- Loading: skeleton row or spinner, consistent with whatever
  `ConversationSection` already does for its own loading state (reuse
  the pattern, don't invent a new skeleton style).
- Empty (thread never started): "No comments yet — be the first to say
  something." (or similar; match this codebase's existing empty-state tone
  — check `ConversationSection`'s empty-reply-list copy for the house
  style before finalizing wording).
- Rate-limited: inline error under the composer showing the server's
  message, textarea retains the unsent draft so the user doesn't lose
  their text.

## Work items

1. `spa/src/lib/services/api.ts` — `listRipples`, `postRipple`,
   `deleteRipple`.
2. `spa/src/lib/components/RipplesSection.svelte`.
3. Mount into the reed detail page.
4. WS dispatch cases wired in 03's SPA work item — confirm here rather than
   duplicate.
5. Playwright test: open a reed, post a ripple, see it appear; open the
   same reed in a second browser context, confirm live delivery; delete
   own ripple, confirm it disappears; confirm a ripple never renders while
   the parent reed is still pending (simulate via the same technique used
   for existing pending-state tests, if one exists — check
   `spa/tests/` for a precedent before inventing a new pending-simulation
   harness).

## Risks

- **140-char limit may feel cramped for a "comment" UX** — same risk noted
  in [02](02_post_and_list_api.md), deliberately deferred rather than
  solved twice.
- **No offline/optimistic durability** — a ripple posted while briefly
  offline just fails and the draft stays in the textarea for the user to
  retry manually; no background retry queue (00/README non-goal).

## Dependencies

02 (API), 03 (realtime), `ReedAuthorHeader.svelte` (existing component).

## Parallelism

None further downstream — this is the last step in the set.
