# Protobuf 02 — WebSocket schema

## Status

Proposed.

## Depends on

[01](01_shared_messages.md)

## Context

Live WS traffic is JSON with string `type` discriminators. The checked-in
`proto/websocket.proto` only sketches ping/subscribe/notification and is
not what production sends.

## Scope

- Replace `proto/websocket.proto` with a `WSMessage` envelope whose
  `MessageType` + `oneof payload` cover **every** client→server and
  server→client event handled in `realtime/service.go` and
  `serverConnection.ts` (including publish-ready, relay, sync, profile
  and reed subscriptions, coverage, removal deliveries).
- Reuse shared messages from 01 inside payloads (e.g. `Reed` inside
  `DataResponse`, cert messages inside removal deliveries).
- Freeze enum numbers; document the assignment table in the proto file
  comments.

## Non-goals

- Flipping the runtime to binary yet (05).
- Changing event semantics (fanout, miss, READY idempotency).

## Work

1. Inventory message types from `realtime/messages.go`,
   `handleJSONMessage`, and `ServerConnection.send` / `onmessage`.
2. Define one payload message per type; nest `Reed` / certs where
   today’s `data` carries full objects.
3. Regenerate Go/TS; keep JSON path working until 05 (new types unused
   or used only in tests).

## Acceptance

- Proto enum ↔ live event set is 1:1 (review against the inventory).
- Generated Go types build; no runtime switch yet.
