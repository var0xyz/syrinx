# Ripples 02 — Post / list APIs + rate limiting

## Status

Proposed.

## Depends on

[01](01_schema_and_expiry.md)

## Context

With schema and store methods in place, this step exposes HTTP endpoints to
post a ripple, list a thread, and delete one's own ripple — plus the
per-user rate limit needed because, unlike a reed (slow, deliberate,
signed), a ripple is a one-field POST with no cryptographic cost, meaning
nothing today stops a script from posting hundreds per second. Confirmed
via code audit: **no endpoint in this codebase rate-limits today** — this
is genuinely new ground.

## Scope

- `POST /reeds/{userID}/{reedID}/ripples` — post a ripple to that reed's
  thread.
- `GET /reeds/{userID}/{reedID}/ripples` — list a thread (paginated).
- `DELETE /ripples/{rippleID}` — self-delete.
- Per-user rate limit on the POST route.

## Non-goals

- Federation of ripples across instances.
- Any endpoint for a reed author to delete someone else's ripple (see
  [00](00_design.md) Moderation lock).
- Editing.

## Design

### `POST /reeds/{userID}/{reedID}/ripples`

Auth: standard signature-auth session (same middleware as every other
authenticated route — no special-casing).

Body: `{ "content": string, "inReplyToRippleID"?: string }`.

Validation:

- `content`: non-empty after trim, ≤ 140 chars (`MAX_REED_VISIBLE_CHARS`,
  see [00](00_design.md)). Plain text only — no markdown parsing, no
  mention extraction, no hashtag extraction (00's lock).
- Parent reed (`{userID}/{reedID}`) must exist and not be removed → 404 if
  missing, 410 if removed (mirrors `GetReed`'s existing convention from
  [conversations/02](../conversations/02_index_and_api.md)).
- `inReplyToRippleID`, if present, must reference a ripple in the **same**
  thread → 400 otherwise. Not required to still exist as a live row by the
  time this validates (a race where it's deleted between check and insert
  is harmless — FK is `ON DELETE SET NULL`, see 01).

Response `201`:

```json
{
  "id": "rippleId",
  "userID": "posterId",
  "content": "...",
  "inReplyToRippleID": null,
  "postedAt": "2026-08-07T12:00:00Z"
}
```

Rate limit response: `429` with a human-readable body (this codebase's
established convention per the error-message rewording done in the
[recovery reed-tip-check](../recovery/16_reed_tip_check.md) work — no
machine-code strings), e.g. `"You're posting comments too quickly. Wait a
moment and try again."`

### `GET /reeds/{userID}/{reedID}/ripples`

Auth required (same as POST — no anonymous read path; matches this
instance's closed-community posture, consistent with reeds themselves
requiring auth to read).

Query params: `limit` (default 50, max 100), `before` (cursor: ISO8601
`postedAt` of the oldest item already shown, exclusive) — same pagination
shape as
[`GET /reeds/{userID}/{reedID}/replies`](../conversations/02_index_and_api.md).

Response `200`:

```json
{
  "ripples": [
    {
      "id": "rippleId",
      "userID": "posterId",
      "content": "...",
      "inReplyToRippleID": null,
      "postedAt": "2026-08-07T12:00:00Z"
    }
  ],
  "hasMore": false
}
```

- No thread for this reed yet (nobody has posted) → `200` with empty
  `ripples: []`, not 404 — absence of comments isn't an error state.
- Parent reed removed → `410`, same as the replies endpoint, since a
  removed reed's ripples are meaningless without their context per 00's
  visibility gate.
- Order: `postedAt ASC`, `id ASC` tie-break.

### `DELETE /ripples/{rippleID}`

Auth required. `404` if the ripple doesn't exist (already expired/swept,
or never existed) — same "already gone" semantics used elsewhere in this
codebase rather than a distinct "expired" status, since from the caller's
perspective there's no difference. `403` if the ripple belongs to a
different user (`"You can only delete your own comments."` — see 00,
authors get no special power over others' ripples). `204` on success. Does
**not** bump thread activity or touch `expires_at` (00's lock — deleting
isn't posting).

### Rate limiting

New small package, e.g. `ratelimit/` — an in-process, per-process-memory
token bucket keyed by `userID`, since this app runs as a single server
process today (no multi-instance/shared-state deployment exists in this
codebase — confirmed no Redis or equivalent shared cache anywhere). If
multi-instance deployment is ever added, this needs to move to a shared
store; out of scope here, flagged for whoever builds that.

Limit: **1 ripple per 5 seconds per user, burst of 3** — generous enough
for a real back-and-forth conversation, restrictive enough to stop a
scripted flood. Applied only to the POST route; GET/DELETE are unlimited
(reads and self-deletes aren't the abuse vector this defends against).

Implementation: simple mutex-guarded map of `userID → tokenBucketState`,
lazily evicted (an entry with a full bucket and no recent activity can be
dropped opportunistically on a periodic sweep, reusing the same interval
concept as 01's expiry sweep, or simply left — memory cost per user is a
few bytes and this instance's user count is small-community scale by
design, per the product's own stated constraints in `docs/planned.md` §
Federation: "Syrinx instances are meant to stay small and independent").
Given that framing, start with **no eviction at all** and revisit only if
memory becomes a measured problem.

## Work items

1. `ratelimit/limiter.go` — token bucket, `Allow(userID string) bool`.
2. `ripples/handlers.go` — the three handlers above.
3. `main.go` — route registration alongside the existing `/reeds/...`
   routes.
4. Tests: post success, post-too-long, post-on-removed-parent,
   post-on-missing-parent, list-empty-thread, list-pagination, delete-own,
   delete-others-forbidden, delete-missing, rate-limit-triggers-429,
   rate-limit-resets-after-window.

## Risks

- **In-memory rate limiter resets on restart** — acceptable; a restart is
  already a trust boundary reset for lots of in-process state in this
  codebase (e.g. the realtime connection manager itself).
- **140-char cap may feel small for "comment" framing vs. "reed" framing**
  — deliberately reusing the existing constant rather than inventing a
  second content-length policy; revisit only if product feedback says so.

## Dependencies

[01](01_schema_and_expiry.md) store methods.

## Parallelism

[03](03_realtime_fanout.md) can be scaffolded against this step's response
shapes before both are fully done; the POST handler is the natural place
to trigger 03's broadcast, so final wiring waits on 03 landing.
