# Federation 10 — One event queue per peer

## Status

Proposed. Not to be started before the branches pending merge land: it
touches most of `federation_relay.go`, `federation_delivery.go`, `blocks.go`,
`follows.go` and `realtime.go`.

## Depends on

[08](08_server_reset.md) (`down_at` and the boot notice), [09](09_reed_delivery.md)
(what it replaces).

## Context

Everything a server owes its peers travels today through one cursor and
five separate mechanisms, each with its own table, send loop, lock and
boot-time retry:

| Event | Endpoint | Owed through |
|---|---|---|
| New reed (replies, echoes, mentions included) | `relay/new-reed` | `peer_author_cursors`, a cursor per (peer, author) over `reeds` |
| Reed removal | `relay/reed-removal` | the same cursor, over `reed_removals` |
| Thread removal | `relay/thread-removal` | the same cursor, over `thread_removals` |
| Account removal, kicks included | `relay/account-removal-notify` | `account_removal_peer_notices` |
| Block, unblock of a peer's user | `relay/block-notify`, `relay/unblock-notify` | `block_notices` rows whose blocked user is foreign |
| Follow, unfollow of a peer's user | `relay/follow-notify` | `follow_notices` rows whose followee is foreign |
| User key revocation | `relay/key-revocation` | `public_key_server_allocations` |
| Server key rotation | `relay/server-key` | `server_mesh.peer_key_ack` |

The cursor starts every stream at the peering's `approved_at`, which is why
a restored peer's `approved_at` is set to the restore time instead of the
real one ([11](11_peering_record.md) needs the real one). The other five
each hold a 30 s context around a loop of sends.

## Why a queue, when users get a diff

Local users get no queue: `catchUp` works out what each one is owed from
state on every SYNC, because a device can disappear for good without
telling anyone, and a queue for it would grow forever. A peer is
different on both counts that made the diff necessary:

- **It has a lifetime.** We learn quickly that it's down (its shutdown
  notice, failed calls), and a peer that stays down is revoked and purged
  by its admins. Its backlog is bounded by how long they let it sit.
- **There are few of them.** One row per event per peer costs a handful of
  rows. One row per event per user is what the user model can't afford.

So a peer gets one row per event, deleted when it accepts it.

## Locked decisions

| Topic | Decision |
|---|---|
| Shape | One table, `peer_events`, one row per (peer, event). Deleted when the peer answers |
| Rows | Name a kind and its subject only. The payload is built from existing tables at send time, as `buildNewReedPayload` does today |
| Recipients | **Every peer** for new reeds, reed and thread removals, account removals, user key revocations and server key rotations. **Only the server concerned** for blocks (the blocked user's home) and follows (the followee's home): who blocks or follows whom is nobody else's business, and noise to them |
| Enqueued for | Every peer that isn't revoked, down ones included. A down peer's rows wait for its boot notice |
| Order | Oldest first, per peer, by the row's serial id |
| Down peers | The query that fetches a peer's next row requires `down_at IS NULL AND revoked_at IS NULL`; for a down peer it returns nothing |
| Answers | 2xx: delete. 4xx (except 429): the peer will never take it; run the kind's refusal step, log, delete. 5xx, 429, network error: stop the drain, keep the row |
| Isolation | One drain per peer, in its own goroutine, claimed so a second trigger doesn't start a second drain. A slow peer slows only its own queue |
| Timeout | The federation HTTP client's 3 s (`federationRequestTimeout`) is the only one. No timeout above 3 s on any peer call |
| Revocation | Revoking a peer deletes its queue. A reconnected peer starts empty |
| Purge | Cascades from `server_mesh`, like every other per-peer row |
| Wipe | The queue is lost with the database. A restored peer starts empty: delivery resumes from the restore, as it does today |
| Blank slate | No migration. The retired tables and columns are dropped from `InitDB` |

## Schema

```sql
-- What each peer is owed, oldest first. Rows name a subject; the payload
-- is built when the row is sent.
CREATE TABLE IF NOT EXISTS peer_events (
  id BIGSERIAL PRIMARY KEY,
  server_id VARCHAR(16) NOT NULL REFERENCES server_mesh(server_id) ON DELETE CASCADE,
  kind VARCHAR(32) NOT NULL CHECK (kind IN (
    'new_reed', 'reed_removal', 'thread_removal', 'account_removal',
    'block', 'follow', 'key_revocation', 'server_key')),
  subject_id VARCHAR(255) NOT NULL,
  -- The other side of a pair (the blocked user, the followee).
  object_id VARCHAR(255),

  UNIQUE NULLS NOT DISTINCT (server_id, kind, subject_id, object_id)
);

CREATE INDEX IF NOT EXISTS idx_peer_events_server ON peer_events (server_id, id);
```

