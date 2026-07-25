# Proposal 08 — Client signature validation and reed possession

## Status

Implemented (verify-then-store for public keys, revocations, and
deletion certs; reed ingest checks **author** signature). Remaining
work — verify **every** attestation on every signed resource before
store, plus attested possession — is
[signatures 09](signatures/09_verify_server_countersignatures.md).

## Context

Without client gates, a build can store reeds and register as a holder
without proving it verified anything. Allocations written on mere
delivery ACK pollute `reed_allocations` when the client skipped crypto
checks.

## Scope

- Define **client verification gates** for inbound signed artifacts
  (reeds, identity, distributed public keys, revocations).
- On successful reed verification: store locally, then **report
  possession** with a proof (server verifies its own countersignature).
- Failed verification: **discard silently**.

Landed under this proposal / adjacent work: key attestation verify
([07](07_server_signed_distributed_keys.md)), revocation verify
([06](06_signed_replicated_revocations.md) / [10](10_revocation_resource.md)),
deletion-cert verify ([deletion](deletion/README.md)), and author-sig
checks on reed receive. Follow-through for server countersignatures and
possession: [signatures 09](signatures/09_verify_server_countersignatures.md).

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
| Server countersignature over `(reedID, authorID, …, userSignature)` | Receiving client **and** server on allocation report | Attestation / proof of possession of the attested tuple |
| Holder request auth (existing middleware) | Server | Who is registering as holder |

Details for universal verify-before-store and possession live in
[signatures 09](signatures/09_verify_server_countersignatures.md).

### Other client gates

Apply “verify then store, else discard” to:

- **Distributed public keys** (Proposal 07) — **landed**.
- **Revocation / successor walks** (06 / 10; fanout 09) — **landed**
  for verify-before-store; fanout remains [09](09_revocation_fanout.md).
- **Deletion certs** — **landed**.
- **Identity records** and full reed attestation / possession —
  **signatures 09**.

## Dependencies

- Required 01/03 (reed countersig form + bindings) and 07 (key
  attestation before author-sig verify).
- Benefits from 04/05/06/10 for non-reed gates.

## Parallelism

- Follow-on: [signatures 09](signatures/09_verify_server_countersignatures.md).
