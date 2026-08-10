# Notifications 05 — SPA bell + `/mailbox/[id]` detail

## Status

Proposed.

## Depends on

[04](04_mailbox_message_ws_delivery.md)

## Context

The client side of the mailbox: receive, decrypt, store, ack, and surface
to the user via a bell icon rather than a profile-page section (per the
locked decision in [00](00_glossary_and_design.md)/`README.md`).

## Scope

- Bell icon in the top bar, unread count/dot.
- Click bell → list of mailbox messages.
- Click a message → `/mailbox/[messageID]` detail route.
- WS receipt handler: decrypt, store locally, ACK.

## Non-goals

- No reply UI (mailbox is one-way — see [00](00_glossary_and_design.md)).
- No server-side "read" tracking — see `README.md`'s locked decisions;
  read/unread is purely local client state once a message has been
  decrypted and stored.

## Design

### Bell placement

Next to the existing title in the root layout header
(`spa/src/routes/+layout.svelte:230-232`:
`<header><h1><a href={headerLink}>💫 Syrinx</a></h1></header>`) — the bell
sits in this same `<header>`, not on the profile page. Badge reflects the
count of locally-stored, not-yet-viewed mailbox messages (see Non-goals —
purely local, no server round-trip to compute it beyond the initial
sync).

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
   ([03](03_mailbox_message_schema_and_producers.md)), store locally in a new
   IndexedDB store (e.g. `['mailbox', 'id']`, following the existing
   `storeNames` registration pattern in `spa/src/lib/services/db.ts:36-46`),
   then send `sendMailboxAck(id)` (mirroring `sendDataAck`/
   `sendDataInvalid`, `serverConnection.ts`).
3. On failed decrypt: **do not ack**. Log the failure (matching this
   session's fail-closed-and-report convention from the revocation work)
   — the server keeps the row and will redeliver on next catch-up rather
   than the message being silently lost.

### List + detail routes

- Bell click → a list view (could be a dropdown or a dedicated route,
  implementation's call) reading the local `mailbox` IndexedDB store,
  newest first.
- Clicking an item navigates to `/mailbox/[messageID]`, a detail page
  rendering the decrypted `message`/`meta` for that stored entry — no
  server round-trip needed at this point, since the plaintext already
  lives locally after step 2 above.

## Testing

- Live receipt while online: bell badge increments, message appears in
  list without a page reload.
- Catch-up on reconnect: any messages that arrived while offline appear
  once connected.
- Tampered/corrupted ciphertext: decrypt fails, no ack sent, no entry
  added locally, failure logged.
- Detail route renders the correct message content for its `messageID`
  and 404s (or equivalent) for an unknown one.

## Dependencies

- [04](04_mailbox_message_ws_delivery.md) — the WS message type and ACK protocol
  this consumes.
- IndexedDB store pattern: [`spa/src/lib/services/db.ts`](../../spa/src/lib/services/db.ts).
- Header/layout location: [`spa/src/routes/+layout.svelte`](../../spa/src/routes/+layout.svelte).
- Fail-closed-on-decrypt-failure precedent: this session's revocation
  work, [`09_revocation_fanout.md`](../09_revocation_fanout.md).
