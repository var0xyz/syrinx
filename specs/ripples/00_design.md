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
Ripples need the exact opposite trust and storage model, so this step exists
to lock that contrast explicitly before any schema or API work starts.

## Scope

- Define what a ripple is: unsigned, server-stored, thread-scoped reply
  object attached to exactly one reed.
- Define the thread/expiry model precisely enough to derive a schema from it.
- Define the render-gating rule relative to parent-reed visibility.
- Decide moderation surface (can the reed author or ripple author delete a
  ripple) since that's a modeling question, not just an API detail.

## Non-goals

See [README](README.md) Non-goals — this step doesn't re-derive those, only
locks the shape needed by 01–04.

## Design

### What a ripple is

A ripple is a short unsigned text reply, authenticated by the poster's
signature-auth session, attached to a **thread**. A thread belongs to
exactly one reed (the "parent reed") and is created lazily on the first
ripple posted against that reed.

Contrast with a reed:

| | Reed | Ripple |
|---|---|---|
| Signed | Yes (`BytesToSign` + detached PGP sig + server countersignature) | No |
| Stored body | Not retained server-side (see [conversations/00](../conversations/00_design.md)) | Stored server-side (only copy) |
| Propagation | p2p, held by peers, counted in coverage | Server-only, never leaves this instance |
| Lifetime | Permanent until signed removal | 7 days from last thread activity, unattended |
| Identity of author | Cryptographic (signature verifies key ownership) | Session-based (login identifies the account) |

### Thread shape

Flat, single-level. A thread is: one root reed reference + an ordered list
of ripples, each with an author, content, and posted-at timestamp. A ripple
may optionally reference another ripple in the same thread as
`inReplyToRippleID` for **display-only** @-style addressing (rendering
"replying to @user" inline) — this is not a tree; every ripple still lists
flat in the thread in chronological order, and `inReplyToRippleID` has no
effect on expiry, ordering, or visibility. If the referenced ripple has
already expired (thread-wide expiry means this only happens across separate
threads, which can't happen — same thread only) or was moderation-deleted,
render "replying to a deleted comment" instead of resolving it.

Rationale for flat-only: reed replies already give this codebase a full
recursive-thread model
([`conversations/02_index_and_api.md`](../conversations/02_index_and_api.md)'s
`reed_threads`/`reed_replies`). Duplicating that machinery for a
throwaway, unsigned side-channel is not worth the complexity — ripples are
meant to feel like a lightweight comment box, not a second conversation
system.

### Lifetime / expiry rule (locked)

- A thread has a single `expires_at` timestamp = `last_activity_at + 7 days`.
- `last_activity_at` updates to `now()` on: thread creation (first ripple)
  and every subsequent ripple in that thread.
- Editing is not supported (see Non-goals), so nothing else bumps activity.
- When `expires_at` passes, the **entire thread** — every ripple in it —
  is deleted. There is no per-ripple expiry independent of the thread.
- Deleting is destructive (hard delete), consistent with "the server is the
  only copy, and unsigned content carries no durability promise."

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

**Locked for v1: only the ripple's own author can delete their ripple.**
The parent reed's author has no special ripple-moderation power in this
spec. Rationale: giving reed authors delete power over other people's
ripples is a real feature (moderation) with its own abuse surface (silent
censorship of criticism) that deserves its own design pass, not a
default bolted on here. Revisit in a follow-up step if requested.

A self-deleted ripple is a hard delete of that one row; it does **not**
delete the whole thread and does **not** count as "activity" for the
7-day clock (deleting isn't posting). If it was the last ripple in the
thread and the thread's `expires_at` was already in the past at scan time,
the sweep removes the (now-empty-or-not) thread on its own schedule — no
special-case cleanup needed since the sweep operates on `expires_at`, not
on emptiness.

### Content constraints

Reuse the existing reed visible-length constant as the ripple cap:
`MAX_REED_VISIBLE_CHARS = 140` (from
`spa/src/lib/utils/reedContent.ts`) — same constraint, same reasoning
(short-form, terse). No markdown grammar — ripples render as **plain
text** (no `reedMarkdown.ts` parsing, no mentions, no hashtags, no links).
Keeping ripples markdown-free avoids re-litigating the entire mention/link
security surface for a feature that's deleted in a week anyway; plain text
is escaped/rendered as-is by the SPA.

### Realtime delivery shape (contract only — full design in 03)

New `BroadcastType` `RIPPLE_POSTED`, delivered via the existing
`ConnectionManager.SendToReedSubscribers(authorUserID, reedID, payload)`
primitive (`realtime/connection_manager.go:409`) — the same fanout used for
reed-scoped realtime today. Payload carries the new ripple only (not the
whole thread) so subscribed clients append rather than refetch.

## Risks

- **Session-only auth for ripples is weaker than a reed's cryptographic
  guarantee** — accepted deliberately; ripples are explicitly not meant to
  carry the same trust weight as a reed. Documented here so it's never
  mistaken for an oversight later.
- **No moderation by reed author in v1** — a reed author cannot remove a
  hostile ripple on their own post; they can be revisited once the sweep +
  posting flow are stable. Flagged, not solved, here.

## Dependencies

None — this is the design lock other steps build from.

## Parallelism

None; 01 depends on this.
