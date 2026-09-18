# Protobuf 07 — Federation relay + admin protos

## Status

Proposed.

## Depends on

[03](03_http_codec.md)

## Context

Federation (server-to-server) traffic is signed HTTP+JSON today:
`federation_relay.go` defines 23 relay RPC legs (register-request,
subscribe, deliver, not-held, cancel, ack, unsubscribe, subscribe-reed,
unsubscribe-reed, reed-stats, reply-notify, echo-notify, mention-notify,
reply-removal-notify, echo-removal-notify, holder-notify,
fallback-request, new-reed-notify, search-users,
reply-removal-to-viewer, disconnect-notify, account-removal-notify,
reed-removal-notify) plus roughly 18 admin/handshake endpoints
(invitations, servers, attempts, connect), all registered under
`/api/federation/*` in `main.go`. Each leg has its own ad hoc JSON
request/response struct pair in `federation_relay.go` (e.g.
`relayRequestPayload`, `relayRequestResponse`), marshaled and sent by
`callPeerRelayEndpoint`.

This is a distinct registration surface from the client-facing `/api/`
routes covered by [04](04_http_endpoints.md) — different callers
(peer servers, not end-user clients), different payload shapes, and a
transport-level signature applied identically to both (see
[00](00_design.md)'s Federation section for the signing-safety
constraint this step must honor).

## Scope

- Define per-RPC-leg `*Request`/`*Response` proto messages for all 23
  relay legs and the ~18 admin/handshake endpoints, mirroring today's
  structs in `federation_relay.go` field-for-field.
- Reuse shared resource messages from [01](01_shared_messages.md)
  (`Reed`, `ReedRemoval`, `AccountRemoval`, certs) inside federation
  payloads wherever a leg already carries a full resource, exactly as
  HTTP and WS do.
- Switch `callPeerRelayEndpoint` and every `...FromPeer` handler to the
  codec from [03](03_http_codec.md): `Content-Type:
  application/x-protobuf`, `proto.Marshal`/`proto.Unmarshal` in place
  of `encoding/json`.
- Preserve the sign-once / no-re-marshal rule from
  [00](00_design.md): `setPeerProxyAuthHeaders` signs the exact
  marshaled bytes that are sent; `buildCanonicalRequestString` verifies
  against the exact bytes received. Neither side re-serializes a
  message and compares against a fresh marshal.

## Non-goals

- Changing the peer signature *scheme* — headers
  (`X-Syrinx-Signature`, `X-Syrinx-Public-Key-Id`,
  `X-Syrinx-Signature-Scope`, `X-Syrinx-Timestamp`) and the canonical
  string shape (`method + path[?query] + "\n\n" + body + "\n\n" +
  timestamp`) are unchanged; only what "body" contains changes.
- Changing federation admission/handshake trust logic (pinned key
  fingerprint checks, peer discovery) — encoding only.
- Client↔server HTTP ([04](04_http_endpoints.md)) or WebSocket
  ([05](05_websocket_binary.md)) — those are separate steps.

## Work

1. Inventory every request/response struct pair in `federation_relay.go`
   (grep for `type relay.*Payload`/`type relay.*Response` and
   equivalent admin/handshake structs) and author matching proto
   messages in `proto/federation.proto`.
2. Migrate `callPeerRelayEndpoint` (and any other ad hoc peer-HTTP
   callers) to the shared codec from 03.
3. Migrate each `...FromPeer` handler to decode protobuf request
   bodies and encode protobuf responses.
4. Update `federation_test.go`, `federation_handshake_test.go`, and
   `federation_relay_test.go` to send/receive protobuf.
5. Confirm no re-marshal occurs between sign and send, or between
   receive and verify, in the migrated code paths (see signing-safety
   constraint above) — add a test that signs and verifies the same
   marshaled bytes across two independent `proto.Marshal` calls of an
   equal message to catch any accidental re-marshal-and-compare bug.

## Acceptance

- No federation request or response body uses JSON encoding.
- `callPeerRelayEndpoint` and all `...FromPeer` handlers use the codec
  from 03.
- Existing federation behavioral tests (relay request/response/miss,
  removal notify, handshake) pass under protobuf.
- Peer signature verification still passes end-to-end between two
  local server instances with protobuf bodies.
