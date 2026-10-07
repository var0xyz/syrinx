# Key revocation events 04 — SPA: apply revocations, cache keys without re-checks

## Status

Proposed.

## Depends on

[01](01_relay_key.md), [02](02_revocation_push.md), [03](03_federation.md)

## Applying a revocation

On `KEY_REVOKED`:

1. Verify the revocation (`verifyKeyRevocation`) and store it in
   `revocations`.
2. Mark the cached key revoked.
3. Drop every local reed signed with that key whose server timestamp is at or
   after the revocation's, using the existing time-relative check
   (`isKeyValidAt`). Content signed before stays.
4. `DATA_ACK`.

## Caching keys

- `resolvePublicKey` and `resolvePublicKeyArmor` use the cached key whenever
  there is one, and fetch only on a miss.
- Remove `keyCheckThrottle.ts` and its `shouldRecheck` / `markChecked` calls.
- A fetched key already marked revoked: `isKeyValidAt` fetches its revocation
  record once, to read its timestamp, unless it is cached in `revocations`. It
  never fetches the successor or any other key.

## Tests

- Publishing a reed fetches no key.
- A revocation drops content signed after it and keeps content signed before.
- A forged revocation (bad signature) changes nothing.
