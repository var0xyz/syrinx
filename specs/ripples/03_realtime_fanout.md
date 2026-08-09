# Ripples 03 — Realtime fanout

## Status

Proposed.

## Depends on

[02](02_post_and_list_api.md)

## Context

Someone with a reed's detail page open should see new ripples appear live,
the same way other reed-scoped realtime events already work (e.g.
`REED_STATS`/`REED_REPLIES` per
[`conversations/00_design.md`](../conversations/00_design.md)). The
delivery primitive already exists —
`ConnectionManager.SendToReedSubscribers(authorUserID, reedID string,
payload any) error` at `realtime/connection_manager.go:409` — this step
just adds a new message type and calls it from the POST handler.

## Scope

- New `BroadcastType` `RIPPLE_POSTED`.
- Wire struct for the payload.
- Call `SendToReedSubscribers` from the end of the POST handler in
  [02](02_post_and_list_api.md), after the DB transaction commits.
- (Optional, small) `RIPPLE_DELETED` for self-deletes, so an open viewer's
  list doesn't show a stale entry until next reload.

## Non-goals

- Typing indicators, presence, or read receipts on ripples.
- Cross-instance/federated delivery (ripples never leave the server, per
  [README](README.md) Non-goals).
- Delivery guarantees beyond best-effort — a client that's offline when a
  ripple posts just sees it on next `GET .../ripples` fetch; no queued
  replay of missed realtime events (consistent with how this codebase's
  other realtime events already behave — WS is a live-update convenience,
  not a durable event log).

## Design

### Wire structs (`realtime/wire.go`)

```go
const BroadcastTypeRipplePosted BroadcastType = "RIPPLE_POSTED"
const BroadcastTypeRippleDeleted BroadcastType = "RIPPLE_DELETED"

type RipplePostedPayload struct {
    ReedUserID        string    `json:"reedUserID"`
    ReedID             string    `json:"reedID"`
    RippleID           string    `json:"id"`
    UserID              string    `json:"userID"`
    Content             string    `json:"content"`
    InReplyToRippleID *string   `json:"inReplyToRippleID"`
    PostedAt            time.Time `json:"postedAt"`
}

type RippleDeletedPayload struct {
    ReedUserID string `json:"reedUserID"`
    ReedID      string `json:"reedID"`
    RippleID    string `json:"id"`
}
```

Field shape matches the POST/GET response bodies from
[02](02_post_and_list_api.md) exactly — SPA can reuse the same TypeScript
type for both the fetch response and the WS payload, no separate mapping
layer.

### Trigger points

- End of the `POST /reeds/{userID}/{reedID}/ripples` handler, after the
  insert transaction commits successfully: build `RipplePostedPayload` and
  call `cm.SendToReedSubscribers(reedUserID, reedID, payload)`.
- End of the `DELETE /ripples/{rippleID}` handler, after the delete
  commits: same call with `RippleDeletedPayload`.

Both fire-and-forget with the existing error-tolerance convention used
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
existing per-reed subscription.

## Work items

1. `realtime/wire.go` — the two new types.
2. `ripples/handlers.go` — call sites in POST and DELETE.
3. SPA: extend whatever WS message-type switch already handles
   `REED_STATS`/`REED_REPLIES` (see
   [`conversations/03_spa_reed_detail.md`](../conversations/03_spa_reed_detail.md)
   for the existing dispatch site) with `RIPPLE_POSTED`/`RIPPLE_DELETED`
   cases.
4. Tests: posting a ripple while a second connected client is subscribed
   to that reed delivers the WS message; a client subscribed to a
   *different* reed does not receive it; delete fires the deleted event.

## Risks

- **None beyond what the existing reed-scoped broadcast primitive already
  carries** — this step adds a message type to infrastructure that's
  already proven for `REED_STATS`/`REED_REPLIES`, not new infrastructure
  itself.

## Dependencies

`realtime/connection_manager.go`'s existing `SendToReedSubscribers`; no new
dependency.

## Parallelism

[04](04_spa_ripples_section.md) needs both this step and 02; the SPA can
build against 02's plain fetch first and layer the WS live-append on once
this lands (graceful degradation to poll-only in the interim).
