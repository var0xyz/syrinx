# Invites 05 — SPA invite management (toolbar + page)

## Status

Implemented (toolbar Invites tab; `/invites` management page; create/
list/revoke client; one-time share URL panel; profile `invitedBy`
display).

## Depends on

[02](02_lifecycle_api.md), [03](03_signup_consume.md)

## Context

Authenticated users need a durable place to create invite links, copy them,
see who claimed them, and revoke unused ones. The bottom toolbar is the
primary navigation chrome
([`BottomToolbar.svelte`](../../../spa/src/lib/components/BottomToolbar.svelte));
invites get a first-class tab that is **always** visible regardless of
`signupMode`.

## Scope

- Add an Invites tab to the bottom toolbar (Reeds / Feed / **Invites** /
  Profile — order: place Invites between Feed and Profile unless design
  review prefers otherwise).
- New route [`spa/src/routes/invites/+page.svelte`](../../../spa/src/routes/invites/+page.svelte)
  behind existing `Auth` gate.
- API client methods: `createInvite`, `listInvites`, `revokeInvite`
  (and reuse check if needed).
- Create button:
  - Enabled when `signupMode` is `open` or `invite`, and under quota.
  - Disabled when `closed`, with an on-page explanation (“Signups are
    closed on this server — you can’t create new invites.”).
  - Disabled when at quota, with explanation including the cap from
    `maxInvitesPerUser`.
- List: status badges; for `claimed`, show claimant username (link to
  `/profile/{id}` if that route exists —
  [`profile/[userId]`](../../../spa/src/routes/profile/[userId]/+page.svelte)).
- Copy-to-clipboard for share URL using existing `CopyButton` where
  possible. Share URL =
  `${window.location.origin}/signup?invite=${token}` — **only available
  immediately after create** (token not re-fetchable). After create, show
  the fresh link once in a prominent confirmation; subsequent list rows
  for pending invites show revoke only (no token) — user must create a
  new invite if they lost the link.
- Revoke control on `pending` rows.
- Profile page: when `user.invitedBy` is non-null, show “Invited by
  @{username}” (link to inviter profile). Applies to own profile and
  others’.
- Toolbar `currentPage` prop extended with `'invites'`.

## Non-goals

- Offline queue / IndexedDB for invites.
- Re-displaying raw tokens for old pending invites (impossible without
  storing plaintext; do not add a plaintext column).
- Email share sheet integrations.
- Hiding the toolbar tab based on mode.

## Design

### Why token is shown only once

The server stores only `token_hash`. The create response is the sole time
the SPA receives the secret. UI copy should say so (“Copy this link now —
you won’t be able to see it again”).

Pending rows in the list are still useful: they prove an unused invite
exists and can be revoked if the link may have leaked. They cannot be
re-copied.

### Create flow

1. `POST /api/invites`
2. On 201: show modal/panel with link + Copy button; refresh list.
3. On 403 closed / limit: show server/mode message; do not open modal.

### Quota display

If `maxInvitesPerUser === -1`, do not show a fraction. Else show
`{createdCount} / {max}` (createdCount = list length, since quota is
all-time including claimed/revoked).

### Auth / info

Page loads `server/info` for mode + max (or cache from a small store if one
is introduced; do not require a new global store — local fetch is fine).

### Empty states

- No invites yet + can create: prompt to create first invite.
- No invites + closed: only the closed explanation.

## Test plan

- [x] Toolbar shows Invites on reeds/feeds/profile/invites pages; active
      state works
- [x] Create in `open` / `invite` → token panel; list gains pending row
- [x] After create, reload list does not expose token
- [x] Revoke pending → status revoked; check endpoint false
- [x] Claimed invite shows claimant username
- [x] `closed`: create disabled + message; list still loads
- [x] At quota: create disabled + message
- [x] Profile shows `invitedBy` after redeem signup (e2e or component test)
