# Coverage 02 — Per-reed WS subscription + SPA

## Status

Implemented.

## Depends on

[01](01_counts.md)

## Context

Reed detail needs echoes and coverage without a parallel HTTP fetch. One
subscription carries the initial snapshot and later single-field updates.

## Scope

- `SUBSCRIBE_REED` / `UNSUBSCRIBE_REED`.
- Immediate snapshot ACK with **both** `echoes` and `coveragePercent`.
- Live `REED_ECHOES` (echoes only) and `REED_COVERAGE` (coveragePercent only).
- SPA: subscribe on enter, apply events, unsubscribe on leave.

## Non-goals

- Delivering reed **content** on this channel (still `REQUEST_REED` /
  existing queues).
- Fanout of removal certs (existing paths).
- HTTP stats snapshot for this UI.

## Design

### Wire (client → server)

```json
{ "type": "SUBSCRIBE_REED", "userID": "<authorID>", "reedID": "<reedID>" }
```

```json
{ "type": "UNSUBSCRIBE_REED", "userID": "<authorID>", "reedID": "<reedID>" }
```

- Authenticated WS. `userID` is the **author** of the reed.
- Unknown / removed reed: explicit error (client shows no stats line).
- Per-connection subscription set; disconnect clears it.
- Multiple tabs: each connection subscribes independently.

### Wire (server → client)

**Snapshot** — sent **immediately** after a successful subscribe, before any
other reed-stats event for that sub:

```json
{
  "type": "REED_STATS",
  "userID": "<authorID>",
  "reedID": "<reedID>",
  "echoes": 3,
  "coveragePercent": 12
}
```

| Field | Source |
|-------|--------|
| `echoes` | Non-removed echo count for this target (`reed_echoes`, same predicate as today’s echo index) |
| `coveragePercent` | Helper from [01](01_counts.md) |

**Echo update** — only when the echo count for this reed changes:

```json
{
  "type": "REED_ECHOES",
  "userID": "<authorID>",
  "reedID": "<reedID>",
  "echoes": 4
}
```

No `coveragePercent` on this message.

**Coverage update** — only when coverage for this reed changes due to
allocate/deallocate:

```json
{
  "type": "REED_COVERAGE",
  "userID": "<authorID>",
  "reedID": "<reedID>",
  "coveragePercent": 15
}
```

No `echoes` on this message.

### When to emit

| Trigger | Emit |
|---------|------|
| Successful `SUBSCRIBE_REED` | `REED_STATS` once to that connection |
| New echo indexed against this reed (echo publish) | `REED_ECHOES` to subscribers of the **echoed** reed |
| Echo row removed (echo reed removal / cleanup that changes count) | `REED_ECHOES` |
| Allocation inserted (count bumped) | `REED_COVERAGE` |
| Allocation deleted (count bumped) | `REED_COVERAGE` |
| `active_users` change | **v1:** no blast to all reed subs; `%` may stay slightly stale until next holder event or resubscribe |

### Server sketch

- In-memory map of reed → connections (reconnect → client resubscribes).
- On subscribe: validate tip; compute echoes + coveragePercent; send
  `REED_STATS`; record sub.
- After allocate/deallocate TX: read counters; send `REED_COVERAGE` to subs.
- After echo index insert/delete for target `T`: count echoes for `T`; send
  `REED_ECHOES` to subs of `T`.

### SPA

On reed detail for **published** reeds (`serverSignature` present):

1. `SUBSCRIBE_REED` with page author / reed id.
2. On `REED_STATS` for this reed → set both displayed values (and optional
   local echo-count cache).
3. On `REED_ECHOES` → update echoes only.
4. On `REED_COVERAGE` → update coverage `%` only.
5. Leave / destroy → `UNSUBSCRIBE_REED`.
6. Pending unsigned reed: no subscribe, no stats line.

Ignore events for other reeds. Load the stats line only from `REED_STATS` /
`REED_ECHOES` / `REED_COVERAGE`.

Display:

```
[megaphone-16] {echoes}   [cloud-line-chart-16] {coveragePercent}%
```

### Tests / checklist

- Subscribe → exactly one `REED_STATS` with both fields; no HTTP stats call
  required for the UI.
- New echo of subscribed reed → `REED_ECHOES` only (no coverage field).
- Allocate → `REED_COVERAGE` only (no echoes field).
- Unsubscribe → no further events.
- Leave page → unsubscribe.
- Idempotent allocate → no coverage inflation.
