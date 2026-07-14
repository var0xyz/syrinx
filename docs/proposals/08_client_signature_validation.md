# Proposal 08 — Client signature validation and reed possession

## Status

Proposed. Hardens normal operation so clients only retain
cryptographically attested data, and so `reed_allocations` cannot be
stuffed with fake holdings. Builds on Proposals 01/03 (reed countersig),
04/05 (identity), 07 (server-signed keys), and benefits from 06
(revocations).

## Context

Today a client can store reeds and register as a holder without proving
it verified anything. Allocations are written when content is delivered
or acknowledged, without a proof that the claimant holds an
**attested** reed tuple. A client that skips verification (bug, hostile
build, or confused deputy) can pollute local state and the allocation
table.

## Scope

- Define the **client verification gates** for inbound signed artifacts
  (reeds, identity records, distributed public keys).
- On successful reed verification: store locally, then **report
  possession** to the server with a proof.
- Server updates `reed_allocations` only after verifying its **own**
  countersignature over the submitted tuple.
- Failed verification: **discard silently** (log locally; do not store;
  do not report possession).

## Non-goals

- No change to how `SignReed` countersigns (already binds `reedID`,
  `authorID`, server key fingerprint — Proposals 01/03).
- Server does **not** verify the author signature over reed content on
  the allocation path (and still does not at `SignReed` time).
- No recovery report-back. This is live-operation hardening.

## Design

### Trust split (reeds)

| Check | Who | Purpose |
|-------|-----|---------|
| Author signature over content | Receiving client | Content authenticity |
| Server countersignature over `(reedID, authorID, …, userSignature)` | Receiving client **and** server on allocation report | Attestation / proof of possession of the attested tuple |
| Holder request auth (existing middleware) | Server | Who is registering as holder |

The server countersignature binds `userSignature` into the attested
record. If it verifies, those fields were not swapped. The reed **body**
is not under the server signature — content integrity is the client's
author-sig check, performed **before** any allocation report.

**Server assumption on allocation:** if the server countersignature
verifies over the submitted tuple, the author-signature field in that
tuple was not tampered with relative to what this server attested. The
server does not re-verify the author signature; it assumes a correct
client already did before reporting.

### Reed receive path (client)

When a reed arrives (broadcast, relay, or fetch):

1. Ensure the author's public key is available (fetch if needed;
   verify Proposal 07's server key-attestation before caching).
2. Verify the **author** detached signature over the reed content with
   that key.
3. Verify the **server** countersignature (rebuild via `BytesToSign` /
   the reed countersign headers; select server public key by
   `server.fingerprint`).
4. If either check fails → discard silently; do not store; do not
   notify the server.
5. If both succeed → store in IndexedDB, then report possession.

### Possession report (client → server)

Authenticated request (existing signature middleware) carrying the
attested tuple, e.g.:

- `reedID`
- `authorID`
- `userSignature` (as countersigned)
- `server` block (`id`, `fingerprint`, `timestamp`, `algorithm`,
  `signature`)

No reed body required for the proof — possession here means possession
of the **attested signature tuple** (same object the server can
verify). Relay still depends on holders actually serving content;
`RELAY_MISS` / `DATA_INVALID` remain the operational backstop for
holders who have signatures but not body.

### Possession verify (server)

1. Confirm request auth → holder `userID`.
2. Rebuild the exact countersign payload from the submitted fields.
3. Load the server public key for `server.fingerprint` (historical key
   allowed).
4. Verify `server.signature`.
5. Require `server.id ==` this server's `serverID`.
6. Require `authorID` matches `reeds.user_id` for `reedID` if the reed
   row exists (reject allocations for unknown / mismatched reeds).
7. On success: upsert `reed_allocations (reed_id, holder_user_id)`.

Do **not** verify the author signature on this path.

### Other client gates (same proposal)

Apply the same “verify then store, else discard” rule to:

- **Identity records** (user + server payloads from Proposals 04/05).
- **Distributed public keys** (Proposal 07 attestation), including the
  armor-vs-fingerprint check and the existing local-armor mismatch
  guard on revocation updates.
- **Revocation / successor walks** (Proposal 06): verify signed
  revocation before updating local key state; track visited
  fingerprints and abort on a cycle.

## Work items

1. Document and implement a shared client verifier module (reed,
   identity, key attestation).
2. Wire reed ingest paths (broadcast / relay / load) through the
   verifier; silent discard on failure.
3. New authenticated endpoint (or realtime message) for possession
   report; server verifies self-countersignature; writes allocation.
4. Stop writing allocations on mere delivery ACK without proof (migrate
   existing call sites).
5. Tests:
   - Valid reed → stored + allocation row for holder.
   - Tampered content → discard, no allocation.
   - Tampered `userSignature` with broken server sig → discard / reject
     report.
   - Valid tuple, wrong holder auth → rejected by middleware.
   - Replay of tuple onto different `reedID` → server verify fails.

## Testing

- Unit for payload rebuild + verify.
- Integration for allocation endpoint.
- e2e: A publishes; B receives, verifies, reports; allocation visible;
  corrupted broadcast payload never lands in B's IndexedDB.

## Risks

- **Signatures ≠ body.** Allocation proves the attested tuple, not that
  the holder can serve content. Accept for v1; relay miss handling
  covers bad holders.
- **Silent discard UX.** Correct for hostile/corrupt data; ensure logs
  are detailed enough to debug false rejects (clock skew, wrong server
  key selection).

## Dependencies

- **Requires 01/03** (reed countersig form + bindings).
- **Requires 07** for trusting fetched author keys before author-sig
  verify.
- **Benefits from 04/05/06** for the non-reed client gates; those
  sections of this proposal can land incrementally as those artifacts
  exist.

## Parallelism

- Server possession endpoint can start once 01/03 exist.
- Full reed ingest gate wants 07 first (otherwise author-key fetch is
  still an unsigned trust).
- Identity/revocation client gates track 04–06.
