# Blocking 08 — SPA, blocking side: block action, outbox, blocked list

## Status

Proposed.

## Depends on

[02](02_api.md)

## Block action

On `/profile/[userId]`, for anyone but the viewer: **Block** (or
**Unblock** when the viewer has blocked them). Same control on the blocked
profile page ([07](07_spa_blocked.md)), so a blocked user can block back.

Blocking always opens a confirmation dialog first:

```
  Block alice@b.example?

  • They'll be told you blocked them.
  • They'll be forced to delete everything of yours their device holds,
    and to unfollow you.
  • They won't be able to see your profile or reeds, or follow you again.
  • You'll still see theirs.

  Unblocking later won't restore any of this: they won't follow you again
  and their copies of your content won't come back.

                                  [Cancel]  [Block]
```

Nothing is signed or queued until **Block** is confirmed. Unblocking needs
no confirmation.

## Offline-first outboxes

Mirrors likes ([likes 05](../likes/05_spa_pending_and_button.md)):

- `pendingBlocks`, keyed by blocked ID, holding the signed payload. Signed
  on confirm, so the action survives a reload; sent to
  `POST /users/{id}/block`; on 200 the verified certificate goes to
  `blocks` and the pending row is deleted.
- `pendingUnblocks`, keyed by blocked ID, no signature; sent as
  `DELETE /users/{id}/block`; on 204 the `blocks` entry and the pending row
  are deleted.
- A block and an unblock of the same user cancel each other while both are
  still pending.
- Drained on reconnect and startup.

`blocks` (keyed by blocked ID) holds the viewer's own verified
certificates, so the button state and blocked list render offline.

## Blocked list

`/account/blocked`, linked from `/account`: the viewer's blocks, newest
first, each with **Unblock**. Rendered from `blocks`; `GET /blocks`
reconciles in the background (adds missing, drops ones the server no longer
has, e.g. lifted from another session before a device bind).

## What blocking does not do on the blocking user's device

Nothing of the blocked user is deleted, and the blocking user's follow of them is
untouched: the block is about what the blocked user sees.

## Tests

- **Block** opens the dialog; **Cancel** signs and queues nothing.
- Block offline → pending row survives reload, sent on reconnect, button
  stays **Unblock** throughout.
- Block then unblock while offline → nothing sent.
- Blocked list matches `GET /blocks` after reconcile.
