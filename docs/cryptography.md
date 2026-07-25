# Cryptography

Syrinx uses **OpenPGP** (ProtonMail `go-crypto` on the server, OpenPGP.js in the SPA) for identity keys, content signatures, and request/response authenticity.

## Keys

- **User keys** — Generated on the client at signup. The private key stays on the device (and in encrypted backups). Public keys are distributed so peers can verify.
- **Server signing key** — Countersigns identities, keys, revocations, removals, and related records. The private key is wrapped with a passphrase resolved from the OS keychain (or an env var for HA). Operators export/import identity **bundles** for disaster recovery; the bundle password is separate from the server-key passphrase.
- **Key rotation & revocation** — Users can rotate; revocations are signed resources so peers learn that an old key is dead. Requests signed with revoked keys are rejected.

## Canonical signed envelope (`BytesToSign`)

Signed records share one envelope format on server and client. The helper produces **opaque signing input**—not a document meant to be parsed back.

```
---
<sortedKey>: <value>
<sortedKey>: <value>
...
---
<content>
```

Rules that matter:

- Header keys sorted ASCII byte-lexicographically (Go `sort.Strings` / default JS sort for ASCII keys).
- One header per line: `key: value` (colon-space), LF only (never CRLF).
- Empty-string values: **omit the whole header line.** Absent and empty are equivalent.
- Opening and closing `---` on their own lines.
- Content appended verbatim after the closing `---\n`. No extra trailing newline from the helper.
- Timestamps in headers: UTC, RFC3339, second precision, `Z` suffix.
- **No escaping.** Values are inserted verbatim.

### Why nothing is escaped

The envelope’s only job is a **deterministic byte sequence** both sides can reproduce from the same fields. Signed records travel as structured data (`headers`, `content`, `signature`). The receiver re-runs `BytesToSign` and verifies. Nobody splits the envelope on newlines to recover fields—so literal `\n`, `:`, or `---` inside a value cannot “break parsing,” because there is no parse step.

Adding an escape table would invent a second contract both implementations must share, plus a decode path nobody needs. A future “hardening” that adds escapes would **silently break** signature compatibility.

Helpers return `[]byte` / `Uint8Array` to signal: this is signing input, not prose.

## Detached signatures

- All protocol signatures are **detached PGP** signatures over the exact `BytesToSign` bytes.
- On the wire: base64 (standard alphabet), not nested base64-of-base64.
- Sign and verify must share one helper so signer and verifier cannot drift.

## User vs server signature storage

User attestations and server countersignatures differ (who signs, whether `signed_at` is required). They live in separate tables (`user_signatures` / `server_signatures`) referenced by entity foreign keys—not a single polymorphic “signatures” dump. Wire format uses nested `userSignature` / `serverSignature` blocks (`fingerprint`, armor; server blocks also carry server id and timestamp).

Verification is pushed down so invalid material is not stored: repositories supply verifiers to persistence `put` paths without turning the DB layer into a crypto library.

## HTTP response signing

API responses can be signed by middleware: the complete response (canonical headers + body) is signed with the server key; the client can verify via a signature header (e.g. `X-Syrinx-Signature`). Headers are sorted into a canonical string before signing. This proves the **HTTP response** came from the server key—not a substitute for verifying resource-level user/server blocks on stored entities.

## Two-round flows

Some operations need server-minted fields (timestamps, IDs) inside a payload the **user** must sign: signup, profile update, key rotation, revocation. Those use an **init → complete** handshake with short-lived pending state so the client can sign authoritative fields without inventing them.

## What users should remember

- Protect the device key and backups.
- A strong passphrase matters because offline attacks on wrapped key material do not get the API’s rate limits.
- Peers trust **signatures**, not the server’s reputation alone.
