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
- Document interaction with coverage `allocation_count` bumps (same TX)
  when that feature lands.

## Non-goals

- Changing `RELAY_RESPONSE` / `DATA_ACK` allocate path.
- Client-side “defer miss until store” heuristics.

## Design

### Server `handleRelayMiss`

When holder `H` reports miss for pending event `E` on reed `R`:

1. Verify `H` is the expected relay target for `E` (same checks as today).
2. **`DeleteReedAllocation(R, H)`** (and decrement `allocation_count` when
   coverage counters exist — same TX).
3. Reset `E.dispatched_at` (or equivalent) so the event is eligible again.
4. **`dispatchNext`** / pick another online holder for `R` (exclude `H`).
5. If no other holder is available, leave the pending event undispatched
   until a holder appears (SYNC / later allocate) — do **not** delete the
   author’s tip metadata.

### Author after READY

Author remains allocated from SignReed. First `RELAY_REQUEST`s after READY
should hit IndexedDB. If the author still misses (cleared storage, bug),
removing their allocation is correct; recovery is republish is impossible
for same id — they must re-acquire content (e.g. another holder) or the
pending waits. Tip row stays.

### Client

On `RELAY_REQUEST`: if reed in local `reeds` → `RELAY_RESPONSE`; else →
`RELAY_MISS`. Do not special-case “I just published this.” Fanout after
READY makes the publish race a non-issue; the client answers immediately.

### Tests / checklist

- READY then relay → response, no miss.
- Holder without content → miss → their allocation gone; another holder
  receives `RELAY_REQUEST` when available.
- Sole holder misses → allocation removed; pending undispatched until a
  new holder exists (no silent ignore).
- Ignore-miss temporary behaviour removed from `handleRelayMiss`.
