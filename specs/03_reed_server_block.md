# Proposal 03 — reed `server` block (bind `reedID`, `authorID`, `fingerprint`)

## Status

Implemented (`ReedCountersignHeaders` binds `reedID` / `authorID` /
server-key `fingerprint`; nested `serverSignature` on the wire).

## Context

The reed countersignature today binds the algorithm, `serverID`, timestamp,
and the user signature. It does **not** bind:

- **`reedID`** — so a genuine `(userSignature, serverSignature)` pair from
  reed `Z` could be replayed at recovery time as if it belonged to reed `X`.
- **`authorID`** — same class of replay across authors.
- The **server signing-key fingerprint** — so a verifier that must choose
  among a full key history (recovery) cannot pick the right key without
  side-channel info.

Proposal 01 fixes the *signer/verifier canonical-form mismatch* without
changing what is signed. This proposal changes **what** is signed.

## Scope

- Extend the countersigned payload to include `reedID`, `authorID`, and the
  server signing-key `fingerprint`.
- Extend the wire format so the client stores and later re-submits
  `fingerprint` alongside `userSignature` and `serverSignature`.
- Verifier: require `server.id == serverID` and select the server key by
  `fingerprint`.
- No versioning gymnastics: we are pre-launch, so we ship the new format
  as *the* format. No parallel support for any older shape.

## Non-goals

- No recovery endpoints. This proposal only makes reeds recovery-*capable*.
- No changes to how the client signs reed content with the user key.
- No changes to `reeds` table columns beyond what is already there
  (`private_key_fingerprint` already exists and continues to be the FK into
  `private_keys`).

## Design

### Canonical publication date

The `timestamp` in the countersigned payload is **the server's**, minted in
`SignReed` as `time.Now().UTC().Truncate(time.Second)`, persisted as
`reeds.signed_at`, and returned to the client in the `Signature` response
(where the SPA stores it as `server.timestamp`). It is the **canonical
publication date** for a reed. Every timestamp-driven surface (feed
ordering, "posted 3 minutes ago", detail-page date) must derive from this
value.

We also **drop the user-controlled `timestamp` header** from the reed
markdown. The prior client shape set a `timestamp` inside the user-signed
body (`new Date().toISOString()`) and rendered it in the UI —
but nothing verified it, nothing sorted by it server-side, and having two
timestamps invited drift between "what the user claims" and "what the
server witnessed". With the server timestamp bound into the countersigned
envelope, the user-side one is redundant and slightly harmful (a client
could set it to any value while still producing a valid user signature
over the rest of the body). It is removed rather than kept-and-ignored.

Reeds that are still queued locally (`unsignedReeds`) have no server
timestamp yet; the UI shows a "Pending…" placeholder until the
countersignature lands and `server.timestamp` is populated.

### Canonical payload

Uses the shared `BytesToSign` envelope helper introduced in Proposal 01
(see [`README.md — Shared conventions`](./README.md)). Headers:

- `algorithm: PGP+base64`
- `authorID: <authorID>`
- `fingerprint: <serverKeyFingerprintHex>`
- `id: <serverID>`
- `reedID: <reedID>`
- `timestamp: <RFC3339 UTC seconds>`

Content: `<userSignatureBase64>`.

`BytesToSign` sorts the headers ASCII byte-lex, so the produced envelope is:

```
---
algorithm: PGP+base64
authorID: <authorID>
fingerprint: <serverKeyFingerprintHex>
id: <serverID>
reedID: <reedID>
timestamp: <RFC3339 UTC seconds>
---
<userSignatureBase64>
```

No hand-composed strings anywhere — the helper is the single source of
truth for the byte sequence. All rules (no escaping, LF only, empty-value
headers omitted, no trailing newline) inherit from `BytesToSign`.

### Why bind `fingerprint` *inside* the signed payload

At first glance the fingerprint looks like signature metadata rather than
payload: if the client submits the wrong fingerprint, the verifier picks
the wrong key and the check fails anyway — so what does covering it with
the signature add?

The distinction matters because the fingerprint is not descriptive, it is
a **selector**: the verifier uses it to choose *which* public key to
check against, out of the full server key history. If the selector is
unsigned, the property the verifier can prove degrades from
"the server (identified by this fingerprint) signed these bytes" to
"*some* key the submitter pointed us at signed these bytes." That is a
categorically weaker statement, and it opens two concrete failure modes:

1. **Key-substitution / confusion.** An attacker who possesses any valid
   `(payload, serverSig)` pair and controls an entry in the key set (a
   past compromised key, a wrongly-enrolled key, a homograph fingerprint
   in a UI) can submit `(payload, serverSig, attackerFingerprint)`. The
   verifier now runs the check against the attacker-chosen key. Even
   when that check fails, the *identity* the verifier was asked to
   attribute the signature to was decided by unauthenticated data. In
   the recovery path (Proposal 08+), which walks a *history* of server
   keys to find one that verifies, an unsigned selector is exactly the
   knob an attacker would want.
