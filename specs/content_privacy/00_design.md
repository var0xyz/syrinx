# Content privacy 00 — Design + scope + locked decisions

## Status

Implemented.

## Depends on

—

## Context

Before this change, the server already never *stored* reed content —
`reeds` has no content column — but it still *received* content
transiently on `POST /reeds` to verify the author's signature, extract
tags/mentions, and check length. Reed bodies also crossed the wire in
plaintext during holder → server → requester relay. `mentions.go`'s own
comment anticipated this exact migration before it landed.

The goal: close both gaps. The server should never see reed content, even
transiently. Validation moves entirely to the client that ends up holding
the decrypted content. Relay becomes end-to-end encrypted: the server
tells a holder who the requester is; the holder encrypts for them; the
server relays ciphertext blindly, never able to read it.

This is pre-launch (blank slate) — no migration path, no dual-write, no
backward compatibility for old wire shapes.

## Scope

- `POST /reeds` stops receiving `content`. `signature` is still submitted
  (see "Signature without verification" below).
- Author-claimed `tags` and `mentions` form fields replace server-side
  extraction from content.
- `RELAY_REQUEST` carries the requester's canonical id; holders encrypt to
  their active key before responding.
- A new `RELAY_ERROR` message distinguishes "holder has it but can't relay
  it right now" from `RELAY_MISS` ("holder doesn't have it").
- A durable mention inbox (mentioned user → reed) so mention-driven
  delivery survives the move away from content-derived extraction.
- `ContentRejectedData` gains an optional `reason` for the existing
  client → server validation-failure signal.

## Non-goals

- Admin alerting/action on the new metrics — just make the signal exist
  and get tracked, matching how `KEY_FETCH_ERROR`/`REVOKED_KEY_USED`
  already work.
- Any migration path, dual-write, or wire-format compatibility shim.
- Renaming `fingerprint` → `keyID` in `ReedCountersignHeaders` — a real
  improvement, but a separate, deliberate migration across every
  countersign payload at once, not bundled here.

## Locked decisions

### Signature without server verification is still the integrity backstop

The client still sends `signature` (the author's detached signature over
the content envelope) on `POST /reeds`, and the server's countersignature
payload still includes that signature value — unchanged from before this
change. The server can't verify it (no content to check it against), but
requiring and countersigning it still matters: without it, an attacker
could take a validly-countersigned `(reedID, authorID, timestamp)` triple
and pair it with arbitrary re-signed content. A receiving client's
verification transitively covers the exact signature bytes the server
countersigned, so re-signing different content under the same id produces
a signature value that no longer matches what was countersigned. Content
authenticity is proven entirely by the author's own embedded signature,
verified only by receiving peers — the server's role is limited to
countersigning opaque bytes it never checks.

### Tags stay plaintext claims

A hashed-tag scheme (subscriber/author send `sha256(tag)` instead of the
tag) was considered and rejected. Hashtags are low-entropy,
guessable-by-design strings — an unsalted hash is trivially reversible by
anyone willing to hash a wordlist once, the same failure mode as unsalted
password hashing. It would hide tag names from a passive log-reader but
not from a server operator, and it still leaks that N users cluster on
the same unknown tag. Not worth the complexity for obscurity-only
protection.

### Mentions: hybrid pull inbox, not pure self-detection

A pure "receiving client self-detects its own mention once it organically
sees the reed" design was considered but rejected: today a mention alone
can trigger delivery to a user who doesn't follow the author and isn't on
a shared pipe, and self-detection would silently drop that delivery
guarantee. Instead: the server keeps a durable, client-verified pull
inbox. See [03](03_mention_inbox.md) for the full design.

## Related

- [Trust model](../../docs/trust.md)
- [Content distribution](../../docs/content.md)
- [Content privacy](../../docs/content_privacy.md) — user/operator-facing
  explanation of the security properties this delivers
