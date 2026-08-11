# Likes 01 — `reeds_liked` schema + denormalized like count

## Status

Proposed.

## Depends on

[00](00_design.md)

## Context

Persist like certificates so the API can replay them idempotently and the
stats bar can read a live count without scanning. Follow the current
signature-storage model — FK columns to `user_signatures` /
`server_signatures`, not inline signature text
([signatures 00](../signatures/00_design.md)) — and the denormalized-counter
pattern already used for coverage
([coverage 01](../coverage/01_counts.md)).

## Scope

- DDL for `reeds_liked` (one row per `(liker, author, reed)`).
- `reeds.like_count` column, bumped in the same TX as insert/delete.
- Indexes for: "is this reed liked by user X" (single lookup, drives the
  filled/outlined button) and "list this user's liked reeds, newest
  first" (drives [06](06_spa_liked_feed.md)).

## Non-goals

- Canonical payload bytes (02).
- HTTP handlers (03).
- Realtime wire (04).

## Design

### DDL

```sql
CREATE TABLE IF NOT EXISTS reeds_liked (
    liker_user_id       VARCHAR(255) NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    author_user_id       VARCHAR(255) NOT NULL,
    reed_id               VARCHAR(255) NOT NULL,
    liker_fingerprint     VARCHAR(255) NOT NULL,
    user_signature_id     INT NOT NULL REFERENCES user_signatures(id),
    server_signature_id   INT NOT NULL REFERENCES server_signatures(id),

    PRIMARY KEY (liker_user_id, author_user_id, reed_id),
    FOREIGN KEY (author_user_id, reed_id) REFERENCES reeds(user_id, id),
    FOREIGN KEY (liker_user_id, liker_fingerprint)
        REFERENCES user_keys(owner, fingerprint)
        ON DELETE CASCADE
);

CREATE INDEX IF NOT EXISTS idx_reeds_liked_liker_created
    ON reeds_liked (liker_user_id, user_signature_id DESC);

CREATE INDEX IF NOT EXISTS idx_reeds_liked_reed
    ON reeds_liked (author_user_id, reed_id);
```

`(liker_user_id, author_user_id, reed_id)` as the PK gives:

- **O(1) existence check** for "did I like this?" — the exact PK lookup.
- **FK to `reeds(user_id, id)`** the same composite shape `reed_replies`
  already uses, so a like cannot be recorded against a nonexistent reed.
- `liker_fingerprint` mirrors `reed_removals.user_fingerprint` — the key
  that actually produced `user_signature_id`'s signature, paired the same
  way against `user_keys`.

`idx_reeds_liked_liker_created` orders a user's own liked-reeds list. It
sorts on `user_signature_id` as a proxy for insertion order (monotonic
`SERIAL`) rather than adding a redundant `liked_at` timestamp — the
server's countersign timestamp already lives on `server_signatures.signed_at`
and is the value shown to the user; see [06](06_spa_liked_feed.md) for the
join that surfaces it. If a dedicated `liked_at` column proves clearer in
implementation, adding one is a local decision, not a spec blocker.

No FK from `reeds_liked` back to `reeds.like_count` — the counter is
maintained procedurally (same-TX bump), not derived by a trigger, matching
`allocation_count`'s existing convention (no triggers anywhere in this
codebase today).

### `reeds.like_count`

```sql
ALTER TABLE reeds ADD COLUMN IF NOT EXISTS like_count INT NOT NULL DEFAULT 0;
```

Invariant: `reeds.like_count` equals `COUNT(*) FROM reeds_liked WHERE
author_user_id = reeds.user_id AND reed_id = reeds.id` after every
successful TX that touches `reeds_liked` for that reed — same invariant
shape as `allocation_count` ([coverage 01](../coverage/01_counts.md)).

### Same-TX bump sites

| Event | Counter action |
|-------|-----------------|
| Like insert (`POST`, first time for this `(liker, reed)`) | `like_count += 1` |
| Like insert, idempotent replay (row already exists) | no change |
| Unlike (`DELETE`, row existed) | `like_count -= 1` |
| Unlike, idempotent replay (row already gone) | no change |
| Reed removal cascade | row(s) in `reeds_liked` for that reed become orphaned bookkeeping; no special handling needed since the reed itself is gone and no longer displayed (consistent with how `reed_echoes` targeting a removed reed is handled — see [conversations](../conversations/README.md)) |
| Account removal (author of liked reeds) | out of scope here — governed by [deletion 07](../deletion/07_account_schema.md)'s existing peer-purge set; add `reeds_liked` to that purge set as an implementation-time checklist item, not a new design decision |

**Idempotent like:** conflict on insert (already liked) → no increment,
return existing cert (matches reed-removal idempotency posture in
[deletion README](../deletion/README.md#resolved) item 3).

**Unlike is a plain hard delete, unsigned** (see
[00](00_design.md#unlike-is-unsigned) and [03](03_api.md)): `DELETE FROM
reeds_liked WHERE liker_user_id=$1 AND author_user_id=$2 AND reed_id=$3`,
decrementing `like_count` only if a row was actually deleted. Deleting a
nonexistent row is a no-op, not an error.

### Reading "is this liked by me"

```sql
SELECT 1 FROM reeds_liked
WHERE liker_user_id = $1 AND author_user_id = $2 AND reed_id = $3;
```

Single PK-indexed lookup; called once on reed-detail load (or folded into
the existing reed-fetch query as a `LEFT JOIN EXISTS` if preferred at
implementation time — not a spec-level decision).

## Test plan

- [ ] Insert-once / conflict-on-replay semantics (paired with store in 03)
- [ ] `like_count` invariant holds after insert, replay-insert, delete,
      replay-delete
- [ ] FK to `reeds(user_id, id)` rejects a like against a nonexistent reed
- [ ] `idx_reeds_liked_liker_created` returns a user's likes newest-first
