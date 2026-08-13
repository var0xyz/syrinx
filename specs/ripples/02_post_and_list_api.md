# Ripples 02 — Post / list APIs

## Status

Proposed.

## Depends on

[01](01_schema_and_expiry.md)

## Context

With schema and store methods in place, this step exposes HTTP endpoints to
post a signed response, list a reed's ripples, and soft-delete one's own
response.

`threadID` and `id` are client-signed/server-computed inputs (`threadID`
is client-minted; `id` is server-computed as a hash, not client-supplied)
— see [00](00_design.md)'s Signing and Thread shape sections and
[01](01_schema_and_expiry.md)'s Insert transaction for the full mechanics
this endpoint wraps.

## Scope

- `POST /reeds/{userID}/{reedID}/ripples` — post a signed response to
  that reed (starting a new thread, or replying within an existing one).
- `GET /reeds/{userID}/{reedID}/ripples` — list a reed's ripples
  (paginated, thread-grouped).
- `DELETE /ripples/{rippleID}` — self soft-delete (session-authenticated,
  not re-signed).

## Non-goals

- Federation of ripples across instances.
- Any endpoint for a reed author to delete someone else's response (see
  [00](00_design.md) Moderation lock).
- Editing.
- Client-side signature verification logic (that's SPA-side, see 04) —
  this step only covers what the server validates on write and what it
  returns on read; the server never verifies the *user* signature on the
  read path (list), only on write (post), same as reeds.

## Design

### `POST /reeds/{userID}/{reedID}/ripples`

Auth: standard signature-auth session (same middleware as every other
authenticated route — identifies the caller for the ownership check
below; this is **separate** from the ripple's own `userSignature`, same
as how a reed POST is both session-authenticated *and* carries its own
detached content signature).

Body:

```json
{
  "content": "...",
  "threadID": "3fa85f64-5717-4562-b3fc-2c963f66afa6",
  "replyingTo": null,
  "fingerprint": "...",
  "userSignature": "<base64 armored PGP>"
}
```

- `content`: the ripple text, exactly as signed (see Validation below on
  trim parity).
- `threadID`: client-minted UUID (new thread) or the parent response's
  `threadID` (reply) — see [00](00_design.md)'s Thread shape. Always
  present, always signed.
- `replyingTo`: the response `id`/`hash` being replied to, or omitted/null
  for a top-level post.
- `fingerprint`: the signing key's fingerprint (self-describing, same
  convention as every other signed submission in this codebase).
- `userSignature`: base64(armored PGP detached signature) over the user
  payload built from the fields above — see
  [00](00_design.md)'s Signing section for the exact byte layout.

Validation, in order:

1. Parent reed (`{userID}/{reedID}`) must exist and not be removed → 404 if
   missing, 410 if removed. **The removed case also covers a reed whose
   author's account has been removed** — see [00](00_design.md)'s Account
   removal section: the same parent-reed lookup this validation reuses
   already treats a removed-account author's reed as removed, so this rule
   needs no separate check here.
2. `content`: non-empty after trim, ≤ 140 chars (`MAX_REED_VISIBLE_CHARS`,
   see [00](00_design.md)), validated with a plain Unicode code-point count
   — not this codebase's markdown-aware character counter, which is the
   wrong tool for content that's never parsed as markdown. Plain text only
   — no markdown parsing, no mention extraction, no hashtag extraction
   (00's lock). **The server does not further trim or otherwise mutate
   `content` before it's used to rebuild the signed payload** — see 00's
   Content constraints on client/server byte parity; the client is
   responsible for having already signed the exact bytes it submits.
3. `threadID` must be a syntactically valid UUID → 400 otherwise.
4. `replyingTo`, if present, must reference a response in the **same**
   reed → 400 otherwise. Replying to an already-soft-deleted response is
   **allowed** (tombstones are valid reply targets, per 00's Moderation
   section). Replying to a response whose own author's account has since
   been removed is also allowed — that commenter's past responses persist
   normally per 00's Account removal section. **The submitted `threadID`
   must equal the referenced response's stored `thread_id`** → 400
   (`"Reply must use the same thread as the comment it replies to."`) if
   it doesn't — see [00](00_design.md)'s Thread shape and
   [01](01_schema_and_expiry.md)'s Insert transaction step 0 for why this
   check exists (the signature alone only proves author intent, not
   consistency with the actual parent).
5. Resolve the caller's active public key for `fingerprint` (same
   resolution this codebase already uses for every other signed-write
   endpoint) → 400 if the key is unknown or not the caller's active key.
6. Rebuild the user payload from fields 1–5 above and verify
   `userSignature` against the resolved key → 400
   (`"Invalid signature."`) on failure. This is the actual authentication
   of *content*, distinct from the session-auth check that identified
   the caller in the first place.

