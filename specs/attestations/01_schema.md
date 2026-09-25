# Attestations 01 — `user_vouches` schema

## Status

Proposed.

## Depends on

[00](00_design.md)

## Context

Persist vouch certificates so the API can replay them idempotently and a
profile can list who vouched for a user without scanning. Follow the shared
signature-storage model — FK columns to `user_signatures` /
`server_signatures`, not inline signature text
([signatures 00](../signatures/00_design.md)).

**Blank slate — no migration.**

## Scope

- DDL for `user_vouches`.
- Indexes for the two hot reads.
- Withdrawal and void state.

## Non-goals

- Canonical payload bytes ([02](02_payload.md)).
- HTTP handlers ([03](03_api.md)).
- Void *semantics* on revocation ([04](04_revocation.md)); this step only
  provides the column.

## Design

### DDL

```sql
CREATE TABLE IF NOT EXISTS user_vouches (
    voucher_user_id      VARCHAR(255) NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    voucher_key_id       VARCHAR(255) NOT NULL,
    subject_user_id      VARCHAR(255) NOT NULL,
    subject_key_id       VARCHAR(255) NOT NULL,
    user_signature_id    INT NOT NULL REFERENCES user_signatures(id),
    server_signature_id  INT NOT NULL REFERENCES server_signatures(id),
    created_at           TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP,
    withdrawn_at         TIMESTAMP,
    withdrawal_signature_id INT REFERENCES user_signatures(id),
    PRIMARY KEY (voucher_user_id, subject_key_id)
);
```

**Primary key is `(voucher_user_id, subject_key_id)`, not
`(voucher_user_id, subject_user_id)`.** One vouch per voucher per *key* —
vouching for Bob's new key after a rotation is a new row, and the old row
survives to support the "previously verified" signal
([00](00_design.md#rotation-ends-a-vouch)). Keying on `subject_user_id`
would make re-verification overwrite history, which is exactly the evidence
[04](04_revocation.md) needs.

`voucher_key_id` is stored, not derived. The signature must be verified
against the key that produced it, which may since have been rotated away from
or revoked — a vouch stays valid through both
([04](04_revocation.md#a-vouch-belongs-to-the-person-not-the-key)). Resolving
it later from the voucher's *current* key would verify against the wrong key
and fail. It also groups the audit list
([07](07_spa_trust_display.md#your-vouches-chronologically)).

`subject_user_id` has no FK. A vouch may name a user on another server
(federation), who has no `users` row here.

### Indexes

```sql
CREATE INDEX IF NOT EXISTS idx_user_vouches_subject
    ON user_vouches(subject_user_id) WHERE withdrawn_at IS NULL;

CREATE INDEX IF NOT EXISTS idx_user_vouches_voucher
    ON user_vouches(voucher_user_id) WHERE withdrawn_at IS NULL;
```

Two reads drive everything:

- **"Who vouched for this user?"** — profile display
  ([07](07_spa_trust_display.md)), and the edges a client walks inbound.
- **"Who has this user vouched for?"** — the outbound edges, which is what
  path finding actually traverses ([05](05_trust_paths.md)).

Both are partial indexes on live rows; withdrawn vouches are read only on
the audit path.

### Withdrawal is a state, not a delete

`withdrawn_at` plus `withdrawal_signature_id` record that the voucher
retracted, and the row stays. A bare `DELETE` would be wrong twice: the
server could silently drop vouches it dislikes and claim they were
withdrawn, and a client that already cached the vouch has no signed evidence
of the retraction. The withdrawal is itself a signed cert
([02](02_payload.md)).

Contrast [likes](../likes/README.md), where unlike is an unsigned hard
delete. That is fine for a counter; it is not fine for a security claim.

### Void and stale are derived, not stored

There is no `void` or `stale` column. `withdrawn_at` is the only retraction
this table records; everything else depends on the current revocation state of
the *subject* key, which changes without this table being touched. Computing
it on read keeps one source of truth ([04](04_revocation.md)).

## Testing

- Insert, then re-insert the same `(voucher, subject_key)` → idempotent.
- Vouch for a second key of the same subject → two live rows.
- Withdraw → row retained, `withdrawn_at` set, absent from partial indexes.
- Voucher's key revoked → row untouched and still live.
- Subject on a foreign server inserts without a `users` row.
