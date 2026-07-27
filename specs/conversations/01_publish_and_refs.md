# Conversations 01 — Verify markdown on publish; normalize `replying` ref

## Status

Proposed.

## Depends on

[00](00_design.md)

## Context

`POST /reeds` today accepts `reedID` and `signature` (base64 detached PGP)
but never sees the signed markdown. The server countersigns the user signature
bytes without verifying they attest to a coherent reed payload. Clients
already sign `reedAsMarkdown()` on publish; the body is available at submit
time.

Separately, `replying` is stored as a bare `reedId` while `echoing` uses
`authorId!reedId`, breaking symmetric parsing and the layout's referenced-reed
prefetch ([`+layout.svelte`](../../spa/src/routes/+layout.svelte)).

## Scope

- Extend `POST /reeds` to require the full signed markdown body.
- Verify the detached user signature over those exact bytes before
  countersigning.
- Normalize `replying` to `authorId!reedId` on client and server parsers.
- Return parsed social refs to the index step ([02](02_index_and_api.md)) via
  an internal struct — no new public fields on the countersign response in
  this step.

## Non-goals

- Index tables / list APIs ([02](02_index_and_api.md)).
- SPA conversation UI ([03](03_spa_reed_detail.md)) beyond ref-format changes
  needed to publish and resolve quotes.
- Federation `userID@serverID/reedID` ref format (documented as future).

## Design

### `POST /reeds` request

Add a required form field:

| Field | Required | Description |
|-------|----------|-------------|
| `reedID` | yes | Must match `id:` header inside markdown |
| `signature` | yes | Base64-encoded armored **detached** PGP signature |
| `markdown` | yes | Full signed payload: `---` headers + `---` + content |

Processing order in `SignReed`:

1. Auth → `userID`.
2. Decode `signature` → armored detached sig.
3. Load author's active public key (same lookup as deletion / revocation
   handlers).
4. `VerifySignature(string(markdown), detachedSig, pubKey)` — reject 400 on
   failure.
5. `ExtractReedHeader(markdown)` via existing `MarkdownService`.
6. Validate:
   - `header.ID == reedID` (form)
   - `header.UserID == userID` (auth)
   - `ValidateReedHeader(markdown)` passes (mandatory headers present, no
     unknown headers)
7. Parse social refs (new helper, see below).
8. Countersign + `CreateReed` (unchanged).
9. Pass parsed refs to index inserter ([02](02_index_and_api.md)) in the
   same transaction as `CreateReed` when that step lands; for this step alone,
   parsing + validation is sufficient with a no-op hook or feature flag.

### Social ref parsing

Shared Go helper (e.g. `conversations.ParseSocialRef`):

```go
// SocialRef is a parsed echoing/replying target on this instance.
type SocialRef struct {
    AuthorID string
    ReedID   string
}

// ParseSocialRef parses "authorId!reedId". Returns ok=false for empty input.
func ParseSocialRef(raw string) (ref SocialRef, ok bool)
```

Rules:

- Empty → absent (not an error).
- Split on **last** `!`; both sides non-empty after trim.
- `reedId` must pass existing `validateUUID25`.
- Bare `reedId` without `!` → **reject publish** with 400
  (`invalid_replying_ref` / `invalid_echoing_ref`). No legacy acceptance.

Optional validation (recommended, in same transaction as insert):

- Target `(authorId, reedId)` exists in `reeds` and is not removed / author
  not account-removed.
- Reject if target missing → 400 `unknown_target_reed`. Prevents orphan index
  rows and spam replies to non-existent ids.

A reed may have **both** `echoing` and `replying` set (echo-with-reply is
weird but not forbidden); index both when present.

### Client changes

**Publish** ([`NewReedModal.svelte`](../../spa/src/lib/components/NewReedModal.svelte),
[`reedsService.createReed`](../../spa/src/lib/repositories/reeds.ts)):

```ts
// When replying:
reed.replying = `${replyingTo.userID}!${replyingTo.id}`;
```

Send `markdown: reed.asMarkdown()` alongside existing fields in
`api.createReed`.

**Resolve replying quote** ([`+page.svelte`](../../spa/src/routes/reed/[userID]/[reedID]/+page.svelte),
[`+layout.svelte`](../../spa/src/routes/+layout.svelte)):

- Parse `replying` with the same last-`!` split as `echoing`.
- `getReed(authorId, reedId)` / relay `requestReedContent(reedId, authorId, …)`.

Add a shared TS helper `parseSocialRef(raw: string): { authorId, reedId } | null`
in `$lib/types/reed.ts` (or `$lib/utils/socialRef.ts`).

### Server tests

- Happy path: valid markdown + matching sig → 201.
- Wrong sig → 400.
- `id` / `userID` mismatch → 400.
- `replying: bareId` → 400.
- `replying: author!reed` where reed does not exist → 400.
- `echoing` + `replying` both set → 201, both refs parsed.

### Client tests

- `reedAsMarkdown` roundtrip includes normalized `replying`.
- `parseSocialRef` vectors (single `!`, multiple `!` in author id impossible
  today but document last-split behaviour).

## Work items

1. Go: `ParseSocialRef` + `ValidateSocialRefs` (optional target existence).
2. `SignReed`: accept `markdown`, verify user sig, validate headers + refs.
3. SPA: send `markdown` on create; set `replying` to `authorId!reedId`.
4. SPA: shared `parseSocialRef`; update quote + prefetch call sites.
5. Tests as above.

## Risks

- **Breaking API** — any external publisher must send `markdown`. Acceptable
  pre-launch; document in commit / release notes.
- **Target existence check** — strict validation means you cannot reply to a
  reed the server has not countersigned yet (e.g. race right after publish).
  Same-instance ordering makes this rare; client can retry.

## Parallelism

Independent of [02](02_index_and_api.md) schema work until the publish hook
calls the inserter — can develop parsers and client ref format in parallel.
