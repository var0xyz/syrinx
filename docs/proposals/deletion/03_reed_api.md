# Deletion 03 — Reed-removal API (idempotent)

## Status

Proposed.

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
  (account cert wins over reed cert when both could apply — account first).
- Stop hard-deleting without a cert. Live `reeds` row may be deleted or
  retained after cert insert; tip must ignore removed ids.

## Non-goals

- Realtime fanout (04).
- SPA queues (05–06).

## Design

### Submit

`DELETE /reeds/{userID}/{reedID}` with body including at least `signature`
(user) over the canonical reed-removal payload. Server checks author,
verifies, inserts-or-returns **200** + cert (same on replay).

### GET reed

1. Account removal for `userID`? → **410** + account cert (`type: "account"`)
   — implemented fully in [08](08_account_api_fanout.md); until then skip.
2. Reed removal exists? → **410** + reed cert (`type: "reed"`).
3. Row exists? → **200**.
4. Else → **404**.

## Test plan

- [ ] First removal → cert stored; response has `type: "reed"` + `server` block
- [ ] Retry → identical `server.signature` / timestamp
- [ ] Non-author → reject
- [ ] GET removed reed → 410 reed cert
- [ ] GET unknown reed, live author → 404
