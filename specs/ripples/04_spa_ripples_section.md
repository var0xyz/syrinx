# Ripples 04 — SPA Ripples section

## Status

Proposed.

## Depends on

[02](02_post_and_list_api.md), [03](03_realtime_fanout.md)

## Context

The reed detail page (`spa/src/routes/reed/[userID]/[reedID]/+page.svelte`)
already mounts `ConversationSection` gated on `!isPending`, where
`isPending = !!(reed && !reed.serverSignature)` (confirmed by direct code
read at ~line 579, with a separate tombstone-branch mount at ~478–484).
Ripples get their own section on the same page, gated on the same
condition, per [00](00_design.md)'s locked visibility rule.

`RipplesSection.svelte` already exists as a UI-only mock (hardcoded local
data, no `api.ts` calls, no WS, no persistence — built to nail down
layout/copy/interaction shape before the real backend existed). The work
described here is **wiring the existing mock to real data**, not building
a new component from scratch. Because ripples are signed (see
[00](00_design.md)), this includes the full sign-before-post /
verify-and-cache-on-receipt machinery this section describes.

## Scope

- Wire the existing `RipplesSection.svelte` mock to `api.ts` and WS.
- **Sign each ripple before posting.**
- **Verify and cache each received ripple** — check IndexedDB by `hash`,
  verify both signatures on a cache miss, discard on verification
  failure, resolve/fetch the author's public key and profile as needed.
