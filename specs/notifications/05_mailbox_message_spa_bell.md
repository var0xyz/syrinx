# Notifications 05 — SPA bell + popover

## Status

Proposed.

## Depends on

[04](04_mailbox_message_ws_delivery.md)

## Context

The client side of the mailbox: receive, decrypt, store, ack, and surface
to the user via a bell icon rather than a profile-page section (per the
locked decision in [00](00_glossary_and_design.md)/`README.md`).

## Scope

- Bell icon in the header, right-aligned opposite the logo, visible only
  when logged in; red dot when any stored message is unread.
- Click bell → popover listing stored mailbox messages, newest first.
- Per-message actions in the popover: open (follows `Link` if present,
  marks read), mark as read, delete (local only).
- WS receipt handler: decrypt, store locally, ACK.

## Non-goals

- No reply UI (mailbox is one-way — see [00](00_glossary_and_design.md)).
- No server-side "read" tracking — see [00](00_glossary_and_design.md)'s
  updated non-goal: read/unread and delete are purely local client state
  once a message has been decrypted and stored; the server never learns
  about either.
- No `/mailbox/[id]` (or any) detail route — the popover is the only
  surface; full message content and its `Link` action render inline in
  the popover row, since the plaintext is already local after decrypt.

## Design

### Bell placement

Same `<header>` as the existing logo in the root layout
(`spa/src/routes/+layout.svelte:268-270`:
`<header><h1><a href={headerLink}>💫 Syrinx</a></h1></header>`) — not on
the profile page. The logo stays left; the bell is added right-aligned in
that same header row, same level as the logo. Unlike the logo (which
renders regardless of auth state), the bell only renders when the user is
logged in — a mailbox is inherently per-authenticated-user, and there is
nothing to show or fetch for a signed-out visitor.

Badge is a red dot, shown whenever at least one locally-stored message
has `isRead === false` (see IndexedDB shape below) — not a count, just
presence. Purely local, no server round-trip to compute it beyond the
initial catch-up sync.

### Receipt handler

New `ServerEvent` entry (e.g. `Mailbox = 'MAILBOX'`) alongside the
existing ones in `serverConnection.ts`'s `ServerEvent` enum. Handler
registered in `+layout.svelte` next to the existing `ReedRemoved`/
`AccountRemoved` handlers (`+layout.svelte:170-199`, the same file that
already dispatches server-pushed events into local state):

1. On `MAILBOX` receipt: decrypt `ciphertext` with the user's own private
   key (client-side only, via the existing crypto service — same
   decrypt-with-own-key operation already used for e.g. viewing one's own
   revocation detail during key rotation).
2. On successful decrypt: parse the `MailboxPayload` JSON
   ([03](03_mailbox_message_schema_and_producers.md): `kind`, `message`,
   `link`, `meta`), store locally in a new IndexedDB store — add
   `['mailbox', 'id']` to the `storeNames` list in
   `spa/src/lib/services/db.ts:50-60` (not present there yet) — as
   `{ id, kind, message, link, meta, isRead: false, createdAt }`, then
   send `sendMailboxAck(id)` (mirroring `sendDataAck`/`sendDataInvalid`,
   `serverConnection.ts`). `isRead` always starts `false` regardless of
   `kind`.
3. On failed decrypt: **do not ack**. Log the failure (matching this
   session's fail-closed-and-report convention from the revocation work)
   — the server keeps the row and will redeliver on next catch-up rather
   than the message being silently lost.

### Popover + per-message actions

- Bell click → a popover anchored to the bell icon, reading the local
  `mailbox` IndexedDB store, newest first. No dedicated route: this is
  the only surface for mailbox content, since the plaintext already
  lives locally after step 2 above and there is nothing further to fetch.
- Each row renders `message` (and, if `link` is set, a clickable action
  using it) plus mark-read and delete controls:
  - **Open** (clicking the row, or its `link` action if present): if
    `link` is set, `goto(link)` (SvelteKit navigation, app-relative route
    only — the server never validates this string, so treat it as
    untrusted input and do not `goto` anything that isn't a same-origin
    relative path); either way, sets that row's `isRead = true` in
    IndexedDB.
  - **Mark as read**: sets `isRead = true` locally. No server call — the
    server already has no copy of this message post-ACK.
  - **Delete**: removes the row from the local `mailbox` IndexedDB store
    only. This is distinct from the ACK-triggered server-side delete in
    [04](04_mailbox_message_ws_delivery.md) — that already happened at
    receipt time; this is the user tidying their own local copy.
- **Mark all as read**: a footer control in the popover, shown only when
  at least 2 unread messages are present (a single unread message already
  has its own per-row mark-read action; the bulk control exists to save
  repeated clicks once there's more than one). Sets `isRead = true` on
  every currently unread message locally, same no-server-call semantics
  as the per-message action.
- The bell's red-dot badge recomputes from the store after any of these
  actions (open/mark-read/mark-all-read/delete), same as after a new
  receipt.

## Testing

- Live receipt while online: bell badge lights up, message appears in the
  popover without a page reload.
- Catch-up on reconnect: any messages that arrived while offline appear
  once connected.
- Tampered/corrupted ciphertext: decrypt fails, no ack sent, no entry
  added locally, failure logged.
- Marking a message read clears the badge (once no unread rows remain)
  with no server round-trip.
- Deleting a message removes it from the popover immediately and it does
  not reappear after a page reload (i.e. gone from IndexedDB, not just
  hidden in memory).
- Opening a message with a `link` navigates to that route and marks the
  message read as a side effect.
- A message with no `link` renders with no navigable action, just
  mark-read/delete.
- "Mark all as read" is hidden with 0 or 1 unread messages, appears at 2+,
  and clears the badge and every row's unread state in one action.

## Dependencies

- [04](04_mailbox_message_ws_delivery.md) — the WS message type and ACK protocol
  this consumes.
- IndexedDB store pattern: [`spa/src/lib/services/db.ts`](../../spa/src/lib/services/db.ts).
- Header/layout location: [`spa/src/routes/+layout.svelte`](../../spa/src/routes/+layout.svelte).
- Fail-closed-on-decrypt-failure precedent: this session's revocation
  work, [`09_revocation_fanout.md`](../09_revocation_fanout.md).
