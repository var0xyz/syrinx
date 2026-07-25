# Proposal 08 — Client signature validation and reed possession

## Status

Implemented (verify-then-store for public keys, revocations, and
deletion certs; reed ingest checks **author** signature). Full
verify-before-store for every signed resource (including reed server
countersig and identity) landed in
[signatures 09](signatures/09_verify_server_countersignatures.md).
Attested possession from early drafts was **cancelled** there.

## Context

Without client gates, a build can store reeds without proving it
verified anything. Local trust depends on verify-before-store.

## Scope

- Define **client verification gates** for inbound signed artifacts
  (reeds, identity, distributed public keys, revocations).
- On successful reed verification: store locally; allocate via existing
  `DATA_ACK` after delivery (attested possession cancelled in
  [signatures 09](signatures/09_verify_server_countersignatures.md)).
- Failed verification: **discard silently** (and `DATA_INVALID`, no
  allocation).

Landed under this proposal / adjacent work: key attestation verify
([07](07_server_signed_distributed_keys.md)), revocation verify
([06](06_signed_replicated_revocations.md) / [10](10_revocation_resource.md)),
deletion-cert verify ([deletion](deletion/README.md)), and author-sig
checks on reed receive. Universal verify-before-store completed in
[signatures 09](signatures/09_verify_server_countersignatures.md)
(attested possession cancelled).

## Non-goals

- No change to how `SignReed` countersigns (Proposals 01/03).
- Server does **not** verify the author signature over reed content on
  the allocation path.
- No recovery report-back.

## Design

### Trust split (reeds)

| Check | Who | Purpose |
|-------|-----|---------|
| Author signature over content | Receiving client | Content authenticity |
| Server countersignature over `(reedID, authorID, …, userSignature)` | Receiving client | Attestation before local store |
| Holder request auth (existing middleware) | Server | Who is registering as holder |

Universal verify-before-store lives in
[signatures 09](signatures/09_verify_server_countersignatures.md).
Allocation remains ACK-after-delivery (attested possession cancelled).

### Other client gates

Apply “verify then store, else discard” to:

- **Distributed public keys** (Proposal 07) — **landed**.
- **Revocation / successor walks** (06 / 10; fanout 09) — **landed**
  for verify-before-store; fanout remains [09](09_revocation_fanout.md).
- **Deletion certs** — **landed**.
- **Identity records** and full reed attestation — **signatures 09**.

## Dependencies

- Required 01/03 (reed countersig form + bindings) and 07 (key
  attestation before author-sig verify).
- Benefits from 04/05/06/10 for non-reed gates.

## Parallelism

- Completed follow-on: [signatures 09](signatures/09_verify_server_countersignatures.md).
