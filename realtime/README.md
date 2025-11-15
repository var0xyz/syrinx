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
