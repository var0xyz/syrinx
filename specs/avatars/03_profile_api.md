# Avatars 03 — Profile PUT (set / keep / clear)

## Status

Proposed.

## Depends on

[01](01_schema_and_identity.md), [02](02_process_api.md)

## Context

Profile update already replaces the full signed identity. Avatar bytes and
process attestation join that write in one transaction when the hash
changes or is cleared.

## Scope

- Extend `PUT /users/me` (or successor body shape) with optional image
  bytes + process attestation alongside the signed identity fields.
- **Set/replace:** verify user identity signature; `SHA-256(png) ==
  avatarHash`; verify process attestation for `(caller, hash)`; upsert
  `avatars` in the **same TX** as the identity update.
- **Unchanged:** `avatarHash` equals stored hash; no image; leave
  `avatars` row untouched.
- **Clear:** empty `avatarHash` (omitted from signed bytes) and
  null/absent image; delete `avatars` for caller in the same TX.
- Reject mixed / inconsistent combinations listed in [00](00_design.md).

## Non-goals

- Re-inspecting PNG dimensions/palette on PUT (attestation + hash
  equality only).
- `GET /avatars/<hash>` (04).

## Work

1. Wire request fields (multipart or protobuf-ready parts — match current
   stack; prefer a shape that can become `bytes` later).
2. Branch set / keep / clear; keep existing identity no-op via
   `userSignature` byte-equality.
3. Tests for each branch and rejection cases (hash mismatch, missing
  bytes on change, clear with leftover hash, forged attestation).

## Acceptance

- Changing avatar without a valid process attestation fails.
- Keep-hash without bytes succeeds and does not delete the row.
- Clear removes the row and leaves identity with empty hash.
- Identity + avatar write/rollback together (TX).