`server_mesh` gains one column, operational and outside the signed peering
record:

```sql
-- Held by the drain sending this peer its queue; a claim older than a
-- minute is free again.
delivery_claimed_at TIMESTAMP,
```

| Kind | `subject_id` | `object_id` | Sent as | Built from |
|---|---|---|---|---|
| `new_reed` | reed id (thread head for a thread) | — | `relay/new-reed` | `buildNewReedPayload` |
| `reed_removal` | reed id | — | `relay/reed-removal` | `buildReedRemovalPayload` |
| `thread_removal` | thread id | — | `relay/thread-removal` | `GetThreadRemoval` → `pbThreadRemoval` |
| `account_removal` | user id | — | `relay/account-removal-notify` | `GetAccountRemoval`, as `sendOwedAccountRemovals` builds it |
| `block` | blocking user id | blocked user id | `relay/block-notify` if the block stands, `relay/unblock-notify` if not | `GetBlock` |
| `follow` | follower id | followee id | `relay/follow-notify` | the pair's row in `follow_certs`, follow or unfollow |
| `key_revocation` | revoked key id | — | `relay/key-revocation` | `GetKeyRevocation` |
| `server_key` | our current key id | — | `relay/server-key`, signed with that key | — |

`block` and `follow` carry the pair's **current** state, not the change that
queued them, so `UNIQUE` lets a pair hold one row per peer: queuing it again
while it waits does nothing (`ON CONFLICT DO NOTHING`), and whatever the
pair's state is when the row goes out is what the peer gets. That is what
`block_notices` and `follow_notices` do today with one row per pair.

## Enqueuing

Rows are written in the **same transaction** as the event they announce, so
a crash between the two can't lose one. Every-peer kinds insert one row per
non-revoked peer:

```sql
INSERT INTO peer_events (server_id, kind, subject_id)
SELECT server_id, $1, $2 FROM server_mesh WHERE revoked_at IS NULL
ON CONFLICT DO NOTHING
```

After the commit, the writer triggers a drain for every peer (see
[Triggers](#triggers)).

| Kind | Enqueued in | Today's equivalent |
|---|---|---|
| `new_reed` | `ClaimPendingFanout`'s transaction (`services.go`), for a reed or thread head published here; the claim is what makes a reed published | `deliverAuthorHook` on PUBLISH_READY |
| `reed_removal` | the store of a **local** reed's removal (`InsertReedRemoval` → `insertReedRemovalCert`, `services.go`, which runs on `*sql.DB` today and needs a transaction for this); a foreign reed's removal, or one restored in recovery, enqueues nothing | `deliverAuthorToPeers` (`handlers.go`, reed removal) |
| `thread_removal` | `InsertThreadRemoval` (`services.go`), local threads only | `deliverAuthorToPeers` (`handlers.go`, thread removal) |
| `account_removal` | `storeAccountRemovalTx`, local accounts only (self-removal and kick) | `QueueAccountRemovalNotices` |
| `block` | `oweBlockEventTx` (`blocks.go`), when the blocked user is foreign: one row for their home server | the foreign branch of `block_notices` |
| `follow` | `oweFollowCertTx` (`follows.go`), when the followee is foreign: one row for their home server | the foreign branch of `follow_notices` |
| `key_revocation` | the transaction inserting a **local** user key's `public_key_revocations` row (`AddPublicKey`'s rotation path in `services.go`, the rebind executor in `admin_rebind.go`); recovery's `insertRecoveryKeys` enqueues nothing | `notifyPeersOfKeyRevocation` |
| `server_key` | `insertServerKeyRevocationTx` (`server_key.go`), run by `ops rotate-key`. The ops binary only writes rows; the server drains them on its next boot | `notifyPeersOfServerKey` + `PeersAwaitingKey` at boot |

`block_notices` and `follow_notices` keep their local half: a local blocked
user's client and a local followee's client are still owed through them, by
SYNC. Only rows for foreign users move to `peer_events`.

## Draining

One function, `drainPeerEvents(peerID)`, replaces `drainPeerStream`,
`sendOwedAccountRemovals`, `sendOwedBlockNotices`, `sendOwedFollowNotices`,
`sendOwedKeyRevocations` and `notifyPeersOfServerKey`.

1. **Claim.** `UPDATE server_mesh SET delivery_claimed_at = now()` where the
   peer isn't revoked or down and the claim is free (NULL or older than a
   minute). No row updated: return. Released (set to NULL) on every return.
