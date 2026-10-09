# Admin actions 02 — Public ledger

## Status

Proposed.

## Depends on

[01](01_requests_and_approvals.md)

## Context

Authority is paid for with accountability: every closed request is public to
every local member, with its reason. The ledger is not a new store. It is
a read of closed `admin_requests` rows.

## API

| Route | Auth | Returns |
|-------|------|---------|
| `GET /api/ledger` | Any signed-in local member | Closed requests (`executed`, `rejected`, `withdrawn`, `moot`), newest first, keyset cursor on `closed_at` |
| `GET /api/ledger/{requestID}` | Any signed-in local member | One closed entry |

Each entry is the full `AdminRequest` from [01](01_requests_and_approvals.md):
request signature, decision signature and server signature. Pending requests
are **not** in the ledger. They are visible only to admins, through
`/api/admin/requests`.

Rebind entries carry the uploaded public key in `params`. It's a public key,
so it's safe to show.

Broadcast entries carry the message, which is their reason.

Not in the ledger: flags ([08](08_flags.md)) and reserved usernames
([07](07_reserved_usernames.md)).

## Verification (SPA)

`lib/verifiers/adminRequest.ts` verifies an entry before it is shown or
stored:

1. The requester signature over `buildAdminRequestPayload`, against the
   requester's key `keyID`.
2. The decision signature over `buildAdminDecisionPayload`, if there is one.
3. The server signature over `buildAdminServerPayload`, against the server
   key selected by fingerprint.

An entry that fails is dropped and not shown. Roles are not checked here: the
ledger records what happened, and [04](04_kick.md) checks roles where they
matter, on a kick removal.

## SPA

`routes/ledger/+page.svelte`, linked from the side nav for every member:

- One row per entry: action, targets (names, or "removed account"), requester,
  approver, outcome, reason, relative time.
- Filter by action.
- Stored in an IndexedDB `ledger` store after verification and rendered from
  there first (offline-first), then corrected from the network.

## Tests

- Server: pending requests never appear; a non-member gets 401.
- SPA: a tampered reason fails verification and the row is dropped.
