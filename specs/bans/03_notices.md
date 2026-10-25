# Bans 03 — Ban notices A → B, mailbox, B's admin list

## Status

Proposed.

## Depends on

[01](01_requests.md)

## Notices

One leg, `POST /api/federation/relay/ban-notify`, B answers 204:

```proto
message RelayBanNotice {
  string user_id = 1;
  AdminAuthorization ban = 2;                // the approved ban request
  ServerSignature ban_signature = 3;         // A's account-ban countersignature
  optional AdminAuthorization unban = 4;     // the approved unban request, once lifted
  optional ServerSignature unban_signature = 5;
}
```

A notice is one ban's current state: the ban, and its lift once there is one.
Sending the whole ban each time means B never misses one, even when the lift
comes before the ban's first notice gets through. One notice per ban for one of
B's users: one delivery per peer.

A records what it owes in `user_ban_notices`:

```sql
-- The bans whose current state each banned user's home server is owed.
CREATE TABLE IF NOT EXISTS user_ban_notices (
  request_id VARCHAR(255) NOT NULL,
  user_id    VARCHAR(255) NOT NULL,

  PRIMARY KEY (request_id, user_id),
  FOREIGN KEY (request_id, user_id) REFERENCES user_bans (request_id, user_id) ON DELETE CASCADE
);
```

- Inserted in the execute transaction of a ban, and again in that of its unban.
- Deleted on a 2xx, unless the ban was lifted after the notice was read, which
  leaves it owed.
- Sends to one peer are serialized, oldest ban first, so a lift always reaches B
  before the next ban of the same user.
- Retried when A boots and when B announces its own boot.

## Receiving a notice (B)

- The target must be B's user; the requests' requesters, approvers and
  countersignatures must belong to the calling peer.
- Verify the ban with `verifyForeignAdminRequest` for the `ban` action, then A's
  countersignature with `verifyPeerCountersignature`. With a lift, verify it the
  same way for the `unban` action: it must name the user and be signed after the
  ban.
- Upsert `peer_bans` by `(server_id, request_id, user_id)`: insert the ban if
  it's new, set the unban columns if the notice has a lift and the row doesn't.
  A repeated notice changes nothing.
- For each change actually recorded, in order:
  - a new ban: send Bob a `peer-ban` mailbox message and push `USER_BANNED` (as
    `USER_BLOCKED` is pushed) with the certificate;
  - a new lift: send Bob a `peer-unban` mailbox message, push `USER_UNBANNED`,
    and owe A the snapshot ([05](05_unban_snapshot.md)).

A tampered notice, one signed by another server's admins, or one naming a user
who isn't B's is refused with 400 and changes nothing.

## Mailbox

`SendMailboxMessage` with two new kinds, sent by B on its own: no admin of B is
involved.

| Kind         | `message`          | `meta`                   |
| ------------ | ------------------ | ------------------------ |
| `peer-ban`   | the ban's reason   | the ban's certificate    |
| `peer-unban` | the unban's reason | the notice with its lift |

Reasons and mailbox messages share the same limit (420 characters, 1400 raw), so
the reason always fits. The SPA shows it only once the certificate in `meta`
verifies ([07](07_spa_banned.md)).

## Bob's bans

`GET /api/bans`: the caller's active bans, certificates included. The SPA calls
it on sign-in and on every `USER_BANNED` / `USER_UNBANNED`.

## B's admin list

`GET /api/admin/peer-bans`, for admins and root: every ban peers made of B's
users, active and lifted, from `peer_bans`, newest first, keyset cursor,
optionally for one user. Each carries both certificates and reasons. No ledger
entry: these are another server's actions.

## Tests

- A ban notice is stored once, and a second delivery is a no-op.
- A notice with a bad admin signature, a bad countersignature, another server's
  request, a target that isn't B's, or a lift signed before its ban is refused
  with 400.
- A ban lifted before its first notice was sent reaches B as one notice, and B
  holds the ban with its lift.
- A user banned, unbanned and banned again by A ends up on B with two rows, one
  lifted and one active.
- Bob gets one mailbox message per recorded change, and `GET /api/bans`
  reflects it.
- Owed notices are retried at A's boot and at B's boot announcement.
- A lift reached after the notice was read leaves the notice owed.
- The admin list shows an active ban, then the lifted one.
