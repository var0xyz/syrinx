# Ripples 00 — Design + locked model

## Status

Proposed.

## Depends on

—

## Context

`docs/planned.md` § Ripples (ephemeral comments) describes a comment layer on reeds
that behaves opposite to a reed: high-churn, low-permanence, gone after a
week of silence. Reeds are signed, p2p-propagated, and (per
[`conversations/00_design.md`](../conversations/00_design.md)) the server
never retains reed bodies — durability lives entirely in the p2p mesh.
Ripples share the *lifetime* contrast (short-lived vs. permanent) but
**not** the trust-model contrast: ripples are signed, exactly like reeds,
and this step locks that shape before schema or API work starts.

Ripples are user-signed and server-countersigned, following the identical
two-payload pattern reeds already use (`signing.BytesToSign` +
`identity.go`'s user-payload/server-payload split).

## Scope

- Define what a ripple is: signed, server-stored, thread-scoped reply
  object attached to exactly one reed.
- Define the signing/countersigning envelope and the content-addressed
  `hash` that serves as a ripple's id.
- Define the thread/expiry model precisely enough to derive a schema from it.
- Define the render-gating rule relative to parent-reed visibility.
- Decide moderation surface (can the reed author or ripple author delete a
  ripple) since that's a modeling question, not just an API detail.

## Non-goals

See [README](README.md) Non-goals — this step doesn't re-derive those, only
locks the shape needed by 01–04.

## Design

### What a ripple response is

A **response** is a short user-signed, server-countersigned text reply,
attached to a **thread**. A thread belongs to exactly one reed (the
"parent reed"). A reed is not limited to a single thread — see Thread
shape below. "Ripple" remains the product/feature name; "response" is the
term used below whenever this doc needs to distinguish the individual
reply object from the feature as a whole or from table names (the storage
layer's `ripples` table is bookkeeping-only, not a table of responses —
see [01](01_schema_and_expiry.md)).

Contrast with a reed:

| | Reed | Ripple response |
|---|---|---|
| Signed | Yes (`BytesToSign` + detached PGP sig + server countersignature) | **Yes** — same envelope shape, see Signing below |
| Stored body | Not retained server-side (see [conversations/00](../conversations/00_design.md)) | Stored server-side (only copy) |
| Propagation | p2p, held by peers, counted in coverage | Server-only, never leaves this instance |
| Lifetime | Permanent until signed removal | 7 days from last activity anywhere on the parent reed's ripples, unattended |
| Identity of author | Cryptographic (signature verifies key ownership) | **Cryptographic** — same as a reed, see Signing below |
| Id | Client-minted UUIDv7 | Server-computed content hash, see Signing below |

The remaining, genuine contrast with a reed is **propagation and
retention**, not trust: a ripple is signed and verified exactly like a
reed, but the server is still the only copy (never p2p-propagated, never
counted in holder coverage, gone after a week of silence). Signing a
ripple does not make it durable — it makes its authorship and content
tamper-evident for as long as it exists.

### Signing (locked)

A ripple response has **two** signed byte sequences, mirroring
`identity.go`'s user-payload/server-payload split exactly — read that
file's package doc before implementing this section, since the rule
"both payloads flow through `signing.BytesToSign`, which is the sole
canonicalisation authority" applies here without exception.

**User payload** — the exact bytes the ripple's author signs client-side,
*before* the server has assigned anything:

```
headers: { reedAuthorID, reedID, rippleAuthorID, fingerprint, threadID, replyingTo? }
content: <ripple text, verbatim, unescaped>
```

- `threadID` is present for every response, including a new top-level
  post — see Thread shape below for exactly how the client obtains it.
  `replyingTo` is omitted (and therefore dropped by `BytesToSign`, which
  drops empty-value headers) for a top-level post, present for a reply.
  Both are part of what's signed: a server (or anyone) cannot reattribute
  a reply to a different parent, or move a response into a different
  thread, without breaking the user's signature. This mirrors reeds
  signing their own optional `replying`/`echoing`/`threadId` headers (see
  `reedAsMarkdown` in `spa/src/lib/types/reed.ts`).
- **No timestamp is signed by the client.** Consistent with every other
  signed flow in this codebase, client clocks are never trusted. The
  timestamp is added later, server-side, in the server payload below —
  this is the same split reeds already use: `reedAsMarkdown`'s
  user-signed envelope has no timestamp; `ReedCountersignHeaders`' server
  envelope adds one from `now()` at countersign time.
- `fingerprint` is the signing key's fingerprint, self-describing, same
  as `identity.go`'s `userIdentityHeaders`.
- The user's detached PGP signature over these bytes is `userSignature`
  (base64-armored, same wire convention as every other signature in this
  codebase — travels as `base64(armored PGP)`).

**Server payload** — built and countersigned by the server *after*
receiving the client's POST, once a server timestamp exists:

```
headers: { serverID, reedAuthorID, reedID, rippleAuthorID, fingerprint, threadID, replyingTo?, timestamp }
content: <userSignature, base64 armor>
```

- Identical header set to the user payload, plus `serverID` (this
  server's identity) and `timestamp` (server-supplied `now()`, RFC3339,
  truncated to whole seconds — same truncation discipline
  `BuildReedPayload`/`recordTimeFormat` already require, so what's signed
  matches what Postgres stores after any timestamp round-trip).
- Content is the user's signature, not the ripple text — mirrors
  `BuildReedPayload` exactly (content = `signature`, not the reed body),
  welding the user's attestation into the server-signed bytes the same
  way `identity.go`'s package doc describes for profile/reed
  countersignatures: "a compromised server cannot re-pair Alice's
  userSignature with a different set of server-authored fields."
- The server's detached PGP signature over these bytes is
  `serverSignature`, produced by the existing shared `h.countersign(...)`
  primitive (`handlers.go`) — no new countersigning mechanism.

**The response's `id` is the hash of the server payload** — the entire
reason for the two-tier split above. `hash = hex(SHA256(serverPayload
bytes))`, returned to clients as a `"hash"` field (see
[02](02_post_and_list_api.md) for the wire shape). This is content
addressing: the id is not chosen by anyone, it is computed, and any party
holding the same headers + `userSignature` will compute the identical
hash. Two things make this well-defined and collision-free in practice:

- The server payload includes `timestamp`, which is unique per POST (to
  the second) even for byte-identical repeated text from the same author
  on the same reed/thread — this alone makes exact collisions
  vanishingly unlikely in practice, and:
- The server payload's content is `userSignature`, which is a *fresh
  PGP signature* for every signing operation (nondeterministic by
  construction for the ECC/EdDSA keys this codebase generates — see
  `cryptoService.generateKeyPair`, `type: 'ecc'`) — so even a
  hypothetically colliding timestamp+text pair from the same author
  cannot produce the same server payload bytes twice. Hashing the server
  payload (which folds in the signature) rather than the raw text is
  what makes the id genuinely collision-free, not merely
  collision-unlikely.

**This is a new pattern for this codebase.** No other entity derives its
id from a content hash — reed ids are client-minted UUIDv7s (signed, but
not hash-derived); user/server/invite ids are `crypto.NewID()` random
strings. Flagged explicitly so a future contributor doesn't go looking
for an existing "hash-as-id" helper that doesn't exist yet — this is the
first one, and the composition is `hex.EncodeToString(crypto.Hash(...))`
over `signing.BytesToSign(...)` output, using existing primitives, not a
new hashing scheme.

**The id is frozen at creation and never recomputed.** A self-delete
(see Moderation below) replaces `content` with `"[DELETED]"` but does
**not** touch the stored `hash`/`id`. This is a deliberate, necessary
choice: if the id were derived from *current* content, soft-deleting a
response would silently change its own identity, breaking every
`replyingTo` pointer into it and desyncing any client's IndexedDB key.
The id is a fingerprint of *what was originally posted and signed*, not
a live-recomputed digest of current state — this is also why a
tombstoned response's stored `content` no longer matches what its own
signature attests to, which is expected (see Client-side verification
below), not a bug.

### Client-side verification and caching (locked)

**On receiving a ripple (from a list fetch or a `RIPPLE_POSTED` WS
event), the client:**

1. Checks whether it already has a response with this `hash` in its local
   `ripples` IndexedDB store (mirrors the existing `reeds` repository's
   shape — see [04](04_spa_ripples_section.md)). If present, skip
   verification and rendering proceeds from the cached copy — this is
   the same "verify once, trust the cache thereafter" posture every other
   signed entity in this codebase already uses (reeds, profiles, keys).
2. If absent, verify **both** signatures before caching, following the
   exact `verifyReed` shape in `spa/src/lib/verifiers/index.ts`:
   - Resolve the author's public key: check `publicKeyRepository` first
     (`hasPublicKey(fingerprint)`), fetch via `apiService.getPublicKey`
     and cache it if missing — the exact pattern already implemented as
     `resolvePublicKey` in `verifiers/index.ts`. Reused as-is for
     ripples, not reinvented.
   - Verify the server's attestation on that key itself
     (`verifyPublicKey`, already implemented, reused as-is).
   - Rebuild the user payload from the response's own fields and verify
     `userSignature` against it with the resolved public key.
   - Rebuild the server payload (adding this server's own identity/key)
     and verify `serverSignature` against it.
   - Check the author's key wasn't revoked before the response's
     `timestamp` (`isKeyValidAt`, already implemented, reused as-is).
3. **If either signature fails to verify, discard the response** — do not
   render it, do not cache it. This is a hard failure, not a degraded
   fallback; a response that doesn't verify is treated as if the server
   never sent it. (The one deliberate exception is a soft-deleted
   response — see below.)
4. **For the response's author**, independent of the response's own
   verification: check whether the client already has that user's
   **profile** cached (not just their key) — if not, fetch and cache it
   the same way `storeReed`'s author-profile caching already does in
   `spa/src/lib/repositories/reeds.ts`. This is what lets the SPA render
   a real username/avatar instead of a bare userID for an author the
   viewer hasn't encountered before.

**Verifying a tombstoned (soft-deleted) response.** A response with
`deleted: true` has `content = "[DELETED]"`, which by construction no
longer matches what `userSignature` was computed over (see Signing
above — the id/hash is frozen, but the *content the signature covers* is
the pre-delete text). Re-running the normal verification steps above
against a tombstoned response's *current* content would therefore always
fail — this is expected, not a sign of tampering, and must be special-
cased: **a client that already has this response cached from before the
delete does not need to re-verify it** (per step 1's cache-hit shortcut —
this is precisely why that shortcut matters here). A client encountering
a tombstoned response **for the first time** (e.g. it never had this
reed's ripples loaded before) trusts the server's `deleted: true` flag
directly without attempting signature verification against the
now-mutated content — the flag itself is server-asserted, and the
original signed content is gone by design (see [01](01_schema_and_expiry.md)'s
Soft delete section — this codebase does not retain the pre-delete text
anywhere to re-verify against, on the server or the client).

### Thread shape

**A reed can have multiple independent threads**, not just one. A thread
is identified by `threadID`, a **client-minted UUID**, signed as part of
the user payload (see Signing above) — minting it client-side is what
makes it possible to sign.

- **New top-level post** (no `replyingTo`): the client mints a fresh
  UUID for `threadID` and signs it.
- **Reply** (`replyingTo` set): the client already has the parent
  response loaded locally (it's rendering the reply-to chip the user
  tapped) — it reads the parent's `threadID` from its own cache and
  signs that same value for the new response. The client never needs a
  round-trip to "ask" the server what the parent's thread is; it already
  has the answer.
- **Server-side validation**: `threadID` must be a syntactically valid
  UUID → 400 otherwise. If `replyingTo` is present, the server
  independently looks up the referenced response's stored `threadID` and
  rejects the post (400) if the client-submitted `threadID` doesn't
  match. This is a deliberate integrity check, not merely trusting the
  signed value: the signature only proves the *author* intended that
  `threadID`, not that it's factually consistent with the parent — a
  buggy or malicious client could otherwise fork a reply into a
  different thread than its parent while still passing signature
  verification. See [02](02_post_and_list_api.md) for the exact
  validation ordering.

A reply to any response in a thread inherits that response's `threadID`,
regardless of how deep the reply chain runs — the client resolves this by
walking to whichever response it's directly replying to and copying
*that* response's already-known `threadID` (not by walking the full
chain to a root; every response already carries its own resolved
`threadID`, so one hop is always sufficient).

Ordering: threads are ordered by the thread's own creation time (the
timestamp of its top-level response), oldest thread first. Within a
thread, responses are ordered by post time, oldest first. **The UI renders
one flat list — there is no visual nesting.** Thread grouping is purely an
ordering guarantee the server provides (all of one thread's responses
appear together, in a fixed position relative to other threads), not a
nested/tree rendering structure. A response may optionally reference
another response in the *same* thread as `replyingTo` for **display-only**
@-style addressing (rendering "replying to @user" inline); this pointer
does not itself define thread membership — `threadID` does that
independently, though the two are validated to agree, per above — and has
no effect on expiry or visibility. If the referenced response was
moderation-deleted (soft-deleted, see below), render it as its tombstone
rather than falling back to a generic "replying to a deleted comment"
line; that fallback text is reserved for the rarer case where the
referenced response isn't present in the currently-loaded page at all (a
pagination edge case — see [04](04_spa_ripples_section.md)).

