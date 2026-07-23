# Deletion 02 — Reed-removal canonical payload + countersign

## Status

Proposed.

## Depends on

[01](01_reed_schema.md)

## Context

Author and server must sign the same canonical bytes. Follow shared
`BytesToSign` conventions ([proposals README](../README.md)).

## Scope

- User payload headers/content for reed removal.
- Server countersign payload (binds user signature + server ts + fingerprint).
- `identity` (or deletion package) helpers: build, verify user, countersign,
  verify server.
- Wire `type: "reed"` on the JSON resource and in signed headers
  (`type: reed`). Same spelling everywhere.

## Non-goals

- HTTP (03).
- Account payload (07/08) — mirror this step’s patterns there.

## Design

### User-signed payload

Headers (illustrative):

- `type: reed`
- `serverID: <serverID>`
- `userID: <authorID>`
- `reedID: <reedID>`

Content: empty (or omit).

Signed by the author’s **active** key.

### Server countersign payload

Headers: user headers + `timestamp` / `fingerprint` / `serverID` as needed to
match other resources, plus binding of `userSignature` (same class as
identity / revocation countersign).

Content: empty.

Wire resource matches [README example](README.md#example--reed).

### Verification

- Reject if `userID` ≠ reed owner when a live reed row exists.
- Reject if signatures fail.
- On accept path (03): only the reed’s author may submit.

## Test plan

- [ ] Roundtrip: sign → countersign → verify both
- [ ] Tamper `reedID` / `userID` → verify fails
- [ ] JSON `type` is `"reed"`
