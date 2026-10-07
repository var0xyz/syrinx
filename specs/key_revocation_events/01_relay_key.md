# Key revocation events 01 — Relay requests name the key to encrypt to

## Status

Implemented.

## Depends on

[00](00_design.md)

## Scope

- `RelayRequestMessage` replaces `requester_id` with `requester_key_id`
  (same field number): the key's owner is the requester, so the holder reads
  who asked from the key itself. `newRelayRequestMsg` (`realtime.go`) fills it
  with the requester's `users.active_key_id`.
- **Foreign requesters:** the federation relay request carries
  `requester_key_id`, set by the requester's home server; it is stored in
  `foreign_relay_requests` (new column) next to `requesting_user_id` and used
  when the request is dispatched to a holder. The receiving server checks the
  key belongs to the requesting user (same owner prefix) and to the calling
  peer.
- **Holder** (`encryptReedForRequester`): encrypts to `requester_key_id`, and
  takes the requester from the key's owner. It
  no longer calls `GET /users/{id}/info`. It still refuses when a vouch names
  a different key for the requester, and resolves the armor from its cache,
  fetching it once on a miss.
- A request without a key ID is answered with `RELAY_ERROR`.

## Tests

- A local relay request carries the requester's active key.
- A foreign relay request carries the key its home server sent; a key owned by
  another user, or by a user of another server, is refused.
- The holder encrypts to the named key without fetching `/info`.
