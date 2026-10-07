# Threads 05 — SPA: composer, thread view, removal

## Status

Proposed.

## Depends on

[02](02_publish.md), [03](03_fetch.md), [04](04_removal.md)

## Scope

- **Composer** (`NewReedModal.svelte`): generate every part's ID first, sign
  each part with its `thread` object, sign the thread record, call
  `POST /threads` once, then `PUBLISH_READY` with the head's ID. Drop the
  sequential `chainPublished` publishing. Store the parts locally only after
  the server accepts the thread.
- **IndexedDB** (bump the DB version; blank slate):
  - a compound index on `['thread.head', 'thread.index']` in `reeds`;
  - `threads`, keyed by `threadID`: the thread record (ID array, user and
    server signatures), verified before store;
  - `removedThreads`, keyed by `threadID`: thread-removal certificates,
    verified before store and kept permanently, like `removedReeds`;
  - `pendingRemoval` entries gain a `kind` (`reed` | `thread`) for the
    author's offline-first queue;
  - `threads` and `removedThreads` go into the backup export/import
    (`backupRestore.ts`) alongside `removedReeds`.
- **Fetching:** `requestThread(threadID)` in `serverConnection.ts`, local
  first (complete only if the record and every part are there), else
  `REQUEST_THREAD`. A holder answers `RELAY_THREAD` from the index.
- **Mentions and pipe feeds** list a thread once, linking to the first part
  that mentions the user or carries the tag.
- **Feeds** show the head with a "1/n" marker; a part reached on its own
  (echo, mention, link) shows its position from `thread.index` and links to
  the thread.
- **Thread view** at `/reed/<head>`: all parts in order. A link to a later
  part opens the thread scrolled to it.
- **Delete** is offered on the head only, labelled as deleting the thread;
  calls `DELETE /threads/{id}` through the existing offline-first removal
  queue.
- **Removal:** on a verified `THREAD_REMOVED`, store the certificate in
  `removedThreads` first, then delete the thread record and every part, as
  in [04](04_removal.md). Storing a reed whose `thread.head` is in
  `removedThreads` deletes it instead, so a delete cut short by a crash heals
  the next time a leftover part is touched, and late copies are never kept.
- Each part keeps its own reply, echo and like actions.

## Non-goals

- Aggregating replies across parts.
- Appending to or editing a published thread.
