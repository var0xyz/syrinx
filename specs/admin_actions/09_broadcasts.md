# Admin actions 09 — Broadcasts and private messages

## Status

Implemented.

## Depends on

[01](01_requests_and_approvals.md), [02](02_ledger.md)

## Context

An admin message to every local member ("maintenance tonight", "rules
changed"), or to one local member ("can we talk about your replies?"). Not a
reed: it isn't in any feed, can't be replied to, and never leaves the
instance. Both kinds need a second admin's approval.

## Request

`action = broadcast`, no targets, no reason, `body` = the message (UTF-8,
≤140 characters, the mailbox limit). The ledger entry for the opened
request carries the message as its `content`. Requester and approver need
`message.broadcast`.

A private message (unicast) is the same with `action = unicast`, `message.unicast` as
its permission, and exactly one target, the recipient, who must be a local user. It is **never** in the
ledger ([02](02_ledger.md)): only admins see it, through
`/api/admin/requests`, whatever its outcome.

## Executor

After the approval commits, for every local user who isn't removed or
suspended:

1. `SendMailboxMessage(..., kind = "broadcast", message, ...)`, with no
   link: the ledger is for visibility and audit, not something the mailbox
   points to. The message comes from the system, not the requesting admin;
   the ledger records who asked and who approved.
2. `NotifyMailboxMessage` for connected users. The rest get it on catch-up,
   as today.

This is one mailbox row per member, all local, so no federation is
involved. It runs in the background after commit. A failure for one user is
logged and doesn't stop the others.

A user who signs up after the broadcast doesn't receive it. The ledger still
has it.

A private message runs the same two steps for its one recipient, with
`kind = "admin-message"`.

## Mailbox

Both kinds go through the user mailbox, which needs two fixes first:

- `SendMailboxMessage` looks up `users.user_fingerprint` and
  `public_key_revocations.revoked_id`, which no longer exist. It reads the
  recipient's `users.active_key_id` and checks `public_key_revocations.key_id`
  instead.
- The SPA's mailbox bell was hidden and its WebSocket handling turned off.
  The bell comes back in the upper right corner, and the client handles
  mailbox messages again.

## SPA

`MailboxBell` renders `kind = "broadcast"` and `kind = "admin-message"`
as system messages ("Broadcast", "Message from @admins") with no sender.
"@admins" hints at how to answer: mention them in a reed ([11](11_admins_mention.md)).
The mailbox only holds system messages; interactions have their own page.

- The bell turns into a close cross while the popover is open. The popover
  is at most the screen width minus a 1rem gutter on each side.
- The kind's badge sits in its own column on the left, and the title, with
  the envelope before it, the message and the timestamp stack beside it.
- An unread message shows a closed envelope; clicking it, or the message,
  marks it read and shows an open envelope.
- A "Delete" link follows the timestamp and asks for confirmation.

## Tests

- Approval puts one mailbox row per active local member, none for suspended
  or removed members.
- The broadcast's ledger entry shows the message.
- A private message puts one mailbox row for its recipient and never appears
  in the ledger, in any outcome.
