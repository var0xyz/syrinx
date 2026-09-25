# Attestations 01 — `user_vouches` schema

## Status

Implemented.

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
    withdrawal_signature_id        INT REFERENCES user_signatures(id),
    withdrawal_server_signature_id INT REFERENCES server_signatures(id)
);

CREATE TABLE IF NOT EXISTS user_vouches_active (
    vouch_id        VARCHAR(255) PRIMARY KEY REFERENCES user_vouches(id) ON DELETE CASCADE,
    voucher_user_id VARCHAR(255) NOT NULL,
    subject_user_id VARCHAR(255) NOT NULL,
    subject_key_id  VARCHAR(255) NOT NULL,
    UNIQUE (voucher_user_id, subject_key_id)
);
```

### History is append-only; the active set is separate

`user_vouches` holds **every attestation ever made**, each keyed by its own
random id. Withdrawing adds signatures to a row; re-vouching inserts a new row
rather than reviving the old one. The withdrawal a re-vouch replaces therefore
stays fetchable and verifiable — a peer still reconciling can prove the
retraction happened instead of seeing the vouch silently live again.

`user_vouches_active` carries one row per **live** attestation and is what every
list read joins. The uniqueness lives there, where it is actually true: one live
vouch per voucher per subject key, while history holds as many withdrawn rows as
that pair accumulates. Withdrawing deletes the active row in the same
transaction that writes the signatures.

There is **no `withdrawn_at`**. The withdrawal countersignature carries the
authoritative time, as every other signed resource does.

Unbounded withdraw/re-vouch cycling is bounded by a 24h cooldown per
`(voucher, subject key)` ([03](03_api.md#post-vouches)), not by the schema.

**The active set is keyed `(voucher_user_id, subject_key_id)`, not
`(voucher_user_id, subject_user_id)`.** One live vouch per voucher per *key* —
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
(federation), who has no `users` row here. The `voucher_user_id` FK stays even
under federation: a vouch is only ever stored on the voucher's own server, so
every row in this table has a local voucher
([08](08_federation.md#the-local-row-for-a-foreign-counterparty)).

### The subject key id is the tamper evidence

`subject_key_id` is what a vouch asserts, so it is also what a client
compares against later: a vouch naming one key while the server serves
another is the substitution alarm
([07](07_spa_trust_display.md#relay-refusal)). A key id's last segment is the
OpenPGP primary-key fingerprint, a hash of the key material, so it cannot be
minted for a key its owner does not hold, and holding the id is as good as
holding the key for the purpose of detecting a swap.

### Indexes

```sql
CREATE INDEX IF NOT EXISTS idx_user_vouches_voucher_created
    ON user_vouches(voucher_user_id, created_at DESC);

CREATE INDEX IF NOT EXISTS idx_user_vouches_subject_key
    ON user_vouches(voucher_user_id, subject_key_id, created_at DESC);

CREATE INDEX IF NOT EXISTS idx_user_vouches_active_subject
    ON user_vouches_active(subject_user_id);

CREATE INDEX IF NOT EXISTS idx_user_vouches_active_voucher
    ON user_vouches_active(voucher_user_id);
```

Three reads drive everything:

- **"Who vouched for this user?"** — profile display
  ([07](07_spa_trust_display.md)), and the edges a client walks inbound.
  Served from the active set.
- **"Who has this user vouched for?"** — the caller's own audit list
  ([07](07_spa_trust_display.md#your-vouches-chronologically)), which walks
  history so withdrawn rows are visible.
- **"When did this voucher last vouch for this key?"** — the cooldown check
  on create.

### Withdrawal is a state, not a delete

The two withdrawal signature columns record that the voucher retracted, and the
row stays. A bare `DELETE` would be wrong twice: the
server could silently drop vouches it dislikes and claim they were
withdrawn, and a client that already cached the vouch has no signed evidence
of the retraction. The withdrawal is itself a signed cert
([02](02_payload.md)).

Contrast [likes](../likes/README.md), where unlike is an unsigned hard
delete. That is fine for a counter; it is not fine for a security claim.

### Void and stale are derived, not stored

There is no `void` or `stale` column. The withdrawal signatures are the only
retraction this table records; everything else depends on the current
revocation state of the *subject* key, which changes without this table being
touched. Computing it on read keeps one source of truth
([04](04_revocation.md)).

## Testing

- Insert, then re-insert the same `(voucher, subject_key)` → idempotent.
- Vouch for a second key of the same subject → two live rows.
- Withdraw → history row retained with both signatures, active row gone.
- Re-vouch after a withdrawal → a **new** row with a new id; the withdrawn
  original keeps its retraction and is still fetchable by id.
- Re-vouch inside the cooldown → refused.
- Voucher's key revoked → row untouched and still live.
- Subject on a foreign server inserts without a `users` row.
