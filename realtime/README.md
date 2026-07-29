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
split races:

1. Author's browser sends `POST /api/reeds`.
2. Server inserts into `reeds`, inserts into `reed_allocations` (author = first
   holder), returns the countersignature. Fanout waits for **`PUBLISH_READY`**.
3. Client applies the signature, writes the fully signed reed to IndexedDB, then
   sends **`PUBLISH_READY`** `{ reed_id }` over WS.
4. Server deletes the `pending_fanout` row (if present), runs follower / broadcast /
   profile fanout and `dispatchNext(author)`, and always replies
   **`PUBLISH_READY_ACK`** when the tip exists for this author (including when
   fanout already ran). Client keeps ready-pending locally until ACK; reconnect
   resends only unacked READY rows.
5. **`RELAY_MISS`** drops the reporting holder's allocation, resets dispatch,
   and retries another online holder.

Spec: [`specs/publish/`](../specs/publish/README.md).
