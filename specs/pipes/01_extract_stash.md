# Pipes 01 — Extract tags at SignReed; stash until READY

## Status

Implemented.

## Depends on

[00](00_design.md)

## Context

Content is available on `POST /reeds` for verify, then discarded. Live pipe
fanout at `PUBLISH_READY` still needs the tag names for that tip — without
keeping a durable tip index or the body.

## Scope

- Shared tag-extract helper (Go) mirroring SPA `extractTags` / markdown
  hashtag scan used for links.
- At successful SignReed: extract tags; intersect with tags that currently
  have ≥1 pipe subscriber via `FilterSubscribedPipeTags`; store those names
  on the unlogged `pending_fanout` row.
- No durable `reed_tags` table; no cleanup on reed/account removal.

## Non-goals

- WS subscribe handlers (02) — but the intersect needs the in-memory
  subscriber set from 02; land extract/stash wiring with or just after
  subscribe tracking.
- Issuing pipe deliveries (02) — READY only needs tags available to claim.
- Serving tag history over HTTP.

## Design

Extend the existing unlogged `pending_fanout` row (1:1 with tip, already
claimed+deleted at READY):

```sql
CREATE UNLOGGED TABLE pending_fanout (
    user_id VARCHAR(255) NOT NULL,
    reed_id VARCHAR(255) NOT NULL,
    tags    TEXT[] NOT NULL DEFAULT '{}',
    PRIMARY KEY (user_id, reed_id),
    FOREIGN KEY (user_id, reed_id)
        REFERENCES reeds(user_id, id) ON DELETE CASCADE
);
```

- `tags` holds normalized names that had listeners **at SignReed**. Empty
  means no pipe work at READY.
- `ClaimPendingFanout` returns and clears the row (including `tags`).

## Work

1. DDL + `InitDB` (`tags` on `pending_fanout`; blank-slate recreate).
2. `ExtractTags` (SPA parity) + unit tests.
3. SignReed: extract → intersect subscribers → write `tags` in the create TX.
4. Claim returns `tags` for 02; no removal-path deletes.

## Acceptance

- After SignReed, when any extracted tag has a pipe listener, `pending_fanout.tags`
  lists those normalized names for that reed.
- After READY claim (or tip cascade), no tag residue remains.
- No reed body stored for tags.
