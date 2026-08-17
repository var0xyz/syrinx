# Federation 07 — Server presence + durable event delivery

## Status

**The problem this doc solves shipped a fire-and-forget version, with no
durability.** `servers.online`, `pending_mention_events`, the
online/offline/ping endpoints, and the entire drain-on-reconnect
mechanism described below were never built. What actually ships for
"this server has something to tell a peer" (mention-notify, reply-notify,
echo-notify, reply/echo-removal-notify — `federation_relay.go`) is a
direct synchronous signed HTTP `POST`, fired from a detached goroutine
at the moment of the local event (e.g. `handlers.go`'s `SignReed`
dispatching `notifyForeignMentionToPeer`), with a short timeout and a
logged-but-swallowed error on failure. **There is no fallback path**: if
the peer is unreachable at that instant, the notification is simply
lost — no backlog row, no retry, no re-delivery when the peer comes
back. This is a real gap this doc anticipated and explicitly designed
around (`servers.online` + backlog drain), but the shipped
implementation does not have it. If durable delivery to a
currently-unreachable peer is ever needed, this doc's design (or
something like it) is still the right starting point — it was never
disproven, just not built.

## Depends on

[06](06_content_relay.md) (`pending_events.server_id` as delivery target,
`pending_reed_events.server_id`)

## Context

[06](06_content_relay.md) covers explicit fetch: a peer asks this server
for a reed, synchronously, and waits. This step covers the opposite
direction — this server has something to *tell* a peer without being
asked (a mention of one of its users, a reed removal, an account
removal) — and the peer might not be reachable right now.

Same philosophy as content relay: reuse `pending_events`, don't invent a
queue. The only new primitive is `servers.online`, and a drain loop that
looks like `dispatchNext` but targets a peer over HTTP instead of a local
holder over WebSocket.

## Scope

- `servers.online BOOLEAN NOT NULL DEFAULT FALSE`
- `POST /federation/server/{id}/online`, `.../offline`, `.../ping`
- Live delivery path: synchronous HTTP push at event-creation time when
  the target peer is online
- Fallback: write to `pending_events` (existing schema, no new table)
  when the peer is offline or the live push fails
- Backlog drain on `.../online`, flipping `online = true` only once
  drained

## Non-goals

- Federated follow
- Open federation / discovery
- Automated reciprocal revoke
- Ordering or idempotency guarantees beyond "one HTTP request/response is
  one delivery" — see [Design](#no-queue-semantics) below
- Retrying a *live* push that fails for a reason other than "peer
  offline" (e.g. a `4xx`) — falls back to the backlog like any other
  failure; no special-casing by status code

## Design

### `servers.online`

```sql
ALTER TABLE servers ADD COLUMN online BOOLEAN NOT NULL DEFAULT FALSE;
```

Tracked independently per side of a peering — this server's view of
whether *the peer* is reachable, not a shared/negotiated value. Each
server only ever writes the `online` column on its own peer rows.

### Why reuse `pending_events`

A durable event bound for a peer is structurally the same shape as an
inbound federation relay request from [06](06_content_relay.md): a
`pending_events` row with `requester_user_id = NULL` and `server_id` =
the delivery target. The generalization from "requester's origin" to
"delivery target" (already applied to [06](06_content_relay.md)'s schema
comment) is exactly what makes this reuse possible — no new table.

The subject of the event reuses the existing subject tables where they
already fit:

- Reed removal → `pending_reed_events` (`ReedRemovedEvent`), unchanged
  shape from the local case
- Account removal → `pending_account_events` (`AccountRemovedEvent`),
  unchanged shape
- Mention of a foreign user → new subject table `pending_mention_events`
  (below) + new `MentionEvent` `EventName`

```sql
CREATE TABLE pending_mention_events (
    event_id VARCHAR(255) PRIMARY KEY REFERENCES pending_events(event_id) ON DELETE CASCADE,
    mentioning_user_id VARCHAR(255) NOT NULL,
    mentioning_reed_id VARCHAR(255) NOT NULL,
    mentioned_user_id VARCHAR(255) NOT NULL,
    FOREIGN KEY (mentioning_user_id, mentioning_reed_id)
        REFERENCES reeds(user_id, id) ON DELETE CASCADE
);
```

`mentioned_user_id` is **not** FK'd — it names a user row on the peer,
which this server has no local row for. This mirrors
`reed_mentions.mentioned_server_id`/`mentioned_user_id`, which already
anticipated this case (see `db.go`); this step is what actually starts
populating a federated mention instead of rejecting it at write time.

`mentioning_user_id`/`mentioning_reed_id` stay hard-FK'd because the
mentioning reed is always authored locally in this flow — a server only
ever notifies a peer about a mention *it* is the source of.

### Live delivery path

At every call site that today creates one of these `pending_*_events`
rows for a **foreign** target (`server_id != self`):

1. If `servers.online` for that target is `true`: attempt a synchronous
   signed `POST` to the peer (payload shaped like the corresponding
   local WebSocket push — `ReedRemovedEvent`/`AccountRemovedEvent`
   payload, or a new mention payload). Success (`2xx`) → done, no
   `pending_events` row written at all.
