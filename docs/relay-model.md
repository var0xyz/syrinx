# Relay model (implementation reference)

[Content distribution](/content) and [Architecture](/architecture) describe
the tracker/relay design conceptually. This page is the schema-level
reference for the same machinery, local and across server borders. The
DDL lives in `InitDB` (`src/backend/db.go`).

## Tables

### `reed_allocations` / `reed_server_allocations`

Who currently holds a verified copy of a reed's body.

```sql
CREATE TABLE reed_allocations (
    reed_id VARCHAR(255) NOT NULL REFERENCES reed_identities(id) ON DELETE CASCADE,
    holder_user_id VARCHAR(255) NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    delivered_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP,
    PRIMARY KEY (holder_user_id, reed_id)
);

CREATE TABLE reed_server_allocations (
    reed_id VARCHAR(255) NOT NULL REFERENCES reed_identities(id) ON DELETE CASCADE,
    server_id VARCHAR(16) NOT NULL REFERENCES servers(id) ON DELETE CASCADE,
    delivered_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP,
    PRIMARY KEY (reed_id, server_id)
);
```

- `holder_user_id` is a local user; a holder earns a row by verifying a
  body it requested (`DATA_ACK` — see below).
- `reed_id` references `reed_identities`, not `reeds`, so a local user can
  hold a **foreign** reed the same way as a local one.
- `reed_server_allocations` records that a **peer server** has a holder of
  one of our reeds: one row per peer, never per remote user. It is the
  fallback when no local holder is online.

### `pending_events` and its subject tables

The event ledger that gates every relay delivery — nothing is pushed to a
client without a row here first. All of these are `UNLOGGED`: they are
in-flight state, not history.

```sql
CREATE UNLOGGED TABLE pending_events (
    event_id VARCHAR(255) PRIMARY KEY,
    request_id VARCHAR(255) NOT NULL,
    requester_user_id VARCHAR(255) REFERENCES online_users(user_id) ON DELETE CASCADE,
    event_name VARCHAR(255) NOT NULL,
    subscription_id VARCHAR(255) REFERENCES profile_subscriptions(subscription_id) ON DELETE CASCADE,
    dispatched_at TIMESTAMP,
    dispatched_to VARCHAR(255),   -- only this holder may answer
    relayed_at TIMESTAMP,         -- content reached the requester; gates DATA_ACK
    created_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP
);
```

Each event has one subject row: `pending_reed_events` (a `reed_id` in
`reed_identities`), `pending_account_events` (a `user_id` in `identities`),
or `pending_key_events` (a revoked `key_id`). Local and foreign subjects
look the same.

### Cross-border bookkeeping

| Table | Lives on | Records |
|-------|----------|---------|
| `foreign_pending_events` | The requester's server | Which peer is home to the reed, and the id that peer knows the event by |
| `foreign_relay_requests` | The reed's home server | Which peer, user and key an event is really for; the event itself is attributed to a per-peer sentinel identity |
| `pending_thread_parts` | The requester's server | The verified parts of a foreign thread relayed for an event, allocated on `DATA_ACK` |

## Lifecycle: explicit fetch (`REQUEST_REED`)

1. Viewer sends `REQUEST_REED { requestID, reedID, authorID }`.
2. Server creates a `pending_events` row (`requester_user_id` = viewer,
   `event_id` fresh) + a `pending_reed_events` row (author, reed), replies
   `REQUEST_ACK`.
