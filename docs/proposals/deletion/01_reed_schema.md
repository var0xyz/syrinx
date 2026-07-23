# Deletion 01 — Reed-removal schema

## Status

Implemented (DDL + list/tip exclusions). Store helpers deferred to the
handler step that first needs them ([03](03_reed_api.md)).

## Depends on

[00](00_design.md)

## Context

Persist reed-removal certificates so the API can replay them and GET/fanout
can return a stable attestation. See [README](README.md).

## Scope

- DDL for reed-removal storage (`reed_removals`).
- Listing / tip-related queries exclude removed reeds (or treat removal as
  authoritative over a live row).

## Non-goals

- Store / service package helpers (introduce with handlers when used).
- Canonical payload bytes (02).
- HTTP handlers (03).
- Account removals (07).

## Design

### DDL

```sql
CREATE TABLE IF NOT EXISTS reed_removals (
	reed_id VARCHAR(255) UNIQUE NOT NULL,
	user_id VARCHAR(255) NOT NULL REFERENCES users(id),
	user_signature TEXT NOT NULL,
	user_fingerprint VARCHAR(255) NOT NULL,
	server_signature TEXT NOT NULL,
	server_fingerprint VARCHAR(255) NOT NULL,
	server_signed_at TIMESTAMP NOT NULL,

	PRIMARY KEY (user_id, reed_id),
	FOREIGN KEY (user_id, user_fingerprint)
		REFERENCES user_keys(owner, fingerprint)
		ON DELETE CASCADE
);
```

`reed_id` is UNIQUE so reed-only lookups stay efficient; the composite PK
is `(user_id, reed_id)` (user-first convention). No FK to `reeds(id)` so
the live row may be dropped after the cert is stored — the cert is the
source of truth for “gone.” Tip/list queries must exclude ids present in
`reed_removals`.

`user_fingerprint` is the author key that produced `user_signature` (same
pairing as on `users` / `user_key_revocations`).

### Store API (deferred)

When handlers land, helpers should take **both** `userID` and `reedID`
(composite PK), include `user_fingerprint` on the cert struct, and follow
insert-once / conflict-on-mismatch semantics. Do not add unused store
code ahead of callers.

## Test plan

- [x] Tip/list helpers skip removed reed ids (`HasReeds`, realtime diffs)
- [ ] Insert-once / conflict (with store in 03)
