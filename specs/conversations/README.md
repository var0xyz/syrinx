# Echoes and replies (conversations)

This directory is the **echo count + threaded reply UI** feature proposal
set. Numbered files below are independently reviewable implementation steps.
Land them in order unless a step's "Depends on" says otherwise.

**Blank slate — no migration, no backwards compatibility.** Recreate the DB
when schema changes; normalize the `replying` header at the same time (see
[01](01_publish_and_refs.md)).

**Not ephemeral comments.** [Planned ephemeral comments](../../docs/planned.md)
are a separate, future product. Replies here are **full signed reeds** with a
`replying` header — same permanence and deletion story as any other reed.

**Code organization (suggested):** conversation index helpers in a
`syrinx/conversations` package (or colocated with reed handlers if thin).
Main wires DDL, route mounting, and `SignReed` hooks. SPA owns the reed-detail
conversation section and local reply cache.

| #                                    | Title                                              | Depends on |
|--------------------------------------|----------------------------------------------------|------------|
| [00](00_design.md)                   | Design + UX model                                  | —          |
| [01](01_publish_and_refs.md)         | Verify publish payload; normalize `replying` ref   | 00         |
| [02](02_index_and_api.md)            | Echo/reply index tables + list/count APIs          | 01         |
| [03](03_spa_reed_detail.md)          | Echo count + conversation section on reed detail   | 02         |
| [04](04_mentions.md)                 | `@` mentions → `web+syrinx` links + `reed_mentions` | 01         |
| [05](05_thread_reply_counts.md)      | Recursive reply counts: thread total (`threadId`, live WS stat) + per-reed subtree count | 02 |

After 00, [01](01_publish_and_refs.md) can land alone (security hardening).
[02](02_index_and_api.md) needs the publish hook from 01. SPA ([03](03_spa_reed_detail.md))
needs the APIs from 02. Mentions ([04](04_mentions.md)) only need 01 (content on
publish); notification/read delivery stays in [`notifications/`](../notifications/README.md).
Thread reply counts ([05](05_thread_reply_counts.md)) only need the
`reed_replies` schema from 02; independent of 03/04.

---

## Status

| #  | Title                                              | Status        |
|----|----------------------------------------------------|---------------|
| 00 | Design + UX model                                  | Implemented   |
| 01 | Verify publish payload; normalize `replying` ref   | Implemented   |
| 02 | Echo/reply index tables + list/count APIs          | Implemented   |
| 03 | Echo count + conversation section on reed detail   | Implemented   |
| 04 | `@` mentions → links + `reed_mentions`             | Implemented   |
| 05 | Thread reply counts (thread total + subtree)       | Implemented   |

**Track status: Implemented.** All six steps have landed.

## Motivation

Echo and reply headers already exist on signed reeds, but the product does
not surface them:

- Authors cannot see how often their reed was echoed.
- Readers cannot browse replies without already holding every reply reed
  locally.
- The `replying` header historically stored only a reed id while `echoing` used
  `authorId!reedId` — inconsistent and federation-hostile. Both now use
  `userID@serverID/reedID`.

This feature adds **server-side social indexes** (metadata only — bodies stay
on peers) and a **conversation section** on the reed detail page: direct
replies first; drill into a reply to see *its* direct replies.

## Locked decisions

| Decision | Choice |
|----------|--------|
| Reply / echo reference wire format | `userID@serverID/reedID` (same for both), reused for `threadId` |
| Index scope | Instance-local; built at countersign time |
| Index payload | `(parent, reply ids, timestamp)` — timestamp for list order only; no markdown on server |
| Publish body | Client sends form fields (`content`, optional `echoing`/`replying`); server rebuilds canonical markdown and verifies detached user sig before countersign |
| Echo count surface | `GET /reeds/{userID}/{reedID}/echoes` + stats line on reed detail (not on Echo button, not on `GET /reeds`) |
| Reply list surface | `GET /reeds/{userID}/{reedID}/replies` — metadata rows, oldest-first |
| Conversation depth | **One level at a time** — list direct children only; click a reply to navigate to that reed's page |
| Removed reeds | Excluded from counts and reply lists; parent quote already shows "unavailable" via deletion certs. Navigating into a removed reed's own permalink is an open question — see [05](05_thread_reply_counts.md) |
| Realtime v1 | Direct reply list reuses `FOLLOW_REED` fanout (no new WS type); the thread total gets a dedicated `REED_STATS`/`REED_REPLIES` live path — see [05](05_thread_reply_counts.md) |
| Mention href | `web+syrinx://users/<serverID>/<userID>` inside markdown `[Name](…)` — domain-free; see [04](04_mentions.md) |
| Mention index | `reed_mentions` at countersign; cleared on reed/account removal; no notification delivery in conversations |
| Reply counts | One number per viewed reed: **subtree count** (descendants via `reed_replies` graph), delivered as `replies` on `REED_STATS`/`REED_REPLIES`; shown in stats line and `Replies (N)`. Thread-wide total only when viewing the root (same query — no separate field). |

## Actors

- **Author** — publishes a reed; may echo or reply to others' reeds.
- **Reader** — opens a reed detail page; sees echo count and direct replies;
  navigates into reply threads by opening reply reeds.
- **Server** — verifies user signatures on publish, maintains echo/reply
  indexes, serves counts and reply metadata. Does **not** store reed bodies.

## Cross-links

- Reed social headers: [`services.go` `ExtractReedHeader`](../../services.go),
  SPA [`ReedType`](../../spa/src/lib/types/reed.ts).
- Reed detail UI: [`spa/src/routes/reed/[userID]/[reedID]/+page.svelte`](../../spa/src/routes/reed/[userID]/[reedID]/+page.svelte).
- Deletion: removed reeds must drop out of indexes
  ([`deletion/`](../deletion/README.md)).
- Reed refs: `userID@serverID/reedID` via `ParseReedRef` / SPA `parseReedRef`
  ([`+layout.svelte`](../../spa/src/routes/+layout.svelte) prefetch).