2. **Ambiguity across key rotations.** With multiple historical server
   keys present, "this signature verifies under *some* key we know about"
   is not the assertion we want to make about a reed. We want
   "this reed was countersigned by the key with fingerprint F, at time
   T." That is only true if `F` is inside the bytes `F` signed.

This is the same reason JWS puts `kid` inside the protected header rather
than alongside it: the industry learned the hard way (algorithm/key
confusion CVEs) that letting the submitter choose the verification key
via unsigned metadata is a footgun. Binding `fingerprint` makes the
server's assertion self-describing: "I, the key with this fingerprint,
signed this," rather than "somebody signed this; trust the sidecar hint
about who."

The cost is negligible — one extra header in an envelope we are already
building — and the property gained (the signer's own identity is covered
by the signature) is exactly what recovery and any future third-party
verifier need.

`reedID` and `authorID` are bound for a related-but-distinct reason
(cross-context replay prevention, spelled out under "Context" above);
`fingerprint` is bound for **key-selection integrity**. Together they
turn a bag of bytes plus two signatures into a self-contained,
self-describing statement about *which reed*, *by which author*,
*witnessed by which server key*.

### Wire format

`SignReed` response (`Signature` struct) grows a `Fingerprint` field:

```go
type Signature struct {
    ID          string    `json:"id"`
    Fingerprint string    `json:"fingerprint"`
    Timestamp   time.Time `json:"timestamp"`
    Algorithm   string    `json:"algorithm"`
    Signature   string    `json:"signature"`
}
```

Client persists `fingerprint` in its IndexedDB reed record. During recovery
Phase 2 it will submit `{reedID, authorID, userSignature, serverSignature,
serverFingerprint}`; today it only needs to store it.

### ~~`VerifySignature` (`/reeds/{userID}/{reedID}/verify`)~~ — removed

This HTTP endpoint was removed. Verification is client-side only:

- SPA `verifyReed` rebuilds the canonical payload and selects the server key by
  `fingerprint` before IndexedDB write.
- Recovery uses inline `verifyReedCountersig` in `recovery/handlers.go`.
- Go `signing/roundtrip_test.go` pins signer/verifier parity.

The original design (lookup reed → reconstruct payload → verify by fingerprint)
is unchanged; only the HTTP surface was dropped.

### Selecting the right server key

Today verification always uses `h.signingKey` (the currently-active key).
With `fingerprint` bound in, the verifier must select the key that produced the
countersignature. Since `reeds.private_key_fingerprint` is already a FK into
`private_keys`, this is a straight lookup — no schema change, just a small
addition to the data-service API (e.g. `GetPublicKeyByFingerprint`).

Note: this is also a prerequisite for the recovery import path (Proposal 08+),
which must verify against the *restored* historical key of the matching
fingerprint.

## Work items

1. Add `Fingerprint` to the `Signature` response struct and populate it in
   `SignReed`.
2. Extend `SignReed` to include `reedID`, `authorID`, and `fingerprint` in
   the header map passed to `BytesToSign` (from Proposal 01). No new helper
   is needed — the helper is shape-agnostic; the record type is defined by
   which keys are present.
3. SPA: no change to the reed *signing* code (the SPA does not compose the
   server countersignature). Only add `fingerprint` to the `Server` TS type
   and persist it alongside the existing server-block fields. If we later
   implement local verification of countersignatures, the SPA will call
   `bytesToSign` with the same header set — but that is out of scope here.
4. Client verification reconstructs the payload from stored reed fields and
   selects the server key by fingerprint (SPA `verifyReed`; no HTTP endpoint).
5. Add `GetPublicKeyByFingerprint` (or equivalent) on `DataService`.
6. SPA: remove the `timestamp` field from the reed `Headers` type and from
   the `Reed` constructor. Update every display site (feed cards, reed
   detail page, quotes, reeds list) to read `reed.server.timestamp`, with a
   "Pending…" placeholder when the reed has not yet been countersigned.
7. Tests:
   - Roundtrip: sign a reed, verify succeeds.
   - Replay across reeds: take `(userSig, serverSig)` from reed A and try to
     verify against reed B → fails.
   - Replay across authors: swap `authorID` → fails.
   - Wrong fingerprint → fails.

## Testing

- Existing signup+publish e2e test extended to run local `verifyReed` on the
  IndexedDB reed and expect success.
- Explicit negative tests as above.

## Risks

- **Client migration.** The SPA must store the new `fingerprint` field.
  Backfilling for existing dev reeds is not attempted — dev DBs will re-sign
  on next publish. Per the blank-slate premise this is acceptable.
- **Any external verifier** (peers) needs the new form. Coordinate the client
  change with backend rollout.

## Dependencies

- **Requires Proposal 01** to land first (or as part of the same PR). The
  signer/verifier helper must exist and be correct before this proposal can
  extend it.

## Parallelism

- Independent of Proposals 02, 04–07 code-wise; can be developed in parallel
  once Proposal 01 has merged.
