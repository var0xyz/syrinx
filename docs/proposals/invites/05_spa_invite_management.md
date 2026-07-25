# Invites 05 — SPA invite management (toolbar + local store)

## Status

Implemented (toolbar Invites tab; `/invites` page; signed create + IndexedDB;
pending status refresh; revoke; profile `invitedBy`).

## Depends on

[02](02_lifecycle_api.md), [03](03_signup_consume.md)

## Context

Authenticated users create invite links on-device, keep signed invite records
in IndexedDB, refresh only **pending** statuses from the server, and revoke
unused invites. No server list API.

## Scope

- Toolbar Invites tab (always visible).
- Route [`spa/src/routes/invites/+page.svelte`](../../../spa/src/routes/invites/+page.svelte).
- Client: mint id + token; sign `invite-user`; `POST /api/invites`; verify +
  put to IndexedDB (`invites` store, db v22).
- On page load: for each local invite with `status === 'pending'`,
  `GET /api/invites/{id}` and merge unsigned status; clear local `token` when
  terminal.
- Create disabled when `closed` or at quota.
- Pending rows can re-copy share URL from local `token`.
- Profile `invitedBy` display (unchanged).

## Non-goals

- Server-side invite list.
- Signing status / claimedBy into the invite attestation.
- Hiding the toolbar tab based on mode.

## Design

### Create flow

1. Client generates `id`, `token`, `createdAt`.
2. Sign `buildInviteUserPayload(serverID, userID, id, createdAt)`.
3. `POST /api/invites` → verify nested sigs → `invitesRepository.put`.
4. Show share URL (also retained locally while pending).

### Quota

`{localCount} / {max}` using IndexedDB length (all-time local creates).
Server still enforces all-time count independently.

## Test plan

- [x] Toolbar shows Invites; route exists; auth gate
- [x] Create path uses `createSignedInvite` / local store (static checks)
