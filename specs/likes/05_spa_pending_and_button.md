# Likes 05 — SPA `pendingLikes` / `pendingUnlike` outboxes + like button + stats

## Status

Proposed.

## Depends on

[03](03_api.md), [04](04_realtime.md)

## Context

Likes toggle offline-first: durable pending → server → local confirmed
state → clear pending, mirroring reed removal's author queue
([deletion 05](../deletion/05_spa_reed_author.md),
`spa/src/lib/services/reedRemoval.ts`,
`spa/src/lib/repositories/pendingRemoval.ts`). Because liking is signed
and unliking is not ([00 § Unlike is unsigned](00_design.md#unlike-is-unsigned)),
the two directions are **not** one row with an `action` flag — they are
two separate, independently-shaped pending stores:

- `pendingLikes` — carries a signature, mirrors `pendingRemoval` exactly.
- `pendingUnlike` — carries no signature at all, just enough to identify
  which row to delete.

## Scope

- IndexedDB stores (bump `db.ts` schema `version`):
  - `pendingLikes` — keyed by `authorID:reedID`; fields: `serverID`,
    `authorID`, `reedID`, `likerID`, user signature.
  - `pendingUnlike` — keyed by `authorID:reedID`; fields: `authorID`,
    `reedID` (no signature field — there is nothing to sign).
  - `likedReeds` — confirmed like certs the signed-in user holds, keyed
    by `authorID:reedID`; used both to render the filled/outlined button
    state and as the local cache backing the liked-reeds feed
    ([06](06_spa_liked_feed.md)).
- `likeReed(authorID, reedID)` (signed, mirrors `removeReedAsAuthor`) and
  `unlikeReed(authorID, reedID)` (unsigned, a plain queued DELETE)
  service functions.
- On app start / online: flush `pendingLikes` and `pendingUnlike`
  independently.
- Reed-detail like button: outlined/filled heart, optimistic toggle on
  tap, wired next to Share in the action bar. **Purely local, per
  signed-in user** — reflects only this device's own `likedReeds` state,
  confirmed once `pendingLikes`/`pendingUnlike` clears. It is not derived
  from and does not react to the `REED_LIKES` broadcast in any way; that
  broadcast only updates the shared count (see [04](04_realtime.md)).
- Stats bar: like count as a fourth entry; `ReedStatsInfoModal.svelte`
  gains a fourth explainer row.

## Non-goals

- The liked-reeds feed UI itself (06).
- Any UI for "who liked this" (out of scope per [00](00_design.md)).

## Design

### `PendingLikeRecord` (signed)

```ts
export interface PendingLikeRecord {
  authorID: string;
  reedID: string;
  serverID: string;
  likerID: string;
  signature: string; // base64 user detached sig over BuildReedLikeUserPayload
}
```

Repository `pendingLikeRepository` — mirrors `pendingRemovalRepository`
exactly (`put`, `delete`, `get`, `getAll`, `syncPending()`). A
`pendingLikeSynced` writable store, incremented after each successful
flush, same role as `pendingRemovalSynced`.

### `PendingUnlikeRecord` (unsigned)

```ts
export interface PendingUnlikeRecord {
  authorID: string;
  reedID: string;
}
```

Repository `pendingUnlikeRepository` — same `put`/`delete`/`get`/`getAll`/
`syncPending()` shape, but there is no signature to carry and therefore
no signing step in its write path at all; `put()` here is purely "remember
that I asked to unlike this, in case the app closes before the DELETE
lands."

Both stores keyed by `authorID:reedID` (composite string key). **Only one
of the two stores may hold an entry for a given `authorID:reedID` at a
time** — tapping the button writes to one and deletes any stale entry
from the other:

```ts
async function setPendingLike(record: PendingLikeRecord) {
  await pendingUnlikeRepository.delete(`${record.authorID}:${record.reedID}`);
  await pendingLikeRepository.put(record);
}

async function setPendingUnlike(record: PendingUnlikeRecord) {
  await pendingLikeRepository.delete(`${record.authorID}:${record.reedID}`);
  await pendingUnlikeRepository.put(record);
}
```

This is the same "last tap wins" reasoning as a single-store `action`
field would have given, just expressed as two stores instead of one
polymorphic row — necessary because a `PendingLikeRecord` and a
`PendingUnlikeRecord` don't share a shape (one has a signature, one
doesn't), so a single store would need a nullable `signature` field that
is meaningless half the time. Two small, fully-typed stores are clearer
than one store with a conditionally-present field.

### `LikedReedRecord`

```ts
export interface LikedReedRecord {
  authorID: string;
  reedID: string;
  serverID: string;
  likedAt: string; // server.timestamp from the confirmed like cert
  userSignature: UserSignature;
  serverSignature: ServerSignature;
}
```

Stored via `likedReedsRepository.put(cert)` / `.delete(authorID, reedID)`
/ `.get(authorID, reedID)` / `.getAll()` (newest-`likedAt`-first for the
feed — see [06](06_spa_liked_feed.md)). An unlike removes the entry
entirely; there is no "unliked cert" to store since none exists.

### `db.ts` schema addition

```
['pendingLikes',   'compositeKey'],   // compositeKey = `${authorID}:${reedID}`
['pendingUnlike',  'compositeKey'],
['likedReeds',     'compositeKey', 'likedAt'],
```

Bump `version` (currently 6 → 7). Per existing `IndexedDbService`
behavior, this drops and recreates all local object stores on next open
— acceptable, since every store here is a cache of server-verified or
locally-queued state, never a source of truth (same posture the existing
pending/removed stores already rely on).

### `likeReed` (signed)

```ts
export async function likeReed(authorID: string, reedID: string): Promise<api.ReedLike> {
  const info = get(serverInfo);
  const serverID = info?.id || localStorage.getItem('serverId');
  if (!serverID) throw new Error('Server ID not available');

  const likerID = authService.getUserID(); // however the active user id is currently read elsewhere
  const fingerprint = authService.getActiveKeyFingerprint();
  const passphrase = authService.getPassphrase();
  if (!fingerprint || !passphrase) throw new Error('Active key or passphrase not available');

  const privateKey = await privateKeyRepository.getPrivateKey(fingerprint);
  if (!privateKey?.armor) throw new Error('Private key not found');

  const userPayload = buildReedLikeUserPayload(serverID, authorID, reedID, likerID);
  const sigArmor = await cryptoService.signMessage(userPayload, privateKey.armor, passphrase);
  const signature = btoa(sigArmor);

  await setPendingLike({ authorID, reedID, serverID, likerID, signature });

  const cert = await apiService.likeReed(authorID, reedID, signature);
  if (!(await verifyAndCommitReedLike(cert))) {
    throw new Error('Server like countersignature failed verification');
  }
  await pendingLikeRepository.delete(`${authorID}:${reedID}`);
  return cert;
}
```

`verifyAndCommitReedLike` / `commitReedLikeLocally` mirror
`verifyAndCommitReedRemoval` / `commitReedRemovalLocally`
(`spa/src/lib/services/reedRemoval.ts:16-34`) exactly: verify inside the
repository `put`'s verifier argument, no separate verify step.

### `unlikeReed` (unsigned)

```ts
export async function unlikeReed(authorID: string, reedID: string): Promise<void> {
  await setPendingUnlike({ authorID, reedID });

  await apiService.unlikeReed(authorID, reedID); // plain DELETE, no signature param at all
  await likedReedsRepository.delete(authorID, reedID);
  await pendingUnlikeRepository.delete(`${authorID}:${reedID}`);
}
```

No signing, no verification step, no cert to check — `apiService.unlikeReed`
takes only `authorID`/`reedID` (plus whatever auth header every other
authenticated request already carries). The only failure mode is a
network/HTTP error, in which case the `pendingUnlike` row is left in
place for `syncPending()` to retry later — same durability reasoning as
the signed path, just without anything cryptographic in it.

### Flush on reconnect

Two independent flushes, wired via the same `onReconnect(...)` hook
already used for `reedsService.processUnsignedReeds()` and
`pendingRemovalRepository.syncPending()`:

```ts
onReconnect(async () => {
  await pendingLikeRepository.syncPending();   // replays signed likeReed submits
  await pendingUnlikeRepository.syncPending();  // replays unsigned unlikeReed submits
});
```

Each `syncPending()` iterates its own store and calls its matching
submit path. Because the two stores are kept mutually exclusive (see
above), there is never an ordering question between "flush the like" and
"flush the unlike" for the same reed — at most one of the two has a
pending row for any given `authorID:reedID` at flush time.

### Reed-detail like button

Action bar gains a fourth button next to Share (immediately before it):

```svelte
<button class="action-btn" on:click={handleLike} aria-label={isLiked ? 'Unlike' : 'Like'} disabled={isPending}>
  <span class="action-icon icon-like" class:filled={isLiked}></span>
  <span class="action-label">Like</span>
</button>
```

`icon-like` mask-image is `/icons/like-24-outlined.png` by default,
switched to `/icons/like-24-filled.png` via the `.filled` class — same
`background-color: currentColor` mask-icon technique already used for
`icon-echo` / `icon-reply` / `icon-share` on this page
(`spa/src/routes/reed/[userID]/[reedID]/+page.svelte`).

`handleLike`:

```js
async function handleLike() {
  if (isPending) return;
  const wasLiked = isLiked;
  isLiked = !wasLiked; // optimistic: filled ↔ outlined immediately on tap
  try {
    if (wasLiked) {
      await unlikeReed(userID, reedID);
    } else {
      await likeReed(userID, reedID);
    }
  } catch (error) {
    isLiked = wasLiked; // revert on failure
    console.error('Error toggling like:', error);
  }
}
```

Optimistic toggle is safe here specifically because the pending-queue
write happens synchronously before the network call inside
`likeReed`/`unlikeReed` (same ordering guarantee reed removal relies on)
— even if the network call itself fails or the tab closes, the intended
end state is durable in `pendingLikes`/`pendingUnlike` and will flush
later. The `catch` block only reverts the icon for the case where the
*local* step fails before anything was queued (signing failure for like;
effectively never for unlike, since it has nothing local to fail besides
the IndexedDB write itself).

**`isLiked` and `REED_LIKES` are unrelated signals.** `REED_LIKES` only
carries a count and is broadcast to every subscriber of the reed
regardless of who liked or unliked it — updating that shared `likeCount`
display is the *only* thing it does. `isLiked` (the button's filled vs.
outlined state) is never set from a WS message; it comes exclusively
from this device's own `likedReeds` cache. If the same user has the same
reed open in a second tab, that second tab's button does **not**
update live when the first tab unlikes — it will show the correct state
next time it loads `likedReeds` (e.g. on navigation/reload), same as any
other purely local UI state in this app. This is a deliberate scope
limit, not a bug: cross-tab-same-user button sync is not required by
this spec.

`isLiked` initialized from `likedReedsRepository.get(userID, reedID)` on
page load, alongside the existing `authorUser`/`reed` fetch.

### Stats bar + modal

Fourth entry, `/icons/like-16-outlined.png`, always outlined regardless
of `isLiked` (see [00](00_design.md#ux)):

```svelte
<span class="reed-stat-icon likes" aria-hidden="true"></span>
{likeCount}
```

`likeCount` sourced from `REED_STATS`/`REED_LIKES`
([04](04_realtime.md)), same pattern as `echoCount`/`replyCount`/
`coveragePercent` — including on unlike, where the incoming `REED_LIKES`
message carries the new, lower count and this line updates to match with
no separate handling needed beyond the existing "set `likeCount` from
the message" logic.

`ReedStatsInfoModal.svelte` gains a fourth `<dt>/<dd>` pair:

```svelte
<div class="stats-row">
  <dt>
    <span class="stats-icon likes" aria-hidden="true"></span>
    Likes
  </dt>
  <dd>How many users have liked this reed.</dd>
</div>
```

Using `/icons/like-24-outlined.png` for the modal icon (24px, matching
the other three modal icons, per the original instruction to use the
larger icon set in the modal).

## Test plan

- [ ] Offline: pending like survives reload; flushes when back online
- [ ] Offline: pending unlike survives reload; flushes when back online
- [ ] Tap like then immediately unlike before either flushes → only
      `pendingUnlike` ends up populated; `pendingLikes` entry for that
      reed is gone
- [ ] Tap unlike then immediately like before either flushes → only
      `pendingLikes` ends up populated; `pendingUnlike` entry is gone
- [ ] Like success path order: submit → verify → `likedReeds` put →
      `pendingLikes` clear
- [ ] Unlike success path order: submit (no signing) → `likedReeds`
      delete → `pendingUnlike` clear
- [ ] Optimistic UI reverts if local signing fails before a like is queued
- [ ] Reject/abort like if server countersig fails verification
- [ ] Unlike request never includes a signature field, under any code
      path
- [ ] `isLiked` restored correctly from `likedReeds` on page reload
- [ ] Stats-bar like **count** updates live via `REED_LIKES` on both like
      and unlike, without a page reload, matching existing echo/coverage
      live-update tests
- [ ] A second user viewing the same reed sees only the shared count
      change when the first user likes/unlikes — their own like button
      is unaffected (reflects their own like state, not the first user's)
- [ ] A second tab of the **same** user does not live-update its like
      button when the first tab unlikes (out of scope per design — button
      state is read fresh from `likedReeds` on load only)
