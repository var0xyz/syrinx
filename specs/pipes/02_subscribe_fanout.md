# Pipes 02 — Subscribe + fanout

## Status

Implemented.

## Depends on

[01](01_extract_stash.md), publish READY
([publish 01](../publish/01_publish_ready.md))

## Context

Listeners need a WS subscription. Fanout must run only after
`PUBLISH_READY` so holders can serve relay (same race as follows). Tag
names for the tip are already on the claimed `pending_fanout` row
([01](01_extract_stash.md)).

## Scope

- `SUBSCRIBE_PIPE` / `UNSUBSCRIBE_PIPE` with `{ tag }` (normalized
  server-side).
- Connection-local set of tags per client; drop on disconnect.
- In-memory reverse index: tag → current listeners (for SignReed intersect
  and READY fanout).
- On `PUBLISH_READY`, pipe listeners receive **`PIPE_REED`**; followers who
  are not on the pipe receive **`FOLLOW_REED`**. Overlap prefers one
  `PIPE_REED` (SPA also updates followcast when the viewer follows the author).
- No sync catch-up of historical tagged reeds for pipes.

## Non-goals

- Persistent tag follows in DB.
- Deduplicating delivery if the listener is also a follower (delivering
  twice is wasteful—prefer one pending event per recipient reed; lock
  “union of recipient sets” in implementation).

## Work

1. Wire handlers in `realtime` JSON (and later protobuf) paths.
2. Track subscriptions in the connection manager (+ tag → listeners map).
3. SignReed intersect uses that map when writing `pending_fanout.tags`.
4. READY: use claimed `tags`; resolve current listeners; union into fanout;
   row already deleted by claim.
5. Tests: subscribe bookkeeping; filter tags with listeners; union/subtract
   recipient sets; disconnect clears via `clearPipeSubscriptions`.

## Acceptance

- Only current subscribers of the stored tags receive live pipe deliveries
  at READY.
- Offline / unsubscribed publishes do not queue for later pipe catch-up.
- No durable tag tip rows remain after READY.
- Listener who is also a follower gets one `PIPE_REED` (SPA also updates followcast).
