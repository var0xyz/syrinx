# Pipes 00 — Design + naming + locked model

## Status

Proposed.

## Depends on

—

## Context

Hashtags in reed markdown already become links
([`reedMarkdown.ts`](../../spa/src/lib/utils/reedMarkdown.ts) →
`web+syrinx://channel/…`), but there is no destination page and no live
subscription. Mentions
([conversations 04](../conversations/04_mentions.md)) show the pattern:
parse at `SignReed`, keep a tip index, discard the body.

Users want: click `#tag` → a live view of that tag. The network does not
owe them the full history of that tag—only what is blowing through **now**,
plus whatever matching reeds they already hold.

## Scope

- Name and URI scheme for the feature.
- Lock ephemeral-server / durable-local semantics.
- Outline tag index, WS subscribe, fanout on `PUBLISH_READY`, and SPA page.

## Non-goals

- Server-side hashtag search or paginated history API.
- Persistent “follow this tag” across sessions without an open pipe
  (v1 = subscribe while the pipe page is mounted; reconnect while still
  on the page may resubscribe).
- Autocomplete of tags in the composer (may come later).
- Federation of pipe subscriptions across instances.

## Naming

“Channel” and “stream” are accurate but generic (Slack/Discord/Twitch).
Syrinx’s myth is Pan and the naiad **Syrinx**: she becomes river-reeds;
Pan’s breath through those reeds becomes music; he binds unequal reeds
into the **syrinx** (panpipes).

| Candidate | Fit | Notes |
|-----------|-----|--------|
| **Pipe** | Strong | One tube of the syrinx; you put your ear to `#tag` and hear what sounds through it. Pairs with **reed** (the post). |
| Reedbed | Strong | A bed of reeds of one kind; a bit heavier / place-like. |
| Murmur | Poetic | Ovid’s soft wind in the marsh reeds — live, ambient, forgetful. Less obvious as a noun for a screen. |
| Ladon | Myth-pure | The river of the transformation — live current. Opaque to new users. |
| Stream | Clear | Too product-generic; overlaps “livestream.” |

**Locked product term: pipe.**

- UI: “Pipe `#climate`”, route `/pipe/climate`.
- Wire: `SUBSCRIBE_PIPE` / `UNSUBSCRIBE_PIPE`, `tag` field.
- URI: `web+syrinx://pipe/<tag>` (replace provisional `…://channel/…`).

## Design

### Terms

- **Tag** — normalized hashtag string without `#` (lowercase).
- **Pipe** — the live listening surface for one tag.
- **Listener** — a connected client that has `SUBSCRIBE_PIPE` for that tag.

### Ephemeral server, sticky local

```text
Server:  no tag timeline; only tip metadata + who is listening now
Client:  lists reeds already in IndexedDB that carry the tag;
         while subscribed, new matching reeds are verified and stored
```

Opening the pipe again shows those stored reeds (e.g. the three received
last time) even though the server never replays them. Missed publishes
while not subscribed are not recoverable from the server.

This matches philosophy: ambient flood is not a write ticket; **choosing
to open a pipe** is the agreement to keep what arrives there (same family
as opening a reed or following an author—not broadcast session storage).

### Sequence

```mermaid
sequenceDiagram
  participant Author
  participant HTTP as SignReed
  participant WS as Realtime
  participant Listener

  Listener->>WS: SUBSCRIBE_PIPE tag=climate
  Author->>HTTP: POST /reeds (content has #climate)
  HTTP->>HTTP: verify, tip + reed_tags rows, pending fanout
  HTTP-->>Author: ServerSignature
  Author->>WS: PUBLISH_READY
  WS->>WS: fanout followers + pipe listeners for tags
  WS->>Listener: delivery (relay path as for other new reeds)
  Listener->>Listener: verify, storeReed, show on /pipe/climate
```

### Tag extraction

At `SignReed`, parse tags from `content` with the same rules as the SPA
(`(^|\s)#\S+` → trim `#` → lowercase → unique). Persist tip rows in
`reed_tags` so `PUBLISH_READY` can fan out without the body. Drop rows on
reed/account removal (same cleanup family as `reed_mentions`).

### Live delivery

Reuse the existing new-reed relay machinery (pending event → holder →
`DATA_RESPONSE` / ack). Pipe listeners are an additional recipient set
beside followers / broadcast / profile subs—not a second content path.

Author’s own reed is already local; listeners who do not hold it yet go
through relay.

### SPA

1. Rename link helper/path from `channel` → `pipe`.
2. Route `/pipe/[tag]`: title `#tag`; list local reeds matching tag
   (newest first); while mounted, `SUBSCRIBE_PIPE` / on destroy
   `UNSUBSCRIBE_PIPE`.
3. On live arrival for this tag: verify, `storeReed`, prepend to the list.
4. Offline: show local matches; subscription waits until reconnect (no
   server backfill).

### Tag rules (v1)

- Normalize as above.
- Empty tag after `#` is not a tag.
- No server-side ban list in v1 (operators may add later).
- Max tags per reed: inherit content size limits; no separate cap required
  unless abuse appears.

## Open points (resolve in 01–02)

- Exact WS payload shape when protobuf cutover lands (string `type` until
  then, matching today’s JSON WS).
- Whether leaving the pipe page always unsubscribes (yes for v1) or a
  background “pinned pipes” list exists (out of scope).