2. **Next row.**

   ```sql
   SELECT e.id, e.kind, e.subject_id, e.object_id, s.base_url
   FROM peer_events e
   JOIN server_mesh m ON m.server_id = e.server_id
   JOIN servers s ON s.id = e.server_id
   WHERE e.server_id = $1 AND m.down_at IS NULL AND m.revoked_at IS NULL
   ORDER BY e.id
   LIMIT 1
   ```

   Nothing: return. A peer that went down or was revoked mid-drain stops
   here on its own.
3. **Build** the payload for the kind (table above). A subject that no
   longer exists (purged, cascaded) can't be built: log, delete the row,
   go to 2.
4. **Send** with `callPeerRelayEndpoint`. The client's 3 s timeout is the
   only one.
5. **Answer.**
   - 2xx: run the kind's success step, delete the row.
   - 4xx other than 429: run the kind's refusal step, log at error level
     with the kind, subject and status, delete the row.
   - 5xx, 429, network error: log at warn level, keep the row, release the
     claim, return. The next trigger retries from this row.
6. Refresh the claim, go to 2.

| Kind | Success step | Refusal step |
|---|---|---|
| `follow` | — | undo the refused follow, as `SettleFollowNotice(cert, true)` does today |
| `server_key` | — | — (the peer walks our revocations on its next request either way) |
| every other kind | — | — |

## Triggers

`drainPeers(peerID)` starts `go drainPeerEvents(id)` for one peer, or every
non-revoked peer when `peerID` is empty. The claim makes extra triggers free.

| When | Trigger |
|---|---|
| After any enqueue commits | `drainPeers` for the peers it enqueued for |
| The peer's boot notice (`RealtimeResetFromPeer`, after clearing `down_at`) | `drainPeers(peer)`, replacing the five `sendOwed*`/`deliverBehindStreams` calls there |
| Our boot (`main.go`) | `drainPeers("")`, replacing the five `notifyPeersOf*` calls |
| Every SYNC from a local client | `drainPeers("")`, replacing `deliverBehindHook` (the keepalive that retries a peer stuck on an earlier failure) |
| A peer's approval | `drainPeers(peer)`, in case anything was queued between the `server_mesh` insert and the commit |

## Steps

Each step builds, passes `make test`, and leaves delivery working.

### 1. Queue, drain, triggers

- `InitDB`: `peer_events`, its index, `server_mesh.delivery_claimed_at`.
- `federation_delivery.go`: `enqueuePeerEventTx` (every-peer and one-peer
  variants), `drainPeers`, `drainPeerEvents`, the kind table as a
  `map[kind]peerEventSender` with build, success and refusal functions.
- `services.go`: `ClaimPeerEvents`, `ReleasePeerEvents`, `NextPeerEvent`,
  `DeletePeerEvent`.
- Revocation (`revokeFederationServerQ`) deletes the peer's rows in the
  same statement batch.
- Metric: `FederationDeliveryBacklog` reports `count(*)` of the peer's rows,
  read when a drain starts. Update the dashboard generator if the panel's
  title or description names cursors or authors.
- Tests: order is by id; a down or revoked peer gets nothing; 5xx and 429
  stop the drain and keep the row; 4xx deletes it; two concurrent drains of
  one peer send each row once; a slow peer (a handler that sleeps 2 s)
  doesn't delay another peer's drain; revocation empties the queue; a purge
  cascades.

### 2. New reeds, reed and thread removals

- Enqueue `new_reed` in `ClaimPendingFanout`, `reed_removal` in the local
  branch of the reed removal store, `thread_removal` in `InsertThreadRemoval`.
- Drop `deliverAuthorHook`/`deliverBehindHook`, `SetPeerDeliveryHooks`,
  `deliverAuthorToPeers`, `deliverBehindStreams`, `drainPeerStream`,
  `drainPeerStreamWith`, `peerStreamSender`, `peerStreamSQL`,
  `ClaimPeerStream`, `NextPeerStreamItem`, `AdvancePeerStream`,
  `ReleasePeerStream`, `BehindAuthors`, `ListDeliveryPeers` if nothing else
  reads it, and `peer_author_cursors` from `InitDB`.
