# Likes 00 — Design + locked model

## Status

Proposed.

## Depends on

—

## Context

Reed detail already shows a small "stats for nerds" line — echoes, replies,
coverage — explained by a click-to-open modal
(`ReedStatsInfoModal.svelte`). A **like** adds a fourth, simpler signal: a
one-tap acknowledgement with no content, no thread, no fanout beyond a
count. It is the smallest signed resource in the app and a clean second
exercise of the offline-first pending-queue pattern already proven by reed
removal ([deletion 05](../deletion/05_spa_reed_author.md)).

Two new icon pairs already exist under `spa/static/icons/`:
`like-24-outlined.png` / `like-24-filled.png` for the action button, and
`like-16-outlined.png` for the stats-bar entry (no filled 16px variant —
the stats bar is a static icon regardless of the signed-in user's own
like state, same as the other three stats-bar icons).

## Scope

- Define the like resource, its signed payload shape, and its trust model.
- Lock **offline-first** submit: durable `pendingLikes` row → HTTP → local
  confirmed cache → clear pending, mirroring reed removal exactly.
- Lock **counted, not boolean**: server maintains a denormalized
  `reeds.like_count`, delivered live over the existing per-reed WS
  subscription used for echoes/coverage.
- Lock UX: filled/outlined heart button next to Share; like count joins
  the stats bar + its modal; a "Liked reeds" feed entry point on the
  profile page.

## Non-goals

- Notifying the author of a new like (belongs to
  [notifications](../notifications/README.md) if ever built).
- Public "who liked this" identity list — v1 is a count only, same
  audience/privacy posture as coverage
  ([coverage 00](../coverage/00_design.md)).
- Self-like prevention (see Resolved #4 below — explicitly deferred, not
  rejected).
- Likes on anything other than a reed (no comment-likes; ripples, if they
  ever ship, are explicitly out of scope per
  [ripples](../ripples/README.md)).

## Design

### Terms

- **Like** — a signed attestation `{serverID, authorID, reedID, likerID}`
  from a user, countersigned once by the server. One like per
  `(likerID, authorID, reedID)` — liking again is idempotent (returns the
  same stored cert, no double count).
- **Unlike** — a plain, **unsigned** `DELETE` that hard-deletes the
  `reeds_liked` row. See "Unlike is unsigned" below.
- **Like count** — denormalized counter on `reeds`, bumped in the same TX
  as insert/delete into `reeds_liked`, exactly like `allocation_count`
  ([coverage 01](../coverage/01_counts.md)). Never `COUNT(*)` on a hot
  path.
- **Liked (by me)** — whether the signed-in user has an unrevoked like row
  for this reed; drives the filled-vs-outlined button state.

### Why liking is signed but unliking isn't