Rationale for flat-only rendering: reed replies already give this codebase
a full recursive-thread model
([`conversations/02_index_and_api.md`](../conversations/02_index_and_api.md)'s
`reed_threads`/`reed_replies`). Duplicating that machinery for a
lightweight comment box is not worth the complexity — ripples are meant
to feel like a comment section, not a second conversation system.
Allowing multiple threads per reed (rather than forcing every response on
a reed into one shared conversation) gives commenters room to start
independent side-conversations without them colliding into a single
undifferentiated stream, while still keeping the render itself simple.

### Lifetime / expiry rule (locked)

- A **reed** has a single shared `expires_at` timestamp = `last_activity_at
  + 7 days`, covering **all of that reed's threads together** — not one
  `expires_at` per thread. This is a deliberate choice: activity in any one
  thread on a reed keeps the *entire* ripples section on that reed alive,
  not just that one thread.
- `last_activity_at` updates to `now()` on: the reed's first-ever response
  (creating its ripples bookkeeping row) and every subsequent response to
  **any** thread under that reed. Posting to a quiet, older thread on a
  reed that has other, more recently active threads still counts as
  activity and resets the shared clock for everything on that reed.
- Editing is not supported (see Non-goals), so nothing else bumps activity.
- When `expires_at` passes, **every thread and every response under that
  reed** is removed in one operation. There is no per-thread or per-response
  expiry independent of the reed-level clock.
