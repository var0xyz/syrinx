# Ripples 03 — Realtime fanout

## Status

Proposed.

## Depends on

[02](02_post_and_list_api.md)

## Context

Someone with a reed's detail page open should see new responses appear
live, the same way other reed-scoped realtime events already work (e.g.
`REED_STATS`/`REED_REPLIES` per
[`conversations/00_design.md`](../conversations/00_design.md)).

The delivery mechanism is not a direct call from the HTTP handler. This
codebase's dispatch path is indirect: an HTTP handler pushes a message
onto the app's shared broadcast channel; a separate dispatch goroutine
(`RealtimeService`'s broadcast-handling loop) reads from that channel and
is the one that actually invokes
`ConnectionManager.SendToReedSubscribers(authorUserID, reedID string,
payload any) error` (`realtime/connection_manager.go:409`). Every other
reed-scoped realtime event in this codebase (echo counts, reply counts,
like counts, reply posted, reed removed) already goes through this same
indirection — this step adds a new message type and a new dispatch
branch, it does not call `SendToReedSubscribers` directly from the
handler.

The realtime payload carries the same full signed shape the POST/GET
endpoints return (see [02](02_post_and_list_api.md)), because a client
receiving a `RIPPLE_POSTED` event over WS must be able to run the
identical verify-or-discard path described in [00](00_design.md)'s
Client-side verification section — realtime delivery is explicitly
**not** a trusted shortcut around signature checking, it's just a faster
transport for the same signed object a list fetch would eventually
deliver.

## Scope

- New `BroadcastType` values `RIPPLE_POSTED` and `RIPPLE_UPDATED`.
- Wire structs for the payload (now including signature fields), shared
  across both event types.
- A new dispatch branch (alongside the existing ones for echo/reply/like
  counts) that receives the broadcast-channel message and calls
  `SendToReedSubscribers`.
- Push points in the POST and DELETE handlers from
  [02](02_post_and_list_api.md), after their respective DB transactions
  commit.
- `RIPPLE_UPDATED` for self-deletes is **required**, not optional — see
  Design below for why.

## Non-goals

- Typing indicators, presence, or read receipts on ripples.
- Cross-instance/federated delivery (ripples never leave the server, per
  [README](README.md) Non-goals).
- Delivery guarantees beyond best-effort — a client that's offline when a
  response posts just sees it on next `GET .../ripples` fetch; no queued
  replay of missed realtime events (consistent with how this codebase's
  other realtime events already behave — WS is a live-update convenience,
  not a durable event log).
- Any server-side "verify before broadcasting a second time" step — the
  payload pushed here is exactly what was already verified once at POST
  time (the server's own countersignature step, see
  [01](01_schema_and_expiry.md)); the server does not re-verify its own
  just-written row before fanning it out.

## Design

### New `BroadcastType` constants (`realtime/types.go`)

```go
RipplePosted  // fired on successful POST
RippleUpdated // fired on soft-delete (content -> "[DELETED]")
```

There is no `RipplePosted`-adjacent `RippleDeleted` type. Because
self-delete is a **soft** delete (see [00](00_design.md),
[01](01_schema_and_expiry.md)) — the row is patched in place, never
removed — the corresponding realtime event is `RippleUpdated`, and it is
required, not optional: a stale, un-patched row left showing old content
in an already-open viewer's list is a persistent *wrong-content* bug (the
row displays the pre-delete text indefinitely until a manual reload).

Add a `Ripple *RipplePayload` field to the shared `BroadcastMessage`
struct used to push onto the broadcast channel.

### Wire structs (`realtime/wire.go`)

```go
// RipplePayload is the wire shape of one ripple response — shared by the
// POST/GET HTTP response bodies (see 02) and both realtime message types
// below. Single source of truth: the SPA can reuse one TypeScript type for
// the fetch response and the WS payload, no separate mapping layer.
//
// Includes both signatures on every delivery, including RIPPLE_UPDATED
// for a tombstoned response — a client running the verify-or-discard path
// from 00 needs the full signed shape regardless of delivery channel;
// realtime is a transport, not a trust shortcut.
type RipplePayload struct {
    Hash            string          `json:"hash"`
    ThreadID        string          `json:"threadID"`
    UserID          string          `json:"userID"`
    Content         string          `json:"content"`
    ReplyingTo      *string         `json:"replyingTo"`
    Deleted         bool            `json:"deleted"`
    PostedAt        time.Time       `json:"postedAt"`
    UserSignature   SignatureWire   `json:"userSignature"`
    ServerSignature ServerSignature `json:"serverSignature"`
}

type RipplePostedMsg struct {
    Type   string        `json:"type"` // "RIPPLE_POSTED"
    UserID string        `json:"userID"` // reed author
    ReedID string        `json:"reedID"`
    Ripple RipplePayload `json:"ripple"`
}

// RippleUpdatedMsg — named Updated, not Deleted, because the client
// patches the matching row in place (deleted/content) rather than
// removing it from its list. See Scope above for why this event is
// required, not optional.
type RippleUpdatedMsg struct {
    Type   string        `json:"type"` // "RIPPLE_UPDATED"
    UserID string        `json:"userID"`
    ReedID string        `json:"reedID"`
    Ripple RipplePayload `json:"ripple"`
}
```

