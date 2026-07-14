# Realtime WebSocket Service

This package provides real-time WebSocket functionality for the Syrinx application. It handles WebSocket connections, authentication, and message broadcasting using Go channels for communication with the main HTTP API.

## Features

- **WebSocket Connection Management**: Handles client connections with proper authentication
- **PGP+base64 Authentication**: Uses signature-based authentication with base64-encoded PGP signatures
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

WebSocket connections require PGP+base64 authentication via headers:

- `X-Syrinx-User-Id` - User ID
- `X-Syrinx-Fingerprint` - Public key fingerprint
- `X-Syrinx-Signature` - Base64-encoded PGP signature
- `X-Syrinx-Algorithm` - "PGP+base64"
- `X-Syrinx-Timestamp` - Unix timestamp for replay protection

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

Reeds are currently published via `POST /api/reeds` (REST), while everything else
in the lifecycle (allocation tracking, holder selection, content relay) happens
over the WebSocket. This split creates a race:

1. Author's browser sends `POST /api/reeds`.
2. Server inserts into `reeds`, inserts into `reed_allocations` (author = first
   holder), and broadcasts the new reed to followers and broadcast subscribers
   — all inside the same HTTP handler.
3. For each subscriber the server creates a `pending_events` row and calls
   `dispatchNext(author)`, which sends a `RELAY_REQUEST` back to the author
   over WS.
4. That `RELAY_REQUEST` typically arrives on the author's browser **before**
   the POST response has been parsed and the reed has been written to
   IndexedDB. The author's handler can't find the reed locally and replies
   `RELAY_MISS`.

Previously `handleRelayMiss` reacted to this by deleting the author's
allocation and trying to find another holder. Since the author was the only
holder, the reed ended up with zero allocations and the subscriber's pending
event sat forever — the reed was effectively orphaned the moment it was
created.

#### Temporary mitigation

`handleRelayMiss` now ignores misses entirely: the allocation is preserved,
`dispatched_at` stays set, and no retry is attempted. The stuck pending event
is recovered the next time the requester sends `SYNC_REQUEST` (on reconnect
or refresh), which calls `redispatchPendingRequests` to reset `dispatched_at`
and re-dispatch to the (still-allocated) author. By then the author has
finished `storeReedInIndexedDB` and answers `RELAY_RESPONSE` correctly.

This is correct but lossy in the sense that recovery requires the requester
to reconnect; subscribers who stay connected can wait indefinitely.

#### Planned fix: move publishing to WebSocket

The proper fix is to make reed publishing a WS-native flow so the server has
explicit, ordered control over the "reed is now available" signal:

1. Client sends a `PUBLISH_REED` (name TBD) message over the existing WS
   connection with the reed payload + author signature.
2. Server stores the reed, allocates it to the author, replies with a
   `PUBLISH_ACK` carrying the server signature.
3. Client receives the ACK, writes the fully-signed reed to IndexedDB, and
   sends a `PUBLISH_READY` (or similar) message confirming local
   availability.
4. **Only on receipt of `PUBLISH_READY`** does the server fan the reed out
   to followers / broadcast subscribers via the normal relay machinery.

This removes the race by construction: the server never dispatches a
`RELAY_REQUEST` for a reed the author hasn't already confirmed they can
serve. Once this is in place, `handleRelayMiss` can go back to actually
trimming stale allocations (with a real retry/unavailability policy) instead
of silently ignoring misses.
