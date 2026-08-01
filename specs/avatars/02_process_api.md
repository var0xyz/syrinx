# Avatars 02 — Process API

## Status

Proposed.

## Depends on

[01](01_schema_and_identity.md)

## Context

Clients need a deterministic, server-attested PNG before they can put
`avatarHash` in a signed profile.

## Scope

- Authenticated `POST` (path locked in implementation, e.g.
  `/api/avatars/process`) accepting image bytes.
- Validate square; enforce upload size / dimension caps.
- Resize to 256×256; quantize to ≤256 colors; encode PNG.
- Return optimized PNG bytes + server attestation over
  `(userID, hash)` (plus any locked headers such as `type` /
  `signedAt`).
- Stateless: do **not** write `avatars` here.

## Non-goals

- Profile PUT (03).
- Client crop UI (05) — server only rejects non-square.

## Work

1. Define canonical attestation payload helper next to other
   `BytesToSign` builders; document headers in code comment.
2. Implement process handler; rate-limit / max body as appropriate.
3. Tests: non-square 400; happy path hash matches returned bytes;
   attestation verifies for caller userID.

## Acceptance

- Process returns PNG whose SHA-256 is the attested hash.
- Attestation fails verification if userID or hash is swapped.
- No DB avatar write on process alone.