- The receiving side doesn't change: `NewReedFromPeer`,
  `ReedRemovalFromPeer` and `ThreadRemovalFromPeer` are already idempotent.
  A removal that reaches the peer before its reed (impossible within one
  queue, possible across a wipe) is stored like any foreign removal.
- Tests: carry over `federation_delivery` and `federation_new_reed` tests to
  the queue: one row per peer per publish, removal after creation, nothing
  for a foreign reed's removal.

### 3. Account removals

- Enqueue `account_removal` in `storeAccountRemovalTx` for local accounts.
- Drop `notifyForeignAccountRemovalToPeers`, `sendOwedAccountRemovals`,
  `notifyPeersOfOwedAccountRemovals`, `QueueAccountRemovalNotices`,
  `AccountRemovalsOwedTo`, `DeleteAccountRemovalNotice` and
  `account_removal_peer_notices`.
- Tests: a self-removal and a kick each queue one row per peer and reach a
  peer that was down when they happened, once it boots.

### 4. Blocks

- `oweBlockEventTx`: for a foreign blocked user, enqueue `block` for their
  home server instead of writing `block_notices`.
- Drop `sendBlockNoticesFor`, `sendOwedBlockNotices`, `blockNoticeLocks`,
  `notifyPeersOfOwedBlockNotices`, `BlockNoticesOwedTo` and the peer
  branch of `DeleteBlockNotice`.
- Tests: block then unblock while the peer is down sends one
  `unblock-notify`; no other peer gets a row.

### 5. Follows

- `oweFollowCertTx`: for a foreign followee, enqueue `follow` for their home
  server instead of writing `follow_notices`.
- The refusal step undoes the follow, as `SettleFollowNotice` does.
- Drop `sendOwedFollowNotices`, `followNoticeLocks`,
  `notifyPeersOfOwedFollowNotices`, `FollowNoticesOwedTo` and the peer
  branch of `SettleFollowNotice`.
- Tests: follow then unfollow while the peer is down sends the unfollow
  once; a refused follow is undone; no other peer gets a row.

### 6. User key revocations

- Enqueue `key_revocation` for every peer where a local user key's
  revocation is stored.
- Drop `notifyPeersOfKeyRevocation`, `sendOwedKeyRevocations`,
  `notifyPeersOfOwedKeyRevocations`, `sendKeyRevocationToPeer`,
  `AllocatePublicKeyToServer`, `PublicKeyServerHolders`,
  `RevokedKeysOwedToServer`, `DeletePublicKeyServerAllocation` and
  `public_key_server_allocations`.
- The receiving side doesn't change. `verifyPeerKeyRevocation` resolves the
  revoked key to check its signature, so a peer that never held it fetches
  it from us once; `MarkAllocatedKeyRevoked` then finds no allocations and
  the peer answers 204.
- Tests: every peer gets the revocation and answers 204, including one that
  never fetched the key.

### 7. Server key rotations

- `insertServerKeyRevocationTx` enqueues `server_key` for every peer.
- Drop `notifyPeersOfServerKey`, `PeersAwaitingKey`, `SetPeerKeyAck` and
  `server_mesh.peer_key_ack`.
- Tests: after `rotate-key`, the server's boot drains one `server-key` call
  per peer, signed with the new key.

### 8. Timeouts

- Remove `peerDeliveryTimeout` (10 s), `vouchDeliveryTimeout` (10 s) and
  `realtimeResetTimeout` (5 s): every peer call is already capped at 3 s by
  the federation client, and the longer contexts only misstate the limit.
- No new peer-call timeout may exceed 3 s.

### 9. Docs and specs

- `docs/relay-model.md` → Crossing a server border: describe the queue and
  remove the paths it retires from the "still per user" list if they
  appear there.
- `specs/federation/09_reed_delivery.md`: mark superseded by this doc.
- `specs/federation/README.md` and `specs/README.md`: status rows.
- `RISKS.md`: no new finding. A slow peer slows only its own queue.

## Out of scope

- Narrowing every-peer kinds to peers with interested users.
- Retrying flags and vouches from the server instead of failing the user's
  request (synchronous today).
- Peers rebuilding what they were owed after **their** database is lost.
- The live channels: reed stats, the content relay, user search.
