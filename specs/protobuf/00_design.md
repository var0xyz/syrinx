# Protobuf 00 — Design + locked model

## Status

Proposed.

## Depends on

—

## Context

Client↔server traffic today is a mix of JSON WebSocket frames, JSON HTTP
bodies, and `application/x-www-form-urlencoded` / multipart form fields.
`proto/websocket.proto` exists but does not match the live event set; the
SPA and `realtime/` speak JSON structs (`RELAY_REQUEST`, `PUBLISH_READY`,
`DATA_RESPONSE`, …). HTTP handlers encode with `encoding/json` or form
values.

That split means two (or three) hand-maintained shapes per resource, easy
drift between Go and TypeScript, and a WS path that already intended
protobuf but never completed the cutover.

## Scope

- Define **protobuf as the only wire encoding** for HTTP request/response
  bodies and WebSocket application messages between client and server,
  **and** for HTTP request/response bodies between federated servers
  (`federation_relay.go`'s relay RPCs and admin/handshake endpoints).
- Lock shared resource messages and the WS envelope.
- Lock codegen, content types, and hard cutover rules.
- Outline implementable steps ([01](01_shared_messages.md)–[07](07_federation.md)).

## Non-goals

- Changing URL paths, HTTP methods, or WS auth query parameters
  (`userID`, `fingerprint`, `timestamp`, `signature`).
- Changing `BytesToSign`, detached PGP, or nested semantic fields of
  `userSignature` / `serverSignature` ([signatures 08](../signatures/08_wire_nested_blocks.md)).
- Encoding IndexedDB, backups, or `localStorage` as protobuf.
- Replacing the Go channel between HTTP handlers and `realtime/` (in-process
  structs stay).
- Adopting gRPC / Connect streaming; keep existing REST routes and one
  WS endpoint.
- Compressing frames (optional later).

## Design

### Principle

Structured fields on the wire are protobuf messages. Domain meaning
(who signed what, which headers go into `BytesToSign`) stays as today:
receivers unmarshal protobuf → typed fields → verify with the same
helpers.

### Package layout

```
proto/
  common.proto       # UserSignature, ServerSignature, Error, …
  identity.proto     # User, PublicKey, KeyRevocation, …
  reed.proto         # Reed, reed stats, removal certs, …
  invites.proto      # Invite messages
  recovery.proto     # Recovery / status probe bodies as needed
  websocket.proto    # WSMessage envelope + every WS payload
  federation.proto   # Relay RPC + admin/handshake request/response messages
```

Go: `option go_package` under `github.com/alvaro/syrinx/proto/…` (or a
single package if preferred in 01 — lock one style and stick to it).

TypeScript: generated into `spa/src/lib/proto/` (or equivalent); SPA
services import generated types instead of hand-written `api.ts` wire
interfaces where those interfaces only mirrored the wire.

### Shared resources

Every signed resource that already has a nested wire shape gets a proto
message with the same field names in **proto3 JSON-mapping camelCase
discipline for docs**, and **snake_case field names in `.proto` files**
(protoc Go/TS idioms). Semantic parity with today’s nested blocks:

| Proto message     | Role                                                                       |
|-------------------|----------------------------------------------------------------------------|
| `UserSignature`   | `fingerprint`, `armor`                                                     |
| `ServerSignature` | `server_id`, `fingerprint`, `armor`, `timestamp`                           |
| `User`            | Identity record + unsigned hints (`active_key_fingerprint`, counts, …)     |
| `PublicKey`       | Distributed key + `server_signature`                                       |
| `KeyRevocation`   | Revocation cert                                                            |
| `Reed`            | Full reed body + signatures (relay / IndexedDB-shaped payload)             |
| `ReedRemoval`     | Deletion certificates                                                      |
| `AccountRemoval`  | Deletion certificates                                                      |
| `Invite`          | Invite resource                                                            |
| `Error`           | `message` (plain English) + optional machine `code` only if already needed |

Timestamps that are RFC3339 strings on today’s JSON wire stay **string**
fields in proto (do not silently switch to `google.protobuf.Timestamp`
unless both verifiers and `BytesToSign` headers are updated in the same
change — they are not).

### HTTP

- **Request bodies:** `Content-Type: application/x-protobuf` (raw
  `proto.Marshal` bytes). No form-urlencoded or multipart for API
  payloads that are today forms; each endpoint gets an explicit
  `*Request` message.
- **Responses:** same content type for success bodies. `Error` message
  for error bodies; HTTP status codes unchanged; `Error.message` remains
  plain English ([AGENTS](../../AGENTS.md)).
- **Empty bodies:** `204` / no content stays allowed where appropriate.
- **Auth:** existing request-signing headers unchanged
  (`X-Syrinx-*`).
- **Idempotent endpoints** (SignReed replay, removals, invite consume)
  keep the same status semantics (`200` match / `409` mismatch); only
  the body encoding changes.

Handlers gain a small shared codec (decode request → domain / encode
response) so individual handlers do not each call `proto.Marshal`
ad hoc with divergent error paths.

### WebSocket

- **Binary frames only.** Text frames are rejected.
- Single envelope:

```protobuf
message WSMessage {
  MessageType type = 1;
  oneof payload {
    // one field per event; names match today’s type strings’ roles
  }
}
```

- `MessageType` enum lists every live event currently handled in
  `realtime/service.go` / `spa/.../serverConnection.ts`
  (`PING`/`PONG`, subscribe/unsubscribe variants, `SYNC_REQUEST`,
  `REQUEST_REED`, `RELAY_*`, `DATA_*`, `PUBLISH_READY` /
  `PUBLISH_READY_ACK`, `BROADCAST_REED`, removal deliveries,
  `REED_COVERAGE`, `ERROR`, …). The checked-in `websocket.proto` is
  rewritten to this set (02).
- Payload messages carry the fields today’s `data` objects carry
  (`event_id`, `request_id`, `reed_id`, nested `Reed`, certs, …).
- Server→client builders in `realtime/messages.go` emit protobuf
  envelopes instead of JSON structs; `SendToUser` writes binary.
- SPA: `WebSocket` `binaryType = 'arraybuffer'`; encode/decode with
  generated code; `ServerEvent` becomes the generated enum (or a thin
  map over it).

### Federation

Server-to-server traffic (`federation_relay.go`: 23 relay RPC legs plus
~18 admin/handshake endpoints, registered in `main.go`) is signed
HTTP+JSON today and is **in scope**, migrating alongside HTTP in the
same spirit as 03–04 but tracked as its own step ([07](07_federation.md))
since it has its own request/response shapes and registration surface,
distinct from the client-facing `/api/` routes.

Federation's transport-level signature
(`X-Syrinx-Signature`/`X-Syrinx-Public-Key-Id`/`X-Syrinx-Timestamp`,
set by `setPeerProxyAuthHeaders` in `handlers.go` and verified by
`buildCanonicalRequestString` in `middlewares.go`) signs the literal
request body bytes as received, not a re-derived or canonicalized form —
the same mechanism used for regular client request signing. This makes
it encoding-agnostic by construction: it is safe to sign protobuf bytes
exactly as it is safe to sign JSON bytes today, **provided** the
marshaled bytes are produced once and passed through unmodified between
signing and sending, and verified against the unretouched received
bytes. `proto.Marshal` is not guaranteed deterministic across calls, so
neither side may re-marshal a message and expect identical bytes to a
prior marshal of the same message — sign/verify must always operate on
the actual bytes that crossed the wire, never on a re-serialized copy.
This does not change the signature *scheme* (headers, canonical string
shape) — only the body encoding underneath it.

### Cutover

Blank slate with the deployed pair:

1. Land protos + codegen (01–02) without flipping production traffic.
2. Land HTTP codec + switch all routes and `api.ts` together (03–04).
3. Land WS binary + drop the JSON WS branch together (05).
4. Delete hand-maintained wire interfaces that duplicate protos (06).
5. Land federation codec + switch all relay/admin endpoints together (07).

No content-negotiation, no “JSON if Accept says so,” no parallel WS
text path after 05.

### Codegen

- Go: `protoc` + `protoc-gen-go`; commit generated `.pb.go` next to
  protos (same practice as today’s `websocket.pb.go`) **or** generate in
  CI with a checked script — lock one approach in 01.
- TS: `protoc` + a maintained generator (`ts-proto` or protobuf-es);
  SPA build depends on generated output.
- A single `make proto` / `npm run proto` entry regenerates both.

### Testing

- Golden encode/decode vectors for `User`, `Reed`, and one WS round-trip
  (`PUBLISH_READY` → `PUBLISH_READY_ACK` or `RELAY_REQUEST`).
- Existing handler and realtime tests decode protobuf bodies instead of
  JSON/form.
- SPA e2e stubs return protobuf bytes with the correct content type.

## Open points (resolve in 01 / 02)

- Exact `MessageType` numeric assignments (freeze once; never reuse).
- Whether recovery/ops-only HTTP is in the first cut or a follow-up
  within 04 (prefer **all** `/api/` in 04).
- Single Go proto package vs per-file packages.
- Whether each federation relay leg gets its own `*Request`/`*Response`
  pair or several legs share one shape where the fields already
  coincide (resolve in 07).
