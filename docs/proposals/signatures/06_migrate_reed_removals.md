# Signatures 06 — Switch `reed_removals` to signature FKs

## Status

Implemented (`reed_removals` + `account_removals` signature FKs; kept
`user_fingerprint` as signing-key bind). Store / handlers / realtime wire
via `syrinx/signing`.

## Depends on

[02](02_store.md), [deletion 01](../deletion/01_reed_schema.md)

## Context

Reed-removal certs inline the same user/server attestation columns.
Point them at `user_signatures` / `server_signatures` once deletion
schema exists.

**Blank slate — no migration** (see [README](README.md) / [00](00_design.md)).
Hard cutover: FKs only, no dual-write, no backfill.

## Scope

- Rewrite `reed_removals` DDL: user/server signature FKs `NOT NULL`;
  remove inline signature columns.
- Removal insert path: signature rows + FKs only.
- GET 410 body loads via FKs; includes `signedFields`.
- Account-removal table (deletion 07) should follow the same pattern when
  created — either extend this step or add 06b.

## Non-goals

- Deletion protocol / fanout (deletion 02–09).
- Dual-write or backfill.

## Test plan

- [x] Fresh `InitDB` `reed_removals` has FKs, no inline signature columns
- [x] Insert path writes FKs only
- [x] GET 410 body includes flattened cert JSON with `signedFields` /
      `server.signedFields`
