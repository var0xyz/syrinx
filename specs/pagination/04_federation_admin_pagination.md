# Pagination 04 — Federation admin list/log pagination

## Status

Implemented (scoped down — see Implementation).

## Depends on

[00](00_audit.md) (convention), [01](01_deduplication.md) (shared helpers)

## Context

Four admin-gated federation endpoints return their full result set in one
shot, uncapped (see [00](00_audit.md)):

| Endpoint | Handler | Order |
|---|---|---|
| `GET /federation/invitations` | `ListFederationInvitations`, `handlers.go:3744` | `ORDER BY created_at DESC`, no LIMIT |
| `GET /federation/servers` | `ListFederationServers`, `handlers.go:3809` | `ORDER BY created_at DESC`, no LIMIT |
| `GET /federation/list` (invitations+attempts+servers) | `GetFederationList`, `handlers.go:4072` | calls the above plus `ListFederationAttempts`, `ORDER BY fa.created_at DESC`, no LIMIT |
| `GET /federation/servers/{id}/logs`, `GET /federation/attempts/{id}/logs` | `handlers.go:3887`, `4160` (`listFederationLog` helper, `services.go:4105-4128`) | `ORDER BY fl.created_at ASC`, no LIMIT |

All four already have a usable `ORDER BY`, matching the shape
chorus/replies/following/followers had before pagination — adding a
keyset predicate is mechanical using `paginateRows`/`clampLimit`/
`parseLimitParam`/`parseBeforeTimeParam` from [01](01_deduplication.md).

Every one of these is admin-only (`isAdmin` gated), so this is lower
urgency than [02](02_user_search_cursor.md) — except the two log
endpoints (`GetFederationServerLogs`,
`GetFederationAttemptLogs`), which are genuinely unbounded: a long-lived
peer connection's log can grow indefinitely with no cap at all, unlike
the three entity-list endpoints which are naturally bounded by how many
peers/invitations/attempts an operator has.

## Scope

- Add `limit`/`before` to all four endpoints, following the
  [00](00_audit.md) convention (`before` = RFC3339 `created_at` of the
  oldest/newest row seen, depending on each endpoint's existing sort
  direction).
- Prioritize the two log endpoints — they're the only genuinely unbounded
  case in this step.
- `GetFederationList`'s combined response will need `hasMore` per
  sub-list (invitations/attempts/servers), not one shared flag, since
  each sub-query paginates independently.
- Frontend: `spa/src/routes/mesh/+page.svelte` (`apiService.listFederation()`)
  and the per-peer/per-attempt log pages
  (`mesh/peer/[serverId]/+page.svelte`, `mesh/attempt/[attemptId]/+page.svelte`,
  currently raw `<pre>` text blobs) adopt pagination — infinite scroll is
  acceptable per the [00](00_audit.md) scroll-behavior rule (informational
  admin lists, not content feeds). The shared `createPaginationStore`
  ([01](01_deduplication.md)) applies here.

## Non-goals

- Any change to who can call these endpoints (`isAdmin` gating stays as-is).
- Redesigning the log format itself (plain text vs. structured) — out of
  scope, purely a pagination addition.

## Implementation

Scoped down to the two log endpoints only, matching this doc's own
priority note above — the 3 entity-list endpoints (invitations, servers,
attempts/`GetFederationList`) are admin-only and naturally bounded by
operator peer count, so they were left unpaginated.

The `limit`/`before` cursor convention doesn't fit here at all: both log
endpoints serve **plain text** (`Content-Type: text/plain`), rendered
verbatim in a single `<pre>` block on the frontend
(`mesh/peer/[serverId]/+page.svelte`, `mesh/attempt/[attemptId]/+page.svelte`)
with no per-line JSON structure to carry a `hasMore`/`nextCursor` field.
Switching these to JSON would be a materially bigger, more disruptive
wire-format change than the actual risk warranted.

Instead, `listFederationLog` (`services.go`, shared by
`ListFederationServerLogs`/`ListFederationAttemptLogs`/
`ListFederationInvitationLogs`) now caps at `federationLogLineCap = 500`
most-recent lines, still returned oldest-first for chronological display
— a subquery selects the most recent N by `created_at DESC LIMIT`, then
re-sorts that page `ASC`. No cursor, no "load more" — if that's ever
needed, it's a separate, larger change (switching these to a real JSON
list endpoint). `GetFederationServerLogs` concatenates attempt-logs +
server-logs from two separate capped calls, so its combined output can
reach ~1000 lines in the worst case — still bounded, just not a single
uniform cap.

Frontend: unchanged. `spa/src/routes/mesh/+page.svelte`,
`mesh/peer/[serverId]/+page.svelte`, and `mesh/attempt/[attemptId]/+page.svelte`
keep fetching the full (now server-side-capped) response in one call, no
pagination UI added.

Covered by `TestListFederationLog_CapsAtMostRecentLines`
(`federation_test.go`): inserts `federationLogLineCap + 10` log lines
with distinct timestamps, asserts exactly `federationLogLineCap` come
back, the oldest-kept line is the correct one (not an arbitrary cut), and
ordering stays ascending.
