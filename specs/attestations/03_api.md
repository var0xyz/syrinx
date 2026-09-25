# Attestations 03 — Create / withdraw / list API

## Status

Proposed.

## Depends on

[01](01_schema.md), [02](02_payload.md)

## Context

Transport for vouches. Shapes follow [likes 03](../likes/03_api.md): signed
create, idempotent replay, server countersigns once.

## Scope

- `POST /vouches`, `DELETE /vouches/{subjectKeyID}`, and three list reads
  (inbound, outbound, and the caller's own audit list).

## Non-goals

- Path finding, which is client-side and needs no endpoint
  ([05](05_trust_paths.md)).

## Design

### `POST /vouches`

Authenticated, signed. Body:

```json
{
  "subjectUserID": "bob@peer5678",
  "subjectKeyID":  "bob@peer5678/9f3c…",
  "userSignature": { "id": "alice@home1234/4a1e…", "armor": "<base64>" }
}
```

Server runs the verification order in
[02](02_payload.md#verification-order-server-on-create), countersigns,
stores, returns **200** with the full cert including the `server` block.

**Idempotent.** Re-posting the same `(voucher, subjectKeyID)` returns the
stored cert unchanged rather than re-countersigning, matching likes. A
withdrawn row being re-vouched clears `withdrawn_at` and stores the new
signature — re-verification after a retraction is legitimate.

Errors: `400` malformed or self-vouch, `401` signature failure, `404`
unknown `subjectKeyID`, `409` subject key revoked.

The voucher's own key need only be able to sign; its revocation state is not
checked, because a revoked voucher key does not invalidate vouches
([04](04_revocation.md#a-vouch-belongs-to-the-person-not-the-key)).

### `DELETE /vouches/{subjectKeyID}`

Authenticated, **signed** — unlike unlike. Body carries the withdrawal
signature ([02](02_payload.md#withdrawal-payload)). Sets `withdrawn_at` and
`withdrawal_signature_id`; the row is retained
([01](01_schema.md#withdrawal-is-a-state-not-a-delete)).

The subject key id is a single full id in the path, never split or
recomposed from parts.

Returns **200** with the withdrawal cert, or **404** if no such vouch.

### `GET /users/{userID}/vouches`

Vouches **for** this user. Public, unauthenticated — trust evidence is
useless if you must already be logged in to see it.

Returns live vouches with full signature blocks so callers can verify
independently, plus `withdrawn` and `void` flags computed per
[04](04_revocation.md). Paginated; a popular account may have many.

```json
{
  "vouches": [
    {
      "voucherUserID": "alice@home1234",
      "voucherKeyID":  "alice@home1234/4a1e…",
      "subjectUserID": "bob@peer5678",
      "subjectKeyID":  "bob@peer5678/9f3c…",
      "userSignature":   { "id": "…", "armor": "…" },
      "serverSignature": { "id": "…", "armor": "…", "timestamp": "…" },
      "void": false,
      "voidReason": null
    }
  ],
  "nextCursor": null
}
```

`void`/`voidReason` are **hints**, like every other server-computed field
([M9](../../RISKS.md)). Clients recompute them. They exist so a client can
skip fetching revocation state for obviously-dead edges, not so it can trust
the answer.

### `GET /users/{userID}/vouches/outbound`

Vouches **made by** this user. This is the edge direction path finding walks
([05](05_trust_paths.md)), and it is the expensive one: a client exploring to
depth 2 fetches this for every contact it trusts.

Same shape, same caveats.

### `GET /vouches/mine`

Authenticated. Every vouch the caller has made, **newest first by server
countersignature timestamp**, including withdrawn and stale ones. Drives the
audit list ([07](07_spa_trust_display.md#your-vouches-chronologically)).

Ordering by the server timestamp is deliberate: it is the one time in the
record the caller's own (possibly compromised) client did not choose, so a
suspicious burst cannot be hidden by backdating.

Each row carries `voucherKeyID`, so the client can group by signing key — the
natural unit of "everything signed while that key was live". Paginated.

This is a separate endpoint from `/users/{id}/vouches/outbound` even though
the data overlaps: that one is public and live-only, this one is authenticated
and includes withdrawn rows the caller is entitled to audit but nobody else
needs to see.

### Rate limiting

Open question in [README](README.md#open-questions). A user can only vouch
with their own key, so volume is bounded by their willingness to sign, and a
vouch from an account nobody trusts has no effect on anyone's path
computation. The realistic abuse is a compromised *well-trusted* account
spraying vouches — which rate limiting barely helps, and which withdrawal
plus key revocation addresses properly.

Recommendation: no dedicated limit in v1, beyond whatever global per-user
request limiting exists. Revisit with evidence.

### Federation

Deferred to a later step, deliberately. A vouch for a user on another server
is storable here ([01](01_schema.md)), but propagating vouches *between*
servers needs the relay machinery in
[federation](../federation/README.md) and a decision about whether a peer's
vouch list is fetched on demand or pushed. v1: a client fetches vouches from
the server that hosts the subject, which it already knows from the
canonical id.

## Testing

- Create → idempotent replay returns identical cert, no second countersign.
- Self-vouch rejected.
- Vouch for revoked key rejected `409`.
- Withdraw → row retained, absent from live list, present with
  `withdrawn: true` on the audit read.
- Withdraw after rotation: signature by the *current* key verifies.
- Unauthenticated `GET` on the public reads succeeds.
- `GET /vouches/mine` requires auth, orders by server timestamp, and includes
  withdrawn rows absent from the public read.
- Create succeeds when the voucher's own key is revoked but still signs.
