# Bans 01 — Ban and unban requests, `user_bans`, `peer_bans`, ledger

## Status

Proposed.

## Depends on

[Admin actions 01](../admin_actions/01_requests_and_approvals.md),
[02](../admin_actions/02_ledger.md)

## Actions

Two new admin request actions in `admin.go`, both needing `user.kick`
(`adminActionPermission`):

| Action  | Targets                    | Executes                                                                           |
| ------- | -------------------------- | ---------------------------------------------------------------------------------- |
| `ban`   | One or more users of peers | Inserts a `user_bans` row per target and owes B a ban notice ([03](03_notices.md)) |
| `unban` | Users with an active ban   | Records the lift on each target's active ban and owes B its notice again           |

Both are in `adminActionNeedsOutsideApprover`, as kicks are. Neither needs
root's approval: roles are local, so an admin of B is just a foreign user to A.

Refused when opening a request:

- a target is local to this server;
- a target's server isn't a connected peer (an unban is allowed on a
  disconnected one, so a ban can always be lifted);
- `ban` on a user with an active ban, `unban` on one without.

A target whose account is removed (kicked or deleted on B) drops out, and a
request with no targets left is moot, as for every request.

## History

A ban is never deleted. An unban records the lift on the ban, and a user banned
again gets a new row, so admins can see a user's earlier bans when they look at
a new one. B keeps the same history of its users in `peer_bans`
([03](03_notices.md)): every ban a peer made of them, and its lift.

## Schema

In `InitDB`, two tables: bans this server made, and bans peers made of this
server's users. They don't share a shape: a ban made here is a row of
`admin_requests`, one received is only the bytes B verified.

```sql
-- Every ban this server made; a lifted one keeps its unban.
CREATE TABLE IF NOT EXISTS user_bans (
  request_id                VARCHAR(255) NOT NULL REFERENCES admin_requests(id),
  user_id                   VARCHAR(255) NOT NULL REFERENCES identities(id) ON DELETE CASCADE,
  server_signature_id       INT NOT NULL REFERENCES server_signatures(id),
  unban_request_id          VARCHAR(255) REFERENCES admin_requests(id),
  unban_server_signature_id INT REFERENCES server_signatures(id),

  PRIMARY KEY (request_id, user_id),
  CONSTRAINT user_bans_unban CHECK (
    (unban_request_id IS NULL) = (unban_server_signature_id IS NULL))
);
CREATE UNIQUE INDEX IF NOT EXISTS idx_user_bans_active
  ON user_bans (user_id) WHERE unban_request_id IS NULL;

-- Every ban a peer made of this server's users; a lifted one keeps its unban.
CREATE TABLE IF NOT EXISTS peer_bans (
  server_id                 VARCHAR(16) NOT NULL REFERENCES server_mesh(server_id) ON DELETE CASCADE,
  request_id                VARCHAR(255) NOT NULL,
  user_id                   VARCHAR(255) NOT NULL REFERENCES identities(id) ON DELETE CASCADE,
  admin_authorization       BYTEA NOT NULL,
  server_signature_id       INT NOT NULL REFERENCES server_signatures(id),
  unban_authorization       BYTEA,
  unban_server_signature_id INT REFERENCES server_signatures(id),

  PRIMARY KEY (server_id, request_id, user_id),
  CONSTRAINT peer_bans_unban CHECK (
    (unban_authorization IS NULL) = (unban_server_signature_id IS NULL))
);
CREATE UNIQUE INDEX IF NOT EXISTS idx_peer_bans_active
  ON peer_bans (user_id, server_id) WHERE unban_authorization IS NULL;
```

- `user_bans` has no server: it is always this one. The banned user's server is
  their identity's, so nothing can drift. A ban request can target several
  users, hence the `(request_id, user_id)` key. `server_signature_id` is our
  `account-ban` countersignature and `unban_server_signature_id` our
  `account-unban` one; the certificates are rebuilt from the requests' rows, as
  for a kick in `account_removals`.
- `peer_bans.server_id` is the banning server, never the user's own: the user is
  always local. It references `server_mesh`, which only holds peers, so it can
  never be this server. `request_id` is the peer's request, so it has no FK;
  it is how B recognizes a ban it already holds. `admin_authorization` and
  `unban_authorization` are the approved requests as B verified them (an
  `AdminAuthorization`, as a peer's kick stores it); the signature columns are
  A's countersignatures.
- A user has at most one active ban per banning server. Every check of a ban
  reads only active ones and must spell out the index's predicate
  (`unban_request_id IS NULL`, `unban_authorization IS NULL`) for the partial
  index to serve it. `user_id` leads B's index so it also answers "which peers
  banned this user".
- That a ban targets a peer's user, and a received one a local user, is checked
  by the request and the notice handler; no constraint can see an identity's
  server against `server_self`.
- An account removal keeps the identity, so the user's bans stay.
- `admin_requests.action`'s check gains `ban` and `unban`.

## Certificate

A countersigns the ban when it executes, the way it countersigns a kick:

```go
buildAccountBanServerPayload(serverID, userID, requestID, requestSignature,
    decisionSignature, serverKeyFingerprint string, signedAt time.Time)
// {"decisionSignature","requestID","requestSignature","serverID",
//  "serverKeyFingerprint","signedAt","type":"account-ban","userID"}
```

with its SPA mirror in `signing.ts` and a shared vector. The certificate is the
approved request plus this countersignature: both admins' signatures and A's.
An unban is countersigned the same way, with `"type":"account-unban"` and the
unban request's fields; the lift is the approved unban request plus that
countersignature.

## Admin list

`GET /api/admin/bans`, for admins and root: the bans this server made, active
and lifted, newest first, keyset cursor, optionally for one user. Each carries
its request and reason, and for a lifted one the unban's.

## Ledger

The usual opened and closed entries of a request, with its targets and reason.
Nothing new.

## Tests

- A ban of a local user, of a user of an unconnected server, or of a user with
  an active ban is refused; an unban of a user without one is refused.
- A ban executes only with an approver holding `user.kick` who isn't the
  requester or their direct invitee.
- Executing writes one row per target and both ledger entries; unban records
  the lift on each target's active ban and deletes nothing.
- A user banned, unbanned and banned again has two rows, one lifted and one
  active, and the admin list shows both.
- A target removed while the request is open drops out; the request goes moot
  when none are left.
- `account-ban` and `account-unban` payload parity between Go and the SPA.
