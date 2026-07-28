# Reed network coverage

This directory specifies **network coverage** for a reed: what fraction of
active users on this instance hold that reed’s content (per
`reed_allocations`), shown as a tiny “stats for nerds” line on the reed
detail page (echoes megaphone + coverage cloud-line-chart icons), with a
REST snapshot and live WS updates.

**Blank slate — no migration, no backwards compatibility.** Recreate the DB
when schema changes.

| # | Title | Depends on |
|---|-------|------------|
| [00](00_design.md) | Design + UX + formula | — |
| [01](01_counts_and_api.md) | Counters + `GET …/stats` | 00 |
| [02](02_reed_subscription.md) | Per-reed WS subscription + SPA | 01 |

Related: allocation semantics and catch-up in
[`deletion/04_reed_fanout.md`](../deletion/04_reed_fanout.md);
echo count index in [`conversations/02_index_and_api.md`](../conversations/02_index_and_api.md).

---

## Status

**Proposed** (00–02).

## Locked decisions

| Topic | Decision |
|-------|----------|
| Audience | Any **authenticated** viewer |
| Active users | Users with **no** `account_removals` row |
| Live updates | Push on **allocate and deallocate** |
| Snapshot | `GET /reeds/{userID}/{reedID}/stats` (echoes + coverage) |
| Holder count | Redundant counter, same TX as allocate/deallocate |
| Active-user count | Redundant network counter, same TX as signup / account removal |
| Display | Next to echoes; megaphone + cloud-line-chart icons; integer `%` |
| Spec home | This directory |

## Motivation

Operators and curious users can see how widely a reed has propagated on the
instance without listing every holder. The server already tracks holders in
`reed_allocations` for relay and catch-up; coverage is a product surface over
that operational fact — not a new trust claim.
