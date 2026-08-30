# Pagination (system-wide audit + convention)

This directory tracks pagination support across syrinx: an audit of which
endpoints and list views paginate today, the convention new work should
follow, and the still-open gaps.

| #                                        | Title                                       | Depends on |
|-------------------------------------------|----------------------------------------------|------------|
| [00](00_audit.md)                          | Audit + locked convention                    | —          |
| [01](01_deduplication.md)                  | Unify duplicated pagination logic            | 00         |
| [02](02_user_search_cursor.md)             | User search cursor pagination                | 00, 01     |
| [03](03_invites_list.md)                   | Invites list endpoint                        | 00         |
| [04](04_federation_admin_pagination.md)    | Federation admin list/log pagination         | 00, 01     |
| [05](05_reeds_list_client_cursor.md)       | `ReedsList` (profile feed) client-side cursor | 00         |

## Status

**In progress.** 00 and 01 are Implemented. 02–05 are Proposed — each is
independent of the others and can land in any order.

## Motivation

Syrinx already had a working keyset-cursor pagination convention on four
endpoints (chorus, replies, following, followers), but it was implemented
by copy-paste, and several list-returning endpoints and frontend list
views had no pagination at all — some capped by an ad-hoc client-side
limit, one (the author's own reed list on their profile) with no cap of
any kind. Before extending pagination further, this track audits the
whole system, locks the convention, removes the existing duplication, and
tracks the remaining gaps as independent steps.

## Locked decisions

| Decision | Choice |
|---|---|
| Backend cursor shape | `limit` (default 50, max 100) + `before`, an opaque cursor — usually the RFC3339 timestamp of the oldest item the client has already seen. Fetch `limit+1` rows ordered by a stable composite key, `hasMore := len(rows) > limit`, trim to `limit`. Response is `{ <items>: [...], hasMore: boolean }`; the client derives the next `before` from the last item's own timestamp field unless ordering can't be represented by a single field (see Ripples exception below). |
| Ripples exception | Ripples order by a 4-column composite (`thread_created_at, thread_id, posted_at, id`), so the server mints an opaque base64-JSON cursor and returns it as `nextCursor` instead of expecting the client to reconstruct it. Use this variant only when a single field genuinely can't represent "where I left off." |
| Frontend state shape | Local component state: `rows`, `hasMore`, `cursor`. A page fetch appends (or, where needed, prepends) into `rows`. No global store required — see [01](01_deduplication.md) for the one shared piece (`createPaginationStore`) that *is* worth factoring out. |
| Live (websocket) updates | New items arriving live splice into `rows` in place; they never trigger a full pagination reset unless the payload lacks per-item identity (e.g. chorus's `ReedEchoes` event, which only carries a new total). |
| Scroll behavior | **Content-bearing lists** (reeds, replies, ripples — anywhere the point is reading, not scanning) use a manual **"Load more" button** — matches `docs/index.md`'s stated value ("no infinite scroll, no engagement optimization, no dark patterns"). **Informational/utility lists** (followers, following, invites, mesh admin lists) may use infinite scroll (auto-fetch on reaching the bottom) since these are lookups, not a feed. |

See [00](00_audit.md) for the full per-endpoint/per-component audit table
this convention was derived from.

## Cross-links

- `docs/index.md` — "no infinite scroll" value that shapes the scroll-behavior decision above.
- `docs/planned.md` — product-level feature roadmap; not itself pagination-specific.

## Parallelism

- **00** (audit) is a prerequisite for everything else — it's where the
  convention was derived and locked.
- **01** (deduplication) should land before 02–05 so new pagination call
  sites use the shared helpers instead of adding a sixth copy.
- **02, 03, 04, 05** are independent of each other and may land in any
  order once 01 is in.
