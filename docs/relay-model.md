# Relay model (implementation reference)

[Content distribution](/content) and [Architecture](/architecture) describe
the tracker/relay design conceptually. This page is the schema-level
reference for the same machinery — written as groundwork for federation,
which needs to know exactly which identities are hard-wired to a local
`users` row today, and where.

## Tables

### `reed_allocations`

Who currently holds a verified copy of a reed's body.

```sql
CREATE TABLE reed_allocations (
    reed_id VARCHAR(255) NOT NULL,
    holder_user_id VARCHAR(255) NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    author_user_id VARCHAR(255) NOT NULL,
    delivered_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP,

    PRIMARY KEY (holder_user_id, author_user_id, reed_id),
    FOREIGN KEY (author_user_id, reed_id) REFERENCES reeds(user_id, id) ON DELETE CASCADE
);
```

- `holder_user_id` is a **hard FK to `users(id)`** — a holder must be a
  local user row.
- `(author_user_id, reed_id)` FKs to `reeds(user_id, id)` — the reed's
  metadata row, not its body (the server never stores bodies).
- A reed can have many allocation rows (many holders); a holder earns one
  by successfully verifying a body they requested (`DATA_ACK` — see
  below).

### `pending_events` / `pending_reed_events` / `pending_account_events`

The event ledger that gates every relay delivery — nothing is pushed to a
client without a row here first.

```sql
CREATE TABLE pending_events (
    event_id VARCHAR(255) PRIMARY KEY,
    request_id VARCHAR(255) NOT NULL,
    requester_user_id VARCHAR(255) NOT NULL REFERENCES online_users(user_id) ON DELETE CASCADE,
    event_name VARCHAR(255) NOT NULL,
    subscription_id VARCHAR(255) REFERENCES profile_subscriptions(subscription_id) ON DELETE CASCADE,
    dispatched_at TIMESTAMP,
    created_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP
);

CREATE TABLE pending_reed_events (
    event_id VARCHAR(255) PRIMARY KEY REFERENCES pending_events(event_id) ON DELETE CASCADE,
    user_id VARCHAR(255) NOT NULL,   -- the reed's author
    reed_id VARCHAR(255) NOT NULL,
    FOREIGN KEY (user_id, reed_id) REFERENCES reeds(user_id, id) ON DELETE CASCADE
);

CREATE TABLE pending_account_events (
    event_id VARCHAR(255) PRIMARY KEY REFERENCES pending_events(event_id) ON DELETE CASCADE,
    user_id VARCHAR(255) NOT NULL REFERENCES users(id) ON DELETE CASCADE
);
```

- `requester_user_id` is a **hard FK to `online_users(user_id)`**, which
  is itself FK'd to `users(id)`. A pending event's requester must be a
  currently-online **local** user — not just any local user, an online one.
- `pending_reed_events.user_id` (the reed's author) and
  `pending_account_events.user_id` are also hard FKs to local tables.
- Every identity slot in this ledger — requester, author, holder — is
  wired to a row that only exists on this server.

## Lifecycle: explicit fetch (`REQUEST_REED`)

1. Viewer sends `REQUEST_REED { requestID, reedID, authorID }`.
2. Server creates a `pending_events` row (`requester_user_id` = viewer,
   `event_id` fresh) + a `pending_reed_events` row (author, reed), replies
   `REQUEST_ACK`.
3. Server picks a holder: `GetOnlineReedHolder` joins `reed_allocations` ×
   `online_users` on `(author_user_id, reed_id)` and takes the **first
   row** — no ranking, no preference, arbitrary DB order among online
   holders.
4. `dispatchNext(holderID)` finds the **oldest undispatched** pending
   event whose subject is allocated to that holder
   (`GetNextPendingForHolder`, `ORDER BY created_at LIMIT 1`), atomically
   claims it (`UPDATE ... SET dispatched_at = now() WHERE dispatched_at IS
   NULL` — race-safe if this ever runs on multiple replicas), and sends
   the holder `RELAY_REQUEST { eventID, authorID, reedID }`.
5. Holder replies `RELAY_RESPONSE { eventID, data }`, or `RELAY_MISS` if
   it no longer actually has the content (deletes that holder's
   allocation row, server retries a different holder).
6. Server looks up the pending event by `event_id`. **No matching row →
   no delivery.** This is the abuse guardrail: a forged `RELAY_RESPONSE`
   citing an unknown or already-consumed `event_id` is a no-op.
7. Server delivers `DATA_RESPONSE { eventID, requestID, reedID, userID,
   data, username }` to the **requester recorded on that event** — never
   to whoever happens to be asking right now.
8. Viewer verifies signatures. Valid → `DATA_ACK` (server inserts a new
   `reed_allocations` row for the viewer, deletes the pending event, which
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

## Wire messages (`realtime/messages.go`, `realtime/wire.go`)

| Direction | Message | Carries |
|---|---|---|
| Viewer → Server | `REQUEST_REED` | `requestID, reedID, authorID` |
| Server → Viewer | `REQUEST_ACK` | `requestID, eventID, reedID` |
| Server → Holder | `RELAY_REQUEST` | `eventID, authorID, reedID` |
| Holder → Server | `RELAY_RESPONSE` | `eventID, data` |
| Holder → Server | `RELAY_MISS` | `eventID` (holder no longer has it) |
| Server → Viewer | `DATA_RESPONSE` / `FOLLOW_REED` / `PIPE_REED` / `BROADCAST_REED` | `eventID, requestID, reedID, userID, data, username` |
| Viewer → Server | `DATA_ACK` / `DATA_INVALID` | `eventID` |
| Server → Viewer | `REED_NOT_FOUND` / `REED_NOT_HELD` | failure paths — no metadata, or metadata exists but no holder |

## Why this matters for federation

Every identity slot in this pipeline — `reed_allocations.holder_user_id`,
`pending_events.requester_user_id` (via `online_users`),
`pending_reed_events`/`pending_account_events.user_id` — is a **hard FK to
a local table**. None of them can hold a federated identity
(`~user@remote-server`) as-is: there is no local `users` row, and
therefore no local `online_users` row, for someone who lives on another
instance.

The design that closes this gap: `pending_events.requester_user_id`
becomes nullable (`NULL` = the requester is a peer server, not a local
user) with a new `server_id` column recording *which* server is asking;
`pending_reed_events` drops its FK to `reeds` and gets its own `server_id`
recording the *author's* server. `dispatchNext` and the rest of the
holder-selection machinery on this page stay local-only and untouched —
the branch between "dispatch to a local holder" and "relay to a peer
server" happens once, at the point a `pending_events` row is created, not
inside the dispatch loop itself. Full design, including the new
`POST /api/federation/relay/reed` endpoints and their auth:
[`specs/federation/06_content_relay`](https://github.com/var0xyz/syrinx/tree/main/specs/federation/06_content_relay.md).

## Related

- [Content distribution](/content) — the conceptual walkthrough and abuse
  guardrails this page underpins
- [Architecture](/architecture) — tracker/relay role of the server
- [`specs/federation/`](https://github.com/var0xyz/syrinx/tree/main/specs/federation)
  — the handshake spec (peering) and
  [06_content_relay.md](https://github.com/var0xyz/syrinx/tree/main/specs/federation/06_content_relay.md)
  (cross-instance relay, proposed)