On success: build the server payload (adding `serverID` and a
server-side `now()` timestamp), countersign it via the existing
`h.countersign(...)` primitive, compute `id` = hex-SHA256 of the server
payload, persist (see [01](01_schema_and_expiry.md)'s Insert
transaction), respond `201`:

```json
{
  "hash": "3a7bd3e2360a3d..." ,
  "threadID": "3fa85f64-5717-4562-b3fc-2c963f66afa6",
  "userID": "posterId",
  "content": "...",
  "replyingTo": null,
  "deleted": false,
  "postedAt": "2026-08-07T12:00:00Z",
  "userSignature": { "fingerprint": "...", "armor": "<base64>" },
  "serverSignature": { "serverID": "...", "fingerprint": "...", "armor": "<base64>", "timestamp": "2026-08-07T12:00:00Z" }
}
```

The response object's id *is* the hash (see [00](00_design.md)'s Signing
section), so the wire field is named `hash`, not a generic `id` that
happens to be implemented as one. `threadID` is an **echo of what the
client submitted**, not a server-assigned value — included in the
response purely for shape-consistency with the GET/realtime payloads, not
because the server is telling the client something new. `deleted` is
always `false` on creation; included for shape consistency with the same
object as it appears in the GET response and the realtime payload (see
[03](03_realtime_fanout.md)). `userSignature`/`serverSignature` are
nested wire objects mirroring the shape every other signed entity in this
codebase already returns (see e.g. a reed's response shape) — a flat
client re-verifying this response needs both, plus `fingerprint`, to
redo the checks in [00](00_design.md)'s Client-side verification section.

### `GET /reeds/{userID}/{reedID}/ripples`

Auth required (same as POST — no anonymous read path; matches this
instance's closed-community posture, consistent with reeds themselves
requiring auth to read). The server does **not** re-verify signatures on
this path — it returns exactly what's stored, and verification is a
client-side responsibility on receipt (see [00](00_design.md)'s
Client-side verification section) — same division of labor as every
other signed-entity list endpoint in this codebase (e.g. reed replies).

Query params: `limit` (default 50, max 100), `before` — an **opaque
cursor string**, not a bare ISO8601 timestamp. A single timestamp can't
disambiguate thread groups (see Pagination cursor below), which is why
this deliberately differs from the plain-RFC3339 `before` cursor
[`GET /reeds/{userID}/{reedID}/replies`](../conversations/02_index_and_api.md)
uses.

Response `200` — a **flat array**, not a nested per-thread structure:

```json
{
  "responses": [
    {
      "hash": "3a7bd3e2360a3d...",
      "threadID": "3fa85f64-5717-4562-b3fc-2c963f66afa6",
      "userID": "posterId",
      "content": "...",
      "replyingTo": null,
      "deleted": false,
      "postedAt": "2026-08-07T12:00:00Z",
      "userSignature": { "fingerprint": "...", "armor": "<base64>" },
      "serverSignature": { "serverID": "...", "fingerprint": "...", "armor": "<base64>", "timestamp": "2026-08-07T12:00:00Z" }
    }
  ],
  "hasMore": false,
  "nextCursor": "eyJ0aHJlYWRDcmVhdGVkQXQiOi4uLn0=",
  "expiresInSeconds": 604800
}
```

Every item carries its full `userSignature`/`serverSignature` — even a
soft-deleted (tombstoned) one, whose signatures now describe content
different from what's currently in `content` (see
[00](00_design.md)'s Client-side verification section on why this is
expected, not an error, and how a client should handle it).

- **Flat array, not `{threads: [{threadID, responses: []}]}`.** A nested
  shape was considered and rejected: a thread's responses can span a
  pagination page boundary, and merging partial thread groups across pages
  client-side adds real complexity for no rendering benefit — the SPA
  renders one flat list regardless (see [00](00_design.md)'s Thread shape
  and [04](04_spa_ripples_section.md)). Each item still carries `threadID`
  so the client can detect thread boundaries if it wants a subtle visual
  divider, but the server does the actual ordering work.
