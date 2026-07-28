# Coverage 02 — Per-reed WS subscription + SPA

## Status

Proposed.

## Depends on

[01](01_counts_and_api.md)

## Context

Profile, broadcast, and follow fanout already exist. Reed detail does not
subscribe to a reed; allocation is a silent DB write on `DATA_ACK`. Coverage
needs a **reed-scoped** live channel so open detail pages update when holders
gain or lose the reed.

## Scope

- `SUBSCRIBE_REED` / `UNSUBSCRIBE_REED` wire messages.
- Server subscription table or in-memory map keyed by `(authorID, reedID)`.
- Emit coverage on allocate and deallocate (and when `active_users` changes
  only if needed — see below).
- SPA: subscribe on reed detail enter, unsubscribe on leave; render next to
  echoes.

## Non-goals

- Delivering reed **content** over this channel (still `REQUEST_REED` /
  existing queues).
- Fanout of `reed_removed` / `account_removed` certs (existing paths).
- Subscribing to all of an author’s reeds at once (use profile sub for that).

## Design

### Wire (client → server)

```json
{ "type": "SUBSCRIBE_REED", "userID": "<authorID>", "reedID": "<reedID>" }
```

```json
{ "type": "UNSUBSCRIBE_REED", "userID": "<authorID>", "reedID": "<reedID>" }
```

- Requires authenticated WS (same as other subs).
- `userID` is the **author** of the reed (path shape matches REST
  `/reeds/{userID}/{reedID}`).
- Unknown / removed reed: ack error or ignore subscribe (prefer explicit
  error so the client keeps REST snapshot only).
- Multiple tabs: allow multiple connections; each connection has its own
  subscription set. Disconnect clears that connection’s reed subs.

### Wire (server → client)

```json
{
  "type": "REED_COVERAGE",
  "userID": "<authorID>",
  "reedID": "<reedID>",
  "holders": 12,
  "activeUsers": 100,
  "coveragePercent": 12
}
```

Same field meanings as [`GET …/stats`](01_counts_and_api.md) coverage fields
(no `echoes` on this event — echo count is publish-time index, not
allocation-driven; refresh echoes via `/stats` or existing surfaces if
needed).

Optional immediate snapshot after successful subscribe (server pushes one
`REED_COVERAGE` with current counters) so the client can confirm sync; not
required if REST already ran.

### When to emit

| Trigger | Emit `REED_COVERAGE`? |
|---------|----------------------|
| Allocation inserted (`AllocateReed` / author seed / recovery insert that bumps count) | Yes, to subscribers of that reed |
| Allocation deleted (single or bulk that changes a reed’s count) | Yes |
| `active_users` increment/decrement | **Yes, to all reed subscribers** (coverage % depends on denominator) — or omit in v1 and accept stale `%` until next holder change / reopen. **v1 choice: emit to all active reed subscriptions on `active_users` change** (instance-wide; keep payload small). If that is too chatty, defer to reopen-only for denominator changes and document the tradeoff. Prefer **emit on allocate/deallocate always**; for `active_users` changes, **reopen/REST is enough in v1** (signup/removal are rarer relative to reading a reed). Locked for v1: **allocate/deallocate only**; `activeUsers` in the payload is current at emit time; signup mid-view may leave `%` slightly stale until next holder event or revisit. |

### Server sketch

- In-memory: `map[reedID]map[connID]*Client` (and author id for validation),
  or a small `reed_subscriptions` table if persistence across process restarts
  is unwanted — **in-memory is enough** (reconnect → client resubscribes).
- After allocate/deallocate TX commits, look up subscribers for `reedID`,
  read `allocation_count` + `active_users`, send `REED_COVERAGE`.

### SPA

On reed detail ([`+page.svelte`](../../spa/src/routes/reed/[userID]/[reedID]/+page.svelte)),
for **published** reeds (has `serverSignature`):

1. `GET …/stats` → set echoes + coverage line.
2. `SUBSCRIBE_REED` with page `userID` / `reedID`.
3. On `REED_COVERAGE` matching this reed → update holders / percent (and
   displayed `%`).
4. `onDestroy` / navigation away → `UNSUBSCRIBE_REED`.
5. Pending unsigned reed: no subscribe, no coverage line (unchanged).

Reuse `serverConnection` patterns from profile/broadcast subscribe. Deduplicate
handlers if layout already owns the socket.

Display (locked in [00](00_design.md)):

```
[megaphone-16] {echoes}   [cloud-line-chart-16] {coveragePercent}%
```

Mask both black PNGs to `currentColor` / `--muted`. Update live without full
page reload.

### Tests / checklist

- Subscribe → allocate another user → subscriber receives higher `holders`.
- Deallocate → lower `holders`.
- Unsubscribe → no further events.
- Disconnect clears sub; reconnect without subscribe → no events.
- Detail page: stats then live update; leave page → unsubscribe.
- Idempotent allocate → no spurious bump / duplicate coverage inflation.
