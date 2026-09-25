# Attestations 02 — Canonical payloads + countersign

## Status

Proposed.

## Depends on

[00](00_design.md)

## Context

The exact bytes a voucher signs, and the bytes the server countersigns.
Mirrors the `Build*Payload` pairs in `identity.go` and their SPA twins in
`signing.ts`, which must stay byte-identical
([01_reed_countersig_canonical_form](../01_reed_countersig_canonical_form.md)).

## Scope

- `buildVouchUserPayload` / `buildVouchServerPayload`.
- `buildVouchWithdrawalUserPayload`.

## Non-goals

- Storage ([01](01_schema.md)), transport ([03](03_api.md)).

## Design

### Vouch, user payload

```
type:        user_vouch
voucherKeyID: <the signing key's full id>
subjectUserID: <subject's canonical userID>
subjectKeyID:  <subject's full key id>
```

Content section: empty (contentless assertion, like a reed like).

```go
func buildVouchUserPayload(voucherKeyID, subjectUserID, subjectKeyID string) []byte {
    return bytesToSign(map[string]string{
        "type":          "user_vouch",
        "voucherKeyID":  voucherKeyID,
        "subjectUserID": subjectUserID,
        "subjectKeyID":  subjectKeyID,
    }, "")
}
```

**`voucherKeyID` is in the payload, self-describing**, matching
`buildReedLikeUserPayload`'s `keyID`. A verifier must know which key to check
against without trusting an out-of-band claim about it.

**`subjectKeyID` and `subjectUserID` are both present** even though the key
id is owner-prefixed and technically contains the user id. They are signed
separately so the assertion is explicit rather than parsed out of a string,
and so a verifier never reconstructs an id from fragments
(`verifyPublicKey` does a prefix check for the same reason).

**No timestamp in the user payload.** The user is asserting a fact about a
key, not about a moment; the server's countersignature carries the
authoritative time, as everywhere else. Adding a client clock would create a
field an attacker controls, and [M7](../../RISKS.md) is already a warning
about attacker-influenced time.

### Vouch, server payload

```
type:                 user_vouch
voucherUserID:        <voucher's canonical userID>
subjectUserID:        ...
subjectKeyID:         ...
signedAt:             <server time, identityRecordTimeFormat>
serverKeyFingerprint: <server signing key>
userSignature:        <base64 of the voucher's detached sig>
```

Same shape as `buildReedLikeServerPayload`: the countersignature binds the
user signature plus a server-authoritative timestamp.

### Withdrawal payload

```
type:          user_vouch_withdrawal
voucherKeyID:  <signing key id>
subjectUserID: ...
subjectKeyID:  ...
```

Deliberately **not** the vouch payload with a flag. A distinct `type` means
a withdrawal signature can never be replayed as a vouch, or the reverse, and
the domain separation is visible in the bytes.

The withdrawal is signed by whatever key is *current* for the voucher at
withdrawal time, which may differ from `voucher_key_id` on the original row.
That is intentional: a user who rotated must still be able to retract. The
server verifies the withdrawal against the voucher's current active key, and
stores it in `withdrawal_signature_id`.

### Verification order (server, on create)

1. Reject if `subjectUserID` is the caller — self-vouching asserts nothing.
2. Resolve the voucher's key from `voucherKeyID`; reject if revoked.
3. Resolve `subjectKeyID`; reject if unknown or revoked. A vouch for a key
   the server has never seen is not storable, and one for a revoked key is
   void on arrival ([04](04_revocation.md)).
4. Reject if `subjectKeyID` is not owner-prefixed by `subjectUserID`.
5. Verify the detached signature over `buildVouchUserPayload`.
6. Countersign, store, return.

Step 4 matters: without it a voucher could sign a well-formed assertion
binding Bob's userID to Carol's key, and clients that trust the pair would
resolve the wrong key.

### Verification order (client, on display)

Clients **must not** rely on the server having done the above. Verify each
vouch independently before counting it:

1. Verify the server countersignature (standard `verify`).
2. Resolve the voucher's key `voucherKeyID` and verify it via
   `verifyPublicKey`.
3. Verify the voucher's detached signature over the rebuilt user payload.
4. Apply void rules ([04](04_revocation.md)).

A client that skips step 3 and trusts the server's countersignature alone
has reintroduced H1 inside the very feature meant to answer it.

## Testing

- Byte-identical Go/TS payload vectors for both types, per
  [01_reed_countersig_canonical_form](../01_reed_countersig_canonical_form.md).
- A withdrawal signature must fail verification against the vouch payload.
- A vouch whose `subjectKeyID` is not prefixed by `subjectUserID` is
  rejected at step 4.
- A vouch signed by key A but claiming `voucherKeyID` B fails at step 5.
