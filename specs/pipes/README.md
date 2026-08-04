# Pipes — live hashtag listening

A **pipe** is a live subscription to reeds that carry a given hashtag.
The server does not keep a hashtag timeline; opening a pipe lists only
reeds **already on the device** that match the tag, and while the pipe is
open the client receives **new** matching reeds as they publish.

SPA markdown turns `#tag` into `web+syrinx://pipe/…`.

| # | Title | Depends on | Status |
|---|-------|------------|--------|
| [00](00_design.md) | Design + naming + locked model | — | Implemented |
| [01](01_extract_stash.md) | Extract tags at SignReed; stash on `pending_fanout` until READY | 00 | Implemented |
| [02](02_subscribe_fanout.md) | WS subscribe + READY fanout to listeners | 01 | Implemented |
| [03](03_spa.md) | Links, `/pipe/[tag]` page, local list + live | 02 | Implemented |

## Locked decisions

| Topic | Decision |
|-------|----------|
| Product name | **Pipe** (see [00](00_design.md) naming) |
| URI | `web+syrinx://pipe/<tag>` → `/pipe/<tag>` |
| Server history | None — no catch-up of past tagged reeds |
| Tag persistence | Unlogged `pending_fanout.tags` only until READY claim; no durable tip index |
| Local list | IndexedDB reeds whose content tags include this tag |
| Live consent | Opening / subscribing to a pipe is agreement to **keep** delivered matching reeds (verify → IndexedDB), unlike broadcast session-only |
| Tag normalize | Lowercase; strip `#`; unique per reed (same as SPA `extractTags`) |
| Stash filter | Only tags with ≥1 pipe subscriber **at SignReed** are stored |

## Status

**Implemented**.
