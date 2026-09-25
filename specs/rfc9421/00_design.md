# RFC 9421 00 — Design, cost, and the case against

## Status

Proposed. Not scheduled. This document exists so the decision is made
deliberately rather than by momentum.

## Depends on

—

## Context

Every response is signed by `responseSigner` (`src/backend/middlewares.go`)
and verified by the SPA (`src/frontend/src/lib/services/responseVerifier.ts`).
The scheme is sound but bespoke. See [README](README.md).

**Blank slate — no backwards compatibility.** Server and SPA ship
together; any step may change the wire.

### What already landed

The bare `Signature` response header was renamed to
`X-Syrinx-Response-Signature`. That name is RFC 9421's, so browsers tried
to parse the PGP envelope as an HTTP Message Signature, found no
`Signature-Input` declaring what was covered, and warned on every request.

**That warning is gone, and it was the only concrete symptom.** Nothing
below is required to keep it gone. Anything here is undertaken for
interoperability, not to fix a defect.

## The current scheme, stated precisely

Per response, `responseSigner`:

1. Sets `Content-Length` when a body was buffered (framing only — excluded
   from coverage).
2. Builds a canonical header string: every response header except
   `X-Syrinx-Response-Signature`, `X-Syrinx-Signed-Headers` and
   `Content-Length`, names sorted, each header's values sorted and joined
   with `", "`, lines joined with `\n`.
3. Concatenates `headers + "\n\n" + body`.
4. Detached-signs with the server's PGP key (`cryptoService.sign`,
   `openpgp.DetachSign`), strips armor delimiters, escapes newlines as
   `\n` literals.
5. Emits `X-Syrinx-Response-Signature`, `X-Syrinx-Signature-Scope: body`,
   and `X-Syrinx-Signed-Headers` (the covered names, so the client does
   not guess when a proxy adds headers).

The SPA mirrors step 2 from `X-Syrinx-Signed-Headers` and verifies against
the pinned server armor.

This is an honest design. Its real weaknesses are canonicalization edge
cases — sorting values within a header changes semantics for
order-sensitive headers, and `Headers.get()` cannot distinguish separate
`Add` calls from one pre-joined value (already noted in
`responseVerifier.ts`). RFC 9421 has reviewed answers for both.

## What conformance actually requires

### 1. A non-PGP signing key

RFC 9421's algorithm registry has no OpenPGP entry, and a conformant
verifier selects its algorithm from `Signature-Input`. A PGP signature
under a made-up `alg` label is not conformant — it is the current scheme
with more ceremony, and strictly worse for the browser, which would then
reject on an unknown algorithm instead of merely warning.

So conformance means a second server key (`ed25519` is the natural pick:
small, fast, WebCrypto-supported) alongside the PGP entity, which must
stay for countersignatures and rotation. Consequences:

- **Two server keys with different lifecycles.** The PGP key already has
  minting, rotation and revocation machinery. The new one needs its own,
  or an explicit statement that it is not rotatable — which is its own
  risk.
- **A second trust path.** `serverKeyTrust.ts` pins PGP armor. An ed25519
  public key needs its own distribution and pinning, and the bootstrap
  question "what authenticates the key that authenticates responses?"
  must be answered again. Binding it to the PGP identity (PGP-sign the
  ed25519 key at mint, verify that once at pin time) is the cheapest
  sound answer and is specified in [03](03_server_signing_key.md).
- **A split trust model.** Responses would be ed25519; everything else
  stays PGP. Reviewers must hold both.

### 2. A different verifier in the SPA

`cryptoService.verifyStrippedSignature` is OpenPGP.js. Conformant
verification is `crypto.subtle.verify` over the signature base. Both
paths must coexist while PGP still covers records.

### 3. Canonicalization rewritten, not adapted

Step 2 above is replaced wholesale by § 2.1–2.5 of the RFC: component
identifiers (`@status`, `@method`, `@target-uri`), structured field
serialization, and `@signature-params`. Covered *content* is unchanged;
covered *bytes* are entirely different. See [01](01_signature_base.md).

### 4. Body coverage via `Content-Digest`

RFC 9421 signs headers, not bodies. Body integrity comes from RFC 9530
`Content-Digest`, which is then covered by the signature. See
[02](02_content_digest.md).

## Cost

| Area | Change |
|------|--------|
| Server key management | New key type, mint, store, distribute, bind to PGP identity |
| Server signing | Replace canonicalization + signer in `responseSigner` |
| Client trust | Second pinned key, new bootstrap validation |
| Client verification | WebCrypto verifier beside the OpenPGP one |
| Tests | Signature-base vectors; both verifiers; bootstrap |

No user-visible behavior changes. No known defect is fixed.

## Recommendation

**Do not implement this for its own sake.** The trigger to revisit is a
concrete consumer: a third party, a gateway, or a non-Syrinx client that
needs to verify Syrinx responses without an OpenPGP stack. Absent that,
the cost buys canonicalization rigor that the current scheme could also
get — far more cheaply — by tightening its own rules:

- Stop sorting values *within* a header; preserve order.
- Cover an explicit body digest rather than concatenating raw bytes.
- Cover `@status`, so a signature cannot be replayed across status codes.

Those three are worth doing whether or not RFC 9421 ever lands, and they
do not touch the trust model. They are deliberately **not** steps in this
set, because they are cheap enough to do directly and would otherwise be
held hostage to a large migration.

## Non-goals

- Request signing (see [06](06_request_signatures.md); separate decision).
- Replacing PGP for records, identity, countersignatures or rotation.
- Any compatibility window between the current and conformant schemes.
