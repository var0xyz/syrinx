# Deletion 03 — Reed-removal API (idempotent)

## Status

Implemented (DELETE + GET 410; fanout deferred to 04).

## Depends on

[02](02_reed_payload.md)

## Context

Replace hard `DELETE` with a countersigned, idempotent removal that returns
the cert (including on replay). See [README](README.md).

## Scope

- Author-authenticated endpoint: evolve today’s
  `DELETE /reeds/{userID}/{reedID}` to accept a signed body (user signature
  over the reed-removal payload).
- Verify user sig; countersign **once**; persist via [01](01_reed_schema.md);
  return cert with `type: "reed"`.
- Idempotent: existing row → return stored cert. **200** on first accept and
  on replay (body = cert). **410** only on GET of a removed reed (see
  [README 410 bodies](README.md#http-410-bodies)).
- `GET /reeds/{userID}/{reedID}`: apply [README decision tree](README.md#get-reed-decision-tree)
  (account cert wins over reed cert when both could apply — account first;
  account check deferred to [08](08_account_api_fanout.md)).
- Stop hard-deleting without a cert. Live `reeds` row is **retained** after
  cert insert so `reed_allocations` survive for catch-up (04); tip/list
  already ignore removed ids (01).

## Non-goals

- Realtime fanout (04) — old unsigned `ReedDeleted` broadcast removed.
- SPA queues (05–06).

## Design

### Submit

`DELETE /reeds/{userID}/{reedID}` with form field `signature` (user) over
`identity.BuildReedRemovalUserPayload`. Path `userID` must match the
authenticated author. Server verifies, countersigns once, stores via
`deletion.InsertCert`, deletes the live reed row, returns **200** + wire
cert. Replay with the same user signature returns the stored cert.

### GET reed

1. Account removal for `userID`? → **410** + account cert — skip until 08.
2. Reed removal exists? → **410** + reed cert (`type: "reed"`).
3. Row exists? → **200**.
4. Else → **404**.

## Test plan

- [x] Store insert-once / conflict (`deletion` package tests)
- [ ] First removal → cert stored; response has `type: "reed"` + `server` block (manual / e2e with 05)
- [ ] Retry → identical `server.signature` / timestamp
- [ ] Non-author → reject
- [ ] GET removed reed → 410 reed cert
- [ ] GET unknown reed, live author → 404
