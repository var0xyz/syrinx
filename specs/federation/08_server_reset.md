# Federation 08 — Server reset notice

## Status

**Implemented.** `realtime-reset` leg, `servers.down_at`, boot-time
`ClearRealtimeState`, `forgetPeer` on reset and revocation, and the
`PEER_SERVER_LOST` WebSocket message. Delivery that keys off `down_at` is
[09](09_reed_delivery.md).

## Depends on

[05](05_revoke_established.md) (revocation), [06](06_content_relay.md)
(relay requests and subscriptions between peers)

## Context

Realtime state between two servers lives on both sides: a peer's users
hold profile and reed subscriptions here, our users hold subscriptions to
the peer's profiles and reeds, and relay requests wait on the other side.
When a server goes down, none of that is torn down. Its peers keep
subscriptions for users who are gone, and our users keep waiting on
requests nobody will answer.

A clean shutdown can say so. A crash can't, so the server says it again
when it comes back up.

## Design

### The notice

`POST /api/federation/relay/realtime-reset`, peer-authenticated like every
other relay leg, body `{"reason": "shutdown" | "boot"}`. Anything else is
`400`.

Sent to every peer in `ListConnectedPeers`, in parallel, best-effort,
bounded by one 5-second timeout for the whole fan-out:

- **shutdown** — from the SIGTERM path, before WebSocket clients are
  closed.
- **boot** — after the server has cleared its own realtime state
  (`ClearRealtimeState`: presence, profile and reed subscriptions, every
  pending event, local and foreign alike) and wired its peer hooks.

Both are gated on `CLEAR_PRESENCE_ON_BOOT`, the single-replica switch.
With several replicas, one of them restarting must not tell peers to
forget the whole server.

### What the receiver does

Either reason means the sender kept nothing, so the receiver drops
everything shared with it (`forgetPeer`), in both directions:

| Direction | Dropped |
|---|---|
| From the peer | profile and reed subscriptions whose viewer is on the peer; the peer's relay requests (`pending_events` rows behind `foreign_relay_requests`) |
| To the peer | our viewers' subscriptions to the peer's profiles and reeds; our relay requests waiting on it, failed through the existing not-held path so the client isn't left waiting |

Our viewers whose subscriptions were dropped get a `PEER_SERVER_LOST`
WebSocket message carrying the server's id and name. The SPA shows a
warning that live updates from that server have stopped.

Then:

- **shutdown** sets `servers.down_at`. Delivery skips a peer that is down
  ([09](09_reed_delivery.md)).
- **boot** clears `down_at` and resumes delivery to it.

`servers.connected` is not reused for this: it means "handshake
confirmed", drives the mesh page badge, and filters `ListConnectedPeers`.

### Revocation

Revoking a peer, by our admins or by the peer's `disconnect-notify`, runs
the same `forgetPeer` without contacting the peer.

## Non-goals

- **Re-subscribing.** Nothing restores a dropped subscription. The
  viewer's client subscribes again the next time it opens that profile or
  reed, and those requests work as soon as the peer is back.
- A "reconnected" message to viewers.
- Marking a peer down because a request to it failed. Only its own
  shutdown notice does that.
- Multiple replicas. See `RISKS.md`.

## Tests

`realtime_peer_reset_test.go`, against the real schema:

- `forgetPeer` drops subscriptions and relay requests in both directions,
  fails our waiting requests, and leaves another peer's rows alone
- the affected local viewers are reported, and the peer's name resolves
  even once it is revoked
- `shutdown` sets `down_at`, `boot` clears it, an unknown reason is `400`
- `ClearRealtimeState` empties every realtime table
