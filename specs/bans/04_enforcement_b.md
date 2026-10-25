# Bans 04 — Enforcement on B

## Status

Proposed.

## Depends on

[03](03_notices.md)

## Context

B refuses at the source from its copy of the ban, so almost nothing reaches A to
be refused there. Bob keeps the use of his own server: his replies and echoes
publish and reach his followers, they just don't cross to A.

## Delivery to A

In `sendPeerStreamItem`, for an item by a user A banned:

| Item                                         | Sent to A                                       |
| -------------------------------------------- | ----------------------------------------------- |
| Reply whose parent is on A                   | No; the stream moves past it                    |
| Echo of a reed on A                          | No; the stream moves past it                    |
| Reed mentioning A's members or A's `@admins` | Yes, with those mentions left out of `mentions` |
| Any other reed                               | Yes                                             |
| Reed, thread or account removal              | Always                                          |

Skipped items aren't kept anywhere: the unban snapshot reads Bob's live reeds
instead ([05](05_unban_snapshot.md)).

## Refused before proxying

- `PostRipple` on A's reed: 403 with the certificate.
- `POST /api/flags` on A's reed: 403 with the certificate.

## Search

- `SearchUsers` for Bob doesn't fan out to A.
- `SearchUsersFromPeer` called by A leaves out B's users A banned.

## A 403 with a certificate

B handles a 403 from A carrying a ban certificate as a ban notice it missed: it
verifies and stores it, as in [03](03_notices.md), mails Bob, and answers the
original request with the same 403.

## Tests

- Bob's reply to A's reed and echo of A's reed publish on B, reach a B follower,
  and are never sent to A; his reply to a reed of C is.
- A reed of Bob's mentioning A's member reaches A without that mention.
- Bob's reed removal reaches A during the ban.
- A ripple and a flag on A's reed are refused before any call to A.
- Bob's user search makes no call to A; A's search never gets Bob back.
- A 403 with a certificate stores the ban on B when the notice was lost.
