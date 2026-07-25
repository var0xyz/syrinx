# Signed deletions (reeds + accounts)

This directory is the **signed deletion** feature proposal set. Numbered
files below are independently reviewable implementation steps. Land them in
order unless a step's "Depends on" says otherwise.

**Not recovery.** These attestations are normal-operation offline-first
resources. Recovery ingest of deletion certs is out of scope here.

**Code organization (suggested):** deletion-specific server helpers in a
`syrinx/deletion` package (or colocated with reed/user handlers if thin).
Shared canonical payload builders live in `syrinx/identity`. Main wires
DDL, routes, and realtime fanout. SPA owns `pendingRemoval` /
`removedReeds` (reed offline-first) and `removedAccounts` / tombstone
stores. Account author delete is **online-only** (no pending queue).

| #                                    | Title                                              | Depends on |
|--------------------------------------|----------------------------------------------------|------------|
| [00](00_design.md)                   | Design + trust model                               | —          |
| [01](01_reed_schema.md)              | Reed-removal schema                                | 00         |
| [02](02_reed_payload.md)             | Reed-removal canonical payload + countersign       | 01         |
| [03](03_reed_api.md)                 | Reed-removal API (idempotent)                      | 02         |
| [04](04_reed_fanout.md)              | Reed-removal realtime fanout + sync catch-up       | 03         |
| [05](05_spa_reed_author.md)          | SPA author queue (`pendingRemoval` → `removedReeds`) | 03       |
| [06](06_spa_reed_holders.md)         | SPA holders: verify cert → drop local reed         | 04, 05     |
| [07](07_account_schema.md)           | Account-removal schema + store                     | 00         |
| [08](08_account_api_fanout.md)       | Account-removal API, 410 bodies, fanout            | 07, 02, 04 |
| [09](09_spa_account.md)              | SPA account removal (author + peers)               | 08, 06     |

After 00, account schema ([07](07_account_schema.md)) may proceed in
parallel with the reed track. Account API/fanout ([08](08_account_api_fanout.md))
needs reed payload conventions (02) and the realtime catch-up pattern (04).
Account SPA ([09](09_spa_account.md)) needs 08 and reed holders (06). Prefer
landing reed end-to-end before account SPA if staffing is serial.

Cross-link: tip check ([recovery 16](../recovery/16_reed_tip_check.md))
must treat tip as newest **non-removed** reed once reed removal ships, and
an account-removed author cannot publish.

---

## Status

**Implemented** via the numbered steps above (00 design accepted; 01–09
landed).

## Motivation

Hard-deleting a reed (or wiping an account) leaves holders with stale local
copies and no verifiable proof they should purge. Deletions must be:

1. **Author-signed** (reeds: offline-first queue + retry; accounts: online-only).
2. **Server-countersigned** once (idempotent replay of the same cert).
3. **Fanout** to connected peers as the signed certificate.
4. **Verified on every client** (user + server sigs) before local purge.

Account deletion uses the same dance. One account-removal cert authorizes
purging **all** of that author's reeds on peers; public keys remain on
server and devices. Optional goodbye **note** (≤140 characters) is part of
the account cert and shown on the tombstone profile.

## Protocol sketch (reeds)

1. Client creates `pendingRemoval` locally: `serverID`, `userID`, `reedID`,
   user signature (offline-first; survives app close).
2. `DELETE` removal to server (body carries the user signature).
3. Server verifies, **countersigns once**, stores the cert, returns **200**
   with a `server` block. Retries return the **same** stored cert (no new
   signature).
4. Client stores cert in `removedReeds`, deletes the reed from `reeds`,
   then deletes the `pendingRemoval` row.
5. Server fans out the cert through the **existing realtime machinery**
   (same class of path as new reeds). Offline peers catch up on
   `SYNC_REQUEST` via a `reed_allocations ∩ reed_removals` diff
   ([04](04_reed_fanout.md)).
6. Holders verify both signatures, then remove the local reed.

## Protocol sketch (accounts)

