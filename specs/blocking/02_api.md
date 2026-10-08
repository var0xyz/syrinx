# Blocking 02 — Block / unblock / list API and immediate effects

## Status

Implemented (`blocks.go`).

## Depends on

[01](01_schema_and_payload.md)

## Routes

Registered in `main.go` next to `/users/{userID}/follow`:

| Route | Handler | Body | Answer |
|-------|---------|------|--------|
| `POST /users/{userID}/block` | `BlockUser` | form: `signature` (over `buildBlockUserPayload`), `fingerprint` (bare, as for likes) | 200 + certificate |
| `DELETE /users/{userID}/block` | `UnblockUser` | none | 204 |
| `GET /blocks` | `ListMyBlocks` | — | 200 + `{ blocks: [certificate…] }`, the caller's own blocks |

`{userID}` is the blocked user. All three are authenticated as the blocking user;
none is proxied to a peer (the block lives on the blocking user's server).

## `BlockUser`

1. Refuse: self (400), unknown user (404), removed account (410 + account
   certificate), a foreign user whose server is not an established peer (404).
2. Verify the signature over `buildBlockUserPayload(thisServer, caller,
   userID)` with the caller's active key; refuse a revoked signer (as
   `UpdateUser` does).
3. Countersign and insert (`InsertBlock`); a replay returns the stored
   certificate and does nothing else.
4. On first insert, in the same transaction:
   - delete the blocked user's follow of the blocking user (`user_following`, or
     the remote-follower record when the blocked user is on a peer);
   - delete the blocked user's pending events for the blocking user's reeds;
   - delete `reed_allocations` rows where the holder is the blocked user and
     the reed's author is the blocking user, firing the usual `REED_COVERAGE`
     notify for each reed whose count changed;
   Both run in `applyBlockEffectsTx`, which only touches rows this server
   keeps, so the blocked user's server reuses it ([05](05_federation.md)).
5. After commit: a `REED_COVERAGE` notify per dropped reed, then push to the blocked user ([04](04_realtime.md)) or notify
   their server ([05](05_federation.md)).

Until [05](05_federation.md) lands, a foreign `{userID}` is refused with
**422** "blocking users on other servers is not supported yet".

## `UnblockUser`

Deletes the `(caller, userID)` row if present and answers 204 either way.
A deleted block owes the blocked user a lift ([04](04_realtime.md)), or
their home server when they are on a peer ([05](05_federation.md)).

## `ListMyBlocks`

The caller's own blocks, newest first by countersignature time. Not
paginated: a user's blocks are few. Only the blocking user can
list their blocks; nobody can list who blocked them (they learn of each
block individually).

## Tests

- Block → 200 + certificate; replay → identical certificate, no second push.
- Block deletes the blocked user's follow of the blocking user (not the
  blocking user's follow of them) and drops the blocked user's allocations of the
  blocking user's reeds only.
- Unblock restores neither.
- Self → 400; unknown user → 404; removed account → 410; revoked signer or a
  signature over another user → 401.
- In-flight requests by the blocked user for the blocking user's reeds are
  dropped with the block.
- Unblock of a missing block → 204, nothing owed.
