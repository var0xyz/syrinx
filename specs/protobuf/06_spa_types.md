# Protobuf 06 — SPA types cleanup

## Status

Proposed.

## Depends on

[04](04_http_endpoints.md), [05](05_websocket_binary.md)

## Context

Hand-written `src/frontend/src/lib/types/api.ts` wire interfaces duplicate
generated protos, and `serverConnection.ts` reshapes decoded WS payloads
back to the field names consumers expected from the old JSON wire (05).

## Scope

- Route SPA services and verifiers through generated proto types (or
  thin domain adapters where IndexedDB needs a concrete class such as
  `Reed`).
- Delete obsolete wire-only TypeScript interfaces and the WS reshaping
  layer.
- Ensure verifiers still rebuild `canonicalJSON` payloads from the same logical
  fields after unmarshal.

## Non-goals

- Redesigning IndexedDB schemas.
- Changing UI behavior.

## Work

1. Replace imports of wire interfaces with generated messages where
   identical.
2. Keep small adapters only where the UI/repository needs methods or
   class instances.
3. Grep for leftover `JSON.stringify` / `JSON.parse` on network paths;
   leave localStorage/sessionStorage alone.

## Acceptance

- Network I/O paths use protobuf types.
- `api.ts` / `serverConnection.ts` no longer define parallel wire DTOs
  for migrated resources.
- Client signature verification tests still pass.
