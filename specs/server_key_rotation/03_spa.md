# Server key rotation 03 — SPA: adopt successors, refuse compromised keys

## Status

Implemented.

## Depends on

[02](02_revocation_endpoint.md)

## Updating the trusted key

When `/api/server/info` rejects the stored key (401) and a user is signed in:

1. `updateTrustedServerKey` (`serverKeyRotation.ts`) fetches the stored key's
   revocation (`GET /keys/{id}/revocation`) and its successor
   (`GET /keys/{successor}`), user-signed. It skips their response signature
   check for now: they are signed by a key not trusted yet.
2. The revocation must name the stored key, verify with both keys, and its
   successor's armor must match its ID. Repeat from the successor until a key
   has no revocation (at most 10 steps; more falls back to the key gate).
3. If every step passes and none is compromised, check every response's
   signature against the last key, store it as trusted
   (`setTrustedServerKey`), cache each key with its state in `publicKeys`, and
   refresh server info. No prompt.
4. Otherwise show the key gate with the existing "rejected" error. If a step
   reached a compromised revocation, the error says the old key was
   compromised, gives the operator's reason, and notes that the old key signed
   the new one, so the user confirms the new key out-of-band. The revoked
   keys' state is still cached (the revocations verified), but the successor
   is not.

No signed-in user (fresh device, signup) → the key gate, as today.

## Refusing a compromised key

`verify()` (`lib/services/verify.ts`) rejects a server signature whose key is
compromised when the signed timestamp is at or after its `revokedAt`. Key
state comes from the verified revocations, which update the cached keys, or
from `GET /keys/{id}` for a key not cached yet.

## Tests

- Two rotations: a client on the first key reaches the current one with no
  prompt.
- A revocation with a broken signature, one naming another key, or a
  compromised revocation → key gate, nothing adopted.
- A countersignature by a compromised key timestamped after its revocation
  fails verification; one before it passes.
