# Deletion 01 — Reed-removal schema + store

## Status

Proposed.

## Depends on

[00](00_design.md)

## Context

Persist reed-removal certificates so the API can replay them and GET/fanout
can return a stable attestation. See [README](README.md).

## Scope

- DDL for reed-removal storage (suggested table `reed_removals`).
- Store helpers: insert-once, get-by-reed, exists.
- Listing / tip queries exclude removed reeds (or treat removal as
  authoritative over a live row).

## Non-goals

- Canonical payload bytes (02).
- HTTP handlers (03).
- Account removals (07).

## Design

### Suggested DDL

```sql
CREATE TABLE IF NOT EXISTS reed_removals (
	reed_id              VARCHAR(255) PRIMARY KEY,
	user_id              VARCHAR(255) NOT NULL REFERENCES users(id),
	user_signature       TEXT NOT NULL,
	server_signature     TEXT NOT NULL,
	server_fingerprint   VARCHAR(255) NOT NULL,
	server_signed_at     TIMESTAMPTZ NOT NULL
);

CREATE INDEX IF NOT EXISTS idx_reed_removals_user_id
	ON reed_removals(user_id);
```

`reed_id` need not FK to `reeds(id)` if the live row may be dropped after
cert persistence; the cert is the source of truth for “gone.” If the reed
row is kept for bookkeeping, tip/list queries must still exclude ids present
in `reed_removals`.

### Store API (suggested)

- `InsertReedRemoval(cert) error` — conflict if row exists with different
  signatures; success if identical (idempotent).
- `GetReedRemoval(reedID) (*ReedRemoval, error)`
- `HasReedRemoval(reedID) (bool, error)`

## Test plan

- [ ] Insert once; second insert with same cert succeeds / no-op
- [ ] Second insert with different user or server sig → conflict
- [ ] Tip/list helpers skip removed reed ids
