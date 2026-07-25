# Signatures 04 — Switch `user_keys` to server signature FK

## Status

Implemented (`user_keys.server_signature_id`; signup / AddPublicKey /
recovery / GetPublicKey via `syrinx/signing`).

## Depends on

[02](02_store.md)

## Context

Distributed user keys carry only a **server** countersignature today
(`server_signature`, `server_fingerprint`, `server_signed_at`).

**Blank slate — no migration** (see [README](README.md) / [00](00_design.md)).
Hard cutover: FK only, no dual-write, no backfill.

## Scope

- Rewrite `user_keys` DDL: `server_signature_id NOT NULL` FK; remove
  inline server signature columns.
- Key upload / recovery insert: insert server signature row, store FK
  only.
- Key GET paths load via FK

## Non-goals

- User signatures on keys (none today).
- Predecessor / revocation linkage changes.
- Dual-write or backfill.

## Test plan

- [x] Fresh `InitDB` `user_keys` has FK, no inline server signature columns
- [x] AddPublicKey / recovery insert populate FK only
- [x] GetPublicKey wire uses serverSignature
