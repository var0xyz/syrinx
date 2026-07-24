# Signatures 03 — Switch `users` to signature FKs

## Status

Implemented (users FKs only; signup/update/recovery/GetUser via
`syrinx/signing`). Kept `users.user_fingerprint` as active-key hint.

## Depends on

[02](02_store.md)

## Context

`users` currently inlines `user_signature`, `user_fingerprint`,
`server_signature`, `server_fingerprint`, and `server_signed_at`.

**Blank slate — no migration** (see [README](README.md) / [00](00_design.md)).
Hard cutover: FKs only, no dual-write, no backfill, no leftover inline
columns.

## Scope

- Rewrite `users` DDL: `user_signature_id` / `server_signature_id`
  `NOT NULL` FKs; remove inline signature columns (and
  `user_fingerprint` unless kept as an active-key hint — see open Q in
  00).
- Signup / profile update / recovery upsert: insert signature rows, store
  FKs only.
- `GetUser` (and equivalents) load via FKs; wire includes `signedFields`.

## Non-goals

- Dual-write or backfill.
- Switching other entities (04–06).

## Design

Active-key hint (`ActiveKeyFingerprint` / `SignatureFingerprint`) stays
on the wire — either a denormalized column on `users` or derived from
the user-signature row (open Q in 00).

## Test plan

- [x] Fresh `InitDB` `users` has FKs, no inline signature columns
- [x] Signup / update / recovery write signature rows + FKs only
- [x] GetUser wire includes `signedFields` / `server.signedFields`
