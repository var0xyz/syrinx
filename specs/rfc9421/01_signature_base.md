# RFC 9421 01 — Signature base + component derivation

## Status

Proposed.

## Depends on

[00](00_design.md)

## Context

Replaces the ad-hoc canonicalization in `buildCanonicalHeaderString`
(`src/backend/middlewares.go`) and its SPA mirror
(`responseVerifier.ts`) with RFC 9421 § 2 signature-base construction.
See [README](README.md).

**Blank slate — no backwards compatibility.**

## Scope

- A `signatureBase` builder shared in shape by server and SPA.
- Covered-component selection for responses.
- `@signature-params` serialization.

## Non-goals

- Body digests ([02](02_content_digest.md)).
- Keys, signing, verification ([03](03_server_signing_key.md)–[05](05_client_verification.md)).
- Request components (`@method`, `@target-uri`, …) — [06](06_request_signatures.md).

## Design

### Signature base format

One line per covered component, then the params line, joined with `\n`:

```
"@status": 200
"content-type": application/json
"content-digest": sha-256=:<base64>:
"@signature-params": ("@status" "content-type" "content-digest");created=1618884473;keyid="…";alg="ed25519"
```

Rules (§ 2.1, § 2.5):

- Component identifiers are lowercased and double-quoted.
- Multiple instances of a field join with `", "` **in the order received** —
  never sorted. This is the substantive fix over the current scheme, which
  sorts values within a header and so can alter meaning for
  order-sensitive fields.
- Obsolete line folding is replaced by a single space; leading/trailing
  whitespace is stripped.
- The `@signature-params` value is the covered-component inner list plus
  the parameters, serialized as an RFC 8941 structured field.

### Covered components for responses

Minimum set:

| Component | Why |
|-----------|-----|
| `@status` | Binds the status code; the current scheme covers none, so a signature is replayable across statuses |
| `content-type` | Prevents reinterpretation of the body |
| `content-digest` | Body integrity ([02](02_content_digest.md)) |

Headers outside this set are deliberately **not** covered. The current
scheme covers every header and then needs `X-Syrinx-Signed-Headers` to
tell the client which survived proxying. An explicit fixed set removes
that machinery: the client requires exactly these components and rejects
anything else.

`X-Syrinx-Signed-Headers` is therefore **deleted**, not ported.

### Parameters

`created` (integer, unix seconds), `keyid` (the ed25519 key id from
[03](03_server_signing_key.md)), `alg="ed25519"`. `expires` is set to
`created + 300` to give verifiers a bounded window; the SPA already
tolerates ±5 min clock skew elsewhere (`VERIFY_CLOCK_SKEW_MS`), so reuse
that constant rather than inventing a second one.

`nonce` is omitted: responses are not replayable into state changes here,
and tracking issued nonces server-side is out of proportion. State this
explicitly so a reviewer does not read the omission as an oversight.

### Structured field parsing

Both sides need an RFC 8941 serializer/parser for the params line. Go:
`github.com/dunglas/httpsfv` or hand-roll the narrow subset used here
(inner list of strings + string/integer params). SPA: hand-roll the same
subset — pulling a structured-fields library into the bundle for one line
is not worth the bytes. **Whichever way, server and SPA must be tested
against shared vectors**, because a serialization mismatch fails closed
and looks exactly like a bad key.

## Testing

- Table-driven vectors for the base builder, including the examples in
  RFC 9421 § 2.5 and appendix B.
- One vector fixture shared by Go and TS tests, checked in under
  `specs/rfc9421/vectors/` or `src/signing/`, so drift is caught.
- Explicit cases: repeated header instances, a header with internal
  commas, an empty body, a non-JSON content type.
