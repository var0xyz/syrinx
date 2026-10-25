# Bans 08 — SPA, admin side

## Status

Proposed.

## Depends on

[01](01_requests.md), [03](03_notices.md)

## On A: banning

- `AdminActionsModal` offers **Ban** on a foreign user's profile to an admin
  holding `user.kick`, and **Unban** when the user is banned. Both need a
  reason, like every request.
- `AdminRequestList` shows `ban` and `unban` requests with the other actions.
- A **Banned users** list: `GET /api/admin/bans`, every ban this server made,
  active and lifted, with the user's server, the reasons and when. A user's
  earlier bans show next to a new one.

## On B: bans of our users

- A **Peer bans** page: `GET /api/admin/peer-bans`, every ban peers made of our
  users, each row the user, the banning server, the reason and when, and for a
  lifted ban the unban's reason and time.
- Rows are verified like Bob's certificates before they show.

## Docs

- `docs/admins.md`: Ban in the actions table; "Reach into another server" no
  longer says every admin action stays on the admin's instance without
  qualification.
- `docs/architecture.md` (federation): ban notices and the snapshot.

## Tests

- Ban shows only on a foreign user's profile and only with `user.kick`; Unban
  replaces it once banned.
- The Banned users list and the Peer bans page show a ban, and the lifted one
  with both reasons; a second ban of the same user shows with the first.
