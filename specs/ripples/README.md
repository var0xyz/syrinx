# Ripples (ephemeral, server-only replies)

This directory is the **ripples** feature proposal set — the concrete
implementation of [`docs/planned.md` § Ripples (ephemeral comments)](../../docs/planned.md).
Numbered files below are independently reviewable implementation steps. Land
them in order unless a step's "Depends on" says otherwise.

**Blank slate — no migration, no backwards compatibility.** Recreate the DB
when schema changes.

**Code organization:** ripple-specific server logic lives in the existing
`main` Go package, alongside every other entity's schema/store/handler
code (`db.go`, `services.go`, `handlers.go`) — **not** a separate
`syrinx/ripples` package. `main.go` wires boot (route mounting, realtime
hooks), same as for every other route. The expiry sweep is a separate,
standalone Go program at `jobs/ripples-cleanup/`, run by cron and
installed by the deploy scripts — it is not started from `main.go` and
is not part of the main `syrinx` binary (see
[01](01_schema_and_expiry.md)'s Expiry sweep section). SPA owns the
ripple composer + list on the reed detail page.

| #                               | Title                                            | Depends on |
|---------------------------------|--------------------------------------------------|------------|
| [00](00_design.md)              | Design + locked model                            | —          |
| [01](01_schema_and_expiry.md)   | `ripples`/`ripple_responses` tables (bookkeeping + content), expiry sweep | 00         |
| [02](02_post_and_list_api.md)   | Post / list APIs                                 | 01         |
| [03](03_realtime_fanout.md)     | Live delivery to reed-detail viewers             | 02         |
| [04](04_spa_ripples_section.md) | SPA Ripples section, gated on parent reed render | 02, 03     |

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

| Decision             | Choice |
|----------------------|--------|
| Trust model          | **Signed**, same as a reed. User-signed (`BytesToSign` + detached PGP signature) and server-countersigned, following the identical two-payload envelope `identity.go` already defines for reeds/profiles. A ripple's id is the hash of its signed server payload — content-addressed, not randomly minted. See [00](00_design.md) for the full model and rationale. |
| Propagation          | **Server-only.** Never relayed, never held by peers, never appears in `reed_allocations` / holder coverage. Federation (when it lands) does not carry ripples across instances either — out of scope, see Non-goals. |
| Storage              | Plain Postgres row (content included) — the whole point is the server *is* the only copy, unlike reed bodies which the server explicitly does not retain. |
| Lifetime             | 7 days from the **reed's** last activity (any response, on any thread on that reed), not per-thread. Every new response anywhere on a reed's ripples resets the whole reed's shared clock. |
| Deletion granularity | Two independent axes. **Expiry** is whole-reed, not whole-thread: a reed's `expires_at` is shared across all of its threads, and when it passes, everything under that reed (every thread, every response) is removed via cascade. **Self-delete** is per-response and soft: it flips a `deleted` boolean and replaces `content` with the literal string `"[DELETED]"`; it does not remove the row or affect expiry timing. See [00](00_design.md) and [01](01_schema_and_expiry.md). |
| Visibility gate      | A reed's ripples are never shown until the **parent reed itself** is fully rendered (has a server signature — same `!isPending` gate the existing Replies/conversation section already uses). A ripple with no visible parent is meaningless out of context. |
| Depth                | Flat rendering, but a reed may have **multiple independent threads** — each top-level response mints a new thread; a reply inherits its parent's thread instead of starting a new one. No recursive sub-threads either way, consistent with reed replies getting their own separate conversation-thread model instead. See [00](00_design.md) for the exact shape. |
| Expiry mechanism     | An **external cron job** running a standalone Go binary (`jobs/ripples-cleanup/` + `jobs/ripples-cleanup.cron`), installed/refreshed by `deploy/scripts/syrinx/setup.sh`/`update.sh` — not a goroutine inside the main server process, since a `time.Ticker`'s schedule would reset on every process restart. Runs every minute, deletes from `ripples` only, relying on `ON DELETE CASCADE` for the content rows. See [01](01_schema_and_expiry.md). |
| Account removal      | Two rules, not one. A removed user's past responses on *other* reeds persist unchanged until that reed's normal 7-day sweep — account removal is cert-only and never cascades to a user's reeds or ripple rows. The ripples section on a removed user's *own* reeds 404s/410s immediately, via the same parent-reed lookup that already treats an individually-removed reed the same way. See [00](00_design.md). |
| Identity / id scheme | A response's id is `hash`, the hex-SHA256 digest of its signed server payload — content-addressed, computed once at creation, **never recomputed** (a soft-delete does not change it). `threadID` is a client-minted UUID, signed as part of what the author attests to, and validated server-side against the parent response's stored value on a reply. Both are new primitives for this codebase — see [00](00_design.md). |

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
- [`conversations/`](../conversations/README.md) — reed replies' signed,
  p2p model. Ripples share the *trust* model (both signed and
  countersigned) but not the *propagation* model (ripples are
  server-only, never p2p, never counted in coverage) — read 00's design
  for the precise contrast.
- [`deletion/`](../deletion/README.md) — reed/account removal certs.
  Ripples do **not** reuse this package's signed-*removal-cert* machinery
  (a self-delete is a soft, unsigned-at-delete-time flip, not a new
  signed certificate — see [00](00_design.md)'s Moderation section) even
  though ripples themselves are signed at creation; the general "how does
  the SPA react to something disappearing" shape is still worth
  comparing.
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