- The removal mechanism is **indirect**: a background sweep deletes the
  reed's bookkeeping row (see [01](01_schema_and_expiry.md)) once expired;
  the responses themselves are removed as a cascade side effect of that
  single delete, not by a direct delete against the content table. This
  is also why the underlying schema is split into two tables — see 01.
- The list API (see [02](02_post_and_list_api.md)) never puts the stored
  `expires_at` on the wire as an absolute timestamp — it sends
  `expiresInSeconds`, a duration computed server-side at request time.
  The client starts a local countdown from that value instead of
  comparing an absolute deadline against its own system clock, so a
  skewed device clock can never make a thread appear to expire early or
  late. See [04](04_spa_ripples_section.md)'s Expiry animation section
  for the client-side countdown/animation this drives.

### Visibility gate (locked)

Ripples for a reed are only fetched/rendered once the parent reed is fully
receivable — same condition the SPA already uses to gate
`ConversationSection` on the reed detail page:
`isPending = !!(reed && !reed.serverSignature)`; ripples render only when
`!isPending`. Concretely:

- The SPA does not request `GET /reeds/{userID}/{reedID}/ripples` (see
  [02](02_post_and_list_api.md)) until the parent reed has rendered past
  its pending state.
- The composer for posting a new ripple is not shown until the same
  condition holds — you cannot comment on a reed you can't yet see.
