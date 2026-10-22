# HA 05 — The gateway

## Status

Proposed.

## Depends on

[04](04_connection_holder.md)

## What it is

`syrinx-gateway`, built from the root package with `//go:build gateway`
(like `ops`). It runs on the LB host behind nginx's `/ws/` location. It has
no database, no keys and no business logic. It:

- accepts client WebSockets, after a replica approves the handshake;
- keeps one socket per user;
- answers heartbeats and closes silent sockets ([03](03_presence_in_memory.md));
- forwards each user's frames to one replica, and writes replicas' frames
  to sockets;
- moves users off a replica that goes away ([06](06_replica_loss.md)).

| Variable | Meaning |
|---|---|
| `GATEWAY_LISTEN` | Client side, e.g. `127.0.0.1:8070` (nginx in front) |
| `GATEWAY_INTERNAL_LISTEN` | Replica side, e.g. `10.0.0.20:8071` |
| `GATEWAY_REPLICAS` | `app-1=10.0.0.21:8080,app-2=…`: id and API address of each replica |
| `GATEWAY_TOKEN` | Shared with replicas |
| `GATEWAY_ORPHAN_GRACE` | How long to keep sockets open with no replica up (default 60 s) |

## Handshake

1. Client opens `/ws/?publicKeyId=…&signature=…&timestamp=…&nonce=…&deviceId=…`
   as today. The gateway does not upgrade yet.
2. It picks a live replica (any) and calls
   `POST /internal/ws-auth` on it with the query string, authenticated with
   `GATEWAY_TOKEN`. The replica runs exactly what `HandleWebSocket` runs
   before upgrading today: `authenticateWebSocket` (signature, nonce,
   revoked key), the suspension refusal and the device check. It answers
   `{userID}` or the same refusal it would give today (status + reason,
   which the gateway relays as the HTTP response).
3. The gateway upgrades, mints a `connID`, closes the user's older socket if
   any (one socket per user, as `registerClient` does today), and sends
   `OPEN` to the user's replica.

`/internal/*` is registered only in HA, on the app's normal listener but
outside `/api` (no response signing, no signature auth), and refused
without the token. The app host's firewall admits it only from the LB.

## Routing

The user's replica is chosen by **rendezvous hashing** of `userID` over the
replicas whose internal link is up. All of a user's frames go there, in
order, so per-user handling stays sequential as it is today. Adding or
removing a replica moves only the users whose top choice changed.

## Internal link

One WebSocket per replica, opened by the replica to
`GATEWAY_INTERNAL_LISTEN` at boot and kept open (reconnect with backoff),
carrying `proto/gateway.proto`:

```proto
message GatewayEnvelope {
  enum Kind {
    OPEN = 0;          // gw → replica: conn_id, user_id, device_id
    FRAME = 1;         // gw → replica: a client frame (WSMessage bytes)
    CLOSE = 2;         // gw → replica: conn_id closed; user_still_connected
    MOVED_IN = 3;      // gw → replica: like OPEN, for a user moved here
    SEND_USER = 10;    // replica → gw: frame for user_id
    SEND_CONN = 11;    // replica → gw: frame for conn_id
    DISCONNECT = 12;   // replica → gw: close user_id's socket
    DISCONNECT_ALL = 13;
    REPLICA_LOST = 20; // gw → replica: re-queue lost replica's relays
    DRAIN = 21;        // replica → gw: move my users away, I'm stopping
  }
  Kind kind = 1;
  string conn_id = 2;
  string user_id = 3;
  string device_id = 4;
  bytes frame = 5;
  bool user_still_connected = 6;
  string replica_id = 7;
}
```

Frames are the client's protobuf bytes, forwarded untouched. The gateway
decodes only the message type, to handle `PING`/`PONG` itself.

Replica side, `gatewayHolder` ([04](04_connection_holder.md)) turns `OPEN`,
`FRAME`, `CLOSE` into `handleOpen`/`handleFrame`/`handleClose`, and
`SendToUser`/`DisconnectUser` into `SEND_USER`/`DISCONNECT`. Any replica
may send to any user: delivery is not limited to the user's own replica.

Presence (`online_users`) is written by the user's replica on `OPEN` and
deleted on `CLOSE` with `user_still_connected = false`, as today.

## Moving a user (`MOVED_IN`)

When a user's replica changes (06, or a drain), the gateway sends
`MOVED_IN` to the new replica and a `SESSION_MOVED` message to the client
on its existing socket. The SPA handles `SESSION_MOVED` by running
`resubscribeAll()` and `syncRequest()`, which it already does on every
reconnect, without reconnecting. Frames that were in flight to the old
replica are lost; the catch-up covers what they would have delivered, and
client requests awaiting a reply time out and retry as they do across a
reconnect. Implementation must audit each inbound message type and confirm
it is safe to receive twice (most already de-duplicate by event or request
id); any that isn't gets an id check.

`SESSION_MOVED` is a new `MessageType` in `websocket.proto`.

## Draining (rolling update)

On SIGTERM a replica sends `DRAIN`, the gateway moves that replica's users
to others (as above) and stops routing to it; the replica finishes in-flight
work, closes its link and exits. Its users notice a resubscribe, no
reconnect.

## Tests

- Handshake refusals (bad signature, revoked key, suspended, wrong device)
  reach the client with today's status and reason.
- Two connects for one user: the older socket is closed; the replica gets
  `CLOSE(user_still_connected = true)` and doesn't tear down.
- With two replicas, frames for one user all land on one replica, in order.
- Drain moves users; the SPA resubscribes without reconnecting.
