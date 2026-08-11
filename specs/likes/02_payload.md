# Likes 02 — Like canonical payload + countersign

## Status

Proposed.

## Depends on

[00](00_design.md)

## Context

Author and server must sign the same canonical bytes, following the
shared `BytesToSign` envelope convention
([proposals README § Shared conventions](../README.md#shared-conventions))
and mirroring reed removal's payload shape
([deletion 02](../deletion/02_payload.md)) as closely as possible, since
liking is structurally the same "user attests to a fact about a reed,
server countersigns once" shape — just with a third identity in play (the
liker is not necessarily the reed's author).

**Unliking is not signed** — see [00 § Unlike is unsigned](00_design.md)
and [03](03_api.md). This document covers **like** only.

## Scope

- User-signed payload headers/content for **like**.
- Server countersign payload (binds user signature + server timestamp +
  fingerprint), same shape as `BuildReedRemovalServerPayload`.
- `identity` helpers: `BuildReedLikeUserPayload`,
  `BuildReedLikeServerPayload` — verification via `crypto.Service` against
  rebuilt payloads (same as identity / revocation / reed removal — no
  unused verify wrappers ahead of handlers, per existing convention).
- Wire `type: "reed_like"` on the JSON resource and in signed headers.

## Non-goals

- Unlike payload/signing — there is none (see Context).
- HTTP handlers (03).
- Realtime wire (04).

## Design

### User-signed payload

Headers:

- `type: reed_like`
- `serverID: <serverID>`
- `authorID: <authorID of the reed being liked>`
- `reedID: <reedID>`
- `likerID: <userID of the person liking>`

Content: empty.

Signed by the liker's **active** key.
Helper: `identity.BuildReedLikeUserPayload(serverID, authorID, reedID, likerID string) []byte`.

`likerID` is included explicitly as a header (not merely inferred from
"whoever authenticated the HTTP request") for the same reason `userID`
appears explicitly in the reed-removal payload
([deletion 02](../deletion/02_payload.md)): the signed bytes must fully
determine what was attested, independent of any request-context field
that isn't itself part of what got signed. `authorID` is likewise
explicit since a like's target reed is identified by the composite
`(authorID, reedID)`, exactly like every other reed reference in this
codebase (`echoing`, `replying`, removal, echo/reply index rows).

### Server countersign payload

Headers:

- `type: reed_like`
- `serverID`, `authorID`, `reedID`, `likerID`
- `signedAt` (wire `server.timestamp`)
- `serverKeyFingerprint`
- `userSignature`

Content: empty.

Helper: `identity.BuildReedLikeServerPayload(serverID, authorID, reedID, likerID, serverKeyFingerprint, userSignatureB64 string, signedAt time.Time) []byte`.

Same binding rationale as reed removal's server payload
([deletion 02 § Server countersign payload](../deletion/02_reed_payload.md)):
the countersignature attests not just to the fact of the like, but to
having verified *this specific* user signature, preventing a server
signature from being replayed against a different (unverified) user
signature.

### Verification

- Reject if the reed identified by `(authorID, reedID)` does not exist
  (or is removed — see [01](01_schema.md) same-TX bump table).
- Reject if `likerID` does not match the authenticated requester (the
  signed header must agree with who is actually calling the endpoint —
  same posture as reed removal rejecting a `userID` that isn't the
  reed's owner).
- Reject if the signature fails to verify.
- **No restriction on `likerID == authorID`** (self-likes count, per
  [00 § Resolved #4](00_design.md#resolved)) — this is a deliberate
  absence of a check, not an oversight; do not add a self-like guard
  without a spec update.

### Idempotency note

A like resource can be created, deleted (via unsigned unlike, [03](03_api.md)),
and re-created over time by the same user (like → unlike → like again).
"Idempotent" here means: *replaying the same submit* (e.g. retry after a
dropped response) returns the same stored cert rather than erroring or
double-counting — not that the resource is permanently write-once. See
[03](03_api.md) for the exact API-level idempotency contract.

## Test plan

- [ ] Roundtrip: sign → countersign → verify (like)
- [ ] Tamper `reedID` / `authorID` / `likerID` → verify fails
- [ ] `likerID` mismatched against authenticated requester → rejected
- [ ] JSON / header `type` is `"reed_like"`
- [ ] Self-like (`likerID == authorID`) verifies and stores successfully
