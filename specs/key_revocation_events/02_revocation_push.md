# Key revocation events 02 — `KEY_REVOKED` push and catch-up

## Status

Implemented.

## Depends on

[00](00_design.md)

## Key allocations

Like `reed_allocations` for reeds, the server records which of its users have
a user key cached:

```sql
CREATE TABLE IF NOT EXISTS public_key_allocations (
  user_id      VARCHAR(255) NOT NULL REFERENCES users(id) ON DELETE CASCADE,
  key_id       VARCHAR(255) NOT NULL REFERENCES public_keys(id) ON DELETE CASCADE,
  allocated_at TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP,
  revoked      BOOLEAN NOT NULL DEFAULT FALSE,  -- set by a peer's notice, see 03
  PRIMARY KEY (user_id, key_id)
);
```

- `GET /keys/{id}` allocates the key to the requesting user when it is a user
  key that isn't revoked and isn't their own. Clients cache keys only through
  that endpoint.
- A key that is already revoked is not allocated: the response marks it
  revoked, the client fetches its revocation (`GET /keys/{id}/revocation`)
  then and there, and has nothing left to be told. It fetches no successor or
  other key.

## Message

`KEY_REVOKED` (server → client): `request_id` and the signed `KeyRevocation`
(the wire shape `GET /keys/{id}/revocation` already returns). The client
answers `DATA_ACK` once it has verified and applied it.

## Fanout and catch-up

On a user key revocation, once it is committed: `AddPublicKey` (a key
rotation, the only place a user key is revoked) broadcasts `realtimeKeyRevoked`
to the realtime service.

- **Owed:** every user with the revoked key allocated.
- **Live:** online users with it allocated get `KEY_REVOKED` right away.
- **Catch-up:** on `SYNC_REQUEST`, a user gets `KEY_REVOKED` for each revoked
  key they still have allocated.
- **`DATA_ACK`** deletes that user's allocation of the revoked key, so the
  debt clears itself and nothing accumulates, as `reed_allocations` does for
  reed removals.
- **Peers:** [03](03_federation.md).

## Tests

- Fetching a user key allocates it; fetching a revoked key, or one's own,
  doesn't.
- A user with the key allocated gets `KEY_REVOKED`; one without it doesn't.
- An offline user gets it on catch-up, and not again after ACK.
