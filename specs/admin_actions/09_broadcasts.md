# Admin actions 09 — Broadcasts and private messages

## Status

Proposed.

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
`broadcast.message`.

A private message is the same with `action = message`, `user.message` as
its permission, and exactly one target, the recipient, who must be a local user. It is **never** in the
ledger ([02](02_ledger.md)): only admins see it, through
`/api/admin/requests`, whatever its outcome.

## Executor

After the approval commits, for every local user who isn't removed or
suspended:

1. `SendMailboxMessage(..., MailboxCategorySystem, kind = "broadcast",
   message, link = "/ledger/{id}", senderUserID = requester, ...)`.
2. `NotifyMailboxMessage` for connected users. The rest get it on catch-up,
   as today.

This is one mailbox row per member, all local, so no federation is
involved. It runs in the background after commit. A failure for one user is
logged and doesn't stop the others.

A user who signs up after the broadcast doesn't receive it. The ledger still
has it.

A private message runs the same two steps for its one recipient, with
`kind = "admin-message"` and no link, since there is no ledger entry.

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

`MailboxBell` renders `kind = "broadcast"` with the requester's name and a
link to the ledger entry, which shows who approved it. `kind =
"admin-message"` shows the requester's name and the message, with no link.

## Tests

- Approval puts one mailbox row per active local member, none for suspended
  or removed members.
- The broadcast's ledger entry shows the message.
- A private message puts one mailbox row for its recipient and never appears
  in the ledger, in any outcome.
