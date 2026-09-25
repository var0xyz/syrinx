# RFC 9421 04 — Emit conformant response signatures

## Status

Proposed.

## Depends on

[01](01_signature_base.md), [02](02_content_digest.md), [03](03_server_signing_key.md)

## Context

Rewrites `responseSigner` (`src/backend/middlewares.go`) to emit
`Signature-Input` / `Signature` instead of the bespoke
`X-Syrinx-Response-Signature` envelope. See [README](README.md).

**Blank slate — no backwards compatibility.** No dual-signing window: the
old headers are removed in the same change that adds the new ones.

## Scope

- Emit `Signature-Input` and `Signature`.
- Remove the bespoke headers and their CORS grants.
- Preserve the existing fail-closed posture.

## Non-goals

- Client verification ([05](05_client_verification.md)).
- Request signing ([06](06_request_signatures.md)).

## Design

### Headers emitted

```
Content-Digest: sha-256=:<b64>:
Signature-Input: sig1=("@status" "content-type" "content-digest");created=…;expires=…;keyid="…";alg="ed25519"
Signature: sig1=:<b64 of 64-byte ed25519 signature>:
```

Label `sig1` is arbitrary but fixed; the client reads whichever label is
present rather than assuming, per § 4.2.

Note this reclaims the bare `Signature` name, which
[00](00_design.md) § "What already landed" renamed away from. That is
correct and intended **only at this point**: the name is now used with
its standard meaning and an accompanying `Signature-Input`, which is
exactly what the browser was asking for.

### Headers removed

- `X-Syrinx-Response-Signature`
- `X-Syrinx-Signed-Headers` (superseded by the covered-component list
  inside `Signature-Input`)
- `X-Syrinx-Signature-Scope: body` — the scope is now expressed by the
  covered components, and the constant `"body"` conveyed nothing anyway

`X-Syrinx-Signature` and `X-Syrinx-Signature-Scope` on **requests** are
untouched by this step.

### CORS

`Access-Control-Expose-Headers` must list `Signature`, `Signature-Input`
and `Content-Digest`, or the SPA cannot read them and every response
fails verification. This is the single most likely way to break this
step; it is one line in `CORSMiddleware` and it must change in the same
commit.

### Fail-closed

`responseSigner` currently panics when no signing key is available —
"every response must be signed". Keep that exactly. A response that
cannot be signed must not be sent, and no configuration flag should be
able to disable signing.

### Ordering inside `flush`

1. Set `Content-Length` (framing; uncovered).
2. Compute and set `Content-Digest` ([02](02_content_digest.md)).
3. Build the signature base ([01](01_signature_base.md)) — reads
   `Content-Digest`, so it must come after step 2.
4. Sign with ed25519 ([03](03_server_signing_key.md)).
5. Set `Signature-Input` and `Signature`.

Steps 2 and 3 are order-dependent in a way the current code is not; get
it wrong and the signature covers an absent digest, which verifies
locally and fails in the browser.

## Testing

- Golden response: fixed body + key → exact expected headers.
- Round-trip against the step [05](05_client_verification.md) verifier.
- Negative: missing signing key → panic, no response emitted.
- CORS: `Access-Control-Expose-Headers` includes all three names.
