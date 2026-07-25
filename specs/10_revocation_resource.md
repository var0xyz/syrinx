# Proposal 10 — Revocations as a separate signed resource

## Status

Implemented (`GET .../keys/{fingerprint}/revocation`; `Key.revoked` is a
boolean; signatures on `user_key_revocations`). Follower **fanout** is
[09](09_revocation_fanout.md).

## Context

Today a `GET`/`RevokeKey` response embeds revoke metadata on the key
(`revoked: { reason, timestamp, successor }`) with **no** signatures on
that metadata. The key's `server` block countersigns only the key
material — not the revoke. That conflates two attestations and is wrong
for both local storage and recovery.

`user_key_revocations` already exists as the real revocations table
(`fingerprint`, `owner`, `reason`, `revoked_at`, `successor`). We do
**not** need a new table — add signature columns and **drop**
`revoked_at` (the revoke time is only `server_signed_at` /
`server.timestamp`).

## Scope

- **Wire:** `Key.revoked` becomes a **boolean** only, computed with
  `EXISTS (SELECT 1 FROM user_key_revocations WHERE …)` — never stored
  on `user_keys`.
- **Wire:** revocation details move to a dedicated resource with a
  standard `server` countersignature block (and a user signature).
- **Endpoints:** keep `POST .../revoke` for create; add
  `GET .../revocation` for fetch.
- **DB:** add signature fields to `user_key_revocations`.
- **Client:** IndexedDB `revocations` store; `publicKeys.revoked` is a
  non-optional boolean.
- Blank-slate (break wire + clear/migrate local key shape).

## Non-goals

- Follower replication / realtime fanout ([09](09_revocation_fanout.md)).
- Recovery report-back ingestion of revocations.
- Changing PGP revocation certificates inside OpenPGP keys.
- Two-round init/complete (not required: user payload has no
  server-authored fields).

## Design

### Two resources

| Resource   | What it attests                        | Signatures                          |
|------------|----------------------------------------|-------------------------------------|
| Public key | “This armor is this user's key”        | key `server` block (existing)       |
| Revocation | “This key was revoked for this reason” | user sig (old key) + `server` block |

### `Key` wire shape

```json
{
  "fingerprint": "...",
  "userID": "...",
  "armor": "...",
  "createdAt": "...",
  "revoked": false,
  "server": { "id", "fingerprint", "algorithm", "signature", "timestamp" }
}
```

`revoked` is computed on read. No `reason` / `timestamp` / `successor`
on the key.

### Revocation wire shape

```json
{
  "fingerprint": "...",
  "userID": "...",
  "reason": "...",
  "successor": null,
  "signature": "<base64 user signature>",
  "server": {
    "id": "<serverID>",
    "fingerprint": "<server signing key>",
    "algorithm": "PGP+base64",
    "signature": "<base64 server signature>",
    "timestamp": "..."
  }
}
```

The revocation time **is** `server.timestamp` (the server-authoritative
time inside the countersignature), same pattern as other signed resources.

`successor` is **bookkeeping**: written later by `AddPublicKey`, returned
on GET when present, **not** covered by either signature (it cannot be —
it is unknown at revoke time). Everything else in the revocation
statement is covered: the GET 200 is a signed resource via `server`
(and `signature`); only `successor` is outside the signed set.

### Canonical payloads (`BytesToSign`)

**User payload** (signed by the **key being revoked**):

- Headers: `type: revocation`, `userID`, `fingerprint`
- Content: `reason` (may be empty)

**Server payload** (countersigned by the active server key):

- Headers: user headers + `signedAt`, `serverID`,
  `serverKeyFingerprint`, `userSignature` (binds the two layers, same
  pattern as identity records). `signedAt` is what the wire exposes as
  `server.timestamp`.
- Content: same `reason`

### Endpoints

#### `POST /users/{userID}/keys/{fingerprint}/revoke`

- Auth: normal signature-auth; request signed by the key being revoked
  (**before** the revocations row exists, so middleware still accepts it).
- Body: `reason`, `userSignature` (base64 of armored detached sig over
  the user payload).
- Server: verify user sig against that key's armor; insert
  `user_key_revocations` with reason, signatures, and
  `server_signed_at = now`; countersign; return the **key** with
  `revoked: true` only (no revoke body).

#### `GET /users/{userID}/keys/{fingerprint}/revocation`

