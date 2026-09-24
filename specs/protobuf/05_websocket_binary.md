# Protobuf 05 — WebSocket binary cutover

## Status

Done. `realtime.go` accepts binary protobuf only — a text frame is
rejected with a close frame, `handleJSONMessage` and every JSON message
struct/constructor are gone. `serverConnection.ts` encodes/decodes every
message as a binary `WSMessage`, reshaping payloads back to the field
names existing consumers (`+layout.svelte`, `RipplesSection.svelte`, the
reed page) already expect, so no consumer code changed. Federation
(`federation_relay.go`) is unaffected — its HTTP payloads stay JSON, with
one exception: the live reed-stats peer push now carries a base64-wrapped
protobuf `WSMessage` inside its existing JSON envelope, so the payload it
ultimately hands to a local client is the same binary format used
everywhere else.

## Depends on

[02](02_websocket_schema.md)

## Context

`realtime` accepts text JSON and binary protobuf; production SPA sends
JSON. After this step, only binary protobuf remains.

## Scope

- Server: parse binary `WSMessage` only; remove the JSON message branch
  and JSON response helpers.
- `connection_manager.SendToUser` / builders emit marshaled
  `WSMessage` bytes.
- SPA: `binaryType = 'arraybuffer'`; encode outbound with generated
  `WSMessage`; dispatch inbound on `type` / payload oneof.
- Preserve auth query string and event semantics (READY, relay, sync,
  coverage, removals).

## Non-goals

- Changing allocation / fanout / miss policies.

## Work

1. Replace `handleJSONMessage` call sites with protobuf-only handling
   (expand today’s stub `handleProtobufMessage` to the full inventory).
2. Rewrite `realtime/messages.go` constructors to return `*pb.WSMessage`
   (or marshal helpers).
3. Update `serverConnection.ts` send/receive paths.
4. Drop JSON struct types that existed only for WS wire.
5. Update realtime tests and any WS e2e stubs.

## Acceptance

- Text WS frames are ignored or close with a clear error (pick one;
   prefer close after error frame if easy, else log + ignore).
- SPA ↔ server round-trip for `PUBLISH_READY`, `REQUEST_REED` /
  `RELAY_REQUEST`, and `SYNC_REQUEST` works under protobuf.
- Offline-capable SPA still loads; WS simply stays down offline (unchanged).
