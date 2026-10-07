# Key revocation events 03 — Revocations across peers

## Status

Implemented.

## Depends on

[02](02_revocation_push.md)

## Allocations across servers

A user on a peer fetches a foreign key through its own server, which proxies
`GET /keys/{id}` to the key's home server. Both sides record it:

- **The peer** checks the home server's countersignature on the key it got
  back, caches it in `public_keys` (as it already does for keys it needs to
  verify), and allocates it to its user in `public_key_allocations`, same
  rules as for a local key: not revoked, not their own. A key that fails
  verification is not passed on.
- **The home server** records that the peer holds the key:

```sql
CREATE TABLE IF NOT EXISTS public_key_server_allocations (
  server_id    VARCHAR(16) NOT NULL REFERENCES servers(id) ON DELETE CASCADE,
  key_id       VARCHAR(255) NOT NULL REFERENCES public_keys(id) ON DELETE CASCADE,
  allocated_at TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP,
  PRIMARY KEY (server_id, key_id)
);
```

## Home server

After a revocation, one `POST /api/federation/relay/key-revocation` per peer
with the key allocated, carrying the signed revocation. On 2xx the peer's
allocation is deleted: the peer now owes its own users. A peer that didn't
accept it is retried when this server boots and when that peer announces its
own boot, until it does, so nothing accumulates.

## Receiving peer

- The revoked key must belong to a user of the calling peer.
- Verify the revocation: the key's own signature (the key is fetched from the
  home server if not cached) and the calling peer's countersignature (its
  pinned key, or the key named by the signature if it rotated since).
- Flag its users' allocations of the key `revoked`; the revocation record
  stays on the home server, the source of truth. Push `KEY_REVOKED` to those
  users who are online.
- Catch-up and ACK as in [02](02_revocation_push.md): a user with a revoked
  allocation is owed `KEY_REVOKED`, with the revocation fetched from the home
  server (`GET /api/keys/{id}/revocation`); the ACK deletes the allocation.

## Tests

- A key fetched through a peer is cached and allocated to the local user.
- A revocation flags the peer's allocations; a tampered one, one countersigned
  by someone else, or one of another server's key is refused and flags nothing.
- The home server sends one notice per peer with the key allocated and drops
  the allocation once accepted; a refused notice stays owed.
