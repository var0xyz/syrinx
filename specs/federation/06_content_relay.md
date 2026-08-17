# Federation 06 — Cross-instance content relay

## Status

Proposed. Not yet implemented.

## Depends on

[04](04_runtime_verify_display.md) (peer authentication —
`federation_established.fingerprint`), [05](05_revoke_established.md)
(revoked peers must be rejected here too)

## Context

[01](01_invitation_create.md)–[05](05_revoke_established.md) establish
**peering**: two admins agree to trust each other's server. They do not
move any reed content between instances — the original README explicitly
listed "cross-instance reed relay" as a v1 non-goal. This step removes
that non-goal and defines how it actually works, by extending the
existing same-server relay machinery (see [Relay
model](/relay-model)) rather than building a parallel system.

Same-server relay today: a viewer's `REQUEST_REED` creates a
`pending_events` row, the server asks an online **holder** (another local
user who already verified a copy) for the body, and delivers it back to
the requester over their own WebSocket. Every identity slot in that
pipeline is a hard FK to a local `users` row — see [Relay
model](/relay-model#why-this-matters-for-federation) for the exact
constraints this step removes.

## Scope

- Schema: `server_id` on `pending_events` and `pending_reed_events`;
  `requester_user_id` becomes nullable; drop the `reeds` FK on
  `pending_reed_events`
- `POST /api/federation/relay/reed` (peer → this server: "relay me this
  reed")
- `POST /api/federation/relay/reed/{eventId}/response` (peer → this
  server: "here's the content you asked me for")
- Dispatch-time branch: local author → existing holder dispatch,
  unchanged; remote author → new federation relay path

## Non-goals

- Federated follow (a follower on one instance subscribing to fanout from
  an author on another) — this step only covers explicit fetch
  (`REQUEST_REED`)
- Presence/online-status sharing and durable event delivery (mentions,
  deletions) between servers — see [07](07_presence_delivery.md)
- Live client notification of a mid-session peering revoke

## Design

### Schema changes

```sql
-- pending_events: requester is EITHER a local online user OR a peer server.
ALTER TABLE pending_events ALTER COLUMN requester_user_id DROP NOT NULL;
-- (requester_user_id keeps its existing FK to online_users(user_id))
ALTER TABLE pending_events ADD COLUMN server_id VARCHAR(16) NOT NULL
    REFERENCES servers(id);
-- server_id is the delivery target: self for a local requester, the
-- peer's id when requester_user_id IS NULL (inbound federation request,
-- or — see 07 — an outbound notification with no requester at all).

-- pending_reed_events: the reed's author can now be on another server.
ALTER TABLE pending_reed_events DROP CONSTRAINT pending_reed_events_user_id_reed_id_fkey;
ALTER TABLE pending_reed_events ADD COLUMN server_id VARCHAR(16) NOT NULL
    REFERENCES servers(id);
-- server_id here is the AUTHOR's origin (self or a peer) — a different
-- fact from pending_events.server_id (the requester's origin). The common
-- case "peer asks me about my own local author" has
-- pending_events.server_id = peer, pending_reed_events.server_id = self.
```

No new tables. `pending_reed_events.event_id` stays 1:1 with
`pending_events.event_id`, so a single join gives every fact needed:
who's asking (`requester_user_id` or `pending_events.server_id`) and
what's being asked for (`user_id`/`reed_id`/`pending_reed_events.server_id`).

### Dispatch-time branch (not inside `dispatchNext`)

`dispatchNext`/`GetNextPendingForHolder`/the holder-selection machinery
stay local-only and untouched — they only ever reason about "which local
holder should I ask," which never changes for federation.

The branch happens once, at every call site that today creates a
`pending_events` row and then calls `dispatchNextIfConnected` (the
`REQUEST_REED` handler, follow-fanout, sync catch-up, removal fanout):
after inserting the rows, check the subject's `server_id`.

```go
if authorServerID == self {
    dispatchNextIfConnected(holderID)   // existing, unchanged
} else {
    relayToFederatedServer(ctx, authorServerID, eventID)   // new
}
```

### `POST /api/federation/relay/reed` (on the author's server)

Called by a peer that has a local requester wanting a reed authored here
(or previously ingested/allocated here).

**Auth:** signed request, same pattern as [04](04_runtime_verify_display.md#peer-authentication-runtime)
— caller's server signature verified against a `federation_established`
row with `revoked = false`. No such row, or revoked → `401`.

**Body:**

```json
{
  "serverId": "<caller's server id>",
  "authorId": "...",
  "reedId": "...",
  "requestEventId": "<caller's own pending_events.event_id>"
}
```

`requestEventId` is the caller's own correlation id, minted when it
created its local `pending_events` row — this server never mints or
remaps it; it's echoed back verbatim on the callback, so the caller does a
plain `WHERE event_id = $1` lookup with no new correlation table.

**Fast refuse** (synchronous, no async path entered):

- Reed doesn't exist locally → `404`
- Reed exists but `reed_allocations` has **zero rows ever** for it (never
  held by anyone, so relay is structurally impossible) → `404`

"No holder online *right now*" is **not** a refuse condition — same as
the local case, an offline holder can still come back online, and the
caller's request waits (see Timeouts below).

**Otherwise:** insert a local `pending_events` row
(`requester_user_id = NULL`, `server_id` = caller's id) +
`pending_reed_events` row (`server_id` = self), run the existing local
`dispatchNextIfConnected` against a local holder exactly as for any other
pending event, respond:

```
202 { "federationEventId": "<this server's own event_id>" }
```

(Informational only for the caller — the caller doesn't need to store
this id; the callback in the next step is keyed by the caller's own
`requestEventId`, not this one.)

### `POST /api/federation/relay/reed/{requestEventId}/response` (on the requesting server)

Called by the peer once its local holder has relayed the content back
(inside its own `handleRelayResponse`, branching on
`requester_user_id IS NULL` — see below).

**Auth:** same signed-request scheme as above.

**Body:**

```json
{ "data": "<signed reed body>" }
```

Requesting server looks up `pending_events WHERE event_id =
$requestEventId`, verifies it's actually waiting on that peer
(`server_id` matches the caller), and delivers to the original client via
the existing local delivery path (`DATA_RESPONSE`), exactly as if a local
holder had relayed it. The viewer verifies signatures and sends
`DATA_ACK`/`DATA_INVALID` exactly as today — federation is invisible to
the client past this point.

### `handleRelayResponse` branch (on the author's/relaying server)

The existing function that runs when a local holder sends
`RELAY_RESPONSE` gets one new branch, at the delivery step:

```go
pe := GetPendingReedEvent(eventID)
if pe.RequesterUserID == nil {
    federationDeliverCallback(pe.ServerID, pe.EventID, data)  // new: signed HTTP POST to the peer
} else {
    // existing: DATA_RESPONSE over the requester's WebSocket
}
```

Lookup, verification, and cleanup (pending-event deletion, allocation
bookkeeping) stay shared between both branches — only the final delivery
transport differs.

### Timeouts

No forced expiry. A federated request that never resolves (holder never
comes online on the peer, the peer goes down mid-flight) behaves exactly
like a local pending event waiting on an offline holder — it waits
indefinitely. This matches the existing design rather than introducing
new failure-mode UX.

### Concurrent requesters

No special handling needed. `pending_events` is already keyed by
`event_id`, not `(author, reed)` — multiple simultaneous requesters
(local and/or federated) for the same content already produce independent
rows today, each drained in turn by `dispatchNext` once a holder is
available. A federation-inbound row is just one more row in that queue.

## Tests

- Fast refuse: unknown reed → `404`, no `pending_events` row created
- Fast refuse: reed exists, zero allocations ever → `404`
- Full round trip: A requests a B-authored reed; B relays via a local
  holder; A's original client receives `DATA_RESPONSE` and can verify it
- Peer not `federation_established` (or revoked) → `401` on both
  endpoints, no rows created/no callback attempted
- Two simultaneous requesters (one local, one federated) for the same
  reed both resolve independently
- Federated request with no holder ever allocated → immediate `404`,
  never enters the async path
- Federated request with a holder allocated but currently offline → `202`
  accepted, resolves later when the holder reconnects (no timeout)
