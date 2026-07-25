# Signatures 02 — Store helpers

## Status

Implemented (`syrinx/signing` store helpers + tests).

## Depends on

[01](01_schema.md)

## Context

Call sites need a single place to insert and load attestation rows so
migrate steps and handlers do not open-code SQL.

**Blank slate — no backwards compatibility** (see [README](README.md) /
[00](00_design.md)).

## Scope

- Insert user signature → id (`fingerprint`, `signature`).
- Insert server signature → id (same plus `signed_at`).
- Get by id (user / server).
- Assemble wire blocks from loaded rows ([08](08_wire_nested_blocks.md)).

## Non-goals

- Writing FKs onto entity tables (per-entity switch steps).
- HTTP handlers (handlers call these helpers).
- Dual-write or backfill.

## Design

Helpers live in `syrinx/signing` (`store.go`). Keep them thin (`*sql.DB` /
`*sql.Tx` aware so migrate steps can share a transaction).

Wire assembly maps rows to nested `userSignature` / `serverSignature`
([08](08_wire_nested_blocks.md)).

## Test plan

- [x] Insert + get user signature roundtrip
- [x] Insert + get server signature (timestamp truncated to seconds like today)
- [x] Assembled wire matches [08](08_wire_nested_blocks.md)
