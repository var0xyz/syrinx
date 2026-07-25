# Proposal 06 — Signed key revocations

## Status

Implemented (user + server signatures on `user_key_revocations`; create via
`RevokeKey`; wire/storage shape finalized in
[10](10_revocation_resource.md)). Follower fanout is
[09](09_revocation_fanout.md).

## Context

`RevokeKey` historically wrote an unsigned row into
`user_key_revocations` carrying only a `reason` string — no proof the
revocation originated with the key owner, and nothing a peer could
resubmit after a server wipe.

## Scope

- Introduce a **signed revocation record** produced by the client and
  verified by the server on `RevokeKey`.
- Server countersigns and persists signatures with the revoke row (for
  auth decisions and later fetch / fanout).
- Client stores the signed record locally (owner device).

## Non-goals

- Follower replication / realtime fanout — [09](09_revocation_fanout.md).
- Recovery ingestion of revocations (recovery report-back).
- No changes to how PGP revocation certificates themselves are formatted
  inside a key; this is about the **standalone revocation statement**
  Syrinx records.
- Resource split / GET path / `Key.revoked` boolean — landed as
  [10](10_revocation_resource.md) (supersedes early wire sketches here).

## Design

### Signed revocation record

Uses the shared `BytesToSign` envelope helper (see
[`README.md — Shared conventions`](./README.md)).

Canonical payloads and signer rule (**old key being revoked** signs the
user attestation; server countersigns) are locked in
[10](10_revocation_resource.md). Early drafts here suggested a two-round
init/complete flow and “active key” signer; those were superseded by 10
(single `POST .../revoke` with `userSignature`; no server-authored fields
in the user payload).

### Auth interaction

Middleware and services that check revoke state continue to key off
`user_key_revocations` presence. Signatures are layered on for
verification, fetch, and (via 09) propagation.

## Work items

Landed (with 10):

1. Persist user + server signatures on `user_key_revocations`.
2. `RevokeKey` verifies `userSignature`, countersigns, returns key with
   boolean `revoked`.
3. Client rotation / pending-revoke UX stores the signed record.
4. Tests: owner revoke countersign verifies; auth denial still functions;
   unsigned / wrong-signer requests fail.

Fanout / follower persist — [09](09_revocation_fanout.md).

## Dependencies

- Required Proposal 01's `BytesToSign` helper.
- Wire/API shape coordinated with [10](10_revocation_resource.md).

## Parallelism

- Fanout is independently shippable as [09](09_revocation_fanout.md).
