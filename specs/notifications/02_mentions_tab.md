# Notifications 02 — Mentions tab (list API + SPA)

## Status

Proposed.

## Depends on

[00](00_glossary_and_design.md), [01](01_everyone_handle.md)

## Context

`reed_mentions` has been populated since
[conversations 04](../conversations/04_mentions.md) landed, but nothing
lets a user browse "reeds that mention me" — a mention is only
discoverable today by already being in the right conversation thread.
This step gives it a dedicated read surface, and — combined with
[01](01_everyone_handle.md) — is also where `@everyone` broadcasts
actually become visible to recipients.

## Scope

- `GET /users/me/mentions?limit=&before=` — list reeds that mention the
  caller, newest first, cursor-paginated.
- SPA mentions tab: filtered view of the caller's mentions, with an
  unread-style affordance.

## Non-goals

- No "mark as read" state — see [00](00_glossary_and_design.md)'s
  non-goals (no durable read/unread log). The badge/affordance is
  presence-based (new mentions since last view), same as other unseen-item
  indicators in this app, not a persisted per-item flag.
- No filtering by mention type (regular vs. `@everyone`) in v1 — both
  surface identically in the tab, since both are just `reed_mentions`
  rows pointing at ordinary reeds.

## Design

### API

`GET /users/me/mentions?limit=&before=`, mirroring the existing
`limit`/`before` cursor convention (`handlers.go:2164-2183`, used by the
reply-list endpoint): `limit` defaults to 50, clamps to 100 max; `before`
is an RFC3339 timestamp cursor, omitted for the first page.

Query joins `reed_mentions` (`mentioned_user_id = caller`,
`mentioned_server_id = this server`) to `reeds`, ordered by the reed's
`signed_at` descending, excluding removed reeds and removed authors —
same `NOT EXISTS` guards already used by `ListReplies`
(`services.go:1344-1354`) for `reed_removals`/`account_removals`. Behind
the existing signature-auth middleware; "me" resolves from the
authenticated caller, same as other `/users/me/...` endpoints.

Response: list of reed references (author, reed ID, timestamp) sufficient
for the SPA to resolve/display each mentioning reed the same way it
already resolves reed refs elsewhere (`ParseReedRef` /
`userID@serverID/reedID`, per `conversations/README.md`'s cross-links) —
not full reed bodies, matching this app's general pattern of index
endpoints returning metadata, not content.

### SPA

New tab/filtered view (exact placement in the reeds/feed navigation TBD
at implementation time — this proposal locks the *mechanism*, not pixel
layout), listing mentions via the endpoint above. Unread affordance
follows the existing coverage/echo-count badge convention already used
elsewhere in the SPA (a count or dot reflecting mentions newer than the
last time the tab was opened) — no new state-tracking concept invented.

## Testing

- A regular `~userID@serverID` mention and an `@everyone` broadcast both
  appear in the mentioned user's list, indistinguishable in shape.
- Pagination: cursor stable across concurrent inserts (new mentions don't
  shift already-fetched pages).
- A mention from a since-removed reed or since-removed account does not
  appear.
- Authorization: caller only ever sees their own mentions; no
  cross-user leakage via the endpoint.

## Dependencies

- [00](00_glossary_and_design.md) for the locked model.
- [01](01_everyone_handle.md) — `@everyone` mentions must exist before
  this tab has anything special to show for them (regular mentions from
  conversations 04 already exist independently).
- `reed_mentions` schema: [`db.go`](../../db.go).
- Cursor pattern precedent: `ListReplies` (`services.go:1335`),
  `handlers.go:2164-2183`.
