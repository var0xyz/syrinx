# Recovery prerequisites — proposals

These proposals decompose the prerequisites listed in
[`../takeover_recovery.md`](../takeover_recovery.md) into independently
shippable pieces. Each one is valuable on its own (mostly bug fixes and
hardening of normal operation) and does not require the recovery feature
itself to be implemented.

The recovery endpoints, `RECOVERY_MODE` boot reconciliation, key bundle
export/import, `unclaimed_accounts`, client sync ledger, and the recovery
UI are treated as a **single later unit of work** and are not proposed here.

## Proposals

| #  | Title                                            | Depends on |
|----|--------------------------------------------------|------------|
| 01 | Fix reed countersignature signer/verifier mismatch + introduce `BytesToSign` | — |
| 02 | Random, server-scoped user IDs                    | —          |
| 03 | reed `server` block (bind reedID/authorID/fp)     | 01         |
| 04 | Signed identity records at signup / rotation      | 01; and 02 should land first to avoid re-signing |
| 05 | Signed profile updates                            | 01, 04     |
| 06 | Signed, replicated key revocations                | 01; shares two-round scaffolding with 04; wire/storage shape superseded in part by 10 |
| 07 | Server-signed client keys on distribution         | 01         |
| 08 | Client signature validation and reed possession   | 01, 03, 07; benefits from 04–06 |
| 10 | Revocations as a separate signed resource         | 01         |
| 11 | Per-user system-notification store                | —          |

## Parallelism

- **Immediately parallel**: 01, 02, 10, 11 have no (or only 01) dependencies and can be
  picked up by separate contributors right now.
- **After 01 lands**: 03, 04, 05, 06, 07 all unblock on the shared
  `BytesToSign` helper.
- **After 02 lands**: 04 is safe to start (avoids re-signing identity
  records with regenerated IDs).
- **After 04 lands**: 05 unblocks. 06 can proceed in parallel with 04 but
  should coordinate on the two-round scaffolding (`pending_*` table
  pattern, TTL cleanup) to avoid duplication — and should follow **10**
  for the resource split / old-key user signature rule.
- **After 01+03+07 land**: 08's reed ingest + possession path is
  unblocked; identity/revocation client gates track 04–06 and 10.

## Shared conventions

### Canonical signed envelope

Several proposals introduce **signed records**. They all use the same
envelope format, produced by a single shared helper on the server and a
mirror on the client:

```
---
<sortedKey>: <value>
<sortedKey>: <value>
...
---
<content>
```

Rules:

- Header keys sorted ASCII byte-lexicographically (matches Go's
  `sort.Strings` and JS `Array.prototype.sort` with the default
  comparator for our ASCII keys).
- One header per line: `<key>: <value>` (colon-space), terminated with a
  single `\n` (LF, never CRLF).
- Empty-string values: **omit the whole header line.** Absent and empty
  are equivalent by convention.
- Opening and closing `---` on their own lines.
- Content is appended verbatim after the closing `---\n`. No trailing
  newline is added by the helper — if the content ends with `\n`, that
  is preserved as-is.
- Timestamps in headers: UTC, `time.RFC3339`, second-precision, `Z`
  suffix.
- **No escaping.** Values are inserted verbatim. See "Why nothing is
  escaped" below.

The helper's signature is:

```go
// Go
func BytesToSign(headers map[string]string, content string) []byte
```

```ts
// TS
function bytesToSign(headers: Record<string, string>, content: string): Uint8Array
```

The return type is `[]byte` / `Uint8Array` rather than a string to
signal that the output is **opaque signing input**, not a document to
be read or re-parsed.

#### Why nothing is escaped

The envelope has exactly one job: produce a deterministic byte sequence
that both sides can reproduce from the same inputs. It is **never
parsed back**. Signed records travel between server and client as
structured fields (`{headers: {...}, content: "...", signature: "..."}`
or equivalent); the receiver re-runs `BytesToSign` on those fields and
compares the signature. Nobody ever consumes the envelope bytes as
markdown, YAML front-matter, or any other format.

Consequences:

- If a header value contains a literal `\n`, `:`, `---`, or any other
  "special" sequence, the envelope will contain those bytes verbatim.
  This is fine — no code splits the envelope on `\n` or on `": "` to
  recover fields, so there is nothing to confuse.
- Adding an escape table would introduce a second contract (the escape
  scheme) that both implementations must agree on, plus test vectors to
  keep them honest, plus a decode path — none of which we need.
- Values *must* be single strings and must not be `nil`/`undefined` at
  the call site; that is a producer-side invariant, not something the
  helper enforces (empty strings are already handled by the omit rule).

**When implementing `BytesToSign`, this rationale must be documented in
the helper's source (a comment at the top of the file is sufficient),
referencing this section.** The reasoning is not obvious from the code,
and a future contributor "hardening" the helper by adding escapes would
silently break signature compatibility.

### Detached signatures

- All signatures are detached PGP signatures over the exact bytes
  returned by `BytesToSign`.
- On the wire, signatures are base64-encoded (std alphabet), never
  nested (no base64-of-base64).
- One helper, called from both signer and verifier in each proposal, so
  the two cannot drift (the specific bug Proposal 01 fixes for reeds).

### Two-round flows

Several proposals introduce a `init` → `complete` handshake so the
client can sign a payload containing server-authoritative fields
(timestamps, IDs) that the server must mint:

- 04 (signup, key rotation)
- 05 (profile update)
- 06 (revocation)

They should share a single `pending_*` table pattern and, ideally, a
small helper for TTL cleanup.
