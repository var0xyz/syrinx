# Pagination 03 — Invites list endpoint

## Status

Proposed.

## Depends on

[00](00_audit.md) (convention)

## Context

Unlike the other gaps in [00](00_audit.md), this isn't a missing cursor —
it's a missing endpoint. `invites/handlers.go` only exposes `Create`,
`Check` (validity), `Status` (single invite by ID), and `RevokeInvite`.
There is no "list my invites" route at all. A user with several
outstanding invites has no server-side way to enumerate them; the SPA's
`invites/+page.svelte` currently works around this by reading
`invitesRepository.getAll()` from local IndexedDB
(`spa/src/lib/repositories/invites.ts:19-24`), which only shows invites
this device has ever seen created — not a true list from the server's
point of view.

`CountByCreator` already exists (referenced at `invites/handlers.go:182`
for the invite-limit check) but is a count, not a list.

## Scope

- Add `GET /invites` (or similar), scoped to the authenticated caller's
  own invites, returning the same shape `Status` returns per-item.
- Given `MAX_INVITES_PER_USER` bounds the result size per user (see
  [`invites/README.md`](../invites/README.md) step 00), a single
  unpaginated page is likely sufficient — no cursor needed unless quotas
  grow substantially. If a cursor is added anyway for consistency, follow
  the [00](00_audit.md) `limit`/`before` convention using
  `paginateRows`/`clampLimit` from [01](01_deduplication.md).
- Frontend: replace or supplement `invitesRepository.getAll()`'s
  IndexedDB-only view with a real server fetch on the invites page, so a
  second device shows the same list.

## Non-goals

- Increasing or redesigning `MAX_INVITES_PER_USER` — that's an existing,
  separate quota decision (see [`invites/README.md`](../invites/README.md)).