- **Ordering**: threads ordered by thread-creation time (the earliest
  `postedAt` among that thread's responses), oldest thread first;
  responses within a thread ordered `postedAt ASC`, `hash ASC` tie-break.
- **`expiresInSeconds`** is included at the top level because it's shared
  across every thread on the reed (see [00](00_design.md)) and is not
  reliably derivable client-side from a possibly-partial page of
  responses — a client that only fetched page 1 of a long list has no way
  to know the true most-recent-activity timestamp otherwise. It's a
  **relative** duration (seconds remaining, computed server-side at
  request time from the stored `expires_at`), not an absolute timestamp —
  the client starts its own local countdown from this value instead of
  comparing an absolute deadline against its own (possibly skewed) system
  clock. Clamped to `0` rather than going negative if the sweep hasn't
  run yet; omitted entirely if the reed has no ripples at all.
- No responses yet for this reed → `200` with `responses: []`, not 404 —
  absence of comments isn't an error state.
- Parent reed removed, or its author's account removed → `410`, same as
  the POST validation above and for the same reason (see
  [00](00_design.md) Account removal).

#### Pagination cursor

Because responses are ordered by thread-creation-time first and post-time
second, a single RFC3339 timestamp cursor cannot correctly resume a page
mid-way through the ordering. `before`/`nextCursor` instead encode an
opaque cursor over the four-part tuple
`(threadCreatedAt, threadID, postedAt, hash)` of the last item on the
previous page — recommended encoding is a base64-encoded JSON object,
e.g. `{"threadCreatedAt":"...","threadID":"...","postedAt":"...","hash":"..."}`.
This differs from the plain-timestamp cursor the replies endpoint uses,
driven by the multi-thread requirement in [00](00_design.md).

### `DELETE /ripples/{rippleID}`

`{rippleID}` is the response's `hash` (see [00](00_design.md)'s Signing
section).

Auth required — **session authentication only, no re-signing**. Deleting
is not itself a signed operation (see [00](00_design.md)'s Moderation
section: "Deleting requires only the author's authenticated session — it
is not re-signed"). `404` if the response doesn't exist at all (already
swept away by expiry, or never existed) — same "already gone" semantics
used elsewhere in this codebase rather than a distinct "expired" status,
since from the caller's perspective there's no difference. `403` if the
response belongs to a different user (`"You can only delete your own
comments."` — see 00, authors get no special power over others'
responses). Idempotent: deleting an already-deleted response succeeds
again as a no-op — there is no distinct "already deleted" error state.

**`204 No Content`** on success — the delete is a **soft** delete
server-side (`deleted` flips, `content` becomes `"[DELETED]"`; `id`/
`user_signature`/`server_signature` are untouched, per
[01](01_schema_and_expiry.md)'s Soft delete section), but the HTTP
response itself stays a plain `204`, matching this codebase's existing
DELETE convention elsewhere. The caller sees the tombstoned state via the
realtime `RIPPLE_UPDATED` event (see [03](03_realtime_fanout.md)) or
their next list fetch — not from the DELETE response body itself.

Does **not** bump the reed's shared `expires_at` (00's lock — deleting
isn't posting).

## Work items

1. `handlers.go` — the three handlers above, including sign-verification
   and threadID-consistency validation.
2. `main.go` — route registration alongside the existing `/reeds/...`
   routes.
3. Tests: post success (valid signature), post-invalid-signature (400),
   post-too-long, post-on-removed-parent, post-on-missing-parent,
   post-on-a-reed-whose-author-account-is-removed (410),
   post-with-replyingTo-in-a-different-reed (400),
   post-with-mismatched-threadID-for-a-reply (400),
   post-with-replyingTo-targeting-an-already-soft-deleted-response
   (201 success), post-with-replyingTo-targeting-a-removed-account's-
   response (201 success), list-empty, list-pagination (including a page
   boundary that falls inside a thread's response run), list-includes-
   soft-deleted-rows-as-tombstones-with-original-signatures-intact,
   list-includes-a-removed-account's-response-unfiltered,
   list-on-a-reed-whose-author-account-is-removed (410), delete-own (204,
   and a subsequent list fetch shows `deleted:true, content:"[DELETED]"`
   with the original `hash`/signatures unchanged rather than the row
   being gone or re-hashed), delete-others-forbidden, delete-missing,
   delete-already-deleted (204, idempotent).

## Risks

- **140-char cap may feel small for "comment" framing vs. "reed" framing**
  — deliberately reusing the existing constant rather than inventing a
  second content-length policy; revisit only if product feedback says so.
- **Signature verification failure UX** — a client/server byte-parity bug
  (e.g. a trimming mismatch, see [00](00_design.md)'s Content
  constraints) would make *every* post fail with a generic "Invalid
  signature" 400, which is hard for a user to self-diagnose. Worth a
  clear, specific error message and — during initial rollout — server-
  side logging of which byte offset/field first diverges, to make this
  class of bug fast to catch rather than silently failing every post.

## Dependencies

[01](01_schema_and_expiry.md) store methods.

## Parallelism

[03](03_realtime_fanout.md) can be scaffolded against this step's response
shapes before both are fully done; the POST handler is the natural place
to trigger 03's broadcast, so final wiring waits on 03 landing.
