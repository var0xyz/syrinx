# Deletion 06 — SPA holders (reed removal)

## Status

Implemented (WS `REED_REMOVED` + GET 410 share `verifyAndCommitReedRemoval`).

## Depends on

[04](04_reed_fanout.md), [05](05_spa_reed_author.md)

## Context

Holders apply reed-removal certs from the realtime / sync path ([04](04_reed_fanout.md))
or from **410** on GET.

## Scope

- On cert with `type: "reed"` (WS/sync or HTTP): verify author signature +
  server countersig (author key from local/cache; server key by fingerprint).
- On success: remove local reed (and allocation mirrors / feed indexes as
  needed); optionally store cert in `removedReeds` to avoid re-fetch thrash;
  complete whatever ack the server uses so catch-up does not re-deliver
  ([04](04_reed_fanout.md)).
- On failure: do **not** delete the reed.
- GET reed → 410 + `type: "reed"` → same apply path.
- GET reed → 410 + `type: "account"` → defer to account handler ([09](09_spa_account.md));
  until 09 exists, at least do not treat account cert as a reed cert.

## Non-goals

- Account purge details (09).
- Inventing a second delivery channel beyond 04 / 410.

## Design

One apply helper for fanout, sync catch-up, and 410.

## Test plan

- [ ] Valid reed cert → local reed removed
- [ ] Bad user or server sig → reed retained
- [ ] Wrong `type` handling does not mis-apply
- [ ] 410 GET, live fanout, and sync catch-up share one apply helper
