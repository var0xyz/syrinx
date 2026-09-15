# Content privacy — server never sees reed content

The server no longer receives reed content at all, even transiently.
Validation (signature, length, hashtag/mention claims) moved to the
client that ends up holding the decrypted body — the receiving client
already did most of this via `verifyReed`. Relay is now end-to-end
encrypted: the server tells a holder who the requester is; the holder
fetches the requester's active key and encrypts the body to them; the
server relays ciphertext blindly.

| # | Title | Depends on | Status |
|---|-------|------------|--------|
| [00](00_design.md) | Design + scope + locked decisions | — | Implemented |
| [01](01_signreed_contentless.md) | `POST /reeds` drops `content`; claimed `tags`/`mentions` | 00 | Implemented |
| [02](02_relay_encryption.md) | `RELAY_REQUEST`/`RELAY_RESPONSE` carry ciphertext; `RELAY_ERROR` | 00 | Implemented |
| [03](03_mention_inbox.md) | Mentioned-user pull inbox (`reed_mentions`, cursor fetch, removal) | 01 | Implemented |
| [04](04_spa.md) | SPA: encrypt/decrypt, claim extraction, tag-claim verify | 01, 02 | Partial |
| [05](05_content_rejected_reasons.md) | `ContentRejectedData.reason` and the standardized set | — | Implemented |

## Locked decisions

| Topic | Decision |
|-------|----------|
| Content on the wire | Never — not even transiently at `SignReed` |
| Author signature | Still submitted and still countersigned, unverified by the server — the integrity backstop against re-signed-content swaps (see [01](01_signreed_contentless.md)) |
| Tags | Plaintext claimed metadata; a hashed-tag scheme was considered and rejected as obscurity-only (see [00](00_design.md)) |
| Mentions | Hybrid: server-tracked claim + durable pull inbox, client-verified on fetch (see [03](03_mention_inbox.md)) |
| Relay encryption | Holder resolves the requester's active key id and encrypts; server relays ciphertext blindly |
| Countersignature scope | Unchanged shape — attests `(reedID, authorID, server fingerprint, signature, timestamp)`, same as before this change |

## Status

**Implemented** on the server and for the core SPA relay flow (encrypt on
`RELAY_REQUEST`, decrypt + verify on delivery, tag-claim check on pipe
delivery). The mentioned-user's client-side inbox consumption (polling
`GET /mentions`, fetching + verifying each entry) is not yet wired up —
the server endpoints and storage exist; see [03](03_mention_inbox.md).
