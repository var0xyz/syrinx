# Blocking 05 — Blocks across peers

## Status

Implemented (`blocks.go`, legs in `federation_relay.go`).

## Depends on

[03](03_enforcement.md), [04](04_realtime.md)

## Shape

Blocking user on A, blocked user on B. A holds the block as the source of truth
and sends it to B once. B stores a copy, enforces it for its own user, and
delivers it to them. A user on a third server C is unaffected; only B's
user was blocked.

Both servers enforce:

- **B** refuses its user from its copy, before proxying or relaying
  anything, so most refusals never cross the border.
- **A** refuses on every leg that names the requester, so a block holds
  while B's copy is still on its way.

## Notices (A → B)

| Leg | Body | B's answer |
|-----|------|------------|
| `POST /api/federation/relay/block-notify` | the certificate | 204 |
| `POST /api/federation/relay/unblock-notify` | `user_id`, `blocked_user_id` | 204 |

One per block, not per anything else: only one remote user is involved.

A records what it owes each peer:

```sql
CREATE TABLE IF NOT EXISTS block_peer_notices (
  server_id  VARCHAR(16)  NOT NULL REFERENCES servers(id) ON DELETE CASCADE,
  user_id VARCHAR(255) NOT NULL REFERENCES identities(id) ON DELETE CASCADE,
  blocked_user_id VARCHAR(255) NOT NULL REFERENCES identities(id) ON DELETE CASCADE,
  kind       VARCHAR(8)   NOT NULL CHECK (kind IN ('block', 'unblock')),
  PRIMARY KEY (server_id, user_id, blocked_user_id)
);
```

It is the peer-side twin of `user_block_events` ([04](04_realtime.md)):
upserted by the same `oweBlockEventTx` in the block / unblock transaction,
replacing whatever was still owed for the pair, and deleted on a 2xx if no
later notice replaced it. Sends to one peer are serialized, so a block and
its lift can't cross on the wire and the peer always ends on the latest
state. Retried when A boots and when B announces its own boot,
as owed key revocations are (`sendOwedKeyRevocations`), so nothing
accumulates.

## Receiving a block (B)

- `user_id` must belong to the calling peer; `blocked_user_id` must be local
  to B.
- Verify the blocking user's signature (key fetched from A if not cached) and A's
  countersignature against A's pinned key, or the key the signature names if
  A rotated since (`verifyPeerCountersignature`).
- Upsert the blocking user's identity; store the certificate in `user_blocks`
  (idempotent; a newer `signedAt` replaces a stored block, an older one is
  ignored).
- Delete its own `user_following` row for its user and the blocking user (A
  deleted its remote-follower record when the block was made), drop B's
  records of its user holding the blocking user's reeds, and cancel its user's
  outstanding relays for them.
- Push `USER_BLOCKED` to the user as in [04](04_realtime.md).

A tampered certificate, one countersigned by another server, or one naming
a user who is not B's is refused (400) and stores nothing.

## Receiving an unblock (B)

- `user_id` must belong to the calling peer.
- Delete the stored block, which owes the user `USER_UNBLOCKED` as in
  [04](04_realtime.md).

## Requester on legs A serves

Every leg where B asks A for something of A's user on behalf of B's user
carries that user, and A applies [03](03_enforcement.md)'s check to them
(`refuseBlockedRequester`):

- `relay/request`, `relay/subscribe-reed`: already carry `requester_user_id`.
- `relay/profile-page`: gains `requester_user_id`, which must belong to the
  calling peer.
- Proxied GETs (`proxyToPeer`: profile, info, reed, lists): the viewer goes
  in a `requester` query parameter, which the peer request signature covers
  (`setPeerProxyAuthHeaders` signs method, path and query).
  `Handlers.requestViewer` accepts it only for a user of the calling peer,
  as `resolveFollower` does for `followerID`.

Not checked on A: `relay/fallback-request` (A asks a holder's server, after
A has already checked the request) and `relay/search-users` (B filters the
merged results with its own copy, [03](03_enforcement.md)).

A refusal on a leg is **403** with the certificate. B stores any block it is
refused with, after the same verification as a block notice
(`acceptRefusalBlock`, hooked into `callPeerRelayEndpoint` and
`proxyToPeer`), which also pushes it to the user. That makes the refusal
itself a second delivery path. A proxied GET streams the 403 body through
unchanged; a WS request answered 403 resolves as not found, the pushed
`USER_BLOCKED` carrying the reason.

## Fanout across the border

A's fanout to peers (`new-reed` and the rest) is per peer, not per user, so
A cannot filter B's users. B filters: foreign reeds are distributed through `openForeignEvent`, which
goes through the same `createPendingReedEvent` check against B's copy.

## Tests

- Block of a remote user → one block-notify; B stores, deletes its follow
  row, pushes, acks; A drops the owed notice and its remote-follower record.
- B unreachable → notice stays owed and is sent when B announces its boot.
- Tampered or foreign-countersigned certificate → 400, nothing stored.
- Block, unblock, block before B is reachable → B ends with the second block.
- Unblock → block gone, the user owed one lift; an unblock naming another
  server's user → 400.
- B without a copy proxies a profile GET → A answers 403 + certificate, B
  stores it.
- A's new reed reaches B's other followers, not the blocked one.
