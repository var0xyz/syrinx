# RFC 9421 03 — Non-PGP server signing key + distribution

## Status

Proposed. **This is the expensive step and the real decision point.**

## Depends on

[00](00_design.md)

## Context

RFC 9421's algorithm registry has no OpenPGP entry, so a conformant
response signature cannot be produced by the existing PGP server entity.
This step introduces a second server key and answers the question that
follows immediately: what authenticates it. See [README](README.md).

**Blank slate — no backwards compatibility.**

## Scope

- Mint and store an ed25519 server signing key.
- Bind it to the existing PGP server identity.
- Distribute and pin it in the SPA.
- State its rotation and revocation story.

## Non-goals

- Replacing PGP anywhere else. Records, identity, countersignatures and
  key rotation stay PGP.
- User-side keys ([06](06_request_signatures.md)).

## Design

### Algorithm

`ed25519`. Registered; 32-byte public key, 64-byte signature; supported by
`crypto.subtle` (`Ed25519`) in current browsers and by Go's
`crypto/ed25519`. `ecdsa-p256-sha256` is the fallback if an environment
without Ed25519 WebCrypto must be supported — decide before implementing,
not during.

### The bootstrap problem

The SPA pins the PGP server key today (`serverKeyTrust.ts`:
`setTrustedServerKey` validates and stores armor, deriving a
fingerprint). An ed25519 key needs the same treatment, and answering
"what authenticates the response-signing key?" with "another key the
client also has to trust" merely moves the problem.

**Resolution: bind the ed25519 key to the PGP identity at mint time.**

1. At server init, if no ed25519 signing key exists, generate one.
2. Sign the raw 32-byte public key with the **PGP server key** — a
   detached PGP signature over a fixed binding payload:
   `syrinx-response-key-v1\n<server id>\n<base64 raw public key>`.
3. Store private key, public key and binding signature.
4. Serve all three from the existing server-info / key endpoint.

The SPA, which already pins the PGP key, verifies the binding signature
with it once, then pins the ed25519 key. **No new root of trust is
introduced** — the PGP key remains the single anchor, which is what keeps
this proposal from splitting the trust model in half.

The binding payload must be domain-separated (`syrinx-response-key-v1`)
and include the server id, or a binding signature from one server is
replayable onto another that shares the key — the same class of defect as
RISKS H1.

### Storage

Alongside the existing PGP server key material. The private key is 32
bytes; store it the same way and with the same access controls as the PGP
private key, and hold it to the same standard — it must never be logged
(see the dropped RISKS C3, which was exactly this failure for the PGP
key).

### Rotation and revocation

The PGP key has minting, rotation and revocation machinery. The ed25519
key would start with none, and **that gap must be closed in this step or
explicitly accepted in writing** — an unrotatable signing key is a
liability that grows quietly.

Cheapest sound option: make the ed25519 key *derivable and disposable*.
It signs nothing durable — only in-flight responses — so it needs no
revocation certificates or history. Rotation is: mint a new one, re-sign
the binding with the PGP key, serve it; clients re-pin on the next
`keyid` they do not recognize, verifying the new binding against the PGP
key they already trust.

This works precisely because response signatures are ephemeral. Say so in
the implementation, so a later reader does not mistake the absence of
revocation machinery for an oversight and build it unnecessarily.

### `keyid`

The `keyid` signature parameter is the base64 raw public key, or a hash
of it. Whichever is chosen, it is opaque to the client: the client looks
up a pinned key by exact string match and never parses or reconstructs it
from parts.

## Testing

- Mint → bind → verify-binding roundtrip.
- Negative: binding signature from a different server id rejected.
- Negative: binding signature by a non-server PGP key rejected.
- Rotation: client holding key A re-pins to B on unrecognized `keyid`,
  and rejects B if its binding does not verify.
