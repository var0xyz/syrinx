# Bans 07 — SPA, banned side

## Status

Proposed.

## Depends on

[03](03_notices.md), [04](04_enforcement_b.md)

## Bans store

- `GET /api/bans` fills a `peerBans` IndexedDB store, keyed by server, on
  sign-in and on every `USER_BANNED` / `USER_UNBANNED`.
- Each certificate is verified before it is stored (`verifyBan` in
  `lib/verifiers/`: both admins' signatures and the banning server's
  countersignature).
- The UI reads the store, never the network, so it renders offline.

## On a reed from a server that banned the user

- One line below the reed: **You're banned from _server_** with an info icon. It
  shows on every reed of that server: timeline, thread, profile.
- Ripples show as usual, without the composer; Bob can delete his own. Reply,
  echo, like and follow stay.
- The mention picker doesn't offer that server's members or its `@admins`.

## Info modal

Opened from the icon:

> **You're banned from _server_**
>
> _server_'s admins banned you on _date_. Their reason: _reason_.
>
> - You can still read, follow and like as usual.
> - You can echo and reply, and your followers will see them, but the author and
>   _server_'s members won't.
> - Your mentions of _server_'s members won't reach them.
> - You can read ripples on _server_'s reeds and delete your own, but not post
>   new ones.
> - _server_'s members can still mention you and reply to you. You can block any
>   of them.
>
> To appeal, contact your server's admins.

The reason comes from the verified certificate.

## Mailbox

- `peer-ban` and `peer-unban` get labels and icons in `MailboxBell.svelte`.
- A `peer-ban` message links to the same modal.

## Tests

- A ban shows the line on that server's reeds only, hides the ripple composer,
  and removes its members from the mention picker.
- A certificate that fails verification isn't stored and shows nothing.
- An unban removes the line.
- The line renders offline from the store.
