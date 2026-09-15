# Content privacy 01 — `SignReed` drops content

## Status

Implemented.

## Depends on

[00](00_design.md)

## Context

`SignReed` used to receive `content` to verify the author's signature,
extract tags/mentions, and check length. None of that is possible or
needed anymore — see [00](00_design.md) for why the signature is still
submitted despite being unverifiable server-side.

## Wire change

`POST /reeds` form fields:

| Field | Before | After |
|---|---|---|
| `reedID` | required | unchanged |
| `signature` | required, verified against content | required, stored/countersigned, unverified |
| `content` | optional | **removed** |
| `echoing` / `replying` / `previousID` | optional, structural | unchanged |
| `tags` | — | **new**, repeated: author-claimed hashtags (no `#`), used only for pipe fanout |
| `mentions` | — | **new**, repeated: author-claimed `userID@serverID` tokens, drives the mention inbox ([03](03_mention_inbox.md)) |

## What changed in `handlers.go` / `services.go`

- Content-based checks removed: `ReedContentWithinLimits`, `ExtractTags`,
  `ReedAsMarkdown` + `VerifySignature`, `MarkdownService` (dead, deleted
  entirely — nothing else called it).
- Echo/reply target blankness checks (`IsBlankEcho`) removed — the
  server's own code already called this "weak trust theater" before this
  change (a client could always misreport blank status); it's now simply
  gone rather than patched for a content-free world. `is_blank` on
  `reed_echoes` defaults to `false` at insert; receiving clients derive
  the real answer from content they actually decrypt.
- `ExtractMentions(content, authorID)` replaced by
  `ValidateMentionClaims(claims, authorID)` (`mentions.go`) — format-only
  validation of the claimed `mentions` field, same dedupe/self-mention
  rules, no regex over content.
- Claimed `tags` normalized server-side (`normalizeClaimedTags`:
  lowercase, trim, dedupe) as defense-in-depth even though the SPA is
  expected to submit already-normalized tags.
- `ReedPublished` metric drops `RawChars`/`VisibleChars` (nothing to
  measure); `ReedRejectedLength` metric removed entirely.

## Self-mention bug fixed along the way

`ValidateMentionClaims`'s self-mention check previously compared a mention
claim's bare `userID` against the authenticated caller's id — but that id
is canonical (`userID@serverID`), so `"alice" == "alice@myserver"` was
always false and self-mentions were never actually filtered. Pre-existing,
unrelated to this migration, fixed here since the function was already
being rewritten: the comparison now uses the claim's `CanonicalAuthorID()`
against the canonical `authorID`.

## Acceptance

- `POST /reeds` never contains prose content in the request body.
- The server still requires and stores `signature`, still countersigns
  the same payload shape as before.
- Structural checks (echo/reply ref parsing, thread resolution, tip/fork
  check, uniqueness) are unchanged.
- Claimed `tags`/`mentions` are stored as claims; nothing downstream
  trusts them without independent client verification.
