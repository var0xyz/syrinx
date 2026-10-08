# Threads 04 — Thread-removal certificate + federation notify

## Status

Implemented. Not covered: refusing a reply or echo to a removed part. No
server check refuses references to any removed reed today.

## Depends on

[02](02_publish.md), [03](03_fetch.md)

## Certificate

A new certificate type, built like a reed removal:

- **User payload** (`buildThreadRemovalUserPayload`): `type: thread_removal`,
  `serverID`, `threadID`, `threadSignature` (base64 of the thread record's
  user signature). Binding the signature pins the removal to the one record
  the author signed, so it can't be paired with a different list.
- **Server payload** (`buildThreadRemovalServerPayload`): as
  `buildReedRemovalServerPayload`, with `threadID` in place of `reedID`, and
  binding the author key ID (`authorKeyID`) like the thread record does.

## Storage

```sql
CREATE TABLE IF NOT EXISTS thread_removals (
  thread_id           VARCHAR(255) PRIMARY KEY,
  public_key_id       VARCHAR(255) NOT NULL REFERENCES public_keys(id) ON DELETE CASCADE,
  user_signature_id   INT NOT NULL REFERENCES user_signatures(id),
  server_signature_id INT NOT NULL REFERENCES server_signatures(id),
  thread_record       JSONB NOT NULL  -- verified, redelivered on catch-up
);
```

`reed_removals` gains a nullable `thread_id REFERENCES thread_removals`. A
part's row sets it and leaves the signature columns NULL; a plain reed
removal is unchanged. `CHECK` that exactly one of the two is set. Every
existing `NOT EXISTS (SELECT 1 FROM reed_removals …)` filter, the tip query
and the checks that refuse references to removed reeds then cover thread
parts without changes. No foreign key on `thread_id` to `reed_identities`: a
peer may get the removal before it has seen some parts.

A removal lookup for a part (`GetReedRemovalWire`) returns the thread-removal
certificate and the thread record.

## Home server

- `DELETE /api/threads/{threadID}`: author-signed, idempotent like
  `DeleteReed`. Countersigns, stores the certificate and one `reed_removals`
  row per part, in one transaction.
- `DeleteReed` refuses every thread part, head included (400, "Delete the
  whole thread").
- **Local fanout.** `THREAD_REMOVED` (new WS type: certificate + thread
  record) to the audience a reed removal reaches today for every part, plus
  holders of any part, deduplicated.
- **Catch-up.** `GetMissingRemovals` already joins allocations to
  `reed_removals`; for a part's row it delivers the thread certificate,
  deduplicated per thread.

## Crossing to a peer

One call per peer per thread, `POST /api/federation/relay/thread-removal`,
carrying the certificate and the thread record. Peer catch-up
(`peer_author_cursors`) re-sends the same body. The peer:

- rejects a thread ID not owned by the calling peer (400);
- verifies the thread record (author key, home server countersignature) and
  its rules from 00;
- verifies the certificate, and that its `threadSignature` equals the record's
  user signature.

Then it stores the certificate, writes one `reed_removals` row per reed in the
record, runs `dropForeignReedReferences` for each, and delivers
`THREAD_REMOVED` to its users who hold or subscribe to any of them,
deduplicated.

Every part ID comes from a record the author signed, so neither the home
server nor anyone else can widen a removal.

## Client

On `THREAD_REMOVED`, verify the record and certificate as above. Store the
certificate first, permanently, then delete the thread record, every local reed
in the record, and any local reed whose signed `thread.head` is the thread ID.
The stored certificate keeps later copies of any part from being stored, and
marks references to them as removed ([05](05_spa.md)).

## Tests

- `DeleteReed` on any part → 400.
- Peer: certificate bound to a different signature → 400; record with a
  foreign ID → 400.
- Holder of only a middle part receives the certificate, live and on
  catch-up.
- Reply or echo to a part arriving after the removal is refused.
- Go/SPA parity for both removal payloads (`test:signing`).