- This is enforced client-side only; the server has no concept of "pending"
  (a reed either exists server-side, signed, or it doesn't — pending is a
  purely client-local receipt state before p2p delivery completes). No
  server-side gate is needed or added.

### Moderation (open question, resolved for v1)

**Locked for v1: only a response's own author can delete it.** The parent
reed's author has no special ripple-moderation power in this spec.
Rationale: giving reed authors delete power over other people's responses
is a real feature (moderation) with its own abuse surface (silent
censorship of criticism) that deserves its own design pass, not a
default bolted on here. Revisit in a follow-up step if requested.

**Self-delete is a soft delete, not a hard delete.** Deleting flips a
`deleted` boolean to `true` and replaces `content` with the literal string
`"[DELETED]"`; the row is **not** removed from the table, and its `id`/
`hash` is **not** recomputed (see Signing above). It continues to occupy
its original position in thread/post order and continues to resolve as
the target of other responses' `replying_to` pointers — rendered as a
visible tombstone (muted styling, no delete button, no reply affordance),
not as the "a deleted comment" fallback described under Thread shape above
(that fallback is reserved for a `replyingTo` id that isn't present in the
currently-loaded page at all, not for the common "the author deleted it"
case, which now has its own tombstone rendering — see
[04](04_spa_ripples_section.md)).

