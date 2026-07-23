# Deletion 07 — Account-removal schema + store

## Status

Proposed.

## Depends on

[00](00_design.md)

## Context

Account deletion uses the same attestation model as reed removal. One cert
covers peer purge of that user’s reeds; **public keys remain**. Optional
goodbye note ≤140 characters.

## Scope

- DDL for account-removal certificates (suggested `account_removals`).
- Store: insert-once, get-by-user, exists.
- Define **peer purge set** (minimum): local profile, reeds by author,
  follow edges involving the user, feed caches for that author. **Retain**
  `publicKeys` (and server `public_keys` / `user_keys` as required for
  historical verify).
- Server may mark user tombstoned / disable auth while keeping key material
  and the removal cert.

## Non-goals

- HTTP / 410 profile (08).
- SPA (09).
- Per-reed certs on account delete (explicitly not required).

## Design

### Suggested DDL

```sql
CREATE TABLE IF NOT EXISTS account_removals (
	user_id              VARCHAR(255) PRIMARY KEY REFERENCES users(id),
	note                 VARCHAR(140) NOT NULL DEFAULT '',
	user_signature       TEXT NOT NULL,
	server_signature     TEXT NOT NULL,
	server_fingerprint   VARCHAR(255) NOT NULL,
	server_signed_at     TIMESTAMPTZ NOT NULL
);
```

Enforce note length ≤140 at API and DB.

### Purge set (peer) — resolved minimum

| Drop                         | Keep        |
|------------------------------|-------------|
| Profile / display fields     | Public keys |
| Reeds by this author         | Removal cert (tombstone) |
| Follow / follower edges      |             |
| Allocations / feed entries for their reeds | |

Refine during implementation if more stores exist; document deltas in this
file when shipping. Account catch-up eligibility (who still needs the cert
on `SYNC_REQUEST`) is defined in [08](08_account_api_fanout.md): still
follow **or** still hold allocations for that author’s reeds.

## Test plan

- [ ] Insert-once idempotent; conflicting sigs rejected
- [ ] Note longer than 140 rejected
- [ ] Keys remain queryable after account removal row exists
