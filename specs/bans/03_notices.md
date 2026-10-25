# Bans 03 — Ban notices A → B, mailbox, B's admin list

## Status

Proposed.

## Depends on

[01](01_requests.md)

## Notices

| Leg                                       | Body                                                                                   | B's answer |
| ----------------------------------------- | -------------------------------------------------------------------------------------- | ---------- |
| `POST /api/federation/relay/ban-notify`   | the certificate: approved `ban` request + A's countersignature                         | 204        |
| `POST /api/federation/relay/unban-notify` | the approved `unban` request + A's countersignature, and the ban's `signedAt` it lifts | 204        |

One per ban and one per lift, for one of B's users: one delivery per peer.

A records what it owes in `ban_notices`, the way `block_notices` does: upserted
in the execute transaction, replacing whatever was still owed for the pair,
deleted on a 2xx unless a later notice replaced it. Sends to one peer are
serialized, so a ban and its lift can't cross. Retried when A boots and when B
announces its own boot.

## Receiving a ban (B)

- The target must be B's user; the request's requester, approver and
  countersignature must belong to the calling peer.
- Verify with `verifyForeignAdminRequest` for the `ban` action, then A's
  countersignature with `verifyPeerCountersignature`.
- Upsert `user_bans` (`server_id` = A). A newer `signedAt` replaces a stored
  ban; an older one is ignored.
- Send Bob a mailbox message (below) and push `USER_BANNED` (as `USER_BLOCKED`
  is pushed) with the certificate.

## Receiving an unban (B)

- Verified as a ban, for the `unban` action.
- Delete the row if its `signedAt` is the one the notice names; a stale unban
  lifts nothing.
- Send Bob a mailbox message, push `USER_UNBANNED`, and send A the snapshot
  ([05](05_unban_snapshot.md)).

A tampered notice, one signed by another server's admins, or one naming a user
who isn't B's is refused with 400 and changes nothing.

## Mailbox

`SendMailboxMessage` with two new kinds, sent by B on its own: no admin of B is
involved.

| Kind         | `message`          | `meta`           |
| ------------ | ------------------ | ---------------- |
| `peer-ban`   | the ban's reason   | the certificate  |
| `peer-unban` | the unban's reason | the unban notice |

Reasons and mailbox messages share the same limit (420 characters, 1400 raw), so
the reason always fits. The SPA shows it only once the certificate in `meta`
verifies ([07](07_spa_banned.md)).

## Bob's bans

`GET /api/bans`: the caller's active bans, certificates included. The SPA calls
it on sign-in and on every `USER_BANNED` / `USER_UNBANNED`.

## B's admin list

`GET /api/admin/peer-bans`, for admins and root: bans peers made of B's users,
newest first, keyset cursor. Active ones come from `user_bans`; lifted ones from
`peer_ban_history`, a row per lift (user, server, request IDs of ban and unban,
both reasons, both times), written when the unban is received. No ledger entry:
these are another server's actions.

## Tests

- A ban notice is stored once, a second delivery is a no-op, and a newer ban
  replaces an older one.
- A notice with a bad admin signature, a bad countersignature, another server's
  request, or a target that isn't B's is refused with 400.
- A stale unban lifts nothing.
- Bob gets one mailbox message per notice, and `GET /api/bans` reflects it.
- Owed notices are retried at A's boot and at B's boot announcement, and a ban
  followed by its lift leaves B with no ban.
- The admin list shows an active ban, then the lifted one.