Deleting requires only the author's authenticated session — it is **not**
re-signed. The original `userSignature`/`serverSignature` remain stored
and displayed as-is (they now describe the pre-delete content, which the
server no longer serves — see Client-side verification above for how a
tombstone is handled without a signature re-check).

A self-delete does **not** delete the whole reed's ripples section, does
**not** delete the response's own thread, and does **not** count as
"activity" for the shared 7-day clock (deleting isn't posting). Rows only
disappear from storage entirely via the expiry sweep described in
[01](01_schema_and_expiry.md), which operates at the whole-reed level
(deleting the bookkeeping row cascades away every response, deleted or
not) — never as a direct consequence of an individual self-delete.

### Content constraints

Reuse the existing reed visible-length constant as the ripple cap:
`MAX_REED_VISIBLE_CHARS = 140` (from
`spa/src/lib/utils/reedContent.ts`) — same constraint, same reasoning
(short-form, terse). No markdown grammar — ripples render as **plain
text** (no `reedMarkdown.ts` parsing, no mentions, no hashtags, no links).
Keeping ripples markdown-free avoids re-litigating the entire mention/link
security surface for a feature that's deleted in a week anyway; plain text
is escaped/rendered as-is by the SPA. Server-side, the 140-char limit is
validated with a plain Unicode code-point count (`utf8.RuneCountInString`
in Go), not the markdown-aware character counter reeds use elsewhere in
this codebase — that counter strips markdown syntax before counting, which
is the wrong tool for content that's never parsed as markdown in the first
place and would let markdown-syntax-heavy plain text dodge the visible
budget.

**Client/server byte parity for signing.** Because the server validates
and (if it trims) normalizes `content` independently of what the client
signed, the client **must** apply the identical trimming the server
applies (leading/trailing whitespace trim) *before* signing — not after.
If the client signs raw untrimmed textarea content but the server trims
before building its own payload/hash, the two sides sign/verify different
bytes and every post fails verification. This is the same class of bug
already fixed once in this codebase for username trimming parity (client
`trimInvisibleChars` mirroring the server's Go function exactly) — the
ripple composer must trim client-side before calling
`buildRipplePayload`, not rely on the server to normalize post-hoc.

### Account removal (locked)

Account removal in this codebase is **cert-only**: it inserts an
`account_removals` row and does not hard-delete the `users` row or any of
that user's `reeds` rows. This has two distinct, independent consequences
for ripples:

- **A removed user's own past responses, posted on other people's reeds,
  are unaffected.** They persist exactly as posted — same content, same
  position, same thread — until the reed they're on naturally reaches the
  end of its own 7-day shared-activity window, same as any other response
  on that reed. Account removal does not force early cleanup of a user's
  own comment history elsewhere; the client renders the response as usual,
  substituting a neutral "removed account" label for the author's username
  (see [04](04_spa_ripples_section.md)). Note this has no bearing on
  signature verification: the response's signatures remain valid
  regardless of the author's current account-removal state — a removed
  account's key was valid at the time of signing, which is all
  verification checks (see Client-side verification above).
- **The ripples section on a removed user's *own* reeds becomes
  inaccessible immediately** — `GET`/`POST .../ripples` for a reed authored
  by a removed account returns the same not-found/removed response a
  request against an individually-removed reed would (see
  [02](02_post_and_list_api.md)), regardless of how much time is left on
  the 7-day timer. This falls out of the existing parent-reed-lookup
  convention every ripples endpoint already uses to validate the parent
  reed — that lookup already treats a removed account's reed the same way
  it treats an individually-removed reed, so no new server-side mechanism
  is needed for this rule; it is a consequence of reusing that lookup, not
  a bolt-on.

### Realtime delivery shape (contract only — full design in 03)

Two `BroadcastType` values: `RIPPLE_POSTED` (a new response landed) and
`RIPPLE_UPDATED` (an existing response was soft-deleted — content is now
`"[DELETED]"`). There is no `RIPPLE_DELETED` event, because nothing is
ever removed from a subscribed client's list by a delete — only patched in
place.

Delivery goes through this codebase's existing indirect fanout path, not a
direct call: the HTTP handler pushes the event onto the app's broadcast
channel; a separate dispatch goroutine reads from that channel and is the
one that actually invokes
`ConnectionManager.SendToReedSubscribers(authorUserID, reedID, payload)`
(`realtime/connection_manager.go:409`) — the same underlying primitive and
the same per-reed subscriber set every other reed-scoped realtime event in
this codebase already uses. A client only receives `RIPPLE_POSTED` events
for a reed once it has subscribed to that reed's channel (the existing
per-reed WS subscription — see [03](03_realtime_fanout.md)); a newly
delivered response goes through the exact same verify-or-discard path as
a list-fetched one (see Client-side verification above) — realtime
delivery is not a trusted shortcut around signature checking. Payload
carries the response itself (not the whole thread or reed) so subscribed
clients append or patch a single item rather than refetch everything.

## Risks

- **Signature verification cost on every uncached response** — every
  first-seen response requires a public-key resolution (possibly a
  network round-trip) plus two signature verifications before it can
  render. Mitigated by the cache-hit shortcut in Client-side verification
  above (already-seen responses skip re-verification entirely) and by
  `resolvePublicKey`'s own caching/throttling — but a reed with many
  first-time authors in its ripples could still see a slower initial
  render. Not solved here, flagged for [04](04_spa_ripples_section.md) to
  consider (e.g. resolving keys in parallel rather than serially).
- **No moderation by reed author in v1** — a reed author cannot remove a
  hostile response on their own post; they can be revisited once the sweep +
  posting flow are stable. Flagged, not solved, here.

## Dependencies

None — this is the design lock other steps build from.

## Parallelism

None; 01 depends on this.