- Fetch on mount (via 02's `GET`), live-append/patch via 03's WS events,
  optimistic local append on successful POST.
- Self-delete affordance (trash icon on own responses only).
- Tombstone rendering for soft-deleted responses.
- Removed-account rendering for a response's author whose account has been
  removed.
- Empty state, loading state, error state.

## Non-goals

- Editing UI (no edit exists server-side either).
- Nested reply UI beyond the flat "replying to @X" display line (00's
  lock) and thread-boundary ordering (server-guaranteed, no client-side
  grouping logic needed — see Data flow below).
- Any reed-author moderation controls (00's lock — not built server-side,
  so nothing to wire client-side).
- Offline queueing/retry for posting (see README — a post either succeeds
  against the live server or it didn't happen).

## Design

### Mount point

In `spa/src/routes/reed/[userID]/[reedID]/+page.svelte`, alongside the
existing `{#if !isPending}<ConversationSection .../>{/if}` block:

```svelte
{#if !isPending}
  <ConversationSection {reed} ... />
  <RipplesSection userID={reed.userID} reedID={reed.id} />
{/if}
```

Same condition, same branch — no new pending-state logic invented. In the
tombstone branch (reed removed), neither section mounts, consistent with
existing behavior for `ConversationSection`.

### IndexedDB store and repository

New IndexedDB object store, `ripples`, keyed by `hash` — mirrors the
shape and conventions of the existing `reeds` repository
(`spa/src/lib/repositories/reeds.ts`), not a new pattern:

- `storeRipple(ripple)` — the write path every fetched/received ripple
  goes through. Mirrors `storeReed`'s shape: resolve/cache the author's
  public key first (see Verification below), then `dbService.put('ripples',
  ripple, verifyRipple)` — a **verification-gated put**, same as every
  other signed entity in this codebase (`dbService.put` "always requires
  one of these [verifiers] (or `allowUnsigned`)" — see
  `spa/src/lib/verifiers/index.ts`'s own file-level doc comment). A
  verification failure means the put simply doesn't happen — the
  response is discarded, not stored in a partially-trusted state.
- `getRipple(hash)` → `dbService.get('ripples', hash)` — single-key
  lookup, mirrors `reeds.ts`'s `getReed`.
- `hasRipple(hash)` → existence check used by the cache-hit shortcut
  described in [00](00_design.md)'s Client-side verification section —
  mirrors `publicKeyRepository.hasPublicKey`'s shape (boolean existence
  check distinct from the full get).

### New verifier: `verifyRipple`

New function in `spa/src/lib/verifiers/index.ts`, alongside `verifyReed`
and modeled directly on it — same file, same conventions, not a new
verification pattern:

```ts
export async function verifyRipple(ripple: RippleType): Promise<boolean> {
  if (ripple.deleted) {
    // Content has been replaced server-side ("[DELETED]"); the stored
    // signatures describe the ORIGINAL content, which this client no
    // longer has and cannot re-verify against. Trust the server-asserted
    // deleted flag directly — see 00's Client-side verification section,
    // "Verifying a tombstoned (soft-deleted) response."
    return true;
  }

  if (!ripple?.userSignature?.armor || !ripple.userSignature?.fingerprint ||
      !ripple.userID || !ripple.serverSignature) {
    return false;
  }

  const publicKeyData = await resolvePublicKey(ripple.userID, ripple.userSignature.fingerprint);
  if (!publicKeyData) return false;
  if (!(await verifyPublicKey(publicKeyData))) return false;

  const userPayload = buildRippleUserPayload(ripple); // see Signing below
  const authorValid = await cryptoService.verifySignature(
    userPayload,
    atob(ripple.userSignature.armor),
    publicKeyData.armor
  );
  if (!authorValid) return false;

  const serverPayload = buildRippleServerPayload(ripple, ripple.userSignature.armor);
  const serverResult = await verify(ripple.serverSignature, serverPayload);
  if (serverResult.ok === false) return false;

  return isKeyValidAt(
    ripple.userID,
    ripple.userSignature.fingerprint,
    publicKeyData.revoked,
    ripple.serverSignature.timestamp
  );
}
```

This is `verifyReed` with the reed-specific payload builders swapped for
ripple ones and a tombstone short-circuit added at the top — same
`resolvePublicKey`/`verifyPublicKey`/`verify`/`isKeyValidAt` helper
functions, reused as-is, not reimplemented. `resolvePublicKey` already
does the "check cached, fetch-and-cache if missing" work described in
[00](00_design.md)'s Client-side verification step 2 — no separate logic
needed here for that.

**A response that fails `verifyRipple` is discarded**, per
[00](00_design.md): the caller (the fetch/receive path below) must treat
a failed `dbService.put` (verifier returned `false`) as "this ripple does
not exist for rendering purposes," not as an error to surface loudly to
the user — same posture this codebase already takes toward a reed that
fails `verifyReed`.

### Signing: `buildRippleUserPayload` / `buildRippleServerPayload`

New functions in `spa/src/lib/services/signing.ts`, alongside
`buildReedPayload` and following the identical `bytesToSign`/
`stringToSign` convention that file already documents as the sole
canonicalization authority (mirrored from Go's `signing.BytesToSign` —
see [00](00_design.md)'s Signing section for the exact header/content
layout both functions must produce, byte-for-byte matching the Go
functions of the same shape added in [01](01_schema_and_expiry.md)).

```ts
export function buildRippleUserPayload(
  reedAuthorID: string, reedID: string, rippleAuthorID: string,
  fingerprint: string, threadID: string, content: string,
  replyingTo?: string
): string {
  return stringToSign(
    {
      reedAuthorID, reedID, rippleAuthorID, fingerprint, threadID,
      ...(replyingTo ? { replyingTo } : {}),
    },
    content
  );
}
```

`buildRippleServerPayload` mirrors this with the additional `serverID`/
`timestamp` headers and `content = userSignature armor`, matching
`buildReedPayload`'s existing shape exactly (see
[00](00_design.md)'s Signing section, Server payload). Both functions
**must** stay byte-identical to their Go counterparts — this is the same
"MUST stay byte-identical" contract `signing.ts`'s file doc already
states for every other payload-builder pair in this codebase, and it
applies here without exception.

### Composer: signing before POST

The composer's submit handler (in `RipplesSection.svelte`) performs real
cryptographic work before the network call:

1. **Trim content client-side first**, using the exact same trimming the
   server applies — see [00](00_design.md)'s Content constraints section
   on byte parity; signing untrimmed text that the server then trims
   before hashing/verifying would make every post fail.
2. **Resolve `threadID`**: a fresh client-generated UUID for a new
   top-level post, or — if replying — the `threadID` already present on
   the local (already-rendered, already-cached) parent response object
   the user tapped "reply" on. No network round-trip needed to determine
   this; see [00](00_design.md)'s Thread shape.
3. **Resolve the caller's own active signing key and passphrase** — same
   pattern this codebase already uses for signing a reed or a profile
   update (`authService.getActiveKeyFingerprint()` /
   `authService.getPassphrase()` / `privateKeyRepository.getPrivateKey(...)`,
   the exact sequence the profile-edit page's `saveProfile` already
   uses).
4. **Build and sign the user payload** via `buildRippleUserPayload(...)`
   + `cryptoService.signMessage(...)`, producing `userSignature` (base64
   armor, same wire convention as everywhere else).
5. **POST** `{content, threadID, replyingTo?, fingerprint, userSignature}`
   to [02](02_post_and_list_api.md)'s endpoint.
6. On success: the server response is the fully-signed, server-
   countersigned object (including the new `hash`) — run it through
   `storeRipple`/`verifyRipple` exactly like any other received ripple
   (see Data flow below) before rendering it as the "real" entry
   replacing the optimistic placeholder. This is a deliberate choice:
   even content the client itself just posted is verified through the
   same path as content from anyone else, rather than being special-cased
   as "trusted because I just made it" — keeps exactly one code path for
   "a ripple is now safe to render," not two.
7. On failure: remove the optimistic entry, show the server's message
   inline, **retain the draft text** (do not clear it).

### Data flow

- On mount: `apiService.listRipples(userID, reedID)` → 02's `GET` endpoint.
  For **each** item in the response, run it through `storeRipple`
  (cache-check by `hash` → verify-and-cache on miss → discard on
  verification failure — see IndexedDB store and `verifyRipple` above)
  before adding it to the rendered list. A response that fails
  verification is silently omitted from the rendered list, not shown as
  an error row.
- **Per-author profile resolution**: independent of a given response's
  own verification, for each response's `userID` the client checks
  whether it already has that user's **profile** cached (not just their
  key — the key gets resolved as a side effect of `verifyRipple` itself,
  but the profile does not). If missing, fetch and cache it the same way
  `storeReed`'s author-profile caching already does in
  `spa/src/lib/repositories/reeds.ts` — this is what lets the row render
  a real username/avatar instead of a bare userID. See
  [00](00_design.md)'s Client-side verification step 4.
- The response is a **flat, already-ordered array** (see 02 — threads
  grouped by creation time, responses within a thread ordered by post
  time); the component renders the (verified subset of the) list in the
  order received, with no client-side re-grouping or re-sorting needed.
  Each item's `threadID` is available if a subtle visual divider between
  threads is wanted, but that is optional polish, not a requirement.
- Pagination: reuses the `limit`/cursor shape from
  [`conversations/02`](../conversations/02_index_and_api.md)'s existing
  replies-list client code as a template, but note the cursor itself is
  an **opaque string** (per 02's Pagination cursor section), not a bare
  RFC3339 timestamp — pass it through as an opaque value, don't attempt to
  parse or construct it client-side.
- The list response's top-level `expiresAt` field (an absolute timestamp)
  drives the countdown display (see 00's Lifetime rule) — it is **not**
  derived client-side from the currently-loaded responses' `postedAt`
  values, since that derivation would be wrong once pagination is
  involved (a partial page may not contain the most recent response). The
  client converts it once, at fetch time, into a local deadline measured
  against `performance.now()` (`performance.now() + (Date.parse(expiresAt)
  - Date.now())`) — every tick after that compares against
  `performance.now()`, not the wall clock, so the animation itself can't
  jump if the system clock changes mid-session. The absolute value is
  still what's stored and re-derived fresh on every fetch, so a session
  that outlives a server-side change to `expires_at` (an operator
  manually rewriting it, for example) self-corrects on the next fetch
  instead of drifting from a stale relative countdown forever. If the
  server reports an `expiresAt` already in the past, the client does not
  render whatever responses came with it, even if the sweep hasn't
  deleted them yet — the section falls back to its ordinary empty state
  instead (see the Expiry animation subsection below).
- WS: subscribe to the existing per-reed channel (already established by
  `ConversationSection`'s own subscription — check whether that
  subscription is page-scoped or component-scoped before adding a second
  one; reuse rather than duplicate the subscribe call if it's page-scoped).
  On `RIPPLE_POSTED`, run the payload through the same `storeRipple`/
  `verifyRipple` path as a list-fetched item — **not** a raw
  trust-and-append — and append to the local list only if verification
  succeeds and it's not already present (by `hash`, guards against the
  optimistic-append-then-echo double-add described in Composer step 6).
  On `RIPPLE_UPDATED`, **patch the matching row in place** (`deleted`,
  `content`) — **do not remove it from the list**, and do **not**
  re-verify (per `verifyRipple`'s tombstone short-circuit above, which
  already handles this correctly if the patch is implemented as "replace
  the cached object and re-run storeRipple" rather than a raw field
  mutation — either implementation is fine as long as the tombstone
  short-circuit is honored, not bypassed). This is the single most
  load-bearing behavioral requirement in this file: there is no
  `RIPPLE_DELETED` event (see [03](03_realtime_fanout.md)), and a client
  that still removes rows on delivery will visually break the tombstone
  model described below.

### Composer (field shape)

Plain `<textarea>`, 140-char counter (reuse `MAX_REED_VISIBLE_CHARS` from
`spa/src/lib/utils/reedContent.ts` — same constant 00/02 reference
server-side, single source of truth split only by language runtime), no
markdown toolbar (00's lock: ripples are plain text). "Replying to
@username" chip appears above the textarea when the user taps "reply" on a
specific response (sets `replyingTo` and resolves `threadID` from that
response — see Signing above), dismissible to go back to top-level
(clears both). Field name is `replyingTo`, matching 02's API.

### Rendering a ripple row

Reuse `ReedAuthorHeader.svelte` (already extracted this session for exactly
this avatar+username+timestamp overflow problem — see
[`conversations/03_spa_reed_detail.md`](../conversations/03_spa_reed_detail.md)
and the pipe-page/reeds-list call sites) for the author line:
`<ReedAuthorHeader userID={response.userID} username={...} subtext={formatRelativeTime(response.postedAt)} avatarSize="32px" nameTag="span" />`,
followed by the plain-text content (rendered via `{response.content}` in a
`<p>` — no `MarkdownParser`, per 00's plain-text lock) and, if present, a
small "↳ replying to @X" line resolved from the local list.

That resolution has two distinct fallback cases:

#### Rendering a soft-deleted response (tombstone)

A response with `deleted: true` stays in the list at its original
position — it is **not** filtered out or removed (see [00](00_design.md)'s
Moderation section and [02](02_post_and_list_api.md), which both include
soft-deleted rows in list results deliberately). Render it as a visible
tombstone: muted/greyed styling, literal content text `[DELETED]`, no
delete button (already deleted), and no reply-inline affordance (replying
to a tombstone is still technically allowed server-side, but hiding the UI
affordance avoids a confusing "why would I reply to a deleted comment" UX
— this is a client-side-only choice, not a server restriction). Other
responses' "replying to @X" chips that point at a tombstoned response
resolve through it normally (showing the tombstoned author's name, or a
neutral marker — implementer's choice of exact copy, e.g. "replying to a
deleted comment" reused here is fine since the row genuinely no longer has
visible content, even though the row itself is still present) — this is
the *common* case for an unresolvable-looking reply chip, distinct from
the fallback case below. A tombstoned row's cached copy in IndexedDB is
never re-verified against its (now-mismatched) `content` — see
`verifyRipple`'s tombstone short-circuit above.

#### Rendering a removed-account commenter

Distinct from the tombstone case above: a response whose **author's
account** has since been removed (not the response itself — the response
may be perfectly live, unmodified content) still renders normally in
every respect — content, position, timestamp, delete/reply affordances if
it's not also otherwise deleted — except the author-line username
resolves to a neutral "[removed account]" label instead of a live
username (see [00](00_design.md)'s Account removal section — this is the
"comments on other people's reeds persist" half of that rule; it has
nothing to do with the reed-author-removed case, which 410s the whole
section instead and never reaches this rendering path at all). Note this
has no bearing on `verifyRipple`: a removed account's response still
verifies normally (its key was valid when it signed), account removal is
purely a rendering-label concern, not a verification concern. This is a
small UI case with no existing lightweight precedent elsewhere in this
codebase to reuse — the codebase's other removed-account handling (e.g.
in `ReedsList.svelte`/`Quote.svelte`) is a heavier signed-cert
verification flow for a *different* kind of removal cert, not applicable
here. Keep this minimal: a label swap only, no extra network call beyond
the profile-resolution already described in Data flow above.

#### Expiry animation

When the local countdown (derived from `expiresAt`, see Data flow above)
reaches zero, the client plays a short fire/burn animation on the visible
list — each row fades, rises, and shifts color toward orange/red in a
staggered sequence rather than the whole list vanishing as one flat block
— then the list clears. This is a client-only visual event: no new WS
message type, no new API call, fires purely off the local countdown
timer already described in Data flow. If the *server* itself reports an
`expiresAt` already in the past on a fresh fetch (the fetch landed in the
race window before the cron sweep in [01](01_schema_and_expiry.md) has
run), the client clears the list without playing the animation — there's
nothing visibly alive on screen to burn in that case.

**Once the burn finishes, the section looks exactly like a reed that
never had any ripples** — the ordinary empty state ("No ripples yet — be
the first to say something"), composer still present, no special
"expired" message, no distinguishing marker of any kind. This is
deliberate: a burned-away thread and a never-started one are the same
state from this point forward. Starting a brand-new top-level post is a
completely fresh thread with its own `expires_at`, unrelated to whatever
just expired — the client clears its stale local deadline/guard the
moment a new post succeeds, so the composer is never blocked and a
freshly-posted ripple can never immediately re-trigger the burn
animation off the old deadline.

Between the countdown hitting zero and a possible next post, the client
does refuse to render any RIPPLE_POSTED/RIPPLE_UPDATED event that
arrives for the burned-away thread — a straggling WS message can't
resurrect content the client has already treated as gone. That guard is
lifted the moment the user's own next post succeeds (see Data flow),
not on any timer.

#### The remaining, rarer fallback

The "replying to a deleted comment" fallback text applies to exactly one
case: a `replyingTo` id that isn't present in the currently-loaded page of
responses at all — a pagination-boundary situation, not the common "the
author deleted it" case, which has its own tombstone rendering as
described above.

### Empty / loading / error states

- Loading: skeleton row or spinner, consistent with whatever
  `ConversationSection` already does for its own loading state (reuse
  the pattern, don't invent a new skeleton style). Verification is
  per-item async work (public key resolution can require a network
  round-trip on a cache miss), so loading may take noticeably longer than
  a plain fetch-and-render; no separate per-item loading indicator is
  required, a single list-level loading state is sufficient.
- Empty (no responses yet, **or** every fetched response failed
  verification and was discarded): "No comments yet — be the first to
  say something." (or similar; match this codebase's existing empty-state
  tone — check `ConversationSection`'s empty-reply-list copy for the
  house style before finalizing wording). These two cases are
  intentionally not distinguished in the UI — a verification failure is
  not surfaced as an error to the viewer (see `verifyRipple` above).
- Post failure (any server-rejected POST): inline error under the composer
  showing the server's message, textarea retains the unsent draft so the
  user doesn't lose their text.
- Signature verification failure on the caller's **own** just-submitted
  post (a genuine byte-parity bug, not expected in normal operation — see
  [02](02_post_and_list_api.md)'s Risks section): surfaced as the
  server's `400 Invalid signature` message via the normal POST-failure
  path in Composer step 7 above, not a distinct UI state.

## Work items

1. `spa/src/lib/services/api.ts` — `listRipples`, `postRipple`
   (now sending the full signed body), `deleteRipple`.
2. `spa/src/lib/services/signing.ts` — `buildRippleUserPayload`,
   `buildRippleServerPayload`.
3. `spa/src/lib/verifiers/index.ts` — `verifyRipple`.
4. New IndexedDB `ripples` object store + repository (`storeRipple`,
   `getRipple`, `hasRipple`) — mirrors `spa/src/lib/repositories/reeds.ts`.
5. `spa/src/lib/components/RipplesSection.svelte` — wire the existing mock
   to real data, including the signing-before-post and
   verify-on-receive flows above; do not rewrite it from scratch.
6. Mount into the reed detail page.
7. WS dispatch cases wired in 03's SPA work item — `RIPPLE_POSTED`/
   `RIPPLE_UPDATED`, routed through `storeRipple`/`verifyRipple`, confirm
   here rather than duplicate.
8. Playwright test: open a reed, post a response (confirm it's actually
   signed — inspect the network request body for `userSignature`), see it
   appear; post a reply, confirm the "replying to @X" chip renders, the
   response lands in the correct thread group, and its `threadID` matches
   the parent's; open the same reed in a second browser context, confirm
   live delivery **and** that the second client independently verifies
   the delivered payload (e.g. by confirming it still renders correctly
   for an author the second client has never seen before, exercising the
   key-fetch path); delete own response, confirm it renders as a
   `[DELETED]` tombstone **in place** (not removed from the DOM), and a
   reply chip pointing at it still resolves and doesn't fall back to the
   pagination-boundary placeholder; confirm a response never renders
   while the parent reed is still pending (simulate via the same
   technique used for existing pending-state tests, if one exists — check
   `spa/tests/` for a precedent before inventing a new pending-simulation
   harness); confirm a response with a deliberately corrupted signature
   (test-only injection) is silently discarded, never rendered.

## Risks

- **140-char limit may feel cramped for a "comment" UX** — same risk noted
  in [02](02_post_and_list_api.md), deliberately deferred rather than
  solved twice.
- **No offline/optimistic durability** — a response posted while briefly
  offline just fails and the draft stays in the textarea for the user to
  retry manually; no background retry queue (00/README non-goal).
- **First-render latency for reeds with many first-time-seen authors** —
  see [00](00_design.md)'s Risks section; each unresolved author key may
  require a network round-trip before that row can verify and render.
  Not solved here; worth measuring once implemented, and worth
  considering resolving keys in parallel (`Promise.all` over the page's
  distinct authors) rather than serially per-row if it proves slow in
  practice.
- **Signing requires an unlocked key** — posting a ripple requires the
  same "active key + passphrase available" precondition reed posting
  already has. If a
  user's session has that unavailable (e.g. passphrase not cached), the
  composer must surface the same "session expired, please sign in again"
  class of error the profile-edit page already handles, not a confusing
  generic failure — reuse that existing error-handling pattern, don't
  invent new copy for what's functionally the same precondition failure.

## Dependencies

02 (API), 03 (realtime), `ReedAuthorHeader.svelte` (existing component),
`RipplesSection.svelte` (existing UI mock being wired up),
`verifyReed`/`resolvePublicKey`/`verifyPublicKey`/`isKeyValidAt`
(existing verification primitives, reused for `verifyRipple`),
`buildReedPayload`/`bytesToSign` conventions (existing signing
primitives, reused for the new ripple payload builders).

## Parallelism

None further downstream — this is the last step in the set.
