# Reply counter double-counting on reed detail page

## Symptom

On the reed detail page, `replyCount` sometimes increments by 2 for a single
new reply, and the inflated value persists until the page is reloaded.
Reported pattern before a clean repro was found: first new reply after page
load double-counts (with one client ACK observed going back to the server),
second reply does nothing (no count change, no ACK), third double-counts
again, then it stabilizes.

## Confirmed repro

User A has User B's reed open (reed detail page). User B replies to their
own reed. User A's client receives **both** a `FOLLOW_REED` event and a
`REED_REPLY` event for the same new reply, and both events drive the reply
counter up by one, producing a double count from a single reply.

## Root cause

Two independent bugs stack to produce this, one server-side and one
client-side. Either one alone would be enough to cause the symptom; fixing
either would resolve the repro, but the client-side gap is the one that
generalizes (see "Why fix the client, not just the server" below).

### 1. Server: two unrelated fanout mechanisms notify the same viewer for the same reply

`realtime/service.go`, `handlePublishReady` (~line 1787-1831), runs on
`PUBLISH_READY` from the replying author's client and unconditionally kicks
off, in independent goroutines with no shared recipient state:

```go
go rs.fanoutNewReed(reedID, tags)          // or fanoutNewReedNoBroadcast
go rs.notifyReplyAncestorsOfReply(reedID)
go rs.notifyForeignParentOfReply(reedID)
```

- `fanoutNewReed(NoBroadcast)` → `fanoutNewReedCore` (~line 415-527) treats
  the reply as "a new reed by its author": it calls
  `GetOnlineFollowers(authorUserID)` and dispatches `FOLLOW_REED` to every
  online follower of the author. It has no concept of "reply" or "which
  thread."
- `notifyReplyAncestorsOfReply` (~line 2679-2693) →
  `notifyReedSubscribersOfReply` (~line 2748-2760) walks the reply's
  ancestor chain and, for each ancestor reed, calls
  `ReedSubscriberUserIDs(ancestorReedID, replyUserID)` — everyone with an
  active `SUBSCRIBE_REED` on that reed (i.e. viewing its detail page) —
  and dispatches `REED_REPLY`.

`fanoutNewReedCore`'s own de-dup (`subtractUserIDs`/`unionUserIDs`) only
reconciles overlap *within its own* follower/pipe/broadcast lists. It never
intersects against the completely separate reply-subscriber set computed by
`notifyReplyAncestorsOfReply`. So a viewer who is both a follower of the
author *and* has the reed open receives two independent events — each with
its own freshly generated `event_id` — describing the same reply. This is
also why observed ACK counts vary (0, 1, or 2 `DATA_ACK`s per reply): it
depends on real-world timing races (e.g. whether A's `SUBSCRIBE_REED` had
already registered before `notifyReplyAncestorsOfReply` ran, whether A was
online at the instant `GetOnlineFollowers` queried). ACK count is a
downstream symptom, not a separate causal mechanism.

### 2. Client: both event types feed the identical counter-increment function with no cross-event de-dup

`spa/src/routes/reed/[userID]/[reedID]/+page.svelte`:

```svelte
$: followArrived = $followReedQueue?.reed;
$: if (followArrived && followArrived.id !== lastHandledFollowReedId && (...)) {
    lastHandledFollowReedId = followArrived.id;
    void onFollowReedArrived(followArrived);
  }

$: reedReplyArrived = $reedReplyQueue?.reed;
$: if (reedReplyArrived && reedReplyArrived.id !== lastHandledReedReplyId && (...)) {
    lastHandledReedReplyId = reedReplyArrived.id;
    void onFollowReedArrived(reedReplyArrived);   // same function, again
  }
```

```svelte
async function onFollowReedArrived(incoming) {
  if (incoming?.replying && incoming.replying === canonicalReedID) {
    if (statsStatus === 'loaded') replyCount += 1;
    await conversationSection?.onReplyArrived(incoming);
  }
}
```

`FOLLOW_REED` → `followReedQueue` and `REED_REPLY` → `reedReplyQueue` are
two independent Svelte stores (routed via `dispatchReedToQueue` in
`spa/src/lib/repositories/reeds.ts`). Each has its own
`lastHandled*ReedId` guard, but that guard only prevents *its own* store
from re-firing for a repeat — it does nothing to prevent the sibling store
from firing for the *same underlying reed id* through the other queue. Both
paths converge on the same `replyCount += 1` statement, so one reply
delivered via both channels increments twice.

(`ConversationSection.svelte`'s `onReplyArrived` is separately, correctly
deduped by `reedID` via `rows.some(r => r.reedID === reedID)` — this is why
only the numeric badge double-counts, not the visible reply list.)

### Why the pattern looked "alternating" before the clean repro

`handleReedStats` / `handleReedReplies` (same file) overwrite `replyCount`
directly from the server's authoritative subtree count whenever a
`REED_STATS`/`REED_REPLIES` push arrives — which happens on roughly the
same fanout wave every reed subscriber gets. Whichever message a client
processes last wins: if an increment lands after the authoritative
overwrite, the doubled count sticks (until the next stats push); if the
overwrite lands last, it silently corrects the count back. This — combined
with the server-side timing race in point 1 (sometimes both events fire,
sometimes only one, depending on subscribe/online timing) — fully accounts
for the observed double/no-op/double alternation, with no need to invoke
Svelte batching or client-side staleness as an independent cause. A full
page reload always "fixes" it because a fresh load reads `REED_STATS` fresh
with no leftover local increment to conflict with.

## Fix plan

1. **Client (primary fix — closes the general case, not just this repro):**
   in `+page.svelte`, de-dupe `onFollowReedArrived` calls across both
   queues by the incoming reed's own canonical id, not just per-queue. Track
   a single `seenReplyIds: Set<string>` (or a single shared
   `lastHandledReplyReedId` keyed on `incoming.id`) checked before
   `replyCount += 1`, shared between the `followArrived` and
   `reedReplyArrived` reactive blocks. This makes the counter correct
   regardless of how many distinct wire events describe the same reply.

2. **Server (belt-and-suspenders — reduces redundant traffic, not
   strictly required once #1 lands):** in `handlePublishReady`, compute the
   reply-subscriber recipient set and the follower recipient set together
   and exclude subscriber-set members from the `FOLLOW_REED` follower
   fanout when the reed being fanned out is itself a reply (i.e. has a
   `replying` parent), so a viewer who's already getting `REED_REPLY` for
   that thread doesn't also get a redundant `FOLLOW_REED` for it. This
   avoids double delivery at the source, cuts needless
   `DATA_ACK` round-trips, and is defense-in-depth if any other client
   surface consumes both queues without the fix in #1.

Fix #1 is necessary and sufficient to resolve the bug from the client's
perspective and should be done first. Fix #2 is optional cleanup to reduce
redundant server fanout once #1 is confirmed working.

## Status: both fixed

1. Client dedup landed in `ec887d7` — `+page.svelte` now tracks
   `countedReplyIds` across both `followReedQueue` and `reedReplyQueue`, so
   a reply is only ever counted once regardless of how many wire events
   describe it.
2. Server-side exclusion landed in `2f40e5c` — `handlePublishReady` now
   looks up the direct parent reed's subscribers before fanning out and
   excludes them from the `FOLLOW_REED` follower set when the new reed is a
   reply, since they're already getting `REED_REPLY` for it.
