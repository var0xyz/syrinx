# HA 04 — Sockets behind a connection-holder interface

## Status

Proposed.

## Depends on

[03](03_presence_in_memory.md)

## Context

`realtimeService` reads frames from and writes frames to
`*websocket.Conn`s it owns through `realtimeConnectionManager`. In HA the
sockets are in another process ([05](05_gateway.md)). Before building the
gateway, split "what to do with a frame" from "where the socket is", so
single mode keeps its exact behaviour and HA swaps only the bottom layer.

## Change

Introduce the seam the rest of realtime talks to:

```go
// connectionHolder is where client sockets live: in process (single)
// or in the gateway (ha).
type connectionHolder interface {
	SendToUser(userID string, msg *pb.WSMessage) bool
	SendToConn(connID string, msg *pb.WSMessage) bool
	DisconnectUser(userID string)
	DisconnectAll()
	HasConnection(userID string) bool
}
```

and one inbound entry point both implementations call:

```go
func (rs *realtimeService) handleOpen(s *realtimeSession)
func (rs *realtimeService) handleFrame(s *realtimeSession, msg *pb.WSMessage)
func (rs *realtimeService) handleClose(s *realtimeSession, userStillConnected bool)
```

`realtimeSession` is today's `realtimeClient` minus the `*websocket.Conn`:
`connID`, `userID`, `deviceID`, subscription flags, sync request id. The
socket-owning part (conn, `writeMu`, `lastSeen`) stays in the holder.

- **single**: `localHolder` is today's `realtimeConnectionManager`, renamed
  only if needed for the split. `HandleWebSocket` authenticates, upgrades,
  then loops calling `handleFrame`. Nothing observable changes.
- **ha**: `gatewayHolder` ([05](05_gateway.md)).

The `handleClose` flag replaces `UnregisterClient`'s "still online"
return: teardown runs only when the user has no socket left anywhere.

This step is a refactor. It lands with single mode only and the full test
suite unchanged.

## Tests

Existing realtime tests pass unchanged against `localHolder`; a fake holder
lets new tests drive `handleOpen`/`handleFrame`/`handleClose` without
sockets.
