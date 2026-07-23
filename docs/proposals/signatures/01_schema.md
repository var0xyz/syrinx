# Signatures 01 — DDL for `user_signatures` + `server_signatures`

## Status

Proposed.

## Depends on

[00](00_design.md)

## Context

Create the two shared attestation tables before any entity migrates.
See [README](README.md).

**Blank slate — no backwards compatibility** (see [README](README.md) /
[00](00_design.md)).

## Scope

- `CREATE TABLE` for `user_signatures` and `server_signatures` in `InitDB`.
- Indexes as needed (e.g. by `fingerprint` if lookups appear).

## Non-goals

- Entity FK columns (03–06).
- Store helpers (02).

## Design

Use the DDL from [00](00_design.md). Prefer `SERIAL` ids (FKs are `INT`).
Do not FK `fingerprint` to `user_keys` / `private_keys` in v1 — historical
keys and server-key rotation already rely on fingerprint lookup tables
separately.

## Test plan

- [ ] Fresh `InitDB` creates both tables
- [ ] Insert/select roundtrip on each table (smoke)
