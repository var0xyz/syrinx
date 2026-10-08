# Protobuf 07 — SPA on generated types everywhere

## Status

Proposed. Final step of the track.

## Depends on

[04](04_http_endpoints.md), [05](05_websocket_binary.md),
[06](06_federation.md)

## Context

04 and 05 keep the SPA's consumers unchanged: `api.ts` and
`serverConnection.ts` decode protobuf and convert each message back to
the hand-written shapes in `src/frontend/src/lib/types/api.ts` (old
camelCase names such as `userID`, `int64` timestamps turned into ISO
strings). That decode layer is a stopgap: the same resource is described
twice, once in `.proto` and once in TypeScript.

## Scope

- Every SPA consumer — services, stores, components, verifiers and
  IndexedDB repositories — uses the generated protobuf-es types
  directly.
- Delete the HTTP and WS conversion layers and every wire-only interface
  in `lib/types/api.ts` that a generated message replaces.
- Timestamps stay `bigint` unix seconds in memory; format them only at
  display and when rebuilding a `canonicalJSON` payload.
- IndexedDB stores the generated shape (blank slate: recreate local
  data, no migration of stored records).

## Non-goals

- Changing UI behavior.
- Changing what is signed (`canonicalJSON` payloads stay byte-identical).

## Work

1. Swap consumers resource by resource (users/keys, reeds/threads,
   removals, ripples, vouches, invites, federation admin).
2. Update verifiers to rebuild payloads from generated fields; keep
   `test:signing` / `test:verify-binary` green.
3. Remove the decode-to-old-shape helpers from `api.ts` and
   `serverConnection.ts`.
4. Grep for leftover `JSON.stringify` / `JSON.parse` on network paths;
   leave localStorage/sessionStorage alone.

## Acceptance

- No hand-written TypeScript type mirrors a protobuf message.
- `api.ts` / `serverConnection.ts` return generated messages unchanged.
- Client signature verification tests still pass.
