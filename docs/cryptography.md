# Cryptography

Syrinx uses **OpenPGP** (ProtonMail `go-crypto` on the server, OpenPGP.js in the SPA) for identity keys, content signatures, and request/response authenticity.

## Keys

- **User keys** — Generated on the client at signup (and on rotation). The private key stays on the device (and in encrypted backups). Public keys are distributed so peers can verify. At signup the client first reserves a user id from `GET /api/users/id`, then mints keys with OpenPGP user id **name** `userID@serverID`, **comment** the server display name, and optional **email**—cosmetic fields only; protocol trust comes from signed identity payloads, not the PGP uid string.
- **Server signing key** — Countersigns identities, keys, revocations, removals, and related records. The private key is wrapped with a passphrase resolved from the OS keychain (or an env var for HA). Operators export/import identity **bundles** for disaster recovery; the bundle password is separate from the server-key passphrase.
- **Key rotation & revocation** — Users can rotate; revocations are signed resources so peers learn that an old key is dead. Requests signed with revoked keys are rejected.

## Canonical signed payload (`canonicalJSON`)

Every signed record is a flat set of named fields serialized as **JSON Canonicalization Scheme** ([RFC 8785](https://www.rfc-editor.org/rfc/rfc8785)) JSON. The server (`canonicalJSON` in `utils.go`, via `github.com/gowebpki/jcs`) and the SPA (`canonicalJSON` in `signing.ts`, via `canonicalize`) produce byte-identical output from the same fields.

```json
{"keyID":"alice@home/k1","reason":"laptop lost","type":"revocation","userID":"alice@home"}
```

Rules that matter:

- JCS fixes everything else: keys sorted by UTF-16 code units, no whitespace, minimal string escaping, ES6 number formatting.
- Empty strings, `null`/`undefined` and empty lists are **dropped** before serializing, so absent and empty sign the same. `false` and `0` stay.
- Free text (bio, reason, note, ripple content) and embedded signatures are ordinary string fields. JSON escaping handles their newlines and quotes, so nothing is base64-wrapped to fit.
- Lists and booleans keep their types: a thread signs `reedIDs` as an array, a server key revocation signs `compromised` as a boolean.
- Timestamps: UTC, RFC3339, second precision, `Z` suffix.
- Every payload has exactly one builder per side (`build*Payload` in `identity.go`, mirrored in `signing.ts`). Signer and verifier both call it; nothing parses a payload back into fields.

Parity is enforced by shared vectors in `src/backend/testdata/canonical_json_vectors.json` (Go `TestCanonicalJSONVectors`, SPA `npm run test:signing`) plus per-payload golden bytes on both sides.

## Detached signatures

- All protocol signatures are **detached PGP** signatures over the exact `canonicalJSON` bytes.
- On the wire: base64 (standard alphabet), not nested base64-of-base64.
- Sign and verify must share one helper so signer and verifier cannot drift.

## User vs server signature storage

User attestations and server countersignatures differ (who signs, whether `signed_at` is required). They live in separate tables (`user_signatures` / `server_signatures`) referenced by entity foreign keys—not a single polymorphic “signatures” dump. Wire format uses nested `userSignature` / `serverSignature` blocks (`fingerprint`, armor; server blocks also carry server id and timestamp).

Verification is pushed down so invalid material is not stored: repositories supply verifiers to persistence `put` paths without turning the DB layer into a crypto library.

## Content relay encryption

Reed content signatures are the one exception to "the server verifies before it accepts": the server never receives content, so it cannot check the author's signature over it—only receiving peers do, once they fetch and decrypt a body. The server's countersignature still covers the author's signature *bytes*, closing the swap attack this asymmetry would otherwise open. See [Content privacy](/content_privacy) for the full mechanism and relay's own asymmetric encryption (distinct from these detached signatures).

## HTTP response signing

API responses can be signed by middleware: the complete response (canonical headers + body) is signed with the server key; the client can verify via a signature header (e.g. `X-Syrinx-Signature`). Headers are sorted into a canonical string before signing. This proves the **HTTP response** came from the server key—not a substitute for verifying resource-level user/server blocks on stored entities.

## Two-round flows

Some operations need server-minted fields (timestamps, IDs) inside a payload the **user** must sign: profile update, key rotation, revocation. Those use an **init → complete** handshake with short-lived pending state so the client can sign authoritative fields without inventing them.

Signup is different: **`GET /api/users/id`** reserves and signs a user id up front (ephemeral, no DB row), then **`POST /api/users/signup`** verifies that signature via `userIDFingerprint` before accepting the new account. See [Identity — Signup](/identity#signup).

## What users should remember

- Protect the device key and backups.
- A strong passphrase matters because offline attacks on wrapped key material do not get the API’s rate limits.
- Peers trust **signatures**, not the server’s reputation alone.
