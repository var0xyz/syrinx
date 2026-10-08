# Threads 00 — Design, data model, trust model

## Status

Implemented.

## Why not self-replies

The interim composer published part N as a reply to part N−1. That made a
thread indistinguishable from a conversation: parts landed in `reed_replies`,
counted as replies, inherited the reply tree's `thread_id`, and the 30-part
cap had to be enforced by walking the reply chain
(`SelfReplyChainLength`). A thread is not a conversation. It is a list, not a
tree, and it has one author.

## Signed reed headers

Two mutually exclusive header objects. In the signed frontmatter
(`reedAsMarkdown`) they are flattened to dotted keys and sorted with the rest;
on the wire and in IndexedDB they are nested objects.

```
replying.root: <reed ID>   # conversation root (was threadId)
replying.to:   <reed ID>   # direct parent (was replying)

thread.head:   <reed ID>   # head's own ID on the head
thread.index:  <0..29>     # zero-based, 0 on the head
```

A thread has at least two parts; a single reed never carries `thread`.

## Thread record

Reed headers prove membership only to someone holding the content, which
servers never do. So the author also signs a **thread record** with no
content:

```
0:        <head reed ID>
1:        <reed ID>
...
serverID: <home server ID>
threadID: <head reed ID>
type:     thread
```

A dictionary from zero-based index to reed ID, as top-level keys in
`bytesToSign` (sorted as strings, so `10` precedes `2`; deterministic, and the
same order JCS will give when signatures move to it).

Nobody parses these keys. The record travels as an **array** of reed IDs plus
its signatures, and every signer and verifier builds the payload from the
array with one helper (`buildThreadUserPayload`), generating the keys itself.
Keys are plain decimal indices with no padding, sorted the same way on every
side. Verifiers check that the array has 2 to 30 IDs, that the
first equals `threadID`, and that every ID belongs to the author.

The home server countersigns it like every other signed resource, binding the
thread ID, author key ID, server key fingerprint, server timestamp and the
author's signature.

## Data model

Home server:

```sql
-- on reeds; thread parts are always local reeds here
thread_head  VARCHAR(255) REFERENCES reed_identities(id),
thread_index SMALLINT,
CHECK ((thread_head IS NULL) = (thread_index IS NULL))

CREATE TABLE IF NOT EXISTS reed_threads (
  id                  VARCHAR(255) PRIMARY KEY REFERENCES reed_identities(id) ON DELETE CASCADE,
  user_signature_id   INT NOT NULL REFERENCES user_signatures(id),
  server_signature_id INT NOT NULL REFERENCES server_signatures(id)
);
```

Peers don't store thread records. They verify one whenever it arrives (with a
relayed thread, with a removal) and act on it.

`reed_replies.thread_id` is renamed `root_id` so "thread" means one thing in
the schema. (Ripple `thread_id` is a third, unrelated concept and stays.)

## Trust model

- **Servers** trust a thread's membership only from a verified thread record.
- **Clients** trust a thread once they hold the record and every part, and
  each part's signed `thread.head` and `thread.index` match the record's
  entry for it, in both directions. That proves order and completeness.
- **Removal** is a thread-removal certificate binding the thread record's
  user signature ([04](04_removal.md)). The record can't be swapped for a
  different list, because the certificate names the exact signature.

## Limitations (accepted)

- No appending, editing, or single-part deletion after publishing.
- Completeness is only known after the whole bundle is verified.
