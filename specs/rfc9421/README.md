# RFC 9421 — HTTP Message Signatures conformance

This directory specifies what it would take for Syrinx to sign responses
(and, optionally, requests) in conformance with
[RFC 9421](https://www.rfc-editor.org/rfc/rfc9421.html), rather than with
the bespoke PGP envelope it uses today.

**Status: Proposed, not scheduled.** Nothing here is implemented. The
motivating console warning was resolved by renaming the header off RFC
9421's reserved name; see [00_design.md](00_design.md) § "What already
landed". Read [00](00_design.md) before any step — it states the cost and
the case for *not* doing this.

**Blank slate — no migration, no backwards compatibility.** Pre-launch:
server and SPA ship in lockstep. No dual-signing window, no version
negotiation, no old-client support. Any step may change the wire freely.

## Why this is not a simple swap

RFC 9421 does not define a signature format — it defines how to derive
the *bytes being signed* (the "signature base") and carries the result
under a fixed algorithm registry: `rsa-pss-sha512`,
`rsa-pkcs1v15-sha256`, `hmac-sha256`, `ecdsa-p256-sha256`,
`ecdsa-p384-sha384`, `ed25519` (plus JWS algorithms, § 3.3.7).

**OpenPGP is not in that registry**, and Syrinx's entire trust model is
PGP: the server key is a PGP entity, the SPA pins its armor
(`serverKeyTrust.ts`), and the same key material anchors countersignatures
and the key-rotation chain. Conformance therefore forces a second,
non-PGP server key and a second trust path for it — the expensive part,
and the reason this is a spec rather than a patch.

## What conformance does and does not buy

**Does:** interoperability with third-party RFC 9421 verifiers (gateways,
other implementations, non-Syrinx clients); a reviewed canonicalization
with defined answers for repeated headers, list ordering, `@signature-params`
and body digests; a standard replay story via `created`/`expires`/`nonce`.

**Does not:** make browsers verify anything. No browser API validates an
RFC 9421 response signature or gates a response on it. The SPA would keep
verifying in `responseVerifier.ts` exactly as now. If no third party will
ever consume this API, conformance buys canonicalization rigor and
nothing else.

## Steps

| #                                  | Title                                          | Depends on |
|------------------------------------|------------------------------------------------|------------|
| [00](00_design.md)                 | Design, cost, and the case against              | —          |
| [01](01_signature_base.md)         | Signature base + component derivation           | 00         |
| [02](02_content_digest.md)         | `Content-Digest` over the body                  | 01         |
| [03](03_server_signing_key.md)     | Non-PGP server signing key + distribution       | 00         |
| [04](04_server_response_signing.md)| Emit conformant response signatures             | 01, 02, 03 |
| [05](05_client_verification.md)    | WebCrypto verifier in the SPA                   | 03, 04     |
| [06](06_request_signatures.md)     | Optional: request signing (large, separate)     | 01–05      |

Land 00–05 in order. [06](06_request_signatures.md) is independently
deferrable and carries its own trust-model problem (it would move *user*
identity off PGP), so it should not be started without a separate
decision.
