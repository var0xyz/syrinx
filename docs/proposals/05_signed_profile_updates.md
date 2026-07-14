# Proposal 05 — Signed, server-countersigned profile updates

## Status

Proposed. Same trust-model rule as Proposal 04 (Rule 1), applied to the
update path (see [`takeover_recovery.md`](../takeover_recovery.md), "What must
be built").

## Context

`PUT /users/me` today accepts partial updates to `username`, `avatarURL`,
and `bio` and writes them to the row with no signature at either layer.
Recovery cannot replay profile state without a signed, server-countersigned
record, and visitors currently have to trust the server for every field it
serves.

## Scope

- Convert `PUT /users/me` into a **full signed replacement** of the user's
  identity record — the same two-payload shape produced by signup
  (Proposal 04), just triggered by an edit.
- Every accepted request produces a fresh identity record: new
  `user_signature`, new `server_signature`, new `server_signed_at`, new
  `server_fingerprint`.
- Every field the user can change (`username`, `avatarURL`, `bio`) is
  covered by `userSignature`.
- Return the fresh identity record (Proposal 04 response shape) so the
  client can replace its stored copy.

## Non-goals

- No new profile fields.
- No changes to signup or key rotation (Proposal 04).
- No recovery ingestion.
- No new endpoint. No new tables. No new columns.

## Design

### Full-record replacement, not partial patch

The request always carries the **complete** post-edit tuple `(username,
avatarURL, bio)`, plus a `userSignature` over the canonical user payload
built from `(username, fingerprint, avatarURL, bio)`. Partial updates
disappear from the wire: the client is responsible for sending the current
values of any unchanged fields alongside whatever it's actually editing.

This is a direct consequence of the signature contract. The user payload
canonicalises exactly one tuple of field values; there is no "unspecified"
state for a signed field. Reconstructing the signed bytes on the server
requires the server to know every field the user signed, which requires
the client to send them.

### Change detection via signature equality

The client is expected to omit no-op edits from the UI. As a defence
against a client that skips that check (or against a hostile client
probing the endpoint), the server uses `userSignature` byte-equality as
the authoritative "did anything change?" test:

- If the submitted `userSignature` equals the row's stored
  `user_signature`, the request is a no-op. Return 200 with the current
  identity record. Do **not** re-verify, do **not** mint a new
  `server_signed_at`, do **not** produce a new `server_signature`, do
  **not** broadcast a realtime update.
- Otherwise the signature necessarily covers a different tuple of field
  values (or was signed by a different key). Proceed with verification,
  countersigning, and persistence.

Signature equality is sufficient because a valid signature deterministically
binds a specific `(username, fingerprint, avatarURL, bio)` tuple under a
specific key. If the stored signature is byte-identical to the submitted
one, the signed bytes are identical too, and by extension so are the four
fields the user cares about. The server's own copy of those fields is
already consistent with that signature (it wrote them together in the
last accepted update or at signup).

This makes the "did the user actually change anything?" question a single
`bytes.Equal` — no field-by-field diff, no ambiguity about which fields
"count."

### Endpoint

`PUT /users/me` — unchanged path, unchanged method. Body (form-encoded):

- `username` — full current value.
- `avatarURL` — full current value (may be empty).
- `bio` — full current value (may be empty).
- `userSignature` — base64 of the user's detached signature over the
  canonical user payload built from `(username, fingerprint, avatarURL,
  bio)`, where `fingerprint` is the caller's active key fingerprint (read
  server-side from the caller's `users` row; also known to the client).

All four are required. Behind the existing signature-auth middleware.

Order of operations:

1. Authenticate the request (existing signature-auth middleware resolves
   the caller's `userID`).
2. Load the caller's `users` row (need `username`, `fingerprint`,
   `avatar_url`, `bio`, `user_signature`, `server_*`).
3. **No-op fast path.** If the submitted `userSignature` equals the row's
   stored `user_signature`, return the current identity record with 200.
4. Validate the submitted fields the same way the current handler does:
   `username` non-empty and ≤32 chars, unique if changed; `avatarURL`
   parseable, `http`/`https` only, live `HEAD` returns 200/301 if
   non-empty; `bio` ≤500 chars.
5. Reconstruct the user payload from `(username, fingerprint, avatarURL,
   bio)` via `buildUserIdentityPayload`; verify `userSignature` against
   the caller's active public key over those exact bytes.
6. Assign `signedAt = now()`. Build the server payload via
   `buildServerIdentityPayload` (superset with `userSignature` as a
   header). Countersign with the server's active signing key; record
   which key was used as `serverKeyFingerprint`.
7. In a single transaction, update the `users` row's `username`,
   `avatar_url`, `bio`, `user_signature`, `server_signature`,
   `server_signed_at`, and `server_fingerprint`.
8. Broadcast the existing realtime `UserUpdate` event.
9. Return the full identity record in the Proposal 04 flat + nested
   `server` response shape.

### Username changes

Username changes flow through this endpoint like any other field change.
The user signs over the new username (their payload includes `username`),
the server verifies, and the existing uniqueness check gates whether the
rename is accepted. There is no separate signed-username-change proposal
gated behind this one — signing over the full user payload covers
username by construction, and leaving it out would mean profile edits
could silently invalidate a user's own record with respect to their
stored username. Including it is both simpler and safer.

### Conflicts

Concurrent edits by the same user: last-writer-wins on the row. Both
requests carry independently valid signatures over their respective full
tuples; whichever transaction commits last is the record that survives,
and its `server_signed_at` reflects the winning update. No pending state,
no stale-init race.

### Storage

- No new tables. No new columns.
- All four identity columns already added by Proposal 04
  (`user_signature`, `server_signature`, `server_signed_at`,
  `server_fingerprint`) are updated in place on every accepted edit,
  alongside `username`, `avatar_url`, and `bio`.

## Work items

1. `services.go`: replace `UpdateUser` with an update that writes the
   full tuple (`username`, `avatar_url`, `bio`) plus the four identity
   columns in one transaction. Signature is
   `UpdateUser(userID, username, avatarURL, bio, userSig, serverSig,
   serverSignedAt, serverFingerprint)`.
2. `handlers.go`: `UpdateUser` handler gains the `userSignature` form
   field, the no-op fast path, the payload reconstruction + verification
   step, and the server-countersign step. Response switches from the
   current `User` shape to the Proposal 04 flat + `server` shape. Field
   validation (username length/uniqueness, avatar URL parsing/HEAD, bio
   length) is preserved verbatim.
3. `main.go`: no route change. Still `PUT /users/me`.
4. Client (SPA): `updateUser` in `spa/src/lib/services/api.ts` stops
   accepting a partial object. Callers pass the full post-edit tuple
   `{ username, avatarURL, bio }`; the client-side profile-edit UX skips
   the network call entirely when nothing changed. The client builds the
   canonical user payload from `(username, fingerprint, avatarURL, bio)`,
   signs it with the active key, and POSTs alongside the three fields.
   On success, the client replaces its stored identity record with the
   returned one.
5. Tests:
   - Round-trip: edit `bio`, verify `userSignature` over the returned
     user envelope and `serverSignature` over the server envelope,
     confirm the DB reflects the new bio and a bumped `server_signed_at`.
   - Round-trip on `username`: rename, verify both signatures cover the
     new username, uniqueness collision still returns 400.
   - No-op fast path: resubmit the current record's `userSignature` with
     the current field values → 200, `server_signed_at` unchanged, no
     realtime broadcast.
   - Tampering with `userSignature` in the response breaks
     `serverSignature` verification (same binding property tested in
     Proposal 04, exercised on the update path).
   - Bad `userSignature` at submit-time is rejected with a clear 400.
   - Auth: unsigned or wrong-key requests rejected by the existing
     middleware.

## Testing

- Unit + integration as above.
- e2e: signup → edit profile (bio, then username) → refresh → new values
  persist and both signatures verify against reconstructed envelopes.

## Risks

- **Breaking wire change for `PUT /users/me`.** Callers must now send
  the full tuple plus `userSignature`. SPA is under active development
  and updated in the same change; there are no external API consumers.
- Otherwise minimal. The record shape is already defined and tested by
  Proposal 04.

## Dependencies

- **Requires Proposal 04** for the two-payload identity record shape,
  the `identity.go` builders, and the `user_signature` /
  `server_signature` / `server_signed_at` / `server_fingerprint` schema
  columns.
- Also relies on Proposal 01's `BytesToSign` helper.

## Parallelism

- Can be developed in parallel with 04 (against a stub of the identity
  builders) but should land after 04 so it doesn't duplicate helper code
  or response-shape decisions.
- Independent of 01, 02, 03, 06, 07.
