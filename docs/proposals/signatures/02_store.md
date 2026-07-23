# Signatures 02 — Store helpers

## Status

Proposed.

## Depends on

[01](01_schema.md)

## Context

Call sites need a single place to insert and load attestation rows so
migrate steps and handlers do not open-code SQL.

**Blank slate — no backwards compatibility** (see [README](README.md) /
[00](00_design.md)). Wire assembly always includes `signedFields`; do not
omit it for older clients.

## Scope

- Insert user signature → id (`fingerprint`, `signature`, `signed_fields`,
  algorithm default).
- Insert server signature → id (same plus `signed_at`).
- Get by id (user / server), including `signed_fields`.
- Assemble wire `Signature` / user sig fields from loaded rows,
  including `signedFields` from `signed_fields`.

## Non-goals

- Writing FKs onto entity tables (per-entity switch steps).
- HTTP handlers (handlers call these helpers).
- Querying / filtering by `signed_fields`.
- Dual-write or backfill.

## Design

Helpers live in `syrinx/signing` (`store.go`). Keep them thin (`*sql.DB` /
`*sql.Tx` aware so migrate steps can share a transaction).

Inserts take `signed_fields []string` (Postgres `TEXT[]`). Call sites that
know the cover set pass it; unknown cover set may pass nil/`{}`. Wire
assembly maps that slice to JSON `signedFields` (see [00](00_design.md#wire-boundary)).

## Test plan

- [x] Insert + get user signature (roundtrip `signed_fields`)
- [x] Insert + get server signature (timestamp truncated to seconds like today;
  `signed_fields` roundtrip)
- [x] Assembled wire includes `signedFields` for user and server shapes
