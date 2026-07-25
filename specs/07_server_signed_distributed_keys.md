# Proposal 07 — Server-signed client keys on distribution

## Status

Implemented (persist + distribute server countersignature on user public
keys; SPA verifies before caching).

## Context

Clients fetch other users' public keys from the server in order to
verify author signatures on reeds and identity records. Today
`GetPublicKey` returns `{fingerprint, armor, createdAt, revoked?}` with
**no server attestation** that this armor is the key this server holds
for that `(userID, fingerprint)`.

A hostile or compromised path that can rewrite HTTP bodies (or a buggy
cache) could substitute a different public key under the same
fingerprint label. Fingerprints are derived from key material, so a
naive swap of armor under a mismatched fingerprint is detectable if the
client re-derives the fingerprint — but the server is still the only
party asserting "this is the key registered to user U." Binding
`(userID, fingerprint, armor)` under a server countersignature makes
that assertion independently verifiable against the server's known
signing key.

## Scope

- On every path that **persists** a user public key (`Signup`'s first
  key insert, `AddPublicKey` rotation), produce a **server
  countersignature** over a canonical key-attestation payload and store
  it with the key row.
- On every path that **distributes** a user public key
  (`GET /users/{userID}/keys/{fingerprint}`, and any other response that
  embeds a full key), return that countersignature (and the server key
  fingerprint that produced it) alongside the armor.
- Clients verify the countersignature against the server's public key
  **before** caching the key in IndexedDB.

## Non-goals

- Signed revocations remain Proposal 06 (fanout: [09](09_revocation_fanout.md)).
  This proposal attests the key
  material and its binding to `userID`; revocation state may appear on
  the wire for convenience but the authoritative signed revocation is
  06's artifact.
- No change to how clients generate or self-sign keys at signup.
- No recovery report-back of keys (later unit of work).

## Design

### Signed key-attestation record

Uses the shared `BytesToSign` envelope (see
[`README.md — Shared conventions`](./README.md)).

Headers:

- `fingerprint: <keyFingerprint>`
- `serverID: <serverID>`
- `serverKeyFingerprint: <active server key fingerprint at attestation time>`
- `signedAt: <RFC3339 UTC seconds, server-authoritative>`
- `userID: <owner userID>`

Content: the armored public key **verbatim** (black box — no
normalization).

Signed by the server's active signing key. The attestation is produced
at insert time and stored; distribution returns the stored signature
rather than re-signing on every read (stable artifact, cheaper reads,
recovery-friendly).

### Storage

Add columns on `user_keys` (names suggestive):

- `server_signature TEXT` — base64 of the server's detached PGP
  signature over the key-attestation payload
- `server_fingerprint VARCHAR(255)` — which server key produced it
- `server_signed_at TIMESTAMP` — the `signedAt` that was signed

All three populated together in the same transaction as the key insert
(`Signup` and `AddPublicKey`).

### Wire shape

Extend the existing public-key response:

```json
{
  "fingerprint": "...",
  "armor": "-----BEGIN PGP PUBLIC KEY BLOCK-----...",
  "createdAt": "...",
  "revoked": { "reason": "...", "timestamp": "...", "successor": "..." },
  "server": {
    "id": "<serverID>",
    "fingerprint": "<server key that signed>",
    "timestamp": "<signedAt>",
    "algorithm": "PGP+base64",
    "signature": "<base64 detached sig>"
  }
}
```

`server` reuses the same block shape as identity records and reeds.

### Client rule

Before `publicKeyRepository.put` / `setRevoked`:

1. Rebuild the key-attestation bytes from the response fields.
2. Verify `server.signature` against the known server public key for
   `server.fingerprint`.
3. Re-derive the fingerprint from `armor` and require it equals
   `fingerprint` (defense against armor/label mismatch).
4. On failure: do not cache; log a detailed error (surface to the user
   later).

Armor remains a black box — compare and store bytes exactly as
received; never normalize.

### Interaction with revocation / successor

- When `AddPublicKey` writes `successor` on the predecessor's
  revocation row, the **predecessor's** key-attestation signature does
  not need to change (it still correctly attests the old armor).
- Clients learn revocation via Proposal 06/10’s signed resource, Proposal
  09’s fanout (when landed), and/or the `revoked` field on fetch;
  verifying the key attestation and verifying the revocation are separate
  checks.

## Work items

1. Schema: `server_signature`, `server_fingerprint`, `server_signed_at`
   on `user_keys` (+ idempotent `ALTER` if needed).
2. `Signup` and `AddPublicKey`: after inserting the key row, build
   payload via `BytesToSign`, countersign, persist the three columns in
   the same transaction.
3. `GetPublicKey`: return the `server` block.
4. SPA: verify before cache; reject on failure.
5. Tests: round-trip verify; tampered armor fails; tampered userID in
   reconstructed headers fails; fingerprint mismatch with armor fails.

## Testing

- Unit + integration on the attestation helper.
- e2e: fetch another user's key → verify → cached; mutate armor in a
  mock response → client refuses to cache.

## Risks

- **Historical keys without attestation.** Pre-launch we can require
  every row to be attested; no dual-read path.
- **Server key rotation.** Attestations carry `serverKeyFingerprint`;
  verifiers must select the matching historical server public key
  (same pattern as reed/identity countersigs).

## Dependencies

- **Requires Proposal 01's `BytesToSign` helper.**
- Independent of 02–06 for the server half; the client verify step is
  consumed by Proposal 08 /
  [signatures 09](signatures/09_verify_server_countersignatures.md).

## Parallelism

- Can proceed once 01 exists.
- Coordinate with 06 if both touch `GetPublicKey`'s wire shape in the
  same window.
