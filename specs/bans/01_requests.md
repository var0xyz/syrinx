# Bans 01 — Ban and unban requests, `user_bans`, ledger

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
| `unban` | Users this server banned   | Deletes their rows and owes B an unban notice                                      |

Both are in `adminActionNeedsOutsideApprover`, as kicks are. Neither needs
root's approval: roles are local, so an admin of B is just a foreign user to A.

Refused when opening a request:

- a target is local to this server;
- a target's server isn't a connected peer (an unban is allowed on a
  disconnected one, so a ban can always be lifted);
- `ban` on a user already banned, `unban` on one who isn't.

A target whose account is removed (kicked or deleted on B) drops out, and a
request with no targets left is moot, as for every request.

## Schema

In `InitDB`, one table for bans made here and bans received from peers, as
`user_blocks` holds both sides of a block:

```sql
CREATE TABLE IF NOT EXISTS user_bans (
  server_id           VARCHAR(255) NOT NULL REFERENCES servers(id) ON DELETE CASCADE,
  user_id             VARCHAR(255) NOT NULL REFERENCES identities(id) ON DELETE CASCADE,
  request_id          VARCHAR(255) NOT NULL,
  request             BYTEA NOT NULL,  -- the approved AdminRequest, as received
  server_signature_id INT NOT NULL REFERENCES server_signatures(id),
  PRIMARY KEY (server_id, user_id)
);
CREATE INDEX IF NOT EXISTS idx_user_bans_user ON user_bans (user_id);
```

- On A, `server_id` is A and the user is foreign. On B, `server_id` is A and the
  user is local.
- `request_id` references `admin_requests` only on A; B keeps the request bytes
  it verified, since it has no row of A's requests.
- The user is never of `server_id`; the request checks catch it.
- An account removal deletes the user's bans, through the identity cascade.

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
The unban notice carries the approved unban request and an `account-unban`
countersignature of the same shape.

## Ledger

The usual opened and closed entries of a request, with its targets and reason.
Nothing new.

## Tests

- A ban of a local user, of a user of an unconnected server, or of a user
  already banned is refused; an unban of a user who isn't banned is refused.
- A ban executes only with an approver holding `user.kick` who isn't the
  requester or their direct invitee.
- Executing writes one row per target and both ledger entries; unban deletes the
  rows.
- A target removed while the request is open drops out; the request goes moot
  when none are left.
- `account-ban` payload parity between Go and the SPA.
