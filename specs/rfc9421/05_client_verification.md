# RFC 9421 05 — WebCrypto verifier in the SPA

## Status

Proposed.

## Depends on

[03](03_server_signing_key.md), [04](04_server_response_signing.md)

## Context

Rewrites `verifyResponseEnvelope` (`responseVerifier.ts`) to verify an
RFC 9421 signature with `crypto.subtle` instead of an OpenPGP detached
signature. See [README](README.md).

**Blank slate — no backwards compatibility.**

## Scope

- Parse `Signature-Input` / `Signature`.
- Rebuild the signature base and verify with the pinned ed25519 key.
- Verify `Content-Digest` against the received body.
- Pin and re-pin the response key.

## Non-goals

- Removing OpenPGP.js. It still verifies records, identity,
  countersignatures and revocations.

## Design

### Verification order

Fail closed at every step. Return `false`, never throw, matching current
behavior:

1. Read `Signature-Input` and `Signature`; both present, or reject.
2. Parse the label, covered-component list and params.
3. **Reject unless the covered set is exactly the expected set**
   (`@status`, `content-type`, `content-digest`). A verifier that accepts
   whatever the server says it covered provides no guarantee — an
   attacker who can set headers picks an empty set and passes. This is
   the central check of the whole step.
4. Check `created`/`expires` against local time with the existing
   `VERIFY_CLOCK_SKEW_MS` tolerance.
5. Resolve `keyid` against the pinned response key; on an unrecognized
   `keyid`, re-pin per [03](03_server_signing_key.md) (fetch key +
   binding, verify binding with the pinned **PGP** key) before
   continuing. Never trust a key delivered by the response it signs.
6. Rebuild the signature base from the actual response.
7. `crypto.subtle.verify('Ed25519', key, sig, base)`.
8. Independently hash the body and compare to `Content-Digest`
   ([02](02_content_digest.md)).

Steps 7 and 8 are both required. Neither alone authenticates the body.

### Key import

`crypto.subtle.importKey('raw', <32 bytes>, {name:'Ed25519'}, false, ['verify'])`.
Import once and cache in memory; `extractable: false` on principle, though
a public key needs no protection.

Feature-detect Ed25519 at startup. If unavailable, the app must fail
visibly rather than silently skipping verification — a verifier that
quietly returns `true` when it cannot verify is worse than none. This is
the same failure mode as RISKS M8 (`verifySignature` silently falling
back binary→text).

### What is deleted

- The `X-Syrinx-Response-Signature` / `X-Syrinx-Signed-Headers` reads.
- `buildCanonicalHeaderString` in `responseVerifier.ts`, replaced by the
  shared signature-base builder from [01](01_signature_base.md).
- `cryptoService.verifyStrippedSignature` — as of this writing
  `responseVerifier.ts` is its only caller, so it becomes dead with this
  step. Re-grep before deleting in case one has been added.

### Trust bootstrap interaction

The SPA gates startup on having a trusted server key. That gate now needs
both the PGP key (records) and the ed25519 key (responses). The ed25519
key can only be fetched over a response that itself needs verifying, so
the first fetch must be exempt — exactly as the PGP key bootstrap is
today. Mirror that existing exemption rather than inventing a second
path, and keep the exemption as narrow as it currently is.

## Testing

- Golden vectors from [04](04_server_response_signing.md) verify.
- Negative: truncated covered set rejected (step 3).
- Negative: expired `created`/`expires` rejected.
- Negative: valid signature, mutated body → digest mismatch.
- Negative: unknown `keyid` with an unverifiable binding → rejected, not
  silently re-pinned.
- Ed25519 unsupported → visible failure, never a pass.