- Auth: **required** (not public) — any caller with a valid non-revoked
  session key, same as other authenticated GETs.
- **200** — revocation resource as above.
- **404** — key is not revoked, or no revocations row (same status;
  do not distinguish “unknown key” vs “not revoked” beyond existing
  key 404s on the key endpoint if we choose to check key existence
  first; default: 404 whenever no revocations row).

### Ordering constraint (rotation)

After `POST .../revoke`, the old key is revoked → it can no longer
authenticate. The owner therefore **cannot** `GET .../revocation` until
they authenticate with another non-revoked key (typically right after
`AddPublicKey` promotes the replacement).

Client rotation flow:

1. `POST .../revoke` → store `publicKeys.revoked = true` (and
   `privateKeys.revoked = true`).
2. `POST /keys` (add replacement) → `put` new public key;
   `setSuccessor` locally.
3. `GET .../revocation` (signed in with the **new** key) → verify +
   persist into the `revocations` store.

Peers fetch the revocation when they need proof, using their own
session.

### Schema (`user_key_revocations`)

Keep the existing table; **drop** `revoked_at`; add:

| Column               | Purpose                                                   |
|----------------------|-----------------------------------------------------------|
| `user_signature`     | base64 user sig (NOT NULL after this lands)               |
| `server_signature`   | base64 server sig                                         |
| `server_fingerprint` | server key that produced `server_signature`               |
| `server_signed_at`   | server-authoritative revoke time; wire `server.timestamp` |

`reason` and `successor` unchanged. No `revoked` column on `user_keys`.

### Client

- **`publicKeys`:** full wire key shape with `revoked: boolean`
  (required). Drop nested revoke metadata.
- **`revocations`:** new IndexedDB store, keyPath
  `(userID, fingerprint)` or equivalent; value = revocation wire
  shape; verify `server` (+ user sig against the revoked public key)
  before put.
- Profile / rotation UI: boolean from the key; reason / successor /
  verify proof from the revocations store (fetch on demand).

## Relationship to proposals 06 / 09

| Topic | 06 | 09 | This proposal |
|-------|----|----|---------------|
| Signed revoke create | **landed** | — | assumes 06 |
| Signer of user revoke | aligned on **old** key | — | **old** key being revoked |
| Storage / API split | early sketches | — | **this doc** |
| Server countersign | required | — | **required** |
| Replication to followers | — | **in scope** | deferred to 09 |

## Work items

1. Schema: drop `revoked_at`; add the four signature columns to
   `user_key_revocations`.
2. `identity.go` / signing helpers: `buildUserRevocationPayload` /
   `buildServerRevocationPayload`.
3. `RevokeKey`: require `userSignature`; verify; countersign; persist;
   return `Key` with boolean `revoked`.
4. `GetPublicKey` / any key serializer: `revoked` via `EXISTS`, strip
   nested `Revoke` object.
5. New `GetKeyRevocation` handler + route
   `GET /users/{userID}/keys/{fingerprint}/revocation`.
6. SPA: `revocations` store; update `publicKey` type; rotation flow
   fetches revocation after add; `setRevoked` only flips boolean /
   uses key response.
7. Tests: verify user+server sigs; GET 404 when not revoked; GET 200
   after revoke; successor still set by `AddPublicKey` and returned
   on GET without being covered by signatures; auth required on GET.

## Testing

- Unit: payload builders + signature round-trip (Go/TS vectors).
- Integration: revoke → key shows `revoked: true` → GET revocation 200
  with verifying `server` → add key → GET shows `successor`.
- Negative: bad `userSignature` → 401/400; GET without auth → reject;
  GET for active key → 404.

## Risks

- **Rotation window:** owner cannot fetch the countersigned revocation
  until a non-revoked key can authenticate. Acceptable if the client
  always completes `AddPublicKey` before relying on the stored proof;
  document in the rotation UX.
- **404 ambiguity:** “not revoked” vs “no such user/key” — prefer
  checking key existence if we need clearer errors; not required for
  v1.

## Dependencies

- Requires Proposal 01 (`BytesToSign`).
- Completes the create/fetch half of [06](06_signed_replicated_revocations.md);
  fanout is [09](09_revocation_fanout.md).
- Independent of 02–05, 07–08, 11.

## Parallelism

- Fanout ([09](09_revocation_fanout.md)) can proceed independently once
  this resource shape is stable.
