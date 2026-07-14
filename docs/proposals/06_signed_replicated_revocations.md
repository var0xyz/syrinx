# Proposal 06 — Signed, replicated key revocations

## Status

Proposed. Rule 2 of the recovery trust model (see
[`takeover_recovery.md`](../takeover_recovery.md), "Monotonic revocation" and
"What must be built").

## Context

`RevokeKey` in `services.go` writes an unsigned row into
`user_key_revocations` carrying only a `reason` string. There is:

- **No signature** proving the revocation originated with the key owner.
- **No replication** to followers, so the revocation lives only on the wiped
  server and cannot be resubmitted during recovery — which is exactly the
  vector by which an attacker holding an old key could reinstate it.

## Scope

- Introduce a **signed revocation record** produced by the client and
  verified by the server on `RevokeKey`.
- Replicate the signed revocation to the revoker's followers via the same
  broadcast/propagation path used for new reeds.
- In normal operation, the server continues to store revocation state (for
  auth decisions today). Signatures pass through and are stored **on client
  devices** — the server does not need to persist the signature for its own
  purposes, but does need to accept and forward it.

## Non-goals

- Recovery ingestion of revocations is part of the later "unit of work"
  (recovery report-back), not this proposal.
- No changes to how PGP revocation certificates themselves are formatted
  inside a key; this is about the **standalone revocation statement**
  Syrinx records.

## Design

### Signed revocation record

Uses the shared `BytesToSign` envelope helper (see
[`README.md — Shared conventions`](./README.md)).

Headers:

- `fingerprint: <revokedKeyFingerprint>`
- `revokedAt: <RFC3339 UTC seconds, server-authoritative>`
- `serverID: <serverID>`
- `serverKeyFingerprint: <active server key fingerprint>`
- `type: revocation`
- `userID: <userID>`

Content: `<reason>` (free-form, may be multi-line, may be empty — same
argument as `bio` in Proposal 04: single legitimately-freeform field goes
in the content section).

Signed by the **owner's currently-active key**, i.e. **not** by the key
being revoked (which is what makes the record still trustworthy after the
old key is compromised, provided the owner had already rotated).

### Countersignature?

Optional but recommended: the server countersigns the revocation with its
active signing key so that during recovery, a resubmitted revocation can be
verified even if the revoker's key state on the restored server does not
yet include the newer owner key. Without a server countersignature we would
need the newer owner key to be restored *before* the revocation can be
verified — a plausible but fragile ordering constraint.

Recommendation: **do countersign**, matching the general pattern.

### Two-round flow

Same as Proposals 04/05:

- `POST /keys/{fingerprint}/revoke/init { reason }` → server returns a
  candidate payload with `revokedAt = now()` and its `serverKeyFingerprint`.
  Held in `pending_revocations` keyed by `(userID, fingerprint)`.
- `POST /keys/{fingerprint}/revoke/complete { userSignature }` → server
  verifies with the user's currently-active key, countersigns, writes
  `user_key_revocations` (existing row shape, plus `revoked_at` from the
  payload), sets `user_keys.revoked = TRUE`, and returns
  `{payload, userSignature, serverSignature}` to the client.

Both endpoints auth'd by the normal middleware.

### Replication

On successful `complete`, push a realtime message to followers:

```go
h.broadcastChan <- realtime.BroadcastMessage{
    Type:     realtime.NewRevocation,
    ServerID: serverID,
    UserID:   userID,
    Payload:  <the triple>,
}
```

- Add `NewRevocation` to `realtime`'s message-type enum.
- Followers persist the received triple in IndexedDB alongside cached
  profiles/reeds. **Every follower is a potential re-submitter during
  recovery.**
- No new server-side table: replication is passthrough. The revoker's own
  device also keeps a copy locally.

### Auth interaction

Middleware and services that today check `user_keys.revoked` continue to
work unchanged — the row is still flipped to `TRUE` at complete time. The
signature apparatus is layered on top for recovery/propagation.

## Work items

1. Schema: `pending_revocations` table (mirrors `pending_signups`).
2. `services.go`: split `RevokeKey` into `BeginRevocation` /
   `CompleteRevocation`. Revocation record builder assembles the header
   map + content string and calls `BytesToSign` (from Proposal 01) — no
   dedicated canonical helper.
3. `handlers.go`: two endpoints, wired in `main.go`.
4. `realtime/`: new `NewRevocation` message type; wire it through the
   existing broadcast fanout.
5. Client (SPA):
   - Two-round revocation UX.
   - Follower-side handler: on receipt of `NewRevocation`, persist the
     triple keyed by `(userID, fingerprint)`.
6. Tests:
   - Owner revokes; the server countersignature verifies.
   - `user_keys.revoked` is set; existing auth denial still functions.
   - Broadcast: two connected followers both receive the record and
     persist it.
   - Rejection: unsigned or wrong-signer complete requests fail.

## Testing

- Unit + integration.
- e2e: A revokes their old key; B (following A) has the revocation in
  IndexedDB after reconnect.

## Risks

- **Follower fanout load.** Same fanout as new-reed broadcasts — no worse.
- **Late signers.** If a user revokes a key with no currently-active
  replacement (edge case), the revocation cannot be signed by anything else
  than the key being revoked. Recommend disallowing this at the endpoint
  layer and requiring rotation first. Document in the endpoint.

## Dependencies

- **Requires Proposal 01's `BytesToSign` helper.**
- **Benefits from Proposal 04** for the two-round scaffolding
  (`pending_*` table pattern, TTL cleanup); can also proceed independently
  by duplicating that pattern and unifying later.
- Independent of 02, 03, 05, 09.

## Parallelism

- Independent of 02, 03, 05, 09.
- Shares scaffolding with 04/05 (two-round wiring, `pending_*` table
  pattern) — coordinate who lands first, whoever is second rebases onto
  the shared pieces.
