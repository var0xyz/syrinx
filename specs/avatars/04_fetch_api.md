# Avatars 04 — `GET /avatars/<hash>`

## Status

Proposed.

## Depends on

[01](01_schema_and_identity.md)

## Context

Peers and the authoring client load PNG bytes by content hash. The
`avatars.hash` index supports lookup; PK remains `user_id`.

## Scope

- `GET /avatars/{hash}` → `200` with `Content-Type: image/png` and raw
  bytes when a row with that hash exists; `404` otherwise.
- Auth policy: same class as other user-visible profile reads (lock
  session-required vs public in implementation to match profile GET).

## Non-goals

- CDN caching headers beyond a sensible immutable hash cache policy.
- Peer relay.

## Work

1. Handler + index query by hash.
2. Test: after PUT, GET returns bytes; after replace, old hash 404s.

## Acceptance

- Fetch by current hash works; stale hash after replace 404s.
