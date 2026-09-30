# Federation 09 — Durable reed delivery between peers

## Status

**Proposed.** Supersedes the delivery half of
[07](07_presence_delivery.md), which was never built.

## Depends on

[06](06_content_relay.md), [08](08_server_reset.md) (`servers.down_at`
and the boot notice as a delivery trigger)

## Context

Everything a server tells a peer about its reeds is a single signed HTTP
call made when the event happens. If the peer is unreachable at that
moment, the notification is lost for good, and SYNC can't bring it back:
`catchUp` replays from local tables, and those rows only exist if the
call got through.

| Leg | Receiver writes | Lost when the call fails |
|---|---|---|
| `mention-notify` | `reed_mentions` row | the mention |
| `reply-notify` | reply reference | the reply, in the parent's count and thread |
| `echo-notify` | echo reference | the echo count |
| `reply-removal-notify`, `echo-removal-notify` | deletes the reference | counts keep the removed item |
| `reed-removal-notify` | removal cert | the removal: the peer keeps listing the reed and never evicts copies |
| `new-reed-notify` | nothing (live push to profile viewers) | the live update |

Foreign mentions also fail when both servers are up. `MentionNotifyFromPeer`
writes the `reed_mentions` row and dispatches nothing, and
`GetMissingMentions` joins `reeds`, which only holds locally signed reeds,
so the row is never read.

## Design

### Two legs instead of six

Each home server sends **every** connected peer two things, and the peer
decides who on its side should see them.

`POST /api/federation/relay/new-reed`

```json
{
  "reed_id": "alice@home1234/0190…",
  "author_id": "alice@home1234",
  "signed_at": "2026-09-30T12:00:00Z",
  "mentions": ["bob@peer5678"],
  "reply": { "parent_reed_id": "…", "thread_id": "…" },
  "echo": { "echoed_reed_id": "…", "is_blank": false }
}
```

`reply` and `echo` are optional and mutually exclusive. `mentions` can go
with either.

`POST /api/federation/relay/reed-removal` carries the signed removal cert,
the same fields `reed-removal-notify` carries today. It covers replies and
echoes too: the receiver finds its own references by the removed reed's
id.

They replace `new-reed-notify`, `mention-notify`, `reply-notify`,
`echo-notify`, `reply-removal-notify`, `echo-removal-notify` and
`reed-removal-notify`.

Unchanged and out of scope: account removal (its own channel),
`holder-notify`, and the live legs (`reed-stats`, subscribe/unsubscribe,
relay request/deliver/ack/cancel), which stay fire-and-forget.

### Streams and cursors

No event rows. What a peer hasn't received yet is derived from facts
already in logged tables, the same way `catchUp` derives what a user
missed.

A **stream** is one local author's history as seen by one peer: the
author's reeds (`reeds.signed_at`) and removals of those reeds
(`reed_removals` → `server_signatures.signed_at`), ordered by
`(timestamp, kind, reed_id)` with a creation sorting before a removal at
the same instant. Payloads are built at send time from `reeds`,
`reed_mentions`, `reed_replies` and the echo relation. Only locally
authored reeds are streamed; reeds imported by account recovery are not.

```sql
CREATE TABLE peer_author_cursors (
  peer_server_id VARCHAR(16)  NOT NULL REFERENCES servers(id) ON DELETE CASCADE,
  author_id      VARCHAR(255) NOT NULL REFERENCES identities(id) ON DELETE CASCADE,
  cursor_at      TIMESTAMP    NOT NULL,
  cursor_kind    SMALLINT     NOT NULL,
  cursor_reed_id VARCHAR(255) NOT NULL,
  claimed_at     TIMESTAMP,
  PRIMARY KEY (peer_server_id, author_id)
);
```

A missing row means the stream starts at `servers.created_at`, when the
peer was approved. A new peer gets no history.

### Delivering a stream

1. **Claim.** Insert the row at its start position if missing (`ON
   CONFLICT DO NOTHING`), then
   `UPDATE … SET claimed_at = now() WHERE … AND (claimed_at IS NULL OR
   claimed_at < now() - interval '1 minute') RETURNING`, joined against
   `servers` so a peer with `down_at` set can't be claimed. No row back
   means another goroutine owns the stream, or the peer is down: stop.
2. **Send.** Read the one item after the cursor, build its payload, send
   it.
3. **On `2xx`**, advance the cursor to that item and refresh
   `claimed_at`. Go to 2.
4. **On a transport error or `5xx`**, stop: later items wait, so a
   removal never overtakes its creation.
