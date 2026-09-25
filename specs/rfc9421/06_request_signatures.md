# RFC 9421 06 — Optional: request signing

## Status

Proposed, **deferred**. Requires a separate decision — do not start this
because 00–05 landed.

## Depends on

[01](01_signature_base.md)–[05](05_client_verification.md)

## Context

Requests are signed today by the service worker
(`src/frontend/src/service-worker.ts`) over a canonical
`METHOD path\n\nbody\n\ntimestamp` string, with the user's PGP key, and
verified in `middlewares.go`. This step would make that conformant too.
See [README](README.md).

**Blank slate — no backwards compatibility.**

## Why this is separate from the response work

Steps [03](03_server_signing_key.md)–[05](05_client_verification.md)
introduce one new *server* key, bound to the existing PGP server
identity, signing only ephemeral responses. Nothing durable depends on
it, so it can be rotated freely and discarded.

Request signatures are the opposite. They are made with the **user's**
key — the same key that signs reeds, identity records, invites,
revocations and removal certificates, and whose rotation chain is the
backbone of the trust model. Making request signing conformant means
either:

- **Signing requests with a second, non-PGP user key.** Every user now
  has two keys with different lifecycles, and the backup, recovery,
  device-binding and rotation flows all have to carry both. This is a
  large change to the most safety-critical paths in the product.
- **Moving user identity off PGP entirely.** A far larger project than
  this spec set, touching every signed record and the server's
  verification of all of them.

Neither is justified by interoperability alone, and the second is a
product-level decision, not a protocol cleanup.

## If it were done anyway

Covered components would be `@method`, `@target-uri` (or `@path` +
`@query`), `content-digest` for bodies, and `created`. That is a genuine
improvement over the current request canonicalization, which covers the
path as a single pre-joined string and takes the body as text.

Independently of RFC 9421, the current request scheme has a real gap
worth noting here so it is not lost: the **WebSocket** handshake
signature covers only a timestamp, unbound to user or server, and is
replayable for its whole window — RISKS H1. That is a defect to fix on
its own terms and must not wait on this spec.

## Recommendation

Leave deferred. Revisit only if a third party needs to make authenticated
*requests* to a Syrinx server without an OpenPGP stack — a much less
likely scenario than a third party needing to *verify responses*, which
is what [00](00_design.md)–[05](05_client_verification.md) address.
