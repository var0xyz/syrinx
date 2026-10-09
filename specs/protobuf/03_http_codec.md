# Protobuf 03 — HTTP codec

## Status

Implemented. Go: `protowire.go` (`writeResponse` takes a `proto.Message`,
`writeError`, `readRequest`, which rejects a non-protobuf body and
accepts an empty one). SPA: the `protoBody`/`request`/`readMessage` helpers in `api.ts`
(the interim `wire.ts` decode layer was removed in 07). Request
and response signatures now cover raw body bytes on both sides
(`request-signer.ts`, `service-worker.ts`, `responseVerifier.ts`).

## Depends on

[01](01_shared_messages.md)

## Context

Handlers mix `json.Encoder`, form parsing, and ad hoc content types.
A single decode/encode path keeps status codes and error English while
standardizing bodies.

## Scope

- Shared helpers: read body → `proto.Message`, write message with
  `Content-Type: application/x-protobuf`, write `Error` on failure
  paths that today encode JSON/plain maps.
- Request middleware or helper that rejects non-protobuf bodies for
  routes that require a body (clear English error).
- Mirror helpers in the SPA (`api.ts` transport layer) for
  `fetch` + `Uint8Array` bodies / arrayBuffer responses.

## Non-goals

- Migrating every endpoint in this step (04).
- WS (05).

## Work

1. Implement Go `WriteProto` / `ReadProto` (names as fit existing style).
2. Implement SPA `requestProto` / `parseProto` next to `apiService`.
3. Unit-test content type and malformed-body handling.
4. Optionally migrate one low-risk read endpoint end-to-end as a pathfinder
   (e.g. server info) to prove the codec — or defer all routes to 04.

## Acceptance

- Codec tests pass.
- Documented content type and error body shape match [00](00_design.md).
