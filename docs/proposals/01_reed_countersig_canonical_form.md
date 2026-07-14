# Proposal 01 — Fix reed countersignature signer/verifier mismatch

## Status

Proposed. Precursor to [`takeover_recovery.md`](../takeover_recovery.md). Also a
standalone bug fix: today the server's own countersignature does not verify
against its own signing key.

## The bug

When you publish a reed, the server produces a **countersignature** — a PGP
signature the server makes over some string. When someone later hits
`/reeds/{userID}/{reedID}/verify` to check that countersignature, the server
tries to verify it against a **different** string. Because PGP signatures are
bound to the exact bytes that were signed, verification always fails. The
server's own countersignature does not verify against the server's own
signing key.

### The concrete disagreement

`SignReed` in `handlers.go` builds this string and signs it:

```
algorithm: PGP+base64
id: <serverID>
timestamp: <RFC3339>
---
<userSignatureBase64>
```

```go
// handlers.go, SignReed
payload := fmt.Sprintf("algorithm: PGP+base64\nid: %s\ntimestamp: %s\n---\n%s",
    serverID, timestamp.Format(time.RFC3339), signature)
serverSignature, err := h.services.crypto.Sign(payload, h.signingKey.Armor)
```

`VerifySignature` in `handlers.go` verifies the same server signature against
just the bare `userSignature` string, with no headers wrapping it:

```go
// handlers.go, VerifySignature
err = h.services.crypto.VerifySignature(userSignature, serverSignature, h.signingKey.Armor)
```

Two different strings → verification always fails.

### Why the bug went unnoticed

The `/verify` endpoint is not on any critical path today. The SPA does not
verify countersignatures locally, so no client is exercising the broken code
path in production.

### Why it becomes critical for recovery

At recovery time, verifying server countersignatures against restored
historical keys is the **entire trust anchor** for restored reeds. If the
signer and verifier don't agree on what was signed, no restored reed will
ever verify, and recovery fails.

## Scope

- Introduce the shared **`BytesToSign`** envelope helper (see
  [`README.md — Shared conventions`](./README.md)) on both server and client.
- Route both `SignReed` and `VerifySignature` through `BytesToSign` so signer
  and verifier cannot drift again.
- Pin the base64 layering (who base64s what, exactly once, at which layer).
- Add tests that sign → verify roundtrip and that a payload with a stray byte
  fails.

## Non-goals

- **No new fields** in the countersigned payload. This proposal is a pure
  bug-fix / hardening. Adding `reedID`, `authorID`, and `fingerprint` to the
  signed payload is Proposal 03 (`server` block hardening), which depends on
  this one but is scoped separately so this can ship immediately.
- No schema changes.

## Design

### Canonical form via `BytesToSign`

The countersigned payload is:

```
Headers:
  algorithm: PGP+base64
  id:        <serverID>
  timestamp: <RFC3339 UTC seconds>

Content:
  <userSignatureBase64>
```

Passed through `BytesToSign(headers, content)`. Because the helper sorts
headers ASCII byte-lexicographically, the produced bytes are:

```
---
algorithm: PGP+base64
id: <serverID>
timestamp: <RFC3339 UTC seconds>
---
<userSignatureBase64>
```

The order happens to match today's hand-composed order, but nothing depends
on that: the helper is the single source of truth. Rules baked into the
helper (see README):

- Keys sorted ASCII byte-lex.
- One header per line, `<key>: <value>\n` (single space, LF only).
- Empty-value headers are omitted.
- No escaping — values are inserted verbatim.
- Content appended verbatim after `---\n`, no trailing newline added.

For this proposal specifically: `timestamp` is UTC, truncated to seconds,
formatted `time.RFC3339`. `<userSignatureBase64>` is the base64 **standard**
encoding of the raw detached PGP signature bytes — exactly the string the
client submits in the `signature` form field.

### `VerifySignature` change

Rebuild the header map from the stored reed row (`serverID`,
`reed.signed_at`, `userSignature`), call `BytesToSign`, verify the
`serverSignature` against those bytes. Do **not** trust any client-supplied
algorithm/timestamp for the canonical form — read them from the stored reed
/ server config.

### Base64 layering

- `userSignature` on the wire: base64(raw PGP detached sig over reed content).
- `serverSignature` on the wire: base64(raw PGP detached sig over the bytes
  produced by `BytesToSign`).
- The envelope embeds the **base64** `userSignature` string, not the raw
  bytes. No nested base64.

Document this in a short comment in both `SignReed` and `VerifySignature`
pointing at `BytesToSign`.

## Work items

1. Add `BytesToSign(headers map[string]string, content string) []byte` in a
   new Go package (e.g. `signing/`). Include the "why nothing is escaped"
   comment described in the README.
2. Add the TS mirror `bytesToSign(headers, content): Uint8Array` in
   `spa/src/lib/services/signing.ts` (or similar). Same comment.
3. Change `SignReed` to build the header map and call `BytesToSign` instead
   of the hand-composed `fmt.Sprintf` payload.
4. Change `VerifySignature` to reconstruct the same header map from the
   stored reed and call `BytesToSign`.
5. Roundtrip test: sign a random `userSignature`, verify it, assert success.
6. Negative tests: mutate one byte of the envelope → verify fails; use
   `time.Now()` without truncation → verify fails; swap `serverID` → verify
   fails.
7. Cross-implementation test: a small shared test-vector list (input
   headers + content + expected bytes) exercised by both Go and TS unit
   tests, to keep the two helpers byte-identical.

## Testing

- Unit tests around the helper and both handlers.
- Manual smoke: sign a reed via the SPA against a dev server, hit the verify
  endpoint, expect success. Today this fails.

## Risks

- **Client compatibility.** If any deployed client canonicalises differently
  from `SignReed`, this fix exposes that. Given the doc's blank-slate premise
  ("pre-launch, no legacy reed blocks or IDs exist") this is acceptable —
  but grep the SPA for signature composition and confirm before merging.
- No data migration needed: existing reeds in dev DBs whose countersignature
  never verified will now verify (or the row is discarded and re-signed in
  dev).

## Dependencies

None. Can start immediately and land independently.

## Parallelism

Fully independent of Proposals 02–07. Proposal 03 (`server` block hardening)
builds on the `BytesToSign` helper introduced here but is otherwise
separately scoped.
