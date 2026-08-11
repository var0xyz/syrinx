# Likes 03 — Like / unlike API (idempotent)

## Status

Proposed.

## Depends on

[01](01_schema.md), [02](02_payload.md)

## Context

Submit a signed like, get back the countersigned cert (including on
replay), following the same idempotent-submit shape as reed removal
([deletion 03](../deletion/03_reed_api.md)). Unlike reed removal, this
resource can transition back and forth (liked → unliked → liked), so the
API needs a `POST`/`DELETE` pair rather than one write-once endpoint —
but the two verbs are **not symmetric**: `POST` is a signed submit
exactly like every other signed resource in this app; `DELETE` is a
plain authenticated hard delete with **no signature at all**
([00 § Unlike is unsigned](00_design.md#unlike-is-unsigned)).

## Scope

- `POST /reeds/{authorID}/{reedID}/like` — submit a signed like.
- `DELETE /reeds/{authorID}/{reedID}/like` — unlike: unsigned, hard
  deletes the `reeds_liked` row.
- Both authenticated as the **liker**, not the author (any authenticated
  user may like any reed, including their own —
  [00 § Resolved #4](00_design.md#resolved)).
- `POST`: verify user sig; countersign **once**; persist via
  [01](01_schema.md); `like_count += 1`; return cert with
  `type: "reed_like"`.
- `DELETE`: no signature to verify; delete the row if present;
  `like_count -= 1` only if a row was actually deleted; return **200**
  either way.
- Idempotent: `POST` while already liked → return the stored like cert
  unchanged, no re-countersign, no double-count. `DELETE` while not
  liked → **200**, no-op, no error.

## Non-goals

- Realtime fanout (04).
- SPA queues + button (05).

## Design

### Submit like

`POST /reeds/{authorID}/{reedID}/like` with form field `signature` (user)
over `identity.BuildReedLikeUserPayload(serverID, authorID, reedID, likerID)`.
`likerID` is the authenticated caller (path carries the reed's author,
**not** the liker — the liker is implicit in auth, exactly like the
existing `POST /reeds` publish endpoint uses the authenticated caller as
`userID`, never a path/body-supplied one).

Server:

1. Verify the reed `(authorID, reedID)` exists and is not removed.
2. Verify the user signature against the liker's active key.
3. If a `reeds_liked` row already exists for `(likerID, authorID, reedID)`
   → return the **existing** stored cert, **200**, no counter change
   (idempotent replay).
4. Else: countersign via `identity.BuildReedLikeServerPayload(...)`,
   insert the row, `reeds.like_count += 1`, same TX. Return **200** + new
   cert.
5. Emit the realtime like-count update ([04](04_realtime.md)), carrying
   the incremented count.

### Unlike

`DELETE /reeds/{authorID}/{reedID}/like` — **authenticated, unsigned.**
No `signature` field, no payload to build or verify. The path's
`authorID`/`reedID` plus the authenticated caller's user ID (as
`likerID`) fully identify the row to delete; nothing here needs a
cryptographic attestation because deleting your own like requires no
proof beyond "you are logged in as the user who owns that row" — the
same authorization bar the server already applies to every
session-scoped write.

Server:

1. `DELETE FROM reeds_liked WHERE liker_user_id=$1 AND author_user_id=$2 AND reed_id=$3`.
2. If a row was deleted: `reeds.like_count -= 1` (floor 0), same TX.
   If no row existed: no counter change.
3. Return **200** either way — `{ "liked": false }`. There is no cert:
   nothing was signed, nothing was countersigned, nothing durable needs
   to be replayed back to the client on a retry beyond "yes, you are now
   in the not-liked state."
4. If a row was actually deleted, emit the realtime like-count update
   ([04](04_realtime.md)), carrying the **decremented** count, to every
   current subscriber of that reed. This only updates the shared count
   display — nobody's like button is affected by it, including the
   unliker's own other open tabs; each device's button reflects only its
   own locally-persisted `likedReeds` state (client-side detail in
   [05](05_spa_pending_and_button.md)). If no row existed, emit nothing
   (no change occurred).

### Response shapes

Like (first time or replay):

```json
{
  "type": "reed_like",
  "serverID": "syrinx-example",
  "authorID": "Ab3xY9pQ…",
  "reedID": "0v4…",
  "likerID": "R90a8qxg…",
  "signature": "<base64 user detached sig>",
  "server": {
    "id": "syrinx-example",
    "fingerprint": "A1B2C3…",
    "algorithm": "PGP+base64",
    "signature": "<base64 server countersig>",
    "timestamp": "2026-08-11T12:04:00Z"
  }
}
```

Unlike (row existed or not — same shape either way, since there is
nothing to distinguish a first successful delete from a replay of one):

```json
{ "liked": false }
```

### `GET` "am I liking this" (optional convenience endpoint)

`GET /reeds/{authorID}/{reedID}/like` → `{ "liked": true|false, "likeCount": N }`
for the authenticated caller. Not strictly required if the reed-detail
fetch is extended to include this instead (implementation choice — either
is fine, pick whichever avoids an extra round trip given how reed detail
is currently loaded); flagged here so the endpoint surface is documented
either way.

## Test plan

- [ ] First like → cert stored; response `type: "reed_like"` + `server` block
- [ ] Replay same like → identical `server.signature` / timestamp; `like_count` unchanged
- [ ] Unlike an existing like → `{ "liked": false }`; row hard-deleted; `like_count` decremented
- [ ] Unlike with **no** signature field present in the request → succeeds (confirms the endpoint never requires one)
- [ ] Replay unlike (nothing to unlike) → `{ "liked": false }`, no error, no count change
- [ ] Self-like (`likerID == authorID`) succeeds
- [ ] Like against a removed reed → rejected
- [ ] Like signature mismatch / tampered payload → rejected
- [ ] `like_count` invariant holds through like → unlike → like sequences
- [ ] Unlike does not require, accept, or verify a signature under any circumstance — confirm no code path checks one
