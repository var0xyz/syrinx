# Deletion 02 — Reed-removal canonical payload + countersign

## Status

Implemented (identity payload helpers + tests).

## Depends on

[01](01_reed_schema.md)

## Context

Author and server must sign the same canonical bytes. Follow shared
`BytesToSign` conventions ([proposals README](../README.md)).

## Scope

- User payload headers/content for reed removal.
- Server countersign payload (binds user signature + server ts + fingerprint).
- `identity` helpers: build user payload, build server countersign payload.
  Verification is via `crypto.Service` against rebuilt payloads (same as
  identity / revocation) — no unused verify wrappers ahead of handlers.
- Wire `type: "reed"` on the JSON resource and in signed headers
  (`type: reed`). Same spelling everywhere (`identity.TypeReed`).

## Non-goals

- HTTP (03).
- Account payload (07/08) — mirror this step’s patterns there.

## Design

### User-signed payload

Headers:

- `type: reed`
- `serverID: <serverID>`
- `userID: <authorID>`
- `reedID: <reedID>`

Content: empty.

Signed by the author’s **active** key.
Helper: `identity.BuildReedRemovalUserPayload`.

### Server countersign payload

Headers (revocation / identity class — `userSignature` bound as a header):

- `type: reed`
- `serverID`, `userID`, `reedID`
- `signedAt` (wire `server.timestamp`)
- `serverKeyFingerprint`
- `userSignature`

Content: empty.

Helper: `identity.BuildReedRemovalServerPayload`.

Wire resource matches [README example](README.md#example--reed).

### Verification

- Reject if `userID` ≠ reed owner when a live reed row exists.
- Reject if signatures fail.
- On accept path (03): only the reed’s author may submit.

## Test plan

- [x] Roundtrip: sign → countersign → verify both
- [x] Tamper `reedID` / `userID` → verify fails
- [x] JSON / header `type` is `"reed"` (`TypeReed`)
