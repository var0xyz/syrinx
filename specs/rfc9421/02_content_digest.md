# RFC 9421 02 — `Content-Digest` over the body

## Status

Proposed.

## Depends on

[01](01_signature_base.md)

## Context

RFC 9421 signs fields, not bodies. Body integrity comes from an RFC 9530
`Content-Digest` header that the signature then covers. The current
scheme instead concatenates `headers + "\n\n" + body` and signs the lot.
See [README](README.md).

**Blank slate — no backwards compatibility.**

## Scope

- Emit `Content-Digest` on every signed response.
- Cover it in the signature base.
- Verify it in the SPA before trusting the body.

## Non-goals

- `Repr-Digest` / `Want-Repr-Digest` content negotiation (RFC 9530 § 3).
- Request bodies ([06](06_request_signatures.md)).

## Design

### Header

```
Content-Digest: sha-256=:<base64 of SHA-256 over the exact body bytes>:
```

RFC 9530 structured-field dictionary; value is a Byte Sequence (colon
delimiters are part of the syntax, not decoration). `sha-256` is the
right choice over `sha-512` here: smaller header, and the SPA gets it
from `crypto.subtle.digest` with no dependency.

### Server

In `responseSigner.flush` (`src/backend/middlewares.go`), after the body
buffer is complete and **before** building the signature base:

1. `sha256.Sum256(rs.bodyBuffer.Bytes())`
2. base64-standard-encode
3. `headers.Set("Content-Digest", "sha-256=:"+b64+":")`

Empty bodies still get a digest — the digest of zero bytes — so the
covered-component set stays constant regardless of body presence. A
variable component set is a verification footgun.

### Client

In `responseVerifier.ts`, verification order matters and must be:

1. Verify the signature over the base (which covers `content-digest`).
2. **Then** independently hash the received body and compare to the
   `Content-Digest` value in constant time.

Reversing these, or skipping step 2, leaves the body unauthenticated: the
signature only proves the *digest header* was signed, not that the body
matches it. Call this out in the code comment — it is the single easiest
thing to get wrong in this step.

The body is read via `res.clone().text()` as today. Note the digest is
over *bytes*, not the decoded string: use `new TextEncoder().encode(...)`
on the exact text, and be explicit that this assumes UTF-8 responses
(true for this API — all JSON). If binary responses are ever signed, this
must switch to `arrayBuffer()`.

### Content-Length

The current scheme excludes `Content-Length` from coverage because
framing can rewrite it. That stays true and is now automatic: the fixed
component set from [01](01_signature_base.md) simply does not include it.
With `Content-Digest` covered, length is redundant anyway.

## Testing

- Digest vectors: empty body, ASCII, multi-byte UTF-8.
- Negative: body mutated after signing → digest mismatch → verification
  fails closed.
- Negative: digest header mutated → signature verification fails first.
