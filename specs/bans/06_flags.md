# Bans 06 — Flags reach the reporter's server too

## Status

Proposed.

## Depends on

[Admin actions 08](../admin_actions/08_flags.md)

## Context

Today a flag on a foreign reed is relayed to the reed's home server and only its
admins see it. The reporter's own admins never learn that one of their members
reported a foreign user's reed, which is what they need to decide on a ban.

## Change

- The reporter's server keeps a copy of a flag on a foreign reed, stored once
  the home server answers with the countersigned flag. A flag the home server
  refuses isn't kept.
- `reed_flags` and `flag_dismissals` hold flags on foreign reeds too; "only
  reeds authored on this server" is dropped from 08.
- Each server dismisses on its own. A dismissal on one never closes the flag on
  the other.
- A withdrawal by the reporter reaches both, as it reaches the home server
  today.
- The admin flags list shows foreign reeds next to local ones, marked with their
  server. Visibility rules are unchanged; an author on another server is a
  normal user.

## Tests

- A member's flag on a foreign reed shows on both servers' flags lists.
- A dismissal on one server leaves the flag open on the other.
- A withdrawal removes it from both.
- A flag the home server refuses isn't stored on the reporter's server.
