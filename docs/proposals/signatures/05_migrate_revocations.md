# Signatures 05 — Switch `user_key_revocations` to signature FKs

## Status

Proposed.

## Depends on

[02](02_store.md), [03](03_migrate_users.md)

## Context

Revocations store both user and server attestations, with
`user_fingerprint` pairing the user signature (same naming as `users`).

**Blank slate — no migration** (see [README](README.md) / [00](00_design.md)).
Hard cutover: FKs only, no dual-write, no backfill.

## Scope

- Rewrite `user_key_revocations` DDL: `user_signature_id` /
  `server_signature_id` `NOT NULL` FKs; remove inline signature columns
  (including `user_fingerprint` if it only paired the inline user sig —
  fingerprint lives on the user-signature row).
- Revoke / recovery insert: signature rows + FKs only.
- `GetKeyRevocation` loads via FKs; existence checks may stay on the
  revocation row. Wire includes `signedFields`.

## Non-goals

- Unrelated restructuring of the revocation resource.
- Successor bookkeeping columns.
- Dual-write or backfill.

## Design

Prefer landing after 03 so identity and revocation fingerprint/FK naming
stay consistent.

## Test plan

- [ ] Fresh `InitDB` revocations have FKs, no inline signature columns
- [ ] RevokeKey writes FKs only
- [ ] GET revocation wire includes `signedFields` / `server.signedFields`
