# Ripples (ephemeral, server-only replies)

This directory is the **ripples** feature proposal set — the concrete
implementation of [`docs/planned.md` § Ripples (ephemeral comments)](../../docs/planned.md).
Numbered files below are independently reviewable implementation steps. Land
them in order unless a step's "Depends on" says otherwise.

**Blank slate — no migration, no backwards compatibility.** Recreate the DB
when schema changes.

**Code organization:** ripple-specific server logic lives in a
`syrinx/ripples` Go package (schema, store, sweep, handlers). Main only wires
boot (start the sweep goroutine), route mounting, and realtime hooks. SPA
owns the ripple composer + list on the reed detail page.

| #                                 | Title                                              | Depends on |
|------------------------------------|----------------------------------------------------|------------|
| [00](00_design.md)                | Design + locked model                              | —          |
| [01](01_schema_and_expiry.md)     | `ripples` table, expiry sweep                      | 00         |
| [02](02_post_and_list_api.md)     | Post / list APIs + per-user rate limit             | 01         |
| [03](03_realtime_fanout.md)       | Live delivery to reed-detail viewers                | 02         |
| [04](04_spa_ripples_section.md)   | SPA Ripples section, gated on parent reed render    | 02, 03     |

## Status

**Proposed.** Nothing implemented yet.

## Motivation

Reeds are intentional, signed, p2p-propagated publications — the "honesty
about holders" story means every reed is a durable commitment. That's the
wrong shape for a quick reaction to someone else's post. Ripples give a reed
a **comment section** that behaves like conversation, not archive: short-
lived, server-only, gone a week after the last reply unless someone keeps it
warm.

From `docs/planned.md`:

> Publish a comment on a reed and it disappears after one week — unless
> someone replies. A reply keeps that thread alive for another week. Silence
> lets it go. […] The week-and-extend rule is the product statement:
> presence requires ongoing attention, not archival guilt.

## Locked decisions

| Decision | Choice |
|----------|--------|
| Trust model | **Unsigned.** No `BytesToSign`, no detached PGP signature, no server countersignature. A ripple is authenticated (signature-auth session identifies the author) but not attested the way a reed is. |
| Propagation | **Server-only.** Never relayed, never held by peers, never appears in `reed_allocations` / holder coverage. Federation (when it lands) does not carry ripples across instances either — out of scope, see Non-goals. |
| Storage | Plain Postgres row (content included) — the whole point is the server *is* the only copy, unlike reed bodies which the server explicitly does not retain. |
| Lifetime | 7 days from the thread's **last activity** (root post or any reply in that thread), not from thread creation. Every new ripple in a thread resets the whole thread's clock. |
| Deletion granularity | Whole thread at once. There is no partial expiry — either the thread is live or the entire thread (root + every reply) is gone. |
| Visibility gate | A reed's ripples are never shown until the **parent reed itself** is fully rendered (has a server signature — same `!isPending` gate the existing Replies/conversation section already uses). A ripple with no visible parent is meaningless out of context. |
| Depth | Flat. One thread per reed, ripples reply to the thread (or to a specific ripple for @-style addressing, display only) — no recursive sub-threads the way reed replies get their own conversation section. See [00](00_design.md) for the exact shape. |
| Rate limiting | New primitive — nothing in the codebase rate-limits any endpoint today. See [02](02_post_and_list_api.md). |
| Expiry mechanism | New primitive — nothing in the codebase runs a background sweep/cron today (the one existing ticker is the realtime WS ping, unrelated). See [01](01_schema_and_expiry.md). |

## Actors

- **Author** (of the parent reed) — the reed being ripple-commented on;
  no special ripple privileges beyond what any other viewer has, except
  optionally moderating (see [00](00_design.md) open question).
- **Ripple author** — any authenticated user who posts a ripple on a reed
  they can currently view.
- **Server** — stores ripples, enforces the 7-day rolling expiry, fans out
  new ripples live to currently-viewing clients, never propagates ripples
  to peers or federation.

## Non-goals

- p2p propagation / holder coverage for ripples.
- Federation delivery of ripples across instances.
- Signed ripple content or countersignatures.
- Recursive nested ripple threads (reed-style conversation-of-conversations).
- Edit history / ripple editing (delete-and-repost is the only revision path,
  if even that — see [00](00_design.md)).
- Notifications ("X replied to your ripple") — a social/per-event
  notification concept distinct from [`notifications/`](../notifications/README.md);
  not covered by any current proposal.
- Likes/reactions on ripples (separate [planned](../../docs/planned.md) item).
- Offline queueing/retry the way reed publish does (`unsignedReeds`) — a
  ripple post either succeeds against the live server or it didn't happen;
  no local-first durability story, consistent with "the server is the only
  copy."

## Cross-links

- `docs/planned.md` § Ripples (ephemeral comments) — product framing this
  spec implements.
- [`conversations/`](../conversations/README.md) — the signed, p2p reply
  model ripples are explicitly *not*; read 00's design for the contrast in
  trust model and propagation.
- [`deletion/`](../deletion/README.md) — reed/account removal certs; ripples
  reuse none of its signed-cert machinery (no signature to attest to) but
  the general "how does the SPA react to something disappearing" shape is
  worth comparing.
- Reed detail page mount point:
  [`spa/src/routes/reed/[userID]/[reedID]/+page.svelte`](../../spa/src/routes/reed/[userID]/[reedID]/+page.svelte)
  — `ConversationSection` is gated on `!isPending` (reed has a
  `serverSignature`); the Ripples section uses the same gate.

## Parallelism

- **00** can land alone (design lock only).
- **01 → 02 → 03** are sequential (schema before API before fanout).
- **04** needs 02 (list API) and 03 (live delivery) but not necessarily in
  that exact order relative to each other — SPA can build against 02's
  snapshot fetch before 03's live push exists, degrading gracefully to
  poll-on-mount only until 03 lands.
