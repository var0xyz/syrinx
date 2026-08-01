# Pipes 02 — Subscribe + fanout

## Status

Proposed.

## Depends on

[01](01_tag_index.md), publish READY
([publish 01](../publish/01_publish_ready.md))

## Context

Listeners need a WS subscription. Fanout must run only after
`PUBLISH_READY` so holders can serve relay (same race as follows).

## Scope

- `SUBSCRIBE_PIPE` / `UNSUBSCRIBE_PIPE` with `{ tag }` (normalized
  server-side).
- Connection-local set of tags per client; drop on disconnect.
- On `PUBLISH_READY` fanout, for each tag on the tip, enqueue new-reed
  delivery to current listeners of that tag (in addition to existing
  follower / broadcast / profile paths).
- No sync catch-up of historical tagged reeds for pipes.

## Non-goals

- Persistent tag follows in DB.
- Deduplicating delivery if the listener is also a follower (delivering
  twice is wasteful—prefer one pending event per recipient reed; lock
  “union of recipient sets” in implementation).

## Work

1. Wire handlers in `realtime` JSON (and later protobuf) paths.
2. Track subscriptions in the connection manager.
3. Extend READY fanout to query `reed_tags` for the reed and add listeners.
4. Tests: subscribe → publish tagged reed → listener gets relay path;
   unsubscribe → no delivery; disconnect clears subs.

## Acceptance

- Only current subscribers receive live pipe deliveries.
- Offline / unsubscribed publishes do not queue for later pipe catch-up.
