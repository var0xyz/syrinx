# Server key rotation 00 — Design, key states, trust model

## Status

Proposed.

## Today

`ProcessRevocations` (`services.go`, called from `main.go` before
`InitServerKey`) reads `revocations/<fingerprint>.rvk`, marks that key revoked
and deletes the file. `InitServerKey` then finds no usable key and mints one
that signs only itself. Nothing links the new key to the old one, so every
client hits the key gate and every peer, whose pin no longer matches
(`VerifyFederationPeer`), silently stops accepting the server.

## Key states

The same terms as user keys:

| State                   | Its past signatures                           | Successor adopted automatically  |
|-------------------------|-----------------------------------------------|----------------------------------|
| current                 | valid                                         | —                                |
| revoked                 | valid                                         | yes                              |
| revoked, compromised    | valid only if timestamped before the revocation | no                             |

`private_keys.revoked_at` marks a key revoked. Whether it was compromised, and
why, is part of the signed revocation and stored only there. A compromised key
also backs nothing new: no server-key proof, no peer request, no key update.

## Revocation record

Every key after the first is introduced by the revocation of its predecessor,
signed by **both** keys: the revoked key proves the handover, the successor
proves possession. Like a user key revocation, the reason is the content:

```
compromised: true | false
keyID:       <revoked key ID>
serverID:    <server ID>
signedAt:    <RFC3339 UTC>
successor:   <new key ID>
type:        server-key-revocation
---
<reason; may be empty unless compromised>
```

Built once with `bytesToSign` (`buildServerKeyRevocationPayload`, mirrored in
SPA `signing.ts`). The revocations link keys into a **chain** from the first
key to the current one.

## Trust model

- A client or peer trusts a key it was given out-of-band (the key gate, or
  federation approval).
- It may move to a successor **only** along a revocation that verifies with
  both signatures against the key it already trusts and is not compromised,
  and on through further such revocations.
- A compromised revocation never moves anyone. It is shown as evidence ("the
  old key signed this successor") while the successor is confirmed
  out-of-band, because whoever stole the old key can sign a competing
  successor.
- A signature by a compromised key counts only if its signed timestamp is
  before the revocation. The key signs its own timestamps, so a thief can
  backdate; this is accepted ([`RISKS.md`](../../RISKS.md)).
