# Threads 03 — `REQUEST_THREAD` / `RELAY_THREAD` and the thread ACK

## Status

Proposed.

## Depends on

[02](02_publish.md)

## Messages (`websocket.proto`)

- `REQUEST_THREAD` (client → server): `request_id`, `thread_id`.
- `RELAY_THREAD` (server → holder): same shape as `RELAY_REQUEST`, for a
  thread.

Responses reuse `RELAY_RESPONSE` / `RELAY_MISS` / `DATA_RESPONSE` /
`DATA_ACK` / `DATA_INVALID`. `DATA_RESPONSE.reed_id` carries the thread ID.

## Flow

A client that has a part (or a link to one) and lacks the rest reads
`thread.head` and sends `REQUEST_THREAD`. Single parts can still be fetched
with `REQUEST_REED`, unchanged.

- **Server.** Picks a holder of the head and sends `RELAY_THREAD`; on a miss,
  the next holder. A foreign thread goes to its home server, as
  `REQUEST_REED` does (`handleForeignRequestReedFromClient`).
- **Holder.** Packs the thread record (its ID array and both signatures) and every part,
  in index order, into one bundle encrypted for the requester. If it lacks the
  record or any part, `RELAY_MISS`. Never a partial thread.
- **Requester.** Decrypts and accepts only if the record verifies, its
  `threadID` matches the requested one, and every part verifies and agrees
  with the record (00, trust model). Then it stores everything and sends
  `DATA_ACK`; otherwise `DATA_INVALID`.

The ciphertext stays opaque to the server.

## The thread ACK

`DATA_ACK` on a thread event means "I hold this whole thread". The server
records a `reed_allocations` row for **every** reed in the thread record,
taken from a record it has verified itself, never from the client:

- The home server reads its own `reed_threads` row.
- For a foreign thread, the home server attaches its stored thread record to
  the relayed response. The requester's server verifies it (author key and
  home server countersignature) before recording rows.

The usual `DATA_ACK` guards (requester, `relayed_at`) still apply to the
thread event.

## Tests

- Holder missing one part, or the record → `RELAY_MISS`; requester stores
  nothing.
- Bundle whose record lists a different thread, or whose parts disagree with
  the record → `DATA_INVALID`.
- `DATA_ACK` records rows for every part, on the home server and on a peer;
  a peer given an unverifiable record records none.
