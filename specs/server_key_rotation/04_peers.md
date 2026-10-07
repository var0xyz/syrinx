# Server key rotation 04 — Peers: notify, re-pin, fallback

## Status

Proposed.

## Depends on

[01](01_ops_commands.md), [02](02_revocation_endpoint.md)

## One mechanism: re-pin on an unknown key

Peers pin our key (`servers.key_id`) and refuse requests signed with any other
(`VerifyFederationPeer`). When an established peer signs a request with a key
we haven't pinned, `authenticateAsPeer` calls `updatePeerKey` before refusing:

1. Walk the peer's revocations from the key we pinned
   (`walkRevocationChain`), reading them from the peer's own
   `GET /api/keys/{id}/revocation` and `GET /api/keys/{successor}` at its
   stored base URL, signed with our key, which the peer has pinned.
2. Each revocation must verify with both keys, and each successor's armor must
   match its key ID.
3. **No compromised revocation:** promote each successor into `public_keys`,
   countersigned as at approval, and re-pin `servers.key_id` to the newest.
   Then the request is checked again.
4. **A compromised revocation:** clear the pin, so the peer is refused until an
   admin approves it again with its new key through the existing federation
   handshake (which re-pins). Nothing is re-pinned automatically.

Revocations always come from the peer's stored base URL, never from the
request, so a thief holding the old key can't hand over revocations of their
own. Lookups for the same unknown key are spaced at least 5 minutes apart, so
a burst of requests costs the peer one round.

## Notice

A rotated server tells each peer once, after it starts listening (the peer
calls back for the revocations): `POST /api/federation/relay/server-key`, an empty
request signed with the new key. Authentication on the peer does the
re-pin; the handler only answers 204. `servers.peer_key_ack` (new column,
home side) records the key a peer last accepted, so the notice is sent once
per key and retried on later boots until it succeeds. A peer that misses it
re-pins on the next request it gets anyway.

## Tests

- Rotation: the pin moves to the newest key; the new key is accepted and the
  old one refused.
- Compromise: the pin is cleared and the old key refused.
- A tampered revocation, a successor served under the wrong ID, or
  revocations read as another server's → refused.
- Repeated requests with an unknown key are looked up once.
