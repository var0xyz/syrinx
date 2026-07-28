# Realtime WebSocket Service

This package provides real-time WebSocket functionality for the Syrinx application. It handles WebSocket connections, authentication, and message broadcasting using Go channels for communication with the main HTTP API.

## Features

- **WebSocket Connection Management**: Handles client connections with proper authentication
- **PGP signature authentication**: Base64-encoded detached PGP signatures over the timestamp
- **Subscription Management**: Supports user-specific and broadcast subscriptions
- **Real-time Broadcasting**: Broadcasts reed notifications, user updates, and other events
- **Online User Tracking**: Tracks online users in the database
- **Protobuf Messages**: Uses Protocol Buffers for efficient message serialization

## Architecture

The realtime service runs as a goroutine within the main application and communicates via Go channels:

```
Main App HTTP API → Go Channel → Realtime Service → WebSocket Clients
```

## Message Types

### Client to Server
- `PING` - Ping message for connection health
- `SUBSCRIBE_USER` - Subscribe to user-specific notifications
- `SUBSCRIBE_BROADCAST` - Subscribe to all service traffic
- `UNSUBSCRIBE_USER` - Unsubscribe from user notifications
- `UNSUBSCRIBE_BROADCAST` - Unsubscribe from broadcast

### Server to Client
- `PONG` - Response to ping
- `SUBSCRIBED` - Confirmation of subscription
- `REED_NOTIFICATION` - New reed created/updated
- `USER_UPDATE` - User profile updated
- `ERROR` - Error message

## Authentication

WebSocket connections authenticate via query parameters (browsers cannot set
custom headers on the upgrade request):

- `userID` — User ID
- `fingerprint` — Public key fingerprint
- `signature` — Base64-encoded PGP signature over the timestamp
- `timestamp` — Unix timestamp for replay protection

## Usage

The service is automatically started when the main application starts:

```go
// In main.go
realtimeService := realtime.NewService(db, cryptoService, userService)
broadcastChan := make(chan realtime.BroadcastMessage, 100)
go realtimeService.Start(broadcastChan)

// Pass channel to handlers
h := NewHandlers(services, cfg, broadcastChan)
```

## Broadcasting Messages

To broadcast messages from the HTTP API:

```go
// Broadcast new reed
h.broadcastChan <- realtime.BroadcastMessage{
    Type:   realtime.NewReed,
    UserID: userID,
    ReedID: reedID,
    Data: map[string]interface{}{
        "username": username,
        "content":  content,
    },
}
```

## Testing

Use the test client to verify WebSocket functionality:

```bash
go run test_websocket_client.go
```

Note: The test client currently connects without authentication for testing purposes.

## Known Issues

### Publish/relay race on freshly-created reeds

Reeds are published via `POST /api/reeds` (REST countersign), while allocation
tracking, holder selection, and content relay happen over the WebSocket. That
split races today:

1. Author's browser sends `POST /api/reeds`.
2. Server inserts into `reeds`, inserts into `reed_allocations` (author = first
   holder), returns the countersignature, and (today) broadcasts the new reed
   to followers / broadcast subscribers inside the same HTTP handler path.
3. For each subscriber the server creates a `pending_events` row and calls
   `dispatchNext(author)`, which sends a `RELAY_REQUEST` back to the author
   over WS.
4. That `RELAY_REQUEST` typically arrives **before** the POST response has been
   parsed and the reed has been written to IndexedDB. The author cannot find
   the reed locally and replies `RELAY_MISS`.

Previously `handleRelayMiss` deleted the author's allocation and tried another
holder. With the author as sole holder, the reed could end up with zero
allocations and a stuck pending event — orphaned at birth.

#### Temporary mitigation (current code)

`handleRelayMiss` ignores misses: the allocation is preserved, `dispatched_at`
stays set, and no retry is attempted. The stuck pending event is recovered the
next time the requester sends `SYNC_REQUEST` (reconnect / refresh), which
redispatches to the still-allocated author. By then the author has usually
finished `storeReed` and can `RELAY_RESPONSE`.

Correct but lossy: subscribers who stay connected can wait indefinitely.

#### Planned fix: HTTP countersign + WS `PUBLISH_READY`

Do **not** move countersigning onto the WebSocket. Keep `POST /reeds` for
verify + countersign + tip + author allocation. Gate **fanout** on an explicit
local-availability signal:

1. `POST /reeds` — persist tip and author allocation; return server signature;
   **do not** fan out (`NewReed` / pending events / `RELAY_REQUEST`).
2. Client applies the signature, writes the fully signed reed to IndexedDB, then
   sends **`PUBLISH_READY`** `{ reed_id }` over WS.
3. Server checks the tip exists and the caller is the author; marks the reed
   announced (`fanout_ready`); **only then** runs normal follower / broadcast /
   profile fanout and `dispatchNext`.
4. READY is **idempotent**. On reconnect, the client sends READY for
   self-authored reeds it holds locally; the server no-ops if already
   announced.
5. All `RELAY_REQUEST`s are handled the same way (no “just published”
   special case). Once READY gates fanout, **`RELAY_MISS` means real
   unavailability again**: remove that holder’s allocation and retry another
   holder.

Spec: [`specs/publish/`](../specs/publish/README.md).
