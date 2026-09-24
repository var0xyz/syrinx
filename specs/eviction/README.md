# Client-side eviction under storage pressure

A client that is running out of local storage frees space by dropping a
whole user it has shown no interest in — every reed of theirs it holds,
then their profile and public key. The server is told first, with an
`EVICTION` event per reed, so its `reed_allocations` view never claims a
holder that has already deleted the content.

A cached profile the client holds **no** reeds for is evictable too. It is
already more stored information than a reed would be, and it is otherwise
unreachable: with reeds as the only entry point, such profiles would
accumulate forever with no way to ever be dropped.

**Blank slate — no migration, no backwards compatibility.** Recreate the DB
when schema changes.

---

## Status

**Implemented.**

| Piece | Where |
|-------|-------|
| `EVICTION` / `EVICTION_ACK` wire messages | `src/backend/proto/websocket.proto` |
| Server handler (`handleEviction`) | `src/backend/realtime.go` |
| Server tests | `src/backend/realtime_eviction_test.go` |
| Quota component | `src/frontend/src/lib/services/quota.ts` |
| Eviction component | `src/frontend/src/lib/services/eviction.ts` |
| Offline-first queue | `src/frontend/src/lib/repositories/pendingEvictions.ts` (`pendingEvictions` store) |
| Store-time gate | `ReedsService.storeReed` (`lib/repositories/reeds.ts`) |

## Locked decisions

| Topic | Decision |
|-------|----------|
| Threshold | **85%** of the browser's reported quota (`QUOTA_THRESHOLD`) |
| No estimate available | Treated as **under** threshold — never block a store we can't measure |
| Victim choice | A **random** evictable local user |
| Candidates | Any local user not protected below — **including** cached profiles with no held reeds |
| Protected | The viewer, everyone they follow, and every member of a list |
| Tombstones | Evictable: the signed cert lives in `removedAccounts`, and the profile page re-fetches it |
| Delete order | Every reed first; **profile and key only after the last reed is gone** |
| Per-reed order | `EVICTION` → server `EVICTION_ACK` → local delete. Never the reverse |
| Idempotency | The server **always** acks, including when it holds no such allocation |
| Unacked reed | Stays held (with its author's profile and key); a later attempt retries |
| Per store | **One** user queued, then the reed is stored. Still over threshold? The next store queues another |
| Blocking | **None.** Storing never waits on the server — 85% still leaves room for the incoming reed |
| Queue | `pendingEvictions`, keyed by reed id, carrying the victim's userID. Drained in the background and retried on reconnect/startup |
| Queued victims | Excluded from candidate selection, so pressure doesn't queue a second user for space already on its way |
| Coverage | A dropped allocation fires the usual `REED_COVERAGE` notify |

## Flow

1. `storeReed` asks the quota component whether the device is over threshold.
2. If it is, it asks the eviction component to `freeSpace()`.
3. `freeSpace()` picks one random evictable user and **queues** each of
   their reeds in `pendingEvictions`, then returns. A victim with no reeds
   has nothing to announce, so their profile and key go immediately.
4. The reed is stored — no waiting on the server.
5. In the background, the drainer sends `EVICTION` per queued reed and, on
   each `EVICTION_ACK`, deletes that reed and dequeues it. When a victim's
   last queued reed goes, their profile, info and key follow.

A reed stays in `reeds` while queued, so a `RELAY_REQUEST` for it is still
served — until the server acks, it genuinely is still held.

## Why the queue

An eviction that fails (socket down, ack lost, tab closed mid-drain) must
not strand the victim half-deleted: reeds gone but profile and key left,
or the reverse. The queue is the record of what was promised, so
`syncPendingEvictions()` can retry on reconnect and startup, and the
profile/key deletion keys off the queue being empty for that user rather
than off one in-memory pass succeeding.

## Why reedless profiles are evictable

Profiles and keys are cached whenever a reed, mention or reply needs an
author resolved, and they outlive the reeds that pulled them in — reeds
get evicted, removed by their author, or never held in the first place.
If only reed-holding authors were candidates, every one of those profiles
would be permanently unreachable by eviction.

They also pay for themselves. A PGP public key alone is larger than a
short reed body, so dropping a profile plus its key frees more than the
reed being stored needs. For the rarer profile cached with no key, one
round may not cover the incoming reed — but the next one, landing on a
candidate that does have a key, makes up the difference. There is no need
to weight the random pick by size or to evict repeatedly in one pass.

## Why ack-before-delete

The allocation table is what the server's relay routing and coverage
numbers are built on. If a client deleted first and told the server after,
any lost message would leave the server dispatching `RELAY_REQUEST`s to a
device that no longer has the content. Acking first costs one round trip
and makes the failure mode "still held" instead of "believed held".

The ack is unconditional for the same reason: a client whose ack was lost
must be able to retry and make progress, so an `EVICTION` for an
allocation the server doesn't have is a success, not an error.