Author must be **online**: sign → DELETE → countersign-once → fanout with
`type: "account"` and optional `note`. No client-side pending queue.
Peers purge profile/reeds/follows (etc.) per [07](07_account_schema.md);
**keep public keys**. Idempotent retry of the same DELETE is still fine if
the first response was lost.

## HTTP 410 bodies

Deleted resources return **410 Gone** with a JSON deletion certificate.
Unknown resources remain plain **404**.

Every 410 body includes an explicit **`type`** so clients never infer:

| `type`    | When                                                                 |
|-----------|----------------------------------------------------------------------|
| `"reed"`  | GET reed after reed-removal                                          |
| `"account"` | Account tombstone (GET profile, or GET reed under a deleted author) |

Submit and idempotent replay of a removal return **200** with the same cert
JSON shape; **410** is for GET (and equivalent reads) of gone resources.

### Example — reed

```http
HTTP/1.1 410 Gone
Content-Type: application/json

{
  "type": "reed",
  "serverID": "syrinx-example",
  "userID": "Ab3xY9pQ…",
  "reedID": "0v4…",
  "signature": "<base64 user detached sig>",
  "server": {
    "id": "syrinx-example",
    "fingerprint": "A1B2C3…",
    "algorithm": "PGP+base64",
    "signature": "<base64 server countersig>",
    "timestamp": "2026-07-22T17:02:05Z"
  }
}
```

### Example — account

```http
HTTP/1.1 410 Gone
Content-Type: application/json

{
  "type": "account",
  "serverID": "syrinx-example",
  "userID": "Ab3xY9pQ…",
  "note": "Taking a long break. Thanks for reading.",
  "signature": "<base64 user detached sig>",
  "server": {
    "id": "syrinx-example",
    "fingerprint": "A1B2C3…",
    "algorithm": "PGP+base64",
    "signature": "<base64 server countersig>",
    "timestamp": "2026-07-22T17:04:11Z"
  }
}
```

`note` appears only on `type: "account"` (max 140 characters; may be empty).

### GET reed decision tree

Path is already `/reeds/{userID}/{reedID}`:

1. Author has account-removal cert → **410** + account cert (`type: "account"`).
2. Else reed has reed-removal cert → **410** + reed cert (`type: "reed"`).
3. Else reed row exists → **200**.
4. Else → **404**.

Account deletion does **not** require per-reed removal certs. The account
cert covers reed purge on peers; the server may leave or drop `reeds` rows
as bookkeeping — the authoritative “gone” signal for peers is the cert on
410 / fanout.

## Resolved

1. Offline-first pending queues; clear pending only after local purge steps
   complete.
2. Submit via **DELETE** with signed body (evolve today’s reed DELETE;
   account mirrors the same verb).
3. Server countersigns **once**; idempotent API replays stored cert.
4. Submit / replay → **200** + cert; GET of gone resources → **410** + cert.
5. Dual-signature verify before any local reed/account data purge.
6. Account cert covers all reeds; keys retained everywhere.
7. Account note ≤140; shown under tombstone profile.
8. **410 Gone** (not 404) for verified deletions on read; body is the cert.
9. Explicit `type` in signed headers and JSON: `"reed"` | `"account"`.
10. Peer purge set minimum: [07](07_account_schema.md).
11. Out of scope: recovery report-back of deletion certs; GC of old tombs.
12. **Distribution** — reuse new-reed realtime + `SYNC_REQUEST` catch-up
    (`pending_events` / diff recompute); no separate deletion-outbox table.
    Reed catch-up = `reed_allocations ∩ reed_removals`; clear allocation
    after apply ([04](04_reed_fanout.md)). Account fanout/catch-up:
    peers who still follow or still hold allocations for that author
    ([08](08_account_api_fanout.md)). GET 410 is the safety net.

## Open questions

1. Package name (`syrinx/deletion` vs handlers-only) — implementation choice.
2. Exact WS ack that clears allocation after `reed_removed` delivery —
   match existing pending-event completion hooks in implementation.
