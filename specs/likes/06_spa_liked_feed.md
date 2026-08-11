# Likes 06 — SPA "Liked reeds" list (profile entry point)

## Status

Proposed.

## Depends on

[05](05_spa_pending_and_button.md)

## Context

Users want to find reeds they've liked again later — the same
"come back and browse this later" need `following`/`followers` already
serve for people, but for content. Unlike following/followers
(`FollowListModal.svelte`, a compact list of usernames), a liked-reeds
view needs to render full reed cards (author, timestamp, content
preview) since the point is re-reading the reed, not just recognizing a
username. That makes it structurally closer to the existing `/reeds`
feed page than to the follow-list modal.

## Scope

- A new route, `/profile/liked`, rendering the signed-in user's liked
  reeds newest-liked-first.
- Entry point: a "Liked" link on `/profile`, positioned near the existing
  Following/Followers links.
- Data source: local `likedReeds` cache ([05](05_spa_pending_and_button.md)),
  each row resolved to its full reed body via the existing
  `reedsService.getReed(authorID, reedID)`.
- Empty state ("No liked reeds yet") and loading state, consistent with
  `ReedsList.svelte`'s existing empty/loading states.

## Non-goals

- Server-side "list of reeds I've liked" endpoint — v1 is purely a local
  read of `likedReeds`, which is already fully populated by
  [05](05_spa_pending_and_button.md)'s put-on-confirm / delete-on-unlike
  flow. A device that never liked anything on this browser/install simply
  has an empty local cache; this is the same posture as any other
  purely-local IndexedDB-backed list in this app today (there is no
  cross-device sync of local caches anywhere else either). If cross-device
  liked-list sync is wanted later, that is a new, separate proposal (a
  `GET /users/me/likes` endpoint + pagination), not part of this spec.
- Un-liking from the feed itself — v1 requires opening the reed detail
  page to toggle the like button. Revisit only if it proves annoying in
  practice.
- Showing *other users'* liked-reeds lists (privacy: this is a personal
  view of your own activity, same audience boundary as your own pending
  queues — no public "liked by" list per [00 non-goals](00_design.md#non-goals)).

## Design

### Entry point

On `/profile/+page.svelte`, near the existing follow-list links:

```svelte
<a href="/profile/liked" class="profile-link">Liked</a>
```

Placed as a plain navigation link (not a hash-routed modal like
Following/Followers) since this view is a full page of content, not a
small in-place list — matching how `/reeds` and `/feeds` are also plain
routes, not modals.

### Route: `/profile/liked`

```svelte
<Auth>
  <div class="liked-container">
    <div class="liked-content">
      <LikedReedsList userID={user.id} {scrollRestoreY} />
    </div>
    <BottomToolbar currentPage="profile" />
  </div>
</Auth>
```

`currentPage="profile"` keeps the bottom-nav Profile tab highlighted,
since this is conceptually "your stuff," same as how `/profile/[userId]`
sub-pages behave today.

### `LikedReedsList.svelte` (new component)

Deliberately a **separate** component from `ReedsList.svelte`, not a
mode/flag added to it, because the data-loading shape is fundamentally
different:

- `ReedsList` fetches all reeds **by one author** (`getReedsByAuthor`).
- `LikedReedsList` fetches **one reed each** for a heterogeneous list of
  `(authorID, reedID)` pairs, potentially from many different authors,
  ordered by like time rather than publish time.

Forcing both into one component via a mode flag would mean threading a
different data-fetch strategy and a different sort key through the same
component, which is more contortion than reuse. The **rendering** of an
individual reed card (author header, content preview, quote-container for
echo/reply) is the part actually worth sharing — factor that out only if
implementation finds the duplication painful; not a spec-level
requirement to pre-emptively extract a shared `ReedCard` today.

Loading sequence:

1. `likedReedsRepository.getAll()` → array of `LikedReedRecord`, already
   sorted newest-`likedAt`-first (index from
   [01](01_schema.md)/[05](05_spa_pending_and_button.md)).
2. For each record, `reedsService.getReed(authorID, reedID)` — same
   per-reed resolution `Quote.svelte` already does for echo/reply
   previews. Resolve in parallel (`Promise.allSettled`, same pattern
   `ReedsList.svelte`'s echo-resolution code already uses) rather than
   serially.
3. A liked reed that fails to resolve (author offline, reed since
   removed) is skipped from the rendered list rather than shown as an
   error — consistent with how a removed reed simply disappears from
   view elsewhere in the app (the like relationship itself is left alone
   locally; it is not this view's job to clean up `likedReeds` for a
   since-removed reed, since the reed could reappear if a peer relays it
   again).
4. Each resolved reed renders with the existing reed-card visual
   treatment (author avatar/name, relative time via `likedAt` rather than
   publish time — the point of this list is "when did I like it," not
   "when was it published"), clicking through to
   `/reed/{authorID}/{reedID}` same as everywhere else.

### Empty / loading states

Mirrors `ReedsList.svelte`'s existing `.empty-state` / `.loading`
markup and CSS classes for visual consistency:

```svelte
{#if loading}
  <div class="loading">
    <h2>Loading liked reeds...</h2>
  </div>
{:else if likedReeds.length === 0}
  <div class="empty-state">
    <div class="empty-icon">💛</div>
    <h3>No liked reeds yet</h3>
    <p>Reeds you like will appear here.</p>
  </div>
{:else}
  ...
{/if}
```

## Test plan

- [ ] Liking a reed on its detail page makes it appear in `/profile/liked`
      on next visit (or live, if the list is open in another tab — v1 may
      require a manual revisit; live-updating this list is not required)
- [ ] Unliking removes it from the list on next visit
- [ ] Empty state shown for a user with zero likes
- [ ] A liked reed whose author/content can't currently be resolved is
      skipped, not shown as a broken card
- [ ] Newest-liked-first ordering
- [ ] Clicking a card navigates to that reed's detail page

## Open questions

1. Whether "Liked" belongs in the bottom-nav toolbar directly (a fifth
   tab) versus one level under Profile as proposed here. Proposing
   under-Profile for v1 since the bottom nav is already at four items
   (Reeds, Feed, Invites, Profile) and this is a secondary, personal
   view, not a primary navigation surface — but this is a UX call the
   reviewer may want to override.
2. Whether cross-device sync (a real `GET /users/me/likes` endpoint) is
   worth doing now instead of later, given the local-cache-only
   limitation called out above. Deferred out of this spec's scope
   deliberately, not by oversight.
