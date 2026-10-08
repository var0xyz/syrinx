# Blocking 06 — Refuse follow, like, ripple, reply, echo, mention

## Status

Implemented (`handlers.go`, helpers in `blocks.go`).

## Depends on

[03](03_enforcement.md)

## Context

[03](03_enforcement.md) keeps the blocking user out of the blocked user's sight.
This step keeps the blocked user out of the blocking user's reach: new
interactions aimed at the blocking user are refused with **403** + certificate.

## Refused

| Action by the blocked user | Where |
|----------------------------|-------|
| Follow the blocking user | `FollowUser` (and the peer follow leg) |
| Like a blocking user's reed | `LikeReed` |
| Ripple on a blocking user's reed | `PostRipple` |
| Reply to, or echo, a blocking user's reed | `SignReed` when `replying` / echo ref names a blocking user's reed |
| Mention the blocking user | `SignReed`: the reed publishes as signed, but the mention is not indexed for or delivered to the blocking user (`dropMentionsBlockingAuthor`) |

A mention is not refused, because the reed is the user's own and stays
exactly as they signed it. The server only declines to deliver it to the
blocking user's inbox, a delivery the blocking user opted out of by blocking. What the
user published is unchanged.

## Never refused

- **Unfollow** at the API, though there is nothing left to unfollow: the
  block already removed the follow ([02](02_api.md)), and the SPA offers no
  button for it. A stale unfollow is a 204 no-op.
- **Unlike, ripple delete, mention delete** of their own earlier
  interactions.
- **Block / unblock** of the blocking user. The blocking user can still see the blocked
  user by default; the blocked user may block back.

## Across peers

Interactions with a foreign blocking user are proxied to their server
(follow, like, ripple). The acting user's server refuses from its copy
first; the blocking user's server refuses on the leg with the same check
(`refuseBlockedRequester` against the follower or acting user the peer
vouches for).

A thread holds only its author's own reeds, so it has nothing to refuse.

## Tests

- Each refused action → 403 + certificate; nothing stored.
- A reed mentioning the blocking user publishes; the blocking user gets no mention.
- A stale unfollow is a 204 no-op; block-back succeeds while blocked.
