# Pipes 01 — Tag index at SignReed

## Status

Proposed.

## Depends on

[00](00_design.md)

## Context

Content is available on `POST /reeds` for verify, then discarded. Live pipe
fanout at `PUBLISH_READY` still needs to know which tags a tip carries.

## Scope

- Shared tag-extract helper (Go) mirroring SPA `extractTags` / markdown
  hashtag scan used for links.
- Table `reed_tags` (tip index only): which tags appear on which reed.
- Insert at successful SignReed; delete on reed removal / account removal.

## Non-goals

- WS subscribe (02).
- Serving tag history over HTTP.

## Design

```sql
CREATE TABLE reed_tags (
    reed_id   VARCHAR(255) NOT NULL,
    user_id   VARCHAR(255) NOT NULL REFERENCES users(id),
    tag       VARCHAR(255) NOT NULL,
    signed_at TIMESTAMP NOT NULL,
    PRIMARY KEY (reed_id, tag)
);

CREATE INDEX idx_reed_tags_tag ON reed_tags (tag, signed_at DESC);
```

(`user_id` + `reed_id` should match the tip’s author; FK to `reeds` as
appropriate for this codebase’s reed PK shape.)

## Work

1. DDL + `InitDB`.
2. Extract + insert in SignReed TX with tip/allocation.
3. Cleanup beside mention/echo index deletion paths.
4. Tests: multi-tag reed; case fold; removal clears rows.

## Acceptance

- After publish, `reed_tags` lists normalized tags for that reed.
- No reed body stored for tags.
