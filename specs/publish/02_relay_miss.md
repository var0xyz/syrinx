# Publish 02 — Real `RELAY_MISS` (drop allocation + retry)

## Status

Proposed.

## Depends on

[01](01_publish_ready.md)

## Context

While the publish race existed, `handleRelayMiss` **ignored** misses so the
sole author allocation was not deleted and the reed orphaned. With fanout
gated on `PUBLISH_READY`, the author can serve before any
`RELAY_REQUEST`, so miss can mean genuine unavailability again.

All relay requests are treated equally — no special case for freshly
published reeds.

## Scope

- Restore miss handling: remove the reporting holder’s allocation; clear or
  reset the pending event’s dispatch state; try another online holder.
- **`REED_NOT_HELD`** — terminal WS response when no holder can serve a reed
  (see below). Applies to every `REQUEST_REED` and to pending events once
  all holders are exhausted.
- Document interaction with coverage `allocation_count` bumps (same TX)
  when that feature lands.

## Non-goals

- Changing `RELAY_RESPONSE` / `DATA_ACK` allocate path.
- Client-side “defer miss until store” heuristics.

## Design

### Open question — metadata without holders

**Question:** Reed **metadata** exists on the server (`reeds` row) but
`reed_allocations` has **no holder** other than the requester (or no
holders at all). Should the server still accept `REQUEST_REED`?

**Resolution (locked):** **Yes.** Accept the request, then fail fast with a
**terminal** response instead of leaving the client waiting forever. The
server knows the body is not on the network under current allocations; the
requester should treat the reed as **unheld** (body lost / unavailable).

This is **not** the same as `REED_NOT_FOUND` (no server metadata). Wire name:
**`REED_NOT_HELD`**.

Account recovery bootstrap returns all non-removed ids including unheld
reeds ([account recovery 02](../account_recovery/02_challenge_bootstrap.md)).

### `REED_NOT_HELD` (terminal — no serving holder)

**When to send**

1. **`REQUEST_REED`** — after confirming the reed row exists, before or
   instead of creating a long-lived pending event: if
   `reed_allocations` has **no** `holder_user_id` for this
   `(author_user_id, reed_id)` **excluding the requester**, send
   `REED_NOT_HELD` immediately.
2. **`RELAY_MISS` exhaustion** — pending event exists; the last allocated
   holder missed and no other holder (online or offline) remains → send
   `REED_NOT_HELD` to the requester and delete the pending event.

**Wire (sketch)**

```json
{
  "type": "REED_NOT_HELD",
  "data": {
    "request_id": "<client request id>",
    "reed_id": "<reed id>",
    "author_id": "<author user id>"
  }
}
```

**Client:** delete the outstanding `reedRequests` row (same as
`REED_NOT_FOUND`); surface quiet “content unavailable on the network” UX
where appropriate. Do not retry proactively unless the user explicitly
re-requests.

**Distinction**

| Message | Meaning |
|---------|---------|
| `REED_NOT_FOUND` | No `reeds` row (unknown / never on this server) |
| `REED_NOT_HELD` | Metadata exists; no peer holds the body (excluding requester) |
| `RELAY_MISS` + retry | A holder existed but could not serve; try another |
| Pending forever | **Not allowed** when unheld is knowable |

### Server `handleRelayMiss`

When holder `H` reports miss for pending event `E` on reed `R`:

1. Verify `H` is the expected relay target for `E` (same checks as today).
2. **`DeleteReedAllocation(R, H)`** (and decrement `allocation_count` when
   coverage counters exist — same TX).
3. Reset `E.dispatched_at` (or equivalent) so the event is eligible again.
4. **`dispatchNext`** / pick another online holder for `R` (exclude `H`).
5. If **no other holder** exists in `reed_allocations` (excluding the
   requester on `E`), send **`REED_NOT_HELD`** to the requester and delete
   `E` — do not leave the pending event undispatched indefinitely.
6. If another holder exists but is offline, leave `E` undispatched until
   SYNC / holder connect (same as today).

### Author after READY

Author remains allocated from SignReed. First `RELAY_REQUEST`s after READY
should hit IndexedDB. If the author still misses (cleared storage, bug),
removing their allocation is correct; recovery is republish is impossible
for same id — they must re-acquire content (e.g. another holder) or receive
`REED_NOT_HELD` when no holders remain. Tip row stays.

### Client

On `RELAY_REQUEST`: if reed in local `reeds` → `RELAY_RESPONSE`; else →
`RELAY_MISS`. Do not special-case “I just published this.” Fanout after
READY makes the publish race a non-issue; the client answers immediately.

### Tests / checklist

- READY then relay → response, no miss.
- Holder without content → miss → their allocation gone; another holder
  receives `RELAY_REQUEST` when available.
- Sole holder misses → allocation removed → `REED_NOT_HELD` when no holders
  left (not infinite pending).
- `REQUEST_REED` for metadata-only reed (zero non-requester holders) →
  `REED_NOT_HELD` without `RELAY_REQUEST`.
- `REED_NOT_HELD` vs `REED_NOT_FOUND` distinct on the wire.
- Ignore-miss temporary behaviour removed from `handleRelayMiss`.