3. Server picks a holder: `GetOnlineReedHolder` joins `reed_allocations` ×
   `online_users` on `reed_id` and takes the **first row** — no ranking,
   no preference, arbitrary DB order among online holders. For a foreign
   reed with no local holder online, the request crosses to the reed's
   home server instead (see [Crossing a server border](#crossing-a-server-border)).
4. `dispatchNext(holderID)` finds the **oldest undispatched** pending
   event whose subject is allocated to that holder
   (`GetNextPendingForHolder`, `ORDER BY created_at LIMIT 1`), atomically
   claims it (`UPDATE ... SET dispatched_at = now() WHERE dispatched_at IS
   NULL` — race-safe if this ever runs on multiple replicas), and sends
   the holder `RELAY_REQUEST { id, data: { reedID, requesterID } }` —
   `requesterID` so the holder can resolve that user's key and encrypt
   the body to them before responding (see [Content privacy](/content_privacy)).
   No separate author field: `reedID` is already the canonical id and
   already embeds the author.
5. Holder replies `RELAY_RESPONSE { id, data: <ciphertext> }` — a bare
   armored-PGP string, no wrapper object; `RELAY_MISS { id }` if it no
   longer actually has the content (deletes that holder's allocation
   row, server retries a different holder); or `RELAY_ERROR { id }` if
   it has the content but couldn't complete encryption (e.g. failed to
   resolve the requester's key — allocation is kept, server still
   retries another holder).
6. Server looks up the pending event by that id. **No matching row →
   no delivery.** This is the abuse guardrail: a forged `RELAY_RESPONSE`
   citing an unknown or already-consumed id is a no-op.
7. Server delivers `DATA_RESPONSE { id, data: { requestID, ciphertext } }`
   to the **requester recorded on that event** — never to whoever happens
   to be asking right now. No `reedID`/`userID`: the real reed id only
   exists inside the decrypted ciphertext, so an outer copy would only
   ever duplicate it.
8. Viewer verifies signatures. Valid → `DATA_ACK`, accepted only once
   `relayed_at` is set (server inserts a new `reed_allocations` row for the viewer, deletes the pending event, which
   cascades to its subject row). Invalid → `DATA_INVALID` (pending event
   deleted, no allocation).
9. After resolving one event for a holder, the server calls
   `dispatchNext` again for that same holder to drain any other pending
   work queued for them.

Fanout paths (new-reed-from-followed-author, catch-up on reconnect,
removal notices, account-removal notices) all funnel through the same
`pending_events` insert → `dispatchNext` → `RELAY_REQUEST`/`RELAY_RESPONSE`
→ `DATA_RESPONSE`-family delivery pipeline; only the `event_name` and
which wire message wraps the payload differ (`FOLLOW_REED`, `PIPE_REED`,
`BROADCAST_REED`).

## Wire messages (`src/backend/proto/websocket.proto`)

The event/relay id lives at the message **root** as `id`, sibling to
`type`, not nested inside `data` — so a payload never reads as the
confusing `data.data` a fully-nested shape would produce.

| Direction | Message | Root `id`? | `data` carries |
|---|---|---|---|
| Viewer → Server | `REQUEST_REED` | no | `requestID, reedID, authorID` |
| Server → Viewer | `REQUEST_ACK` | yes | `requestID, reedID` |
| Server → Holder | `RELAY_REQUEST` | yes | `reedID, requesterID` (no author field — `reedID` already embeds it) |
| Holder → Server | `RELAY_RESPONSE` | yes | a bare ciphertext string (armored PGP, opaque to the server) — not wrapped in an object |
| Holder → Server | `RELAY_MISS` | yes | — (holder no longer has it — allocation dropped) |
| Holder → Server | `RELAY_ERROR` | yes | — (holder has it, couldn't complete the relay — allocation kept) |
| Server → Viewer | `DATA_RESPONSE` / `FOLLOW_REED` / `PIPE_REED` / `ARCHIVE_REED` / `REED_REPLY` | yes | `requestID, ciphertext` (opaque to the server; no `reedID` — it only exists after decrypting) |
| Server → Viewer | `BROADCAST_REED` | no | `ciphertext, username` (no event to ack — ephemeral) |
| Server → Viewer | `REED_REMOVED` / `ACCOUNT_REMOVED` | yes | `requestID, data` (`data` carries the signed removal cert, which has its own `reedID`/`userID`) |
| Viewer → Server | `DATA_ACK` / `DATA_INVALID` | yes | — |
| Server → Viewer | `REED_NOT_FOUND` / `REED_NOT_HELD` | no | failure paths — no metadata, or metadata exists but no holder |

## Foreign requesters

A request from a member of a peer server can't be a normal pending event:
that member has no `users` or `online_users` row here. The home server
attributes the event to a **per-peer sentinel identity** and records the
real requester in `foreign_relay_requests`. Dispatch to a local holder runs
unchanged; on delivery, `deliverOrForward` sees the foreign request and
sends the ciphertext to the requesting peer over HTTP instead of a
WebSocket. The holder encrypts to the key the requester's server named
(`requesting_key_id`).

## Crossing a server border

Federation follows one rule: **fold at the border.** Whatever a server sends
a peer, it sends once per piece of content per peer, and the peer
distributes it among its own users with the same machinery it uses for
local reeds. A thousand readers on a peer must cost the home server one
delivery, not a thousand.

Content is the one thing that can't cross as a single copy for everyone:
a holder encrypts a reed for the one requester it is relaying to, so each
copy that crosses serves exactly one user on the other side. That user
then becomes a holder there, and the rest of the peer's readers are served
locally. So for content, folding means capping how many copies cross per
reed per peer, not sending exactly one.

How content crosses, per reed and per peer:

- At most **3** copies are in flight across the border at once. Three, not
  one, so a single requester going offline mid-transfer never strands
  everyone else.
- Carriers are the waiting requesters in arrival order, online ones only.
- When a crossing ends without the carrier acking it (the carrier
  disconnects, the home server has no holder, the relay errors), the next
  waiting requester takes its place.
- A carrier that acks becomes a holder on this server. From then on the
  normal holder bursts spread the reed locally and no further copies
  cross, unless every local holder goes offline while requests wait.

Status. Every path that crosses the border folds:

- Content requests: a request for a foreign reed is served by a local
  holder when one is online, and otherwise crosses only while fewer than
  three copies are in flight (`openForeignEvent`, `promoteWaitingForeign`).
- Events a peer is owed: new reeds, reed and thread removals, account
  removals, key revocations and server key rotations go to every peer;
  blocks and follows of a peer's user go to that user's server alone. Each
  is one row in that peer's queue, written with the event and deleted once
  the peer answers. A peer that is down keeps its queue until it boots, and
  a slow one holds up only its own (`federation_delivery.go`,
  [`specs/federation/10_peer_event_queue.md`](https://github.com/var0xyz/syrinx/tree/main/specs/federation/10_peer_event_queue.md)).
  The peer dispatches to its own followers, mentioned users, subscribers
  and thread viewers.
- Profile pages: the author's server returns one page of reed ids and the
  asking server opens each one itself, through the same fold. Profile
  subscriptions stay on the viewer's own server; the author's server never
  hears of them.
- Live stats: one `reed-stats` push per (peer, reed), forwarded by the peer
  to its own subscribers. A peer that answers 404 has nobody watching any
  more, and its subscriptions to that reed are dropped.
- Blocks: a block of a user on a peer concerns only that one user, so it
  crosses once, to their home server (`block-notify` / `unblock-notify`),
  through that server's queue. The peer delivers it to its user and
  enforces it locally ([`specs/blocking/`](https://github.com/var0xyz/syrinx/tree/main/specs/blocking)).

## Related

- [Content distribution](/content) — the conceptual walkthrough and abuse
  guardrails this page underpins
- [Content privacy](/content_privacy) — why relay payloads are encrypted
  and how the holder resolves a requester's key
- [Architecture](/architecture) — tracker/relay role of the server
- [`specs/federation/`](https://github.com/var0xyz/syrinx/tree/main/specs/federation)
  — the handshake spec (peering) and
  [06_content_relay.md](https://github.com/var0xyz/syrinx/tree/main/specs/federation/06_content_relay.md)
  (cross-instance relay)
