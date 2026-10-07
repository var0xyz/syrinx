# Server key rotation 02 — Revocations through `/keys` and server-side enforcement

## Status

Implemented.

## Depends on

[01](01_ops_commands.md)

## Same endpoints as user keys

No endpoint specific to server keys. Whoever trusts an old server key reaches
the current one through the endpoints user keys already have:

- `GET /keys/{id}/revocation` serves a server key's revocation too, tagged so
  it can't be read as a user key's:

```json
{
  "type": "server-key-revocation",
  "serverID": "...",
  "keyID": "<revoked key>",
  "successor": "<successor key ID>",
  "compromised": false,
  "reason": "",
  "signedAt": "...",
  "signature": "<by the revoked key>",
  "successorSignature": "<by the successor>"
}
```

  404 when the key isn't revoked, as for user keys.
- `GET /keys/{id}` serves the successor's public key.

A client walks from the key it trusts: fetch its revocation, verify it with the
trusted key and the successor's key, adopt the successor if not compromised,
and repeat until a key has no revocation. Usually that is one step.

Both endpoints take signed requests, so only registered users and established
peers can read them.

## Enforcement

- `serverKeyProofMiddleware` stays as it is: only the **current** fingerprint
  passes. A revoked key opens no unauthenticated route, so the old key can't
  be used to sign up.
- `GET /keys/{id}` keeps serving revoked server keys (old countersignatures
  are verified by fingerprint), and adds `revokedAt` and `compromised` so
  verifiers can apply the timestamp rule.
- Server-side verifiers of this server's own past countersignatures (recovery
  paths in `recovery.go`) reject a compromised key's signature timestamped at
  or after `revoked_at`.

## Tests

- `GET /keys/{id}/revocation` serves a revoked server key's revocation, and 404
  for the current key.
- Signup with a revoked key's fingerprint as proof → 401.
- Recovery verification of a compromised key's post-revocation
  countersignature → rejected.