5. **On `4xx`**, the peer will never accept it. Log, advance past it,
   continue.
6. **Nothing left, or stopped:** set `claimed_at` back to `NULL`.

A claim older than a minute is treated as dead, so a goroutine that
crashed mid-stream holds it for at most that long.

No receiver dedupe. A crash between the peer's `2xx` and our cursor write
sends that item again; a repeated reed is discarded by the client, and
every receiver write is idempotent.

### Triggers

- **A local reed is created or removed:** deliver that author's stream to
  every peer that isn't down.
- **A client sends SYNC:** deliver every stream that is behind, to every
  peer that isn't down. This is the keepalive: a stream stuck on a failure
  retries at the next SYNC anywhere on the server.
- **A peer's boot notice** ([08](08_server_reset.md)): deliver every
  stream behind for that peer.

SYNC does not propagate to peers. The receiving server has everything it
needs to backfill its own users once it has the reed.

### Receiving

`new-reed`:

1. upsert the author's identity and the reed's `reed_identities` row, and
   the parent's or echoed reed's too: with one stream per author, a reply
   can arrive before its parent;
2. insert `reed_mentions` for mentioned local users;
3. if the parent or echoed reed is local, record the reply or echo
   reference (`InsertForeignReply`, `InsertForeignEcho`) and push the
   updated counts to that reed's subscribers;
4. dispatch to online local users: followers of the author, mentioned
   users, the author's profile subscribers, broadcast subscribers, and the
   parent reed's subscribers.

Every reed goes to broadcast subscribers. The publish-time `broadcast`
flag is never set by the SPA, and the server treats it as absent, so it
is not carried and likely to be dropped.

Offline users get it from `catchUp` on their next SYNC.

`reed-removal`: store the cert (`InsertReedRemoval`), drop any reply or
echo reference the reed held, and run the existing removal fan-out
(`HandleForeignReedRemoval`). A removal for a reed the receiver never saw
is stored anyway.

Content never moves. As today, a client that wants the body asks its own
server, which relays to the reed's home server by the id.

### Catch-up for foreign reeds

`reed_identities` gains `author_id` (`NOT NULL`, references
`identities`), filled at every insert from the same canonical-id parse
that already fills `server_id`. `GetMissingMentions`, `GetMissingOut`,
`GetMissingReplies` and `GetMissingRemovals` join `reed_identities` for
the author instead of `reeds`, so foreign reeds qualify. A foreign reed
found by catch-up is fetched through the foreign request path instead of
a local holder.

This fixes the undelivered foreign mention.

### Content crosses folded too

`new-reed` carries no content. Each recipient still needs the ciphertext,
and a holder encrypts it for one requester, so one copy crossing the border
serves one user. Folding content therefore caps crossings instead of
sending exactly one.

A request for a foreign reed, whether a client's `REQUEST_REED` or an
event the receiver creates for a `new-reed` recipient:

1. **A local holder is online:** create the event locally and dispatch it
   to that holder, exactly like a local reed. Nothing crosses.
2. **Otherwise, fewer than 3 requests to the home server are in flight for
   this reed** (`foreign_pending_events`): cross, as today.
3. **Otherwise:** create the event locally and let it wait. It is
   dispatched when a carrier acks and becomes a local holder.

When a crossing ends without an ack (the carrier disconnects and teardown
cancels it, the home server answers not-held, or the relay errors), the
oldest waiting event for that reed whose requester is online is promoted
to a crossing, so the cap stays filled while nobody local holds the reed.

A foreign reed counts as existing here once it has a `reed_identities` row
and no removal, so the local holder machinery accepts it.

### Cost

Every reed goes to every peer, and every SYNC runs one "which streams are
behind" query per peer. At most one request per stream is in flight at a
time. The mesh page asks admins to compare load before connecting.

## Non-goals

- Backfilling a new peer with reeds from before it was approved.
- Pipes. They are not federated today; a foreign reed's tags could later
  ride along as an optional field.
- Account removal, `holder-notify`, and the live legs.
- Multiple replicas beyond what the DB claim already makes safe. See
  `RISKS.md`.

## Tests

- A claim is refused while another is live and taken over once it is
  older than a minute; a peer with `down_at` set can't be claimed.
- A stream stops at the first `5xx` and resumes in order; a `4xx` is
  skipped.
- A removal is never sent before its creation.
- A reply that arrives before its parent is stored, and the parent
  arriving later links up.
- A `new-reed` mentioning a local user who is offline reaches them on
  their next SYNC.
- The boot notice delivers what piled up while the peer was down.
