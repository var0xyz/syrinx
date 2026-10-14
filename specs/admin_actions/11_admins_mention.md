# Admin actions 11 — The @admins mention

## Status

Implemented.

## Depends on

[09](09_broadcasts.md)

## Context

A member reads a message from the admins in their mailbox, but has no way
to answer it. Writing `@admins` in a reed mentions every admin of a server
at once, so that is how a member answers. The mailbox labels admin messages
"Message from @admins" as the hint.

## Content

The composer writes the mention as `~!admins@serverID`. `!` is outside the
user ID alphabet, so the token can never name a user. It renders as
`@admins`, followed by the server's name when it isn't the viewer's own.

The `@` picker offers `admins` for this server and for each peer listed in
`/server/info`, so a member can reach the admins of another server.
`admins` is in `reservedUsernames` (`constants.go`), so no member can
take the name.

## Server

- A publish claims `!admins@serverID` like any mention. It is kept when the
  server is this one or a peer, and stored in `reed_admin_mentions`: one
  row per reed and server, never one row per admin.
- New-reed deliveries to peers carry the claim in `mentions`. The peer named
  stores its own row and tells its admins, so the reed crosses once per peer.
- Admins of the server named get an `ADMIN_MENTION`: the reed's ciphertext,
  relayed like a `MENTION`. An online admin gets it on publish; an offline
  one on their next connect, for every such reed they don't hold yet.
- The author never gets it. An admin the reed also mentions by name gets
  the `MENTION` alone.

```sql
CREATE TABLE IF NOT EXISTS reed_admin_mentions (
  mentioning_reed_id VARCHAR(255) NOT NULL REFERENCES reed_identities(id) ON DELETE CASCADE,
  server_id VARCHAR(255) NOT NULL REFERENCES servers(id) ON DELETE CASCADE,
  created_at TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP,
  PRIMARY KEY (mentioning_reed_id, server_id)
);
```

## SPA

On `ADMIN_MENTION` the client verifies and stores the reed, then keeps it as
a mention only when the viewer is an admin or root, and the reed (or a part
of its thread) contains `@admins` for the viewer's server. Anything else is
dropped.

## Tests

- Claim parsing (`adminsMentionServer`).
- Missing admin mentions: listed for an admin and for root, not for a member
  or the author, and not for a reed naming another server's admins.
- `admins` is taken, in any case.
