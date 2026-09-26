# Attestations 02 — Canonical payloads + countersign

## Status

Implemented.

## Depends on

[00](00_design.md)

## Context

The exact bytes a voucher signs, and the bytes the server countersigns.
Mirrors the `Build*Payload` pairs in `identity.go` and their SPA twins in
`signing.ts`, which must stay byte-identical
([01_reed_countersig_canonical_form](../01_reed_countersig_canonical_form.md)).

## Scope

- `buildVouchUserPayload` / `buildVouchServerPayload`.
- `buildVouchWithdrawalUserPayload` / `buildVouchWithdrawalServerPayload`.

## Non-goals

- Storage ([01](01_schema.md)), transport ([03](03_api.md)).

## Design

### Vouch, user payload

```
voucherKeyID: <the signing key's full id>
subjectKeyID: <subject's full key id>
```

Content section: the note (may be empty).

```go
func buildVouchUserPayload(voucherKeyID, subjectKeyID, note string) []byte {
    return bytesToSign(map[string]string{
        "voucherKeyID": voucherKeyID,
        "subjectKeyID": subjectKeyID,
    }, note)
}
```

**`voucherKeyID` is in the payload, self-describing**, matching
`buildReedLikeUserPayload`'s `keyID`. A verifier must know which key to check
against without trusting an out-of-band claim about it.

**Key ids only; no user ids.** A key id is `user@server/keyid`, so the owner
is already inside the signed bytes. A signed `subjectUserID` could only ever
be a copy of a prefix of `subjectKeyID`, and two signed fields where one
contains the other cannot disagree — checking them against each other is a
tautology, not a verification.

What actually binds a vouch to a person happens *before* signing, in the
verify flow ([06](06_spa_verify_flow.md)): the subject's user id comes from
the path segment, the scanned key id from the URL fragment, which browsers
never transmit. The client compares the scanned key against the key the
server serves for that path user, and only signs once they match. That
comparison is the H1 check, and it works because its two inputs have
different provenance — not because a field is duplicated into the payload.

A client displaying a vouch checks it the same way, against an id it already
holds: `subjectKeyID.startsWith(<the id being rendered for> + "/")`. The id
is never split or reconstructed out of the key id.

**No `type` field.** The four payloads are distinguished by their field
sets, not by a discriminator — a vouch signs key ids, a withdrawal signs a
`vouchID`, and no signature over one can be read as the other.

**No timestamp in the user payload.** The user is asserting a fact about a
key, not about a moment; the server's countersignature carries the
authoritative time, as everywhere else. Adding a client clock would create a
field an attacker controls, and [M7](../../RISKS.md) is already a warning
about attacker-influenced time.

**No `serverID`.** Both servers are already named inside the key ids, and a
bare `serverID` would not say which one it meant.

### Vouch, server payload

```
subjectKeyID:         ...
signedAt:             <server time, identityRecordTimeFormat>
serverKeyFingerprint: <server signing key>
userSignature:        <base64 of the voucher's detached sig>
```

Same shape as `buildReedLikeServerPayload`: the countersignature binds the
user signature plus a server-authoritative timestamp. The voucher is not
named here — `voucherKeyID` is covered by the user signature that forms this
payload's body, and the vouch id is owner-prefixed by the voucher.

### Withdrawal payload

```
vouchID: <the vouch being retracted>
```

Content section: empty (a retraction carries no memo).

**A withdrawal names its target and nothing else.** It does not repeat the
voucher or subject: those are already fixed by the vouch it points at. This
is also what makes it unambiguous — re-vouching is allowed
([01](01_schema.md)), so a voucher can hold several vouches for the same
subject key over time, and a retraction that described only
`(voucher, subject key)` would not say which one it retracted.

The withdrawal is signed by whatever key is *current* for the voucher at
withdrawal time, which may differ from `voucher_key_id` on the original row.
That is intentional: a user who rotated must still be able to retract. The
server verifies the withdrawal against the voucher's current active key, and
stores it in `withdrawal_signature_id`.

### Withdrawal, server payload

```
vouchID:              <the vouch being retracted>
signedAt:             <server time, identityRecordTimeFormat>
serverKeyFingerprint: <server signing key>
userSignature:        <base64 of the voucher's withdrawal sig>
```

Same shape as the vouch server payload: the user's signature is the envelope
body, so what the server attests is the signature itself.

A retraction needs **both** signatures. Without the voucher's, the server could
nullify a vouch it dislikes by setting a flag. Without the server's, the
withdrawal time is bound by nothing — and the audit list sorts by server time
precisely because it is the one timestamp the caller's own client did not
choose. The countersignature is also the voucher's receipt that the retraction
was recorded.

**The withdrawal has no id of its own.** It is addressed through the vouch it
retracts, which is what `vouchID` in the headers names. There is no
`GET /withdrawals/{id}`: a client that wants the retraction fetches the vouch
and finds it nested inside.

### Verification order (server, on create)

1. Reject if `subjectKeyID` is owned by the caller — self-vouching asserts
   nothing.
2. Resolve the voucher's key from `voucherKeyID`; reject if revoked.
3. Resolve `subjectKeyID`; reject if unknown or revoked. A vouch for a key
   the server has never seen is not storable, and one for a revoked key is
   void on arrival ([04](04_revocation.md)).
4. Verify the detached signature over `buildVouchUserPayload`.
5. Countersign, store, return.

The subject is whoever owns `subjectKeyID`; the server derives the stored
`subject_user_id` from it rather than accepting one from the request.

### Verification order (client, on display)

Clients **must not** rely on the server having done the above. Verify each
vouch independently before counting it:

1. Check `subjectKeyID` is owner-prefixed by the user id being rendered for.
2. Verify the server countersignature (standard `verify`).
3. Resolve the voucher's key `voucherKeyID` and verify it via
   `verifyPublicKey`.
4. Verify the voucher's detached signature over the rebuilt user payload.
5. Apply void rules ([04](04_revocation.md)).

A client that skips step 4 and trusts the server's countersignature alone
has reintroduced H1 inside the very feature meant to answer it.

## Testing

- Byte-identical Go/TS payload vectors for all four payloads, per
  [01_reed_countersig_canonical_form](../01_reed_countersig_canonical_form.md).
- A withdrawal signature must fail verification against the vouch payload.
- A vouch whose `subjectKeyID` is not prefixed by the id being rendered for
  is rejected at step 1.
- A vouch signed by key A but claiming `voucherKeyID` B fails at step 4.
- Two vouches by the same voucher for the same subject key get distinct
  withdrawal payloads, since each names its own `vouchID`.
- A withdrawal missing either signature is refused by the client.
