# Admin actions 09 — Broadcasts

## Status

Proposed.

## Depends on

[01](01_requests_and_approvals.md), [02](02_ledger.md)

## Context

An admin message to every local member ("maintenance tonight", "rules
changed"). Not a reed: it isn't in any feed, can't be replied to, and never
leaves the instance.

## Request

`action = broadcast`, no targets, `params` = the message (UTF-8, ≤1000
characters). The reason field holds the same text, so the ledger shows it.
The request is approved like any other.

## Executor

After the approval commits, for every local user who isn't removed or
suspended:

1. `SendMailboxMessage(..., MailboxCategorySystem, kind = "broadcast",
   message, link = "/ledger/{requestID}", senderUserID = requester, ...)`.
2. `NotifyMailboxMessage` for connected users. The rest get it on catch-up,
   as today.

This is one mailbox row per member, all local, so no federation is
involved. It runs in the background after commit. A failure for one user is
logged and doesn't stop the others.

A user who signs up after the broadcast doesn't receive it. The ledger still
has it.

## SPA

`MailboxBell` renders `kind = "broadcast"` with the requester's name and a
link to the ledger entry, which shows who approved it.

## Tests

- Approval puts one mailbox row per active local member, none for suspended
  or removed members.
- The broadcast's ledger entry shows the message.
