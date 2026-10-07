# Key revocation events

Clients stop deciding which user key to trust. The server tells them which key
to encrypt to, clients use the keys they cached, and when a key is revoked the
server **pushes** the signed revocation to everyone holding content signed by
it. Replaces the lazy re-check of
[proposal 09](../09_revocation_fanout.md), which this supersedes.

**Blank slate — no migration, no backwards compatibility.**

| #                                   | Title                                              | Depends on |
|-------------------------------------|----------------------------------------------------|------------|
| [00](00_design.md)                  | Design and trust model                             | —          |
| [01](01_relay_key.md)               | Relay requests name the key to encrypt to          | 00         |
| [02](02_revocation_push.md)         | `KEY_REVOKED` push and catch-up                    | 00         |
| [03](03_federation.md)              | Revocations across peers                           | 02         |
| [04](04_spa.md)                     | SPA: apply revocations, cache keys without re-checks | 01–03    |

01 can land alone. 04 removes the re-check, which is only safe once 02 and 03
deliver revocations.

---

## Status

| #  | Title                                              | Status   |
|----|----------------------------------------------------|----------|
| 00 | Design and trust model                             | Proposed |
| 01 | Relay requests name the key to encrypt to          | Implemented |
| 02 | `KEY_REVOKED` push and catch-up                    | Implemented |
| 03 | Revocations across peers                           | Implemented |
| 04 | SPA: apply revocations, cache keys without re-checks | Proposed |

**Track status: In progress.**

## Locked decisions

- **Clients don't choose keys.** A relay request names the requester's key;
  the holder encrypts to that key and nothing else.
- **Cached keys are used as they are.** A key is fetched once, on a cache miss,
  and never re-checked on a timer.
- **Revocations are pushed** to every user who has the revoked key cached
  (`public_key_allocations`), carrying the signed revocation, with catch-up
  for offline users. Acknowledging deletes the allocation, so nothing
  accumulates. A key fetched after its revocation is never allocated; the
  client fetches its revocation then, and nothing else. Clients drop content
  the key signed at or after the revocation.
- **Fold at the border:** one notice per peer server; the peer delivers it to
  its own users.
- The window between a key's compromise and its revocation is accepted:
  clients trust the key until they are told otherwise.
