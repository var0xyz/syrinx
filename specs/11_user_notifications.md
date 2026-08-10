# Proposal 11 — Per-user system-notification store

## Status

**Superseded** by [`notifications/`](notifications/README.md).

This proposal's motivating case — telling a username-collision "loser"
they'd been renamed during server recovery — no longer exists. Commit
`cc735d7` ("On username collision nuke the loser") made **deletion**, not
rename, the actual collision policy, and
[`recovery/upsert.go`](../recovery/upsert.go)'s own comment explains why a
rename could never have worked: a renamed-in-place row would carry a
username that no longer matches what its owner signed, permanently
breaking verification. The server cannot mint a new signed profile for a
user; only the user's own key can, and clients reject anything else. So
there is no "loser" left to notify.

[`notifications/`](notifications/README.md) replaces this proposal's general-purpose
notification substrate goal, split into two mechanisms instead of one
generic store: an encrypted, one-way **Mailbox** for server→user private
messages (the direct successor to this file's schema/API/producer
design, reworked to be encrypted-at-rest and delivered-then-deleted
rather than a durable read/unread log), and **admin mentions**
(`@everyone`) for public, repliable, server/admin→everyone broadcasts,
which this proposal never covered. The existing client-side toast/alert
system is explicitly out of scope for both and untouched.

Kept below for history; do not implement against this file — see
[`notifications/`](notifications/README.md) instead.

## Context (original, historical)

There is no per-user message store today. Recovery needs one to tell a
username-collision loser that they were renamed, since the affected user is
likely offline at the moment the rename happens. Once we have it we will
almost certainly use it for other product events (welcome message, key
rotation confirmations, follower milestones, admin announcements, etc.).

This proposal deliberately scopes the notification store as a stand-alone
feature so it can land and be used independently of recovery.

## Scope

- New `user_notifications` table.
- Server-side helper to insert a notification for a given user.
- Endpoints for the user to list, mark-as-read, and delete their own
  notifications.
- Client UI: a notifications area on the profile page (or a small badge in
  the main nav — one entry point is enough for v1).
- No producers wired up in this proposal beyond a trivial "welcome"
  notification at signup, which doubles as the smoke test.

## Non-goals

- No push/email/OS notifications. Purely in-app.
- No cross-user notifications ("X liked your reed") — not required by
  recovery and not by any current product feature.
- No admin broadcast UI.

## Design

### Schema

```sql
CREATE TABLE user_notifications (
    id            VARCHAR(255) PRIMARY KEY,      -- random ID via generateUserID/generateID helper
    user_id       VARCHAR(255) NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    kind          VARCHAR(64)  NOT NULL,         -- e.g. "welcome", "renamed_on_collision", ...
    body          TEXT         NOT NULL,         -- rendered markdown-safe text; UI escapes
    metadata      JSONB,                          -- kind-specific structured data (e.g. old/new username)
    created_at    TIMESTAMP    NOT NULL DEFAULT NOW(),
    read_at       TIMESTAMP
);

CREATE INDEX idx_user_notifications_user_created
    ON user_notifications(user_id, created_at DESC);

CREATE INDEX idx_user_notifications_user_unread
    ON user_notifications(user_id) WHERE read_at IS NULL;
```

### Server API

- `POST` internal helper `NotifyUser(userID, kind, body, metadata)` on
  `DataService`. Not exposed as an HTTP endpoint — producers are all in-
  process.
- `GET /users/me/notifications?limit=&before=` → list, newest first,
  cursored on `created_at`.
- `POST /users/me/notifications/{id}/read` → set `read_at = now()`.
- `DELETE /users/me/notifications/{id}` → hard delete.

All three HTTP endpoints behind the existing signature-auth middleware.

### Client

- SPA fetches unread count on load; shows a small badge in the top nav.
- Notifications view on the profile page: paginated list, mark-as-read on
  click, delete via a menu action.
- Realtime: add `NewNotification` message type so a logged-in device
  receives new notifications live. If out of scope for v1, poll on focus.

Recommendation: ship realtime in v1 — the plumbing is identical to reed
broadcast and it's trivial once the endpoint exists. But it is not required
for recovery correctness.

### Producers

For this proposal, wire exactly one producer:

- On signup completion (`CompleteSignup` if Proposal 04 has landed;
  otherwise the existing `Signup` handler), call `NotifyUser(userID,
  "welcome", "Welcome to Syrinx.", nil)`.

Recovery's renamed-on-collision producer will be wired as part of the
recovery work-unit, using this proposal's `NotifyUser` helper directly.

## Work items

1. Schema (in `db.go`).
2. `NotifyUser` helper in `services.go`.
3. Three HTTP endpoints in `handlers.go` + routes in `main.go`.
4. Realtime `NewNotification` message type + fanout (optional for v1,
   recommended).
5. SPA: nav badge, notifications view, mark-read, delete.
6. Wire the "welcome" producer as the smoke test.
7. Tests:
   - Insert via `NotifyUser`, list via the endpoint, mark read, delete.
   - Authorisation: user A cannot read/mark/delete user B's notifications.
   - Cursor pagination stable across inserts.

## Testing

- Unit + integration.
- e2e: signup → welcome notification visible → mark read → badge clears.

## Risks

- **Table growth.** Unbounded per user. Mitigate later with a TTL sweep
  (out of scope for v1).
- **Escaping**: `body` renders as text; if we ever want links, do so via
  `metadata` + typed rendering rather than freeform HTML.

## Dependencies

None. Fully standalone.

## Parallelism

Fully independent of 01–08. Can be picked up by anyone at any time. The
recovery unit-of-work will consume it as a library once merged.
