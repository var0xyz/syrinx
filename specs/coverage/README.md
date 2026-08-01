# Reed network coverage + live reed stats

**Network coverage** for a reed: what fraction of active users on this
instance hold that reed’s content (per `reed_allocations`), shown with the
echo count on the reed detail “stats for nerds” line.

Both values are delivered **only over WebSocket**: subscribe to the reed,
receive an immediate snapshot ACK, then independent live updates for echoes
and for coverage.

**Blank slate — no migration, no backwards compatibility.** Recreate the DB
when schema changes.

| # | Title | Depends on |
|---|-------|------------|
| [00](00_design.md) | Design + UX + formula | — |
| [01](01_counts.md) | Denormalized counters (same-TX bumps) | 00 |
| [02](02_reed_subscription.md) | `SUBSCRIBE_REED` snapshot ACK + live updates + SPA | 01 |

Related: allocation semantics in
[`deletion/04_reed_fanout.md`](../deletion/04_reed_fanout.md);
echo index in [`conversations/02_index_and_api.md`](../conversations/02_index_and_api.md).

---

## Status

**Implemented** (00–02).

## Locked decisions

| Topic | Decision |
|-------|----------|
| Audience | Any **authenticated** viewer |
| Active users | Users with **no** `account_removals` row |
| Transport | **WS only** for reed-detail echoes + coverage (no HTTP snapshot for this UI) |
| Subscribe snapshot | Immediate ACK with **both** `echoes` and `coveragePercent` |
| Live echoes | Event with **only** `echoes` when the echo index for this reed changes |
| Live coverage | Event with **only** `coveragePercent` on allocate/deallocate |
| Holder count | Redundant counter, same TX as allocate/deallocate |
| Active-user count | Redundant network counter, same TX as signup / account removal |
| Display | Megaphone + cloud-line-chart icons; integer `%` |
| Spec home | This directory |

## Motivation

Operators and curious users can see how widely a reed has propagated without
listing holders. One subscription carries the initial stats and later
deltas—no parallel HTTP fetch for the same numbers.
