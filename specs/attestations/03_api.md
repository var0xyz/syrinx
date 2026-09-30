# Attestations 03 — Create / withdraw / list API

## Status

Implemented.

## Depends on

[01](01_schema.md), [02](02_payload.md)

## Context

Transport for vouches. Shapes follow [likes 03](../likes/03_api.md): signed
create, idempotent replay, server countersigns once.

## Scope

- `POST /vouches`, `DELETE /vouches/{subjectKeyID}`,
  `GET /users/{userID}/vouches/{vouchID}`, and two list reads (vouches for a
  user, and the caller's own audit list).

## Non-goals

- Transitive trust paths beyond depth 1, which v1 does not compute
  ([05](05_trust_paths.md)).

## Design

### `POST /vouches`

Authenticated, signed. Body:

```json
{
  "subjectKeyID":  "bob@peer5678/9f3c…",
  "userSignature": { "id": "alice@home1234/4a1e…", "armor": "<base64>" }
}
```

The subject is not named separately: a key id is owner-prefixed, so the
server derives `subject_user_id` from `subjectKeyID` rather than accepting
one that could disagree with it.

Server runs the verification order in
[02](02_payload.md#verification-order-server-on-create), countersigns,
stores, returns **200** with the full cert including the `server` block.

**One live vouch per `(voucher, subjectKeyID)`.** Verifying a key you have
already verified asserts nothing new, so a second signature over the same
pair is refused with `409`. The note is not consulted: what matters is that
the key is already verified, not what was written about it.

Re-posting the *same* signature returns the stored cert unchanged rather
than re-countersigning — that is the offline queue retrying, not a second
verification.

Your own key rotating does not reopen it. A vouch survives its voucher's
key being revoked ([04](04_revocation.md)), so the original still stands and
re-signing with the new key would add nothing.

A withdrawn row being re-vouched clears `withdrawn_at` and stores the new
signature — re-verification after a retraction is legitimate.

Errors: `400` malformed or self-vouch, `401` signature failure, `404`
unknown `subjectKeyID`, `409` subject key revoked or already verified.

The voucher's own key need only be able to sign; its revocation state is not
checked, because a revoked voucher key does not invalidate vouches
([04](04_revocation.md#a-vouch-belongs-to-the-person-not-the-key)).

### `DELETE /vouches/{subjectKeyID}`

Authenticated, **signed** — unlike unlike. Body carries the withdrawal
signature ([02](02_payload.md#withdrawal-payload)). The server verifies it,
**countersigns** ([02](02_payload.md#withdrawal-server-payload)), stores both
signatures, and deletes the row from the active set; the history row is
retained ([01](01_schema.md#withdrawal-is-a-state-not-a-delete)).

The subject key id is a single full id in the path, never split or
recomposed from parts.

Returns **200** with the whole vouch, its `withdrawal` object now populated
with both signatures, or **404** if no live vouch stands. Replaying a
withdrawal returns the stored one unchanged rather than countersigning again.

There is no `withdrawnAt` field anywhere on the wire: the retraction time is
`withdrawal.serverSignature.timestamp`, derived like every other server time.

### `GET /users/{userID}/vouches`

Vouches **for** this user. Readable by any authenticated caller, like
`/keys/{id}` and `/users/{id}/info` — every `/api/*` read on this server is
signature-authenticated, and vouches are no more sensitive than the keys
they are about. "Public" here means any signed-in user may read anyone's
vouches, not that the endpoint is open to the world.

Returns live vouches with full signature blocks so callers can verify
independently, plus `withdrawn` and `void` flags computed per
[04](04_revocation.md). Paginated; a popular account may have many.

```json
{
  "vouches": [
    {
      "id":            "alice@home1234/0192f0c1-…",
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

Every vouch has an `id` of the canonical form
`voucherUserID@serverID/uuidv7` — the voucher authored the statement, so the
id is owner-prefixed by them, like a reed or a key. It is minted server-side
at create time and never changes, including when a withdrawn vouch is
re-vouched, so a client's local record stays addressable across the row's
whole life.

The format is **validated, not assumed**. Before a vouch is stored the server
checks the id parses as `userID@serverID/entity`, that the owner it names is
the voucher, that the server it names is this one, and that the entity is a
UUIDv7 — a v4 is rejected, since the id's time ordering is part of its
meaning. A malformed id is a `400`, and the check lives at the store rather
than the handler so no future caller can bypass it. On the read path the id
arrives from a client, so it is shape-checked before any lookup: an id that
cannot name a vouch this server minted is refused without touching the
database.

`void`/`voidReason` are **hints**, like every other server-computed field
([M9](../../RISKS.md)). Clients recompute them. They exist so a client can
skip fetching revocation state for obviously-dead edges, not so it can trust
the answer.

### `GET /users/{userID}/vouches/{vouchID}`

One vouch, with its full signature blocks. Authenticated and readable by
anyone signed in, like the lists.

The two path params are **different identities**, and neither is derived from
the other. `{userID}` is the canonical id of the vouch's **subject** — the
user who was vouched for, which is how the client reached this vouch in the
first place. `{vouchID}` is the vouch's own full canonical id, owned by the
**voucher** who signed it (`voucherUserID/uuidv7`). Both arrive whole and are
used as-is; nothing is split apart or reassembled from fragments. The server
rejects the pair with **404** if that vouch does not name that subject, so a
vouch cannot be served under a subject it says nothing about.

This is the endpoint the reconcile loop is built on
([07](07_spa_trust_display.md#verifying-what-the-server-reports)): a client
holds a set of already-verified vouch ids in local storage, diffs it against
the id list on `/users/{userID}/info`, and fetches only the ids it has never
seen. Steady state is therefore zero fetches, and a first visit is one fetch
per vouch — bounded by the number of vouches that actually exist, not
repeated on every view.

Returns **404** for an unknown id. A withdrawn vouch is still served here,
with `withdrawn: true` and its withdrawal signature, so a client that
cached the vouch can verify the retraction rather than infer it from the id
having vanished from the list.

### Vouch ids on `/users/{userID}/info`

`/info` gains `vouchIDs`: the ids of the **live** vouches naming this user's
current key, and nothing else — no signatures, no names, no count to trust.
It is a list of ids precisely because ids are cheap to ship and useless to
forge on their own: an id the client has not verified buys the server
nothing, since no mark appears until the client has fetched and checked the
cert behind it.

Withdrawn vouches are absent. A client that holds one locally sees its id
missing from the list and fetches it once to obtain the signed withdrawal
([§ `GET /users/{userID}/vouches/{vouchID}`](#get-usersuseridvouchesvouchid)), so a retraction is
confirmed by a signature rather than by an omission the server controls.

### `GET /vouches`

Authenticated. Every vouch the caller has made, including withdrawn and
stale ones. Paginated.

**Recovery only.** The audit list does not read this endpoint
([07](07_spa_trust_display.md#your-vouches-chronologically)); it renders
from the local store, which is the only copy nothing but this device can
edit. This read exists for a device that has lost its store and has no
export to restore from.

That distinction matters because **a list from the server is not an audit**.
Every cert in it is self-verifying — the caller's own signature is over
`(voucherKeyID, subjectKeyID)`, and the server cannot forge one it has no
key for — but no list of valid certs proves it is *complete*. A server that
wants a vouch forgotten omits the row, and the same argument that rejects
server-reported counts ([07](07_spa_trust_display.md#verifying-what-the-server-reports))
rejects this. A compromised server could also serve a cert without the
withdrawal it has since acquired, showing a retracted vouch as live.

So recovery is a **degraded mode**, and the UI says so rather than
presenting a recovered list as the caller's history. What it restores is a
lower bound: everything in it is really yours, and something of yours may
be missing.

Each row carries `voucherKeyID`, so a recovered list can still be grouped by
signing key — the natural unit of "everything signed while that key was
live".

This is a separate endpoint from `/users/{id}/vouches` even though the data
overlaps: that one is public, live-only and lists vouches *for* a user, while
this one is authenticated, lists vouches the caller *made*, and includes
withdrawn rows they are entitled to audit but nobody else needs to see. It is
the one read that walks history rather than the active set — after revoking a
key you need to see what it signed, and a vouch since withdrawn is still
evidence.

### The re-vouch cooldown

Re-verifying someone after withdrawing is allowed — a mistaken withdrawal, or
a genuine re-verification, both deserve it. But history is append-only
([01](01_schema.md#history-is-append-only-the-active-set-is-separate)), so an
unbounded withdraw/re-vouch cycle would grow the table without limit.

`POST /vouches` therefore refuses a vouch for a `(voucher, subject key)` pair
vouched within the last **24 hours**, live or withdrawn, with **429** and a
`Retry-After`. One verification per key per day is far above any honest rate:
verification happens in person.

This is the only rate limit. General vouch spraying needs none — a user can
only vouch with their own key, and a vouch from an account nobody trusts
affects nobody's path computation. The realistic abuse is a compromised
*well-trusted* account, which rate limiting barely helps and which withdrawal
plus key revocation addresses properly.

### Federation

Specified in [08](08_federation.md). A vouch for a user on another server is
storable here ([01](01_schema.md)); the reads on this page route to the
server that hosts the **subject**, which the client already knows from the
canonical id. Fetched on demand, never pushed — the voucher's server notifies
the subject's on create, and the reconcile loop covers a dropped notify.

## Testing

- Create → idempotent replay returns identical cert, no second countersign.
- A vouch id that is malformed, owned by another user, from another server,
  or carrying a non-v7 UUID is rejected before storage.
- Self-vouch rejected.
- Vouch for revoked key rejected `409`.
- Withdraw → row retained, absent from live list, present with
  `withdrawn: true` on the audit read.
- Withdraw after rotation: signature by the *current* key verifies.
- An authenticated `GET` on another user's vouches succeeds; an unsigned
  request is rejected by the auth middleware like any other read.
- `GET /vouches` requires auth, orders by server timestamp, and includes
  withdrawn rows absent from the public read.
- `DELETE` returns both withdrawal signatures; a replay countersigns once.
- A re-vouch inside 24h is refused with `429`; after it, a new id is minted.
- Create succeeds when the voucher's own key is revoked but still signs.