**Liking** is a positive claim other parties build a count from ("I liked
this"); an unsigned like would let the count be forged by anyone who can
reach the HTTP endpoint. Signing costs nothing extra the client doesn't
already pay for other actions (same key, same `crypto.Service.Sign`
call), so like follows the same signed-attestation posture as
publishing, echoing, replying, and removing.

**Unliking is different: it is a retraction, not a claim.** Once the row
is deleted, there is nothing left for a signature to attest to — no
resource, no cert, no third party ever needs to verify "this user
unliked this reed" after the fact the way they need to verify a removal
cert or a like cert. The server already knows who is asking (the
authenticated session) and that is sufficient to authorize deleting
*that same user's own* `reeds_liked` row. Requiring a signature here
would add signing overhead to the cheapest, most reversible action in
the whole feature for no verification benefit — see [00
§ Unlike is unsigned](#unlike-is-unsigned) and [03](03_api.md) for the
exact endpoint contract.

### Unlike is unsigned

- `DELETE /reeds/{authorID}/{reedID}/like`, authenticated as the liker,
  **no signed body**.
- Server hard-deletes the `reeds_liked` row for
  `(likerID, authorID, reedID)` if it exists, decrements
  `reeds.like_count` in the same TX, and returns success. If no row
  exists, still returns success (no-op) — same idempotent-on-replay
  posture as everywhere else in this spec, just without a cert to return.
- No `identity.Build*Payload` helper, no `crypto.Service` call, no
  `user_signatures` row — this path never touches signing at all.

### Resource identity

Composite key `(liker_user_id, author_user_id, reed_id)` — a user can like
a given reed at most once. This mirrors `reed_echoes`' composite PK shape
(`echoing_user_id, echoing_reed_id`) rather than `reed_removals`' shape
(one row per reed), since likes are inherently many-per-reed,
one-per-(user, reed).

### UX

Reed-detail action bar (next to Echo / Reply / Share):

```
[megaphone] Echo   [reply-arrow] Reply   [heart] Like   [share] Share
```

- `/icons/like-24-outlined.png` when not liked by the signed-in user;
  `/icons/like-24-filled.png` when liked. Toggling is instant/optimistic
  in the UI (see [05](05_spa_pending_and_button.md)); the underlying
  signed record follows via the pending queue. **This is entirely
  per-viewer.** Whether the button shows filled or outlined depends only
  on whether *this signed-in user* has liked *this reed* — it has nothing
  to do with whether anyone else has. Two different users looking at the
  same reed at the same time will correctly see different button states.
- Stats bar gains a fourth entry, same tiny "stats for nerds" style as
  echoes/replies/coverage:

  ```
  [megaphone-16] N   [reply-16] N   [graph-16] P%   [like-16-outlined] N   [info-16]
  ```

  Always the outlined 16px icon — it is a **count label**, not a toggle,
  and never changes appearance for any reason, including when the count
  is 0 or when the signed-in user's own like state changes. Same
  non-stateful treatment as the other three stats-bar icons.
- `ReedStatsInfoModal.svelte` gains a fourth `<dt>/<dd>` row: "Likes — How
  many users have liked this reed."
- New profile-page entry point: a "Liked" link near the existing
  Following/Followers links, opening a feed of the signed-in user's liked
  reeds, newest-liked first ([06](06_spa_liked_feed.md)).

### Data path — like (overview)

```mermaid
sequenceDiagram
  participant SPA as ReedDetail
  participant DB_LOCAL as IndexedDB (pendingLikes)
  participant API as HTTP
  participant WS as Realtime
  participant DB as Postgres

  SPA->>DB_LOCAL: put pendingLikes (optimistic UI: filled heart)
  SPA->>API: POST /reeds/{author}/{reed}/like (signed)
  API->>DB: verify, countersign once, insert reeds_liked, like_count++
  API-->>SPA: 200 + cert
  SPA->>DB_LOCAL: put likedReeds (confirmed); delete pendingLikes
  DB-->>WS: like_count changed (same TX commit hook as coverage)
  WS-->>SPA: REED_LIKES (likes count) to all subscribers of this reed
```

1. On tap: optimistic UI flip (outline → filled) + `pendingLikes` row.
2. `POST` the signed like.
3. Server verifies, countersigns once (idempotent), bumps counter,
   responds with the cert.
4. Client commits confirmed state locally, clears the pending row.
5. Server pushes updated count to all current subscribers of that reed's
   detail page over the existing per-reed WS channel
   ([coverage 02](../coverage/02_reed_subscription.md)), same shape as
   `REED_ECHOES` / `REED_COVERAGE`.
6. Offline: pending row survives reload; flush on reconnect, same as
   `pendingRemovalRepository.syncPending()`.

### Data path — unlike (overview)

```mermaid
sequenceDiagram
  participant SPA as ReedDetail
  participant DB_LOCAL as IndexedDB (pendingUnlike)
  participant API as HTTP
  participant WS as Realtime
  participant DB as Postgres

  SPA->>DB_LOCAL: put pendingUnlike (optimistic UI: outlined heart)
  SPA->>API: DELETE /reeds/{author}/{reed}/like (no signature)
  API->>DB: hard-delete reeds_liked row, like_count--
  API-->>SPA: 200
  SPA->>DB_LOCAL: delete likedReeds entry; delete pendingUnlike
  DB-->>WS: like_count changed (same TX commit hook as coverage)
  WS-->>SPA: REED_LIKES (likes count) to all subscribers of this reed
```

1. On tap: optimistic UI flip (filled → outline) + `pendingUnlike` row
   (durable local record; no signing step at all).
2. `DELETE` the like — plain authenticated request, no signed body.
3. Server hard-deletes the row if present, decrements the counter, same
   TX, responds `200` (whether or not a row was actually present — see
   [03](03_api.md)).
4. Client removes the confirmed entry from `likedReeds`, clears the
   `pendingUnlike` row.
5. Server pushes updated count to all current subscribers, same channel
   as the like path.
6. Offline: `pendingUnlike` row survives reload; flush on reconnect —
   same durability guarantee as `pendingLikes`, just no signature to
   attach when it finally sends.

## Resolved

1. **Offline-first**, full parity with reed removal's pending-queue
   sequencing (durable pending row before network call; never clear
   pending before the confirmed state is stored locally) — for **both**
   like and unlike, each with its own pending store
   (`pendingLikes` / `pendingUnlike`).
2. **Like is signed, server-countersigned once**, using the
   `user_signatures` / `server_signatures` FK model
   ([signatures 00](../signatures/00_design.md)) — no inline signature
   columns. **Unlike is unsigned** — a plain authenticated `DELETE`, hard
   deletion of the `reeds_liked` row, no cert produced or stored (see
   "Unlike is unsigned" above).
3. **Counted via denormalized `reeds.like_count`**, bumped same-TX on
   both like insert and unlike delete, delivered live — no `COUNT(*)` on
   read, no polling. Unliking pushes the same `REED_LIKES` event as
   liking, just carrying the **decremented** count — every current
   subscriber of that reed sees the new lower **count**, full stop. The
   like button itself is a purely local, per-user concern (see below) —
   `REED_LIKES` never changes anyone's button, including the liker's own
   other tabs; it only ever updates the shared count everyone sees.
4. **v1 counts all likes, including self-likes.** No guard against a user
   liking their own reed. This was an explicit instruction for this spec,
   not an oversight; revisit only if abuse is observed.
5. **Live delivery reuses the existing per-reed WS subscription**
   (`SUBSCRIBE_REED` / `UNSUBSCRIBE_REED`) rather than a new subscription
   type — a `REED_LIKES` event alongside `REED_ECHOES` / `REED_COVERAGE`,
   and `likes` added to the `REED_STATS` snapshot.
6. **Liked-reeds feed is in scope for v1**, as a profile-page entry point
   listing the signed-in user's own liked reeds.

## Open questions

1. Whether "Liked reeds" needs its own bottom-nav tab or lives one level
   under Profile (see [06](06_spa_liked_feed.md) Open questions).
