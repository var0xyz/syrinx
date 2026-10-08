# Key revocation events 00 — Design and trust model

## Status

Implemented.

## Today

[Proposal 09](../09_revocation_fanout.md) rejected pushing revocations and
made clients find them instead:

- `resolvePublicKey` / `resolvePublicKeyArmor` (`lib/verifiers/index.ts`)
  re-fetch a cached key once its 60-second `sessionStorage` stamp
  (`keyCheckThrottle.ts`) expires, to see whether it was revoked. A client
  re-fetches even its own key every time it stores a reed it just published.
- A holder answering a relay request works out the requester's key itself:
  `encryptReedForRequester` (`lib/services/relayDecrypt.ts`) reads
  `activeKeyID` from `GET /users/{id}/info`, an unsigned hint
  ([RISKS.md](../../RISKS.md) M9), then resolves it through the same
  re-check.

That makes every client responsible for key freshness, and costs a request
per author per minute of activity.

## New model

1. **The server names the key.** `RELAY_REQUEST` carries the key the holder
   must encrypt to: the requester's active key, as their home server knows it.
2. **Clients cache keys.** A key is fetched once, verified, stored, and used
   from then on. No timers.
3. **Revocations come to the client.** When a user key is revoked, its home
   server sends `KEY_REVOKED`, carrying the signed revocation, to every local
   user who has that key cached, and one notice to each peer whose users
   fetched it. Offline users get it on catch-up.
4. **Clients apply it.** They verify the revocation, mark the cached key
   revoked from its timestamp, and drop content the key signed at or after it.

## Who is told

- **Locally:** users who have the key cached. The server records each user
  key it serves to a user (`public_key_allocations`), the way
  `reed_allocations` records reeds. A key fetched after it was revoked is
  never allocated: the client learns of the revocation on that fetch.
- **Peers:** each peer that fetched the key for one of its users, once. The
  peer records its own users' allocations, and tells those.

## Trust model

- The pushed revocation is the existing signed resource (`KeyRevocation`:
  the revoked key's signature plus the home server's countersignature), so a
  client verifies it rather than trusting the push.
- A relay request's key ID comes from the server, as `activeKeyID` does
  today, but no longer through an unsigned profile field the holder fetches
  itself. Vouch contradictions still refuse a key, as now.
- **Accepted:** between a key's compromise and its revocation, clients trust
  it. The re-check never closed that window either; it only noticed a
  revocation sooner.
