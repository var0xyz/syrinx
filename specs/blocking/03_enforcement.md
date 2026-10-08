# Blocking 03 — 403 + certificate on every read path

## Status

Implemented, local blocks only; peer legs land in [05](05_federation.md).

## Depends on

[02](02_api.md)

## The check

One helper, `DataService.GetBlock(ctx, userID, blockedUserID)`, returning the
certificate or nil, used by every path below. The viewer is the caller's
session user, or, on a peer leg, the user the peer vouches for
([05](05_federation.md)).

Decision order on a user or reed GET, extending account removal's:

**account removal (410) → block (403) → reed removal (410) → 200 → 404**

A removed account answers 410 even to a blocked user: the account is gone
for everyone.

## HTTP

Answer **403** with the certificate as the body (`type: "block"`, as the 410
bodies carry `type: "account"` / `type: "reed"`):

- `GET /users/{blocking user}/profile`, `/info`, `/following`, `/followers`, `/vouches`
- `GET /reeds/{blocking user}/{reedID}` and its `/echoes`, `/chorus`, `/replies`,
  `/ripples`

One helper, `Handlers.refuseIfBlocked` (`blocks.go`), runs before any proxy.
Threads have no GET; a thread is fetched with `REQUEST_THREAD` (below).

The check runs **before** `proxyIfForeign`, so the blocked user's own
server answers from its copy without asking the blocking user's server
([05](05_federation.md) covers the case where its copy hasn't arrived yet).

`/keys/{id}` and `/keys/{id}/revocation` are not checked: the key is needed
to verify the certificate.

## WebSocket

A request the block refuses is answered with `USER_BLOCKED` carrying the
request's ID and the certificate ([04](04_realtime.md)), in place of
`REED_NOT_FOUND` / a profile page:

- `REQUEST_REED`, `REQUEST_THREAD` for a reed authored by the blocking user
- `SUBSCRIBE_PROFILE`, `PROFILE_PAGE` for the blocking user
- `SUBSCRIBE_REED` on a blocking user's reed

The last three carry no request ID, so their `USER_BLOCKED` has none.

The check runs before any holder is chosen, so no `RELAY_REQUEST` is ever
dispatched on the blocked user's behalf. Requests already in flight when the
block lands are dropped with it: the block deletes the blocked user's
pending events for the blocking user's reeds ([02](02_api.md)).

## Fanout

The follow is gone ([02](02_api.md)), but followers are not the only
audience. Every delivery of a reed to a user goes through
`realtimeService.createPendingReedEvent` (and
`createProfileSubscriptionEvent`), so the check sits there: an event for a
recipient the reed's author blocked is not created (`errRecipientBlocked`).
That covers `FOLLOW_REED`, `BROADCAST_REED`, `PIPE_REED`, `REED_REPLY`,
`MENTION`, echoes, profile pages and `ARCHIVE_REED` catch-up in one place.
Removal events (`REED_REMOVED`, `THREAD_REMOVED`) still go through. It is a
primary-key lookup per event; an anti-join on the recipient sets is left
for when fanout cost shows up.

## Holders

A blocked user's device is never picked as a holder for a blocking user's reed:
their allocations were dropped at block time ([02](02_api.md)), and new
ones can't form because the content never reaches them.

## Search

`SearchUsers` drops users who blocked the caller from the merged local and
peer results (`DataService.UsersBlocking`). Peers apply the same filter to the
vouched requester on the `search-users` leg ([05](05_federation.md)).

## Tests

- Each HTTP path above → 403 + certificate for the blocked user, 200 for
  anyone else, 410 for a removed account regardless of block.
- `REQUEST_REED` for a blocking user's reed → `USER_BLOCKED` with the request ID,
  no `RELAY_REQUEST` sent to any holder.
- A new reed by the blocking user reaches followers but not the blocked follower.
- Search by the blocked user omits the blocking user.