`SignatureWire`/`ServerSignature` reuse whatever existing nested wire
types this codebase already has for a user/server signature pair (the
same shape a reed's own wire response already nests) — no new signature
wire type is introduced here, only a new `RipplePayload` struct that
uses the existing ones. `Hash` is the field name (not `ID`), matching
[02](02_post_and_list_api.md)'s `hash` field — see [00](00_design.md)'s
Signing section for why the response's id is named for what it is.

### Trigger points

- End of `POST /reeds/{userID}/{reedID}/ripples`, after the insert
  transaction (which now includes the sign/countersign/hash steps from
  [01](01_schema_and_expiry.md)) commits successfully: push a
  `BroadcastMessage{Type: RipplePosted, UserID: reedAuthorID, ReedID:
  reedID, Ripple: &payload}` onto the broadcast channel, where `payload`
  is the exact same fully-signed object the HTTP response body returns —
  no separate construction, one source of truth.
- The dispatch goroutine's broadcast-handling loop gets a new branch for
  `RipplePosted`, calling a new notify method (alongside the existing
  per-event-type notify methods already used for reply/like counts) that
  itself calls `SendToReedSubscribers(authorUserID, reedID,
  RipplePostedMsg{...})`.
- Same shape for `RippleUpdated`, triggered at the end of `DELETE
  /ripples/{rippleID}` after the soft-delete commits — the pushed
  payload's `userSignature`/`serverSignature` are the **original**,
  unchanged signatures (soft-delete never touches them, per
  [01](01_schema_and_expiry.md)'s Soft delete section), even though
  `content` in the same payload now reads `"[DELETED]"`. A client
  receiving this event follows [00](00_design.md)'s tombstone-handling
  rule (trust the `deleted` flag, skip re-verification against the
  now-mismatched content) rather than treating the mismatch as a
  verification failure.
- Both fire-and-forget with the existing error-tolerance convention used
  elsewhere in this codebase (log on error, don't fail the HTTP response —
  the realtime push is a convenience layer on top of an already-successful
  write, not a condition of success).

### Subscription model

Reuses whatever subscription mechanism reed-detail viewers already use for
`REED_STATS`/`REED_REPLIES` — a client that has "opened" a given
`(authorUserID, reedID)` reed detail page is already subscribed to that
reed's broadcast channel (per
[`conversations/00_design.md`](../conversations/00_design.md)); no new
subscribe/unsubscribe API is needed, ripples just piggyback on the
existing per-reed subscription. A client only ever receives
`RIPPLE_POSTED`/`RIPPLE_UPDATED` events for reeds it has actively
subscribed to — there is no broader "all ripples on the server" feed.

## Work items

1. `realtime/types.go` — the two new `BroadcastType` constants and the new
   `Ripple` field on `BroadcastMessage`.
2. `realtime/wire.go` — `RipplePayload` (now including signature fields),
   `RipplePostedMsg`, `RippleUpdatedMsg`.
3. The dispatch goroutine's broadcast-handling file — new branches for
   `RipplePosted`/`RippleUpdated`, new notify methods calling
   `SendToReedSubscribers`.
4. `handlers.go` — push calls in the POST and DELETE ripple handlers
   (plain functions in the existing `main` package, per
   [README](README.md)'s Code organization note — not a separate
   `ripples/handlers.go` package file).
5. SPA: extend whatever WS message-type switch already handles
   `REED_STATS`/`REED_REPLIES` (see
   [`conversations/03_spa_reed_detail.md`](../conversations/03_spa_reed_detail.md)
   for the existing dispatch site) with `RIPPLE_POSTED`/`RIPPLE_UPDATED`
   cases — both cases route the payload through the same verify-or-discard
   logic a list fetch uses (see [00](00_design.md) and
   [04](04_spa_ripples_section.md)), not a raw trust-and-append.
   `RIPPLE_UPDATED` patches the matching row in place, it does **not**
   remove it.
6. Tests: posting a response while a second connected client is subscribed
   to that reed delivers the WS message with a full, independently
   verifiable signed payload; a client subscribed to a *different* reed
   does not receive it; soft-deleting fires `RIPPLE_UPDATED` (not a
   removal-style event) with `deleted:true, content:"[DELETED]"`, and the
   **original, unchanged** `userSignature`/`serverSignature` from the
   pre-delete post.

## Risks

- **Realtime payload is larger than before** — full signature objects
  (armor strings) travel over WS on every post/delete now, versus the
  prior model's plain-text-only payload. Not expected to matter at this
  product's message volume/scale, but a real size increase worth naming.
- **A client that skips verification on WS-delivered content would defeat
  the whole point of signing** — this is a correctness requirement on the
  SPA implementation (see [04](04_spa_ripples_section.md)), not something
  this step's server-side design can enforce; flagged here since it's the
  most likely place an implementer takes an unsafe shortcut ("it came
  from our own WS connection, surely it's fine").

## Dependencies

`realtime/connection_manager.go`'s existing `SendToReedSubscribers`, and
this codebase's existing broadcast-channel dispatch goroutine; no new
infrastructure dependency. The existing signature/countersignature wire
types this codebase already defines for reeds (reused, not reinvented).

## Parallelism

[04](04_spa_ripples_section.md) needs both this step and 02; the SPA can
build against 02's plain fetch first and layer the WS live-append/patch on
once this lands (graceful degradation to poll-only in the interim).
