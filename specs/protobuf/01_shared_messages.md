# Protobuf 01 — Shared resource messages + codegen

## Status

Proposed.

## Depends on

[00](00_design.md)

## Context

HTTP and WS both need the same resource shapes. Generate them once.

## Scope

- Add `proto/common.proto`, `identity.proto`, `reed.proto`,
  `invites.proto`, and recovery-related request/response messages as
  needed for live `/api/` routes.
- Map today’s nested wire fields
  ([signatures 08](../signatures/08_wire_nested_blocks.md),
  `spa/src/lib/types/api.ts`) onto proto3 messages.
- Establish `make proto` / `npm run proto` and Go + TS generation output
  locations (lock the choice called out as open in 00).

## Non-goals

- WebSocket envelope (02).
- Switching handlers or the SPA to use the generated types yet (03–06).

## Work

1. Author `.proto` files for every resource returned or accepted by HTTP
   today (`User`, `PublicKey`, `KeyRevocation`, `Reed`, removal certs,
   invites, server info, stats, probe bodies, …).
2. Add `Error` with a plain-English `message` field.
3. Wire codegen; commit or CI-generate `.pb.go` and TS modules.
4. Add encode/decode golden tests for `User` and `Reed` (round-trip
   bytes).

## Acceptance

- Regenerating protos is one documented command.
- Go and TS both compile against the new messages.
- Goldens cover signature-block nesting parity with today’s field set.
