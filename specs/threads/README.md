# Threads

A **thread** is one post split across up to 30 reeds, published together and
read as one unit. This directory replaces the interim model where thread parts
were published as a chain of self-replies.

**Blank slate — no migration, no backwards compatibility.** Schema and signed
header changes go straight into `InitDB` and `reedAsMarkdown`; recreate the DB.

| #                              | Title                                                     | Depends on |
|--------------------------------|-----------------------------------------------------------|------------|
| [00](00_design.md)             | Design, data model, trust model                           | —          |
| [01](01_signed_headers.md)     | `replying` and `thread` header objects                    | 00         |
| [02](02_publish.md)            | Thread record, `POST /threads`, `PUBLISH_READY`           | 01         |
| [03](03_fetch.md)              | `REQUEST_THREAD` / `RELAY_THREAD` and the thread ACK      | 02         |
| [04](04_removal.md)            | Thread-removal certificate + federation notify            | 02, 03     |
| [05](05_spa.md)                | SPA: composer, thread view, removal                       | 02–04      |

01 can land alone. 02 replaces the self-reply composer path from the
`threads` branch. 03 and 04 can proceed in parallel once 02 is in. 05 lands
with or after each server step.

---

## Status

| #  | Title                                                 | Status   |
|----|-------------------------------------------------------|----------|
| 00 | Design, data model, trust model                       | Proposed |
| 01 | `replying` and `thread` header objects                | Proposed |
| 02 | Thread record, `POST /threads`, `PUBLISH_READY`       | Implemented |
| 03 | `REQUEST_THREAD` / `RELAY_THREAD` and the thread ACK  | Implemented |
| 04 | Thread-removal certificate + federation notify        | Proposed |
| 05 | SPA: composer, thread view, removal                   | Proposed |

**Track status: In progress.**

## Locked decisions

- A reed is either a **reply** (`replying`) or a **thread part** (`thread`),
  never both. Thread parts never echo either.
- `replying` becomes an object: `replying.to` (was `replying`) and
  `replying.root` (was `threadId`).
- Each part carries `thread.head` (the head's ID; the head's own ID on the
  head) and a zero-based `thread.index`. No `prev`, `next` or `count`.
- The author signs a **thread record**: a dictionary from index to reed ID.
  It holds no content, so servers can verify it. It's the authority on which
  reeds form a thread.
- Threads are **immutable**: no appending, editing, or single-part deletion.
- The whole thread is published in one `POST /threads` call, then one
  `PUBLISH_READY` with the head's ID. The last part becomes the author's
  latest reed.
- A thread is delivered **once per recipient**: followers, mentioned users
  (across all parts) and pipe listeners (across all parts' tags) each get it
  once, not once per part.
- A thread is fetched as **one bundle** (`REQUEST_THREAD` → `RELAY_THREAD`),
  all or nothing, and ACKed as a whole. Single parts can still be requested
  with `REQUEST_REED`.
- Removal is **per thread**: a thread-removal certificate that binds the
  thread record's signature. `DeleteReed` refuses every thread part.
- A removal crosses to each peer **once**, with the certificate and the
  thread record. Peers verify both and treat every listed part as a removed
  reed.
- Storage: `reed_threads` for the thread record, `reeds.thread_head` /
  `thread_index` for membership on the home server. In the SPA: a
  `[thread.head, thread.index]` index on `reeds`, a `threads` store for
  thread records, and a permanent `removedThreads` store for removal
  certificates.
- A reply to a part is a reply to that part. No thread-level reply aggregation
  for now.
