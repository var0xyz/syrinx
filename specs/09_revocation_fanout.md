# Proposal 09 — Revocation realtime fanout + catch-up

## Status

Proposed.

## Depends on

[06](06_signed_replicated_revocations.md) (signed revoke),
[10](10_revocation_resource.md) (resource / wire shape).

## Context

Signed revocations are persisted on the server and fetchable via
`GET .../keys/{fingerprint}/revocation` ([10](10_revocation_resource.md)).
Followers still only learn about a peer’s revoke by fetching keys or
revocations themselves. Without push (and offline catch-up), a wiped
server cannot be reseeding from follower-held revoke proofs — the
recovery vector [06](06_signed_replicated_revocations.md) called out.

Deletion already solved the same class of problem for removal certs
([deletion 04](deletion/04_reed_fanout.md)): live fanout + `SYNC_REQUEST`
catch-up over the existing realtime machinery. Revocations should follow
that pattern, not invent a separate outbox.

## Scope

- On **first** successful `RevokeKey` accept: fan out the **full signed
  revocation resource** (proposal 10 wire shape) to the same audience
  class as new-reed / removal distribution for that author (online
  followers / broadcast / profile subscribers as applicable).
- Add a realtime message type (e.g. `NewRevocation` / `KEY_REVOKED`) and
  wire it through `pending_events` + direct send, same path as
  `REED_REMOVED`.
- **Catch-up on `SYNC_REQUEST`:** deliver any revocations the viewer is
  missing for users they follow (or otherwise already receive key/reed
  traffic for). Prefer a small diff query (e.g. followed users’
  `user_key_revocations` not yet ACK’d / not yet held) over replaying
  the entire history every connect.
- Do **not** re-fanout on idempotent revoke retries.
- SPA: on receipt (live or catch-up), verify user + server signatures
  (same gates as [08](08_client_signature_validation.md) /
  [signatures 09](signatures/09_verify_server_countersignatures.md) and
  the revocations IndexedDB store), then persist; flip local
  `publicKeys.revoked` for that fingerprint. **Every follower is a
  potential re-submitter during recovery.**
- No new durable “pending revocation notifications” table unless the
  reed-removal catch-up pattern proves insufficient — reuse
  `pending_events` / ACK semantics.

## Non-goals

- Changing how revokes are created or signed ([06](06_signed_replicated_revocations.md),
  [10](10_revocation_resource.md)).
- Recovery report-back ingestion of revocations (recovery feature).
- Changing auth middleware (server still decides revoke from
  `user_key_revocations` rows).

## Design

### Live path

`RevokeKey` first insert → `BroadcastMessage{NewRevocation, cert}` →
existing broadcast / `pending_events` dispatch to online recipients.
Payload is server-sourced (the stored revocation resource), not
holder-relayed.

### Catch-up path

On `SYNC_REQUEST`, compute missing revocations for the viewer’s follow
set (and any other audiences already used for reed fanout). Deliver the
full cert; on `DATA_ACK`, mark delivered the same way other event types
do. Offline peers must not depend on being connected at revoke time.

### Client

- Handler mirrors deletion holders: verify → put in `revocations` store
  → set `publicKeys.revoked = true` for that fingerprint.
- Failed verify → discard / `DATA_INVALID`; do not corrupt local key
  state.

## Work items

1. `realtime/`: `NewRevocation` (or `KEY_REVOKED`) message type; fanout
   from `RevokeKey` first accept only.
2. Catch-up query + delivery on `SYNC_REQUEST`; ACK clearing.
3. SPA: WS / sync handler → verify → IndexedDB; update key boolean.
4. Tests: two connected followers receive and persist; reconnect catch-up
   delivers a missed revoke; idempotent revoke does not double-fanout;
   bad sig discarded.

## Testing

- Integration: A revokes; B (following, online) gets the cert and stores
  it.
- Catch-up: B offline during revoke → `SYNC_REQUEST` → cert arrives once.
- Negative: tampered payload → no local store / `DATA_INVALID`.

## Risks

- **Fanout load** — same class as new-reed / removal broadcasts.
- **Audience definition** — keep aligned with reed/removal fanout so
  operators do not reason about a third distribution set.
- **Catch-up volume** — long-lived accounts with many historical
  revokes; bound the diff to “not yet held / not yet ACK’d” rather than
  full table scans per connect.

## Dependencies

- Requires [06](06_signed_replicated_revocations.md) + [10](10_revocation_resource.md)
  (signed resource already on the server).
- Benefits from [08](08_client_signature_validation.md) /
  [signatures 09](signatures/09_verify_server_countersignatures.md)
  verify gates (revoke path already partially landed).
- Pattern reference: [deletion 04](deletion/04_reed_fanout.md).

## Parallelism

- Independent of invites / tip check / device binding.
- Can land whenever convenient after 10; does not block recovery claim /
  reed report-back, but recovery **completeness** for monotonic
  revocation is weaker until followers hold copies.
