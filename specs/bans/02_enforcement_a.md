# Bans 02 — Enforcement on A

## Status

Proposed.

## Depends on

[01](01_requests.md)

## Context

A enforces a ban on its own, from its active `user_bans` rows, whatever B does. Every
refusal answers **403** with the ban certificate, as a block answers 403 with
its own, so B learns of a ban whose notice it never got
([04](04_enforcement_b.md)).

## Refused

| Interaction by Bob                      | Where on A                                                                                                                            |
| --------------------------------------- | ------------------------------------------------------------------------------------------------------------------------------------- |
| Reply to one of A's reeds               | `relay/new-reed` with `reply.parent_reed_id` on A: 403, nothing recorded                                                              |
| Echo of one of A's reeds                | `relay/new-reed` with `echo.echoed_reed_id` on A: 403, nothing recorded                                                               |
| Mention of A's members or A's `@admins` | `relay/new-reed`: the reed is taken, its mentions of A's members and A's `@admins` are dropped (as `dropMentionsBlockingAuthor` does) |
| Ripple on one of A's reeds              | `PostRipple`                                                                                                                          |
| Flag on one of A's reeds                | `FlagFromPeer`                                                                                                                        |

`drainPeerStream` skips an item a peer answers with a 4xx, so B's stream to A
moves past a refused reply or echo even when B doesn't enforce.

## Never refused

- Reads: profiles, info, reeds, threads, ripples, relays.
- Follows of A's members, likes of A's reeds, and their undoing.
- Bob's other reeds, delivered to A's members who follow him.
- Reed removals, thread removals and account removals of Bob's.
- Deleting his own ripples from before the ban.
- Anything A's members do to Bob: mentions (a mention typed with his ID goes
  through even though search hides him), replies, likes, echoes, ripples on his
  reeds. The ban is one-way.

## Not amplified

- `dispatchForeignNewReed` sends Bob's reeds to followers, profile viewers and
  thread viewers as before, but never to broadcast subscribers.
- `SearchUsers` leaves banned users out of the results it returns, foreign
  results included.

## Tests

- A reply to or echo of A's reed from Bob is answered 403 with the certificate
  and records nothing; one to a reed of another server is taken.
- A reed of Bob's mentioning A's members and A's `@admins` is taken, with no
  mention row and no `@admins` row.
- A ripple and a flag from Bob are refused with 403 and the certificate; he
  still loads ripples and deletes his own.
- An A member's mention of Bob, typed by ID, reaches him.
- A like, a follow, a reed read and a reed removal from Bob succeed.
- Bob's reed reaches an A follower and no broadcast subscriber.
- A local search never returns Bob.
- After an unban, every refused path above succeeds again.
