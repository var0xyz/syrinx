# Proposal 04 — Signed, server-countersigned identity records at signup

## Status

Proposed. Rule 1 of the recovery trust model (see
[`takeover_recovery.md`](../takeover_recovery.md), "The three rules" and "What
must be built").

## Context

`Signup` validates the user's self-signature over their key but produces
**no server signature** over the identity binding
`(userID, username, avatarURL, bio, memberSince)`. Without such a
countersignature, recovery cannot trustlessly rebuild the `users` and
`user_keys` tables from client-held state, and profile fetches force
visitors to trust the server for every field the server serves.

## Scope

- On signup, produce a **user-signed, server-countersigned identity
  record** carrying:
  - `userID` (server-assigned)
  - `username`
  - `fingerprint` — the key that produced `userSignature` (self-describing,
    see "Fingerprint semantics" below)
  - profile fields (`avatarURL`, `bio`)
  - `memberSince` (server-authoritative, set once at signup, carried
    unchanged through every later record — this is the existing
    `users.created_at` column, exposed as `memberSince` in the API)
  - `signedAt` (server-authoritative, per-record)
  - `serverID`
- Persist both signatures on the `users` row so profile fetches don't have
  to recompute anything on read.
- Return the full identity record (including both signatures) from
  `POST /users/signup` and `GET /users/{userID}`.

## Non-goals

- Profile *updates* are Proposal 05 (they will produce a fresh identity
  record with a bumped `signedAt` reusing the shape defined here).
- **Key rotation is out of scope.** Identity records are decoupled from
  key rotation — see "Fingerprint semantics" and "Key rotation" below.
- Revocations are Proposal 06.
- The recovery **report-back endpoint** that ingests these records is part
  of the later "unit of work". This proposal only produces the records in
  normal operation.
- No client sync/queueing beyond storing the returned record.

## Design

### Fingerprint semantics

The `fingerprint` field in an identity record identifies **the key that
produced `userSignature` for this specific record**. It is self-describing:
verifiers read this header, look up the matching row in `user_keys`, and
use that public key to verify `userSignature`.

`fingerprint` is **not** a pointer to the user's "currently active" key.
Users may have multiple keys, and the operational notion of a preferred
encryption key is a separate concern (future work, unrelated to this
proposal). Decoupling the two means:

- Signed records survive key rotation. Alice's old identity record stays
  cryptographically valid after she adds a new key, because the record
  points to the key it was signed with, not to a mutable per-user pointer.
- Key rotation code doesn't need to re-sign identity records.

The same self-describing convention applies on the server side:
`server.fingerprint` identifies the server key that produced
`server.signature`.

### Record shape (two payloads, one record)

The user's signature and the server's signature cover **different but
overlapping byte sequences**. Both use the shared `BytesToSign` envelope
helper (see [`README.md — Shared conventions`](./README.md)).

**User payload** — what `userSignature` covers. Only user-authored fields.

Headers:

- `avatarURL: <url>` (omitted if empty, per envelope rules)
- `fingerprint: <userKeyFingerprint>`
- `type: identity-user`
- `username: <username>`

Content: `<bio>` (the free-form multi-line field; may be empty).

**Server payload** — what `serverSignature` covers. Superset of the user
payload, plus server-authoritative fields and — critically — the user's
signature as a header.

Headers:

- `avatarURL: <url>`
- `fingerprint: <userKeyFingerprint>`
- `memberSince: <RFC3339 UTC seconds>`
- `serverID: <serverID>`
- `serverKeyFingerprint: <server signing-key fingerprint>`
- `signedAt: <RFC3339 UTC seconds>`
- `type: identity-server`
- `userID: <userID>`
- `userSignature: <base64 of the user's detached signature>`
- `username: <username>`

Content: `<bio>` (same bytes as the user payload).

Rationale for putting `bio` in the content: it is the only legitimately
multi-line field, and the envelope's content section handles arbitrary UTF-8
verbatim. All other fields are single-line and live comfortably as headers.

**Why the user signs a subset.** `memberSince`, `signedAt`, `serverID`,
`serverKeyFingerprint`, and `userID` are server-authored — the user has no
independent knowledge of any of them and no interest in vouching for them.
Asking the user to sign over them would either force a two-round dance
(server tells the user what to sign, then the user signs) or leak the
client's clock into a server-authoritative field. Neither is worth the
complexity when the server can simply sign a superset.

**Why the server payload includes `userSignature`.** Binding the user's
signature as a header in the server-signed bytes welds the two attestations
together. A compromised or malicious server cannot re-pair Alice's
`userSignature` with a different set of server-authored fields (say, a
fabricated `memberSince`) without breaking `serverSignature`. The two
distinct `type` values (`identity-user` vs `identity-server`) also prevent
cross-payload confusion — a user signature over the user payload can never
be misinterpreted as a server signature over a truncated server payload.

### `Signup`

`POST /users/signup` — one round. Body (form-encoded):

- `username`
- `publicKey` (armored)
- `signature` — self-signature over `publicKey` (existing behaviour)
- `userSignature` — base64 of the user's detached signature over the
  **user payload** built from `(username, fingerprint, "", "")`

Order of operations:

1. Validate the user's self-signature over the key (existing behaviour).
2. Extract `fingerprint` from `publicKey`.
3. Reconstruct the user payload from submitted fields; verify
   `userSignature` against `publicKey` over those exact bytes.
4. Assign `memberSince = now()`, `signedAt = memberSince`. Assign
   `userID`.
5. Build the server payload (including `userSignature` as a header).
   Countersign with the server's signing key.
6. In a single transaction, insert the `users` row (with
   `server_signed_at`, `identity_user_signature`,
   `identity_server_signature` populated) and the `user_keys` row.
7. Return the full identity record — see "Profile response shape" below.

The client can construct the user payload entirely from client-known
values (`username`, its own `fingerprint`, empty `avatarURL`, empty
`bio`). No pre-flight round-trip is needed.

### Key rotation

Key rotation is **not** in scope for this proposal and does not produce a
new identity record. Because the `fingerprint` in the record identifies
the signing key rather than the user's "current" key, adding a new key
does not invalidate the existing record — the record still verifies
against the (still-present) old key row in `user_keys`.

If a user wants a profile whose identity record is signed by a newer key,
they publish a new identity record (Proposal 05 covers the endpoint).
Until then, the previous record remains valid.

### Storage

- New `users` columns:
  - `server_signed_at TIMESTAMP` — timestamp of the current record's
    server countersignature. Used later for monotonic newest-wins.
  - `identity_user_signature TEXT` — base64 of the user's detached
    signature over the current user payload.
  - `identity_server_signature TEXT` — base64 of the server's detached
    signature over the current server payload.

  All three are `NULL`able for now; per the blank-slate premise no
  production data exists, and every code path that produces a record
  populates all three together.
- `created_at` (exposed as `memberSince`) is **unchanged** — it already
  exists and already carries the semantics we need.
- **Signatures are stored server-side** so profile fetches can return them
  without recomputing on every read. They are also returned to the client
  at signup for later recovery report-back.
- No `pending_signups` table. Signup is a single POST.

### Profile response shape

Profile reads (`GET /users/{userID}`) and the `POST /users/signup`
response return the full identity record so any visitor can independently
verify it. The shape is flat — all user-owned fields at the root — with a
single nested `server` key grouping the countersignature fields to avoid
colliding with the user's own `signature` / `fingerprint`. The nested
`server` block reuses the shared `Signature` struct from Proposal 03
(reeds) for consistency:

```json
{
  "id": "...",
  "username": "...",
  "fingerprint": "<key that signed `signature`>",
  "memberSince": "...",
  "avatarURL": "...",
  "bio": "...",
  "signature": "<user's detached signature over the user payload>",
  "server": {
    "id": "<serverID>",
    "fingerprint": "<key that signed `server.signature`>",
    "timestamp": "<signedAt>",
    "algorithm": "PGP+base64",
    "signature": "<server's detached signature over the server payload>"
  }
}
```

Clients reconstruct the two canonical envelopes (user payload and server
payload) from these fields and verify both signatures against them. Both
`fingerprint` fields are self-describing: they tell the verifier which
public key to look up in order to check the sibling `signature`.

#### Why return both signatures, not just the server's

The user signature is not redundant with the server signature — they
attest different things and both are needed for the trust model:

- **`signature`** proves that the holder of the key referenced by
  `fingerprint` authored this identity binding's user-authored fields
  (`username`, `avatarURL`, `bio`). Without it, a compromised or
  malicious server could serve any `(username, avatarURL, bio)` tuple
  it wanted and every visitor's client would still see a valid signature.
  With it, visitors can check that the binding was actually signed by
  the user's key — the server cannot unilaterally rewrite a profile.
- **`server.signature`** proves that the server, at `server.timestamp`,
  vouched for this specific binding, including the server-authored
  fields (`id`, `memberSince`) and the user's `signature` itself.
  It is what recovery uses to trustlessly rebuild `users` /
  `user_keys` from client-held state.

Serving only the server signature would reduce profile view to
"trust the server," which is exactly the property signed identity
records exist to avoid. Storing and returning both is cheap (two extra
`TEXT` columns, no extra compute on read) and keeps a single canonical
identity-record shape flowing through profile view, client IndexedDB,
and recovery report-back.

## Future work

**Self-describing signed field lists.** This proposal hard-codes the
header sets of the user and server payloads in source code
(`identity.go` on the Go side; the SPA mirror on the TS side). A verifier
that doesn't share that source cannot reconstruct the exact bytes that
were signed.

A future revision should include, per signature, an explicit enumeration
of the headers and content that were signed — for example
`signedHeaders: ["username", "fingerprint", "avatarURL"]` and
`signedContent: "bio"`. This has two benefits:

1. **Forward compatibility.** New optional fields can be added to
   identity records without breaking verification of older records —
   the verifier reads which fields the signature actually covers from
   the record itself.
2. **Clearer separation of user-authored vs. server-authored data** at
   the wire level, rather than by convention. Today the split lives in
   `identity.go`; tomorrow it should live in the signed bytes.

Out of scope here — it would touch the `signing.BytesToSign` contract
and require a coordinated Go/TS change plus a versioning strategy for
existing records.

## Work items

1. Schema: add `server_signed_at`, `identity_user_signature`, and
   `identity_server_signature` to `users`. No new tables.
2. Envelope: new `identity.go` module exporting
   `buildUserIdentityPayload` and `buildServerIdentityPayload`. Both call
   `signing.BytesToSign` — no new canonicalisation helper.
3. `services.go`: replace `CreateUser(username)` with a `Signup` service
   that verifies `userSignature`, produces `serverSignature`, and
   persists both alongside the row it already inserts.
4. `handlers.go`: `Signup` handler gains the new `userSignature` form
   field and the record-production step. `GetUser`, `UpdateUser`, and
   any other handler returning a `User` emit the new flat + `server`
   shape.
5. Client (SPA — follow-up PR): update signup UX to build the user
   payload locally, sign it with the fresh key, and POST alongside the
   existing self-signature. Store the returned
   `{headers, content, userSignature, serverSignature}` in IndexedDB.
6. Tests:
   - Signup produces a verifying `serverSignature` over the canonical
     server envelope and a verifying `userSignature` over the canonical
     user envelope.
   - Tampering with `userSignature` in the profile response breaks
     `serverSignature` verification (confirms the `userSignature`
     header binds the two payloads).
   - A bad `userSignature` at signup is rejected with a clear 400.
   - Cross-implementation: extend the shared test-vector list from
     Proposal 01 with identity-user and identity-server records to keep
     Go and TS `BytesToSign` byte-identical.

## Testing

- Unit + integration for the signup flow.
- Round-trip: signup → `GET /users/{userID}` → rebuild both envelopes
  from the JSON → verify both signatures against the stored keys.

## Risks

- **Breaking wire change**: the `User` response shape changes
  (top-level `signature`, nested `server` block; old `Server string`
  field removed). SPA is under active development and will be updated
  in a follow-up; dev DBs must be reset.
- **Recovery attack surface**: identity records can outlive the key
  that signed them because rotation no longer produces a new record.
  Recovery / verifier code must treat "signature verifies but signing
  key is revoked" as invalid. This is the same rule that already
  applies to reeds (Proposal 06 handles revocation semantics).

## Dependencies

- **Benefits from Proposal 02** (random user IDs) landing first, so the ID
  written into the countersigned record is already in its final format.
  Otherwise every identity record would need re-signing when IDs change.
- Otherwise independent of 01, 03, 05, 06, 07.

## Parallelism

- Can be developed in parallel with any of 01, 03, 05, 06, 07 but should
  **land after** Proposal 02 to avoid re-signing.
- Proposal 05 (signed profile updates) reuses this proposal's identity
  record shape verbatim: a profile update just produces a new record with
  a bumped `signedAt` and new signatures over the updated user payload.
