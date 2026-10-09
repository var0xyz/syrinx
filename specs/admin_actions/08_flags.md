# Admin actions 08 — Flags

## Status

Proposed.

## Depends on

[00](00_design.md)

## Context

Members flag reeds they think admins should look at. The server stores the
reed ID and never the content: an admin opens the reed and loads it from the
network like any reader.

Flags are private, never appear in the ledger, and are never linked to an
action. Acting on an account is always its own request with its own reason.

## Schema

```sql
CREATE TABLE IF NOT EXISTS reed_flags (
  reed_id     VARCHAR(255) NOT NULL,
  author_id   VARCHAR(255) NOT NULL REFERENCES identities(id) ON DELETE CASCADE,
  reporter_id VARCHAR(255) NOT NULL REFERENCES identities(id) ON DELETE CASCADE,
  note        VARCHAR(280) NOT NULL DEFAULT '',
  created_at  TIMESTAMP NOT NULL DEFAULT NOW(),
  dismissed_at TIMESTAMP,
  PRIMARY KEY (reed_id, reporter_id)
);
CREATE INDEX IF NOT EXISTS idx_reed_flags_open ON reed_flags (created_at)
  WHERE dismissed_at IS NULL;
```

- One flag per reporter per reed. Flagging again updates the note.
- `author_id` is stored so visibility can be checked without the reed body.
  The server knows the author from reed metadata.
- Remote reeds can be flagged. Only this server's admins see the flag. It is
  never forwarded.

## API

| Route | Auth | Does |
|-------|------|------|
| `POST /api/flags` | Any local member | Flag `{reedID, note}` |
| `GET /api/admin/flags` | Admin or root | Open flags, filtered by visibility, keyset cursor |
| `POST /api/admin/flags/{reedID}/dismiss` | Admin or root, if they can see it | Dismiss every flag on that reed |

Refused when flagging:

- the reed's author is root;
- the reporter is the author.

## Visibility

| Reed author | Visible to |
|-------------|------------|
| Normal user (local or remote) | Every admin and root, with the reporter |
| Admin | Every admin and root **except that author** |

A suspended reporter's flags stay. A removed reporter's identity row stays
(account removals reference it), so the listing skips flags whose reporter
has an account removal.

## SPA

- **Flag** item in `ReedActionsMenu`, with an optional note. It isn't shown
  on root's reeds or the viewer's own.
- Admin flags list in [10](10_admin_spa.md): one row per reed with reporter
  count, reporters and notes; content loaded with
  `serverConnection.requestReedContent` (hydrate-then-relay).

## Tests

- Flag on root's reed refused; flag on own reed refused.
- An admin author doesn't see flags on their own reed; another admin does.
- Dismiss hides the reed from the open list.
