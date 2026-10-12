# Admin actions 08 — Flags

## Status

Proposed.

## Depends on

[00](00_design.md)

## Context

Members flag reeds they think admins should look at. A flag goes to the
**reed's home server**, and only that server's admins see it. Admins never
see flags on reeds from other servers, but they do see flags that remote
members raise on their own server's reeds.

The server stores the reed ID and never the content: an admin opens the reed
and loads it from the network like any reader.

Flags are private, never appear in the ledger, and are never linked to an
action. Acting on an account is always its own request with its own reason.

## Schema

```sql
CREATE TABLE IF NOT EXISTS reed_flags (
  reed_id     VARCHAR(255) NOT NULL REFERENCES reed_identities(id) ON DELETE CASCADE,
  author_id   VARCHAR(255) NOT NULL REFERENCES identities(id) ON DELETE CASCADE,
  reporter_id VARCHAR(255) NOT NULL REFERENCES identities(id) ON DELETE CASCADE,
  note        VARCHAR(280) NOT NULL DEFAULT '',
  public_key_id     VARCHAR(255) NOT NULL REFERENCES public_keys(id) ON DELETE CASCADE,
  user_signature_id INT NOT NULL REFERENCES user_signatures(id),
  created_at  TIMESTAMP NOT NULL,   -- the timestamp the reporter signed
  dismissed_at TIMESTAMP,
  PRIMARY KEY (reed_id, reporter_id)
);
CREATE INDEX IF NOT EXISTS idx_reed_flags_open ON reed_flags (created_at)
  WHERE dismissed_at IS NULL;
```

- One flag per reporter per reed. Flagging the same reed again is refused
  with `409 {"error":"already-flagged"}`.
- The reporter's signature is stored, so an admin can verify a flag
  themselves instead of trusting the server.
- `author_id` is stored so visibility can be checked without the reed body.
  The server knows the author from reed metadata.
- Only reeds authored on this server have rows here. `reporter_id` may be a
  remote member.

## API

| Route | Auth | Does |
|-------|------|------|
| `POST /api/flags` | Any local member | Flag `{reedID, note}`. Stored here for a local reed, relayed to the home server for a remote one |
| Peer endpoint (federation) | The reporter's home server | Delivers a remote member's flag on a reed authored here |
| `GET /api/admin/flags` | Admin or root | Open flags, filtered by visibility, keyset cursor |
| `POST /api/admin/flags/{reedID}/dismiss` | Admin or root, if they can see it | Dismiss every flag on that reed |

The reporter signs the flag (`reedID`, `note`, reporter ID, timestamp), so
the home server can check a relayed flag against the reporter's key instead
of trusting the relaying server. One flag is one relay call to one peer.

Refused by the home server when flagging:

- the reed's author is root;
- the reporter is the author;
- the reporter already flagged it.

## Visibility

| Reed author | Visible to |
|-------------|------------|
| Normal user | Every admin and root, with the reporter (local or remote) |
| Admin | Every admin and root **except that author** |

A suspended reporter's flags stay. A removed reporter's identity row stays
(account removals reference it), so the listing skips flags whose reporter
has an account removal.

## SPA

- **Flag** item in `ReedActionsMenu`, with an optional note. It isn't shown
  on root's reeds or the viewer's own.
- The SPA keeps the reeds the user flagged. On those, the item reads
  **Already flagged** and is disabled. A 409 is shown as the same message
  and records the reed.
- Admin flags list in [10](10_admin_spa.md): one row per reed with reporter
  count, reporters and notes; content loaded with
  `serverConnection.requestReedContent` (hydrate-then-relay).

## Tests

- Flag on root's reed refused; flag on own reed refused; a second flag by
  the same reporter refused with 409.
- An admin author doesn't see flags on their own reed; another admin does.
- Dismiss hides the reed from the open list.
- A flag on a remote reed is relayed to its home server and not stored
  locally; a remote member's flag on a local reed appears in the list.
