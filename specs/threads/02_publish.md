# Threads 02 — Thread record, `POST /threads`, `PUBLISH_READY`

## Status

Proposed.

## Depends on

[01](01_signed_headers.md)

## Schema (`InitDB`)

`reeds.thread_head`, `reeds.thread_index` and `reed_threads` as in
[00](00_design.md), plus:

```sql
CREATE INDEX IF NOT EXISTS idx_reeds_thread_head
  ON reeds (thread_head, thread_index) WHERE thread_head IS NOT NULL;
```

## Payload builders (`identity.go`, mirrored in SPA `signing.ts`)

- `buildThreadUserPayload(serverID, threadID string, reedIDs []string)`: the
  thread record from 00, key `i` for `reedIDs[i]`.
- `buildThreadServerPayload(...)`: type, serverID, threadID, author key ID,
  server key fingerprint, signedAt, base64 user signature.

One helper per side, called by both signer and verifier.

## `POST /api/threads`

Signature-authenticated like `SignReed`. JSON body; no content, only metadata
the author also signed:

```json
{
  "previousID": "<author's current tip>",
  "threadSignature": "<author's signature over the thread record>",
  "reeds": [
    { "reedID": "...", "signature": "...", "tags": [], "mentions": [] },
    { "reedID": "...", "signature": "...", "tags": [], "mentions": [] }
  ]
}
```

Array position is the index. The server rejects with 400 unless:

- 2 ≤ `len(reeds)` ≤ `MaxThreadReeds`;
- every `reedID` belongs to the caller and none repeats;
- `threadSignature` verifies, with the active key, over
  `buildThreadUserPayload` rebuilt from these IDs;
- tags and mentions per part pass the `SignReed` rules;
- the active key is present and not revoked.

In **one transaction**: tip check against `previousID` (`checkReedTipTx`),
countersign and insert every part with the same `signed_at`, its
`thread_head` and `thread_index`, then countersign and insert the thread
record. Response: the server signatures for the thread record and every part.

**Tip.** All parts share `signed_at`, and the tip query breaks ties by `id`.
Part IDs must ascend with their index (UUIDv7, generated in order), so the
existing query picks the last part with no change. Ordering by `thread_index`
instead would rank a reed published in the same second after the thread
below its parts.

**Idempotency.** If the thread already exists for this author, return the
stored signatures when the thread signature and every part's signature
match; otherwise 409. A concurrent duplicate that loses the insert race
replays the same way (`isReedUniqueViolation`).

## `PUBLISH_READY`

Unchanged message, sent once with the head's ID. The server marks every part
`published_at` and delivers the thread **once per recipient**, as a unit
(recipients fetch the bundle, [03](03_fetch.md)):

- **Followers, broadcast and profile subscribers:** one new-reed fanout, for
  the head.
- **Mentions:** the union of every part's `reed_mentions` rows (joined on
  `thread_head`), deduplicated, so a user mentioned in several parts gets one
  `MENTION` for the thread. Catch-up (`GetMissingMentions`) groups a thread's
  parts by `thread_head` the same way.
- **Pipes:** the union of every part's claimed tags (`pending_fanout`), with
  listeners deduplicated across tags, so a listener of two tags used in
  three parts gets the thread once.
- Every part's `pending_fanout` row is cleared together.

`reed_mentions` and the tag claims stay per part, so per-reed queries keep
working; only delivery is grouped.

## Removed

- `SelfReplyChainLength` and the self-reply branch in `SignReed`.

## Tests

- Rejects 1 part, 31 parts, foreign reed IDs, IDs that don't ascend with
  their index, a thread signature over a different list or order.
- Replay returns identical signatures; mismatched replay → 409.
- Tip after a thread is its last part.
- A user mentioned in several parts gets one `MENTION`, live and on catch-up.
- A pipe listener matching tags in several parts gets the thread once.
- Go/SPA parity for the thread record payload (`test:signing`).