2. Any failure (peer offline, timeout, non-2xx) → write the
   `pending_events` + subject row exactly as the offline case below, and
   — if the failure indicates the peer is actually unreachable, not just
   a transient app-level error — flip `servers.online = false` for that
   peer immediately, so subsequent events for the same peer skip
   straight to step 2 instead of each retrying a live push that's
   already known to be failing.
3. If `servers.online` is already `false`: skip the live attempt
   entirely, go straight to the `pending_events` write.

This is the same "branch once, at creation, never inside the dispatch
primitive" rule [06](06_content_relay.md) established for content relay.

### `POST /federation/server/{id}/online`

Peer announces itself reachable again (or for the first time after
establishment). Auth: signed request, same peer-authentication pattern
as [04](04_runtime_verify_display.md#peer-authentication-runtime).

Handler:

1. Respond `204` immediately — ack only, no payload, no synchronous
   drain on the request path.
2. Spawn a background goroutine that drains that peer's backlog: repeatedly
   find the oldest undispatched `pending_events` row with `server_id` =
   that peer (shape mirrors `GetNextPendingForHolder`/`dispatchNext`,
   but the delivery step is a signed HTTP `POST` to the peer instead of
   a `RELAY_REQUEST` over WebSocket), deliver it, delete the row on
   success (or leave it and stop the drain on failure — see below).
3. When the drain finds nothing left to dispatch, set
   `servers.online = true` for that peer.

No transaction or row locking around the drain. Correctness comes from
ordering, not isolation: the flip to `online = true` happens strictly
after the drain observes an empty backlog. Anything created *during* the
drain (via the live-delivery path's failure branch, since the peer is
still `online = false` at that point) lands as a fresh `pending_events`
row, which the still-running drain loop will also pick up before it
re-checks for emptiness.

If a delivery attempt inside the drain fails, the goroutine stops
(leaving the remaining backlog in place) rather than marking the peer
online on a partial drain. The peer stays `online = false`; the next
successful `/online` call (or a queued retry — implementation detail,
not required for v1) starts a fresh drain attempt over the same
remaining rows.

### `POST /federation/server/{id}/offline`

Graceful-shutdown announcement. Auth: same. Handler sets
`servers.online = false` for that peer immediately and returns `204`.
No drain, no backlog interaction — this only stops future live-delivery
attempts from being tried against a peer that's about to stop
responding.

### `POST /federation/server/{id}/ping`

Unconditional heartbeat. Every server, on a fixed schedule, pings every
peer in `federation_established` with `revoked = false` — regardless of
whether that peer currently has a backlog. This is what keeps `online`
accurate for peers that are simply idle (no pending events either way),
since both [06](06_content_relay.md)'s live-relay dispatch branch and an
eventual admin UI read `online` directly, not "does this peer have a
backlog."

Receiving side: auth-check, respond `204`. No state change on receipt —
`/ping` only informs the *caller's* view of the callee, not the other
way around.

Sending side: on timeout or non-2xx, the pinging server flips its own
`servers.online = false` for that peer. This is the mechanism that
recovers from a peer that's alive but too degraded to complete a drain
(see below) — pings keep failing, live delivery stops being attempted,
backlog accumulates until the peer genuinely recovers, sends a fresh
`/online`, and starts a clean drain.

### No-queue semantics

No message broker, no ordering guarantee beyond `pending_events`'
existing `created_at` ordering, no idempotency tokens, no ack layer
separate from the HTTP response itself. A completed `2xx` response to a
delivery `POST` **is** the delivery guarantee — the same trust level
[06](06_content_relay.md) already places in a single request/response
pair for content relay. If stronger guarantees are ever needed, that's a
different, larger project (an actual queue), not an extension of this
one.

### Accepted failure mode

A peer that's alive-but-too-degraded-to-drain never gets marked online
via the drain-completion path — by design. That peer is also failing its
regular `/ping` around the same time (it can't be degraded enough to
stall a drain of its own backlog yet still reliably answer pings), which
flips it to `online = false` on this server's side and stops new live
attempts from piling onto an already-struggling peer. The system
self-heals once the peer actually recovers, rather than needing a
special "degraded" state.

## Tests

- Live push, peer online, succeeds → no `pending_events` row ever
  written
- Live push, peer online, request fails → `pending_events` row written,
  `servers.online` flipped to `false`
- Peer already offline → event goes straight to `pending_events`, no
  live attempt made
- `/online` with empty backlog → `servers.online` flips to `true`
  immediately (drain is a no-op)
- `/online` with a non-empty backlog → stays `false` until drain
  completes, flips `true` only after the last row is dispatched
- Event created mid-drain (peer still `online = false`) → lands in
  `pending_events`, picked up by the same in-flight drain
- Drain hits a delivery failure partway through → stops, peer stays
  `offline`, remaining rows untouched
- `/offline` → `servers.online` flips to `false` immediately, no drain
  triggered
- `/ping` timeout → pinging server flips its local view of that peer to
  `false`
- `/ping` success → no state change
- Mention of a foreign user → `pending_mention_events` row created (or
  delivered live) instead of being silently dropped
- Peer not `federation_established` (or revoked) → `401` on all three
  endpoints, no state change
