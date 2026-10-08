# Blocking users

A user blocks another user by signing a **block certificate**. Their home
server countersigns it and from then on refuses the blocked user everything
that would show them the blocking user: profile, info, reeds, relay requests,
profile pages, subscriptions and fanout. The certificate is pushed to the
blocked user's client, which verifies it, stores it, and immediately drops
everything it holds of the blocking user except their public key (kept to verify
the certificate).

The blocked user can forget the certificate and the key. The next time they
open the blocking user's profile, the server answers **403 Forbidden** with the
certificate again, and the client is back where it was.

Blocks work across federation: the blocking user's server sends the certificate
once to the blocked user's home server, which enforces it for its own user
and delivers it to them.

**Blank slate — no migration, no backwards compatibility.** Recreate the DB
when schema changes.

| #                                   | Title                                               | Depends on |
|-------------------------------------|-----------------------------------------------------|------------|
| [00](00_design.md)                  | Design, trust model, locked decisions               | —          |
| [01](01_schema_and_payload.md)      | `user_blocks` schema, canonical payload, countersign | 00         |
| [02](02_api.md)                     | Block / unblock / list API and immediate effects    | 01         |
| [03](03_enforcement.md)             | 403 + certificate on every read path                | 02         |
| [04](04_realtime.md)                | `USER_BLOCKED` / `USER_UNBLOCKED` push and catch-up | 02         |
| [05](05_federation.md)              | Blocks across peers                                 | 03, 04     |
| [06](06_interactions.md)            | Refuse follow, like, ripple, reply, echo, mention   | 03         |
| [07](07_spa_blocked.md)             | SPA, blocked side: verify, purge, blocked profile, forget | 04   |
| [08](08_spa_blocking_user.md)             | SPA, blocking side: block action, outbox, blocked list | 02       |

01–04 can land without federation; until 05 lands, blocking a user on
another server is refused with **422** rather than half-enforced.

---

## Status

| #  | Title                                               | Status   |
|----|-----------------------------------------------------|----------|
| 00 | Design, trust model, locked decisions               | Proposed |
| 01 | `user_blocks` schema, canonical payload, countersign | Proposed |
| 02 | Block / unblock / list API and immediate effects    | Proposed |
| 03 | 403 + certificate on every read path                | Proposed |
| 04 | `USER_BLOCKED` / `USER_UNBLOCKED` push and catch-up | Proposed |
| 05 | Blocks across peers                                 | Proposed |
| 06 | Refuse follow, like, ripple, reply, echo, mention   | Proposed |
| 07 | SPA, blocked side                                   | Proposed |
| 08 | SPA, blocking side                                   | Proposed |

**Track status: Proposed.** Nothing is implemented.

## Locked decisions

- **A block is a signed resource.** The blocking user signs
  `{type: "block", userID, blockedUserID, keyID}`; the blocking user's home
  server countersigns it once with a server-authoritative timestamp and its
  key ID. Re-blocking returns the stored certificate.
- **Unblocking is unsigned**, like unliking: an authenticated `DELETE` that
  deletes the row. It is a retraction, nothing is left to attest to. The
  notice that lifts it elsewhere names the countersignature time of the
  block it lifts, so a stale unblock never lifts a newer block.
- **Force only what the blocked user never signed.** A block forces the
  blocked client to delete the blocking user's data (never the blocked user's),
  and forces the blocked user to unfollow the blocking user: a follow is an
  unsigned `user_following` row, not a signed record, so removing it forges
  nothing. Nothing the blocked user signed is touched: their reeds, replies,
  echoes, likes, ripples and mentions stay.
- **One direction.** A block stops the blocked user seeing the blocking user. It
  does not hide the blocked user from the blocking user.
- **The blocking user's home server is the source of truth**; the blocked user's
  home server holds a copy so it can enforce for its own user without asking.
- **Every refusal carries the certificate**: HTTP answers **403** with the
  certificate as the body (as account removal answers 410 with its own);
  WS answers a request with `USER_BLOCKED` carrying the request ID.
- **The blocked client keeps only the blocking user's key.** Reeds, profile and
  info are dropped as soon as the certificate verifies, and the follow row
  and list memberships go with the forced unfollow (the same cleanup a
  manual unfollow does). Forgetting the certificate also drops the key
  (`KEY_EVICTION`), so nothing of the blocking user is left.
- **A forgotten certificate is never re-pushed.** The push is delivered once
  and acked; after that the client only gets it back by asking for something
  of the blocking user's.
- **Server-side effects at block time:** the blocked user's follow of the
  blocking user is deleted on both servers, and their `reed_allocations` of the
  blocking user's reeds are dropped, so the server stops routing relay requests
  for those reeds to a device that is about to delete them. The blocked
  user cannot follow again while blocked.
- **Unblocking restores nothing.** No follow, no list membership, no
  content. The blocked user may follow again and fetch content as anyone
  would.
- **The blocking user confirms first.** Tapping **Block** opens a dialog saying
  the blocked user will be forced to delete all of the blocking user's content
  and to unfollow them, and that unblocking won't restore any of it.
- **The blocked profile page** shows that the viewer is blocked, offers
  neither **Follow** nor **Unfollow** (the follow is gone and can't be
  remade), and **Block** / **Unblock**: the blocking user can still see the blocked user's
  profile, so the blocked user may block back. Each direction is its own
  independent block.
- **Fold at the border:** one notice per peer (the blocked user's home
  server), owed until accepted, retried at boot and when that peer announces
  its own boot.
- The blocked user's own content and the blocking user's follow of the blocked
  user are untouched. The blocking user still sees the blocked user's profile
  and reeds unless the blocked user blocks back.
