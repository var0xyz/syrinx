# Recovery prerequisites — proposals

These proposals decompose the **normal-operation prerequisites** for
trustless recovery into independently shippable pieces. Each one is valuable
on its own (mostly bug fixes and hardening) and does not require the recovery
feature itself.

The recovery **feature** (endpoints, boot, bundle, client) is specified and
broken into reviewable steps under
[`recovery/`](recovery/README.md). All server-side recovery implementation
belongs in the **`syrinx/recovery`** package; main only wires boot, routes,
and middleware.

## Prerequisite proposals

| #  | Title                                              | Depends on |
|----|----------------------------------------------------|------------|
| 01 | Fix reed countersignature signer/verifier mismatch | —          |
| 02 | Random, server-scoped user IDs                     | —          |
| 03 | reed `server` block (bind reedID/authorID/fp)      | 01         |
| 04 | Signed identity records at signup / rotation       | 01; and 02 should land first to avoid re-signing |
| 05 | Signed profile updates                             | 01, 04     |
| 06 | Signed key revocations                             | 01; wire/storage shape finalized in 10 |
| 07 | Server-signed client keys on distribution          | 01         |
| 08 | Client signature validation (keys / revokes / deletions; author reed sig) | 01, 03, 07, 09 |
| 09 | Revocation realtime fanout + catch-up              | 06, 10     |
| 10 | Revocations as a separate signed resource          | 01         |
| 11 | Per-user system-notification store                 | —          |

## Recovery feature steps

See [`recovery/`](recovery/README.md):

| #  | Title                                                 |
|----|-------------------------------------------------------|
| 00 | Server key passphrase (keychain + optional HA env)    |
| 01 | Key bundle export (`ops` CLI)                         |
| 02 | Key bundle import (`ops` CLI)                         |
| 03 | `RECOVERY_MODE` boot, bookkeeping, import gate, flags |
| 04 | Own identity claim                                    |
| 05 | Peer identity report-back                             |
| 06 | Reeds, follows, complete                              |
| 07 | SPA recover client                                    |

## Invites / signup modes

See [`invites/`](invites/README.md):

| #  | Title                                              |
|----|----------------------------------------------------|
| 00 | `SIGNUP_MODE` + `MAX_INVITES_PER_USER`, info gate  |
| 01 | `invites` table, `users.invited_by`, store         |
| 02 | Create / list / revoke / check APIs + quota        |
| 03 | Consume at signup, identity, `invitedBy`           |
| 04 | Home CTA + invite-link signup path                 |
| 05 | Toolbar Invites tab + management UI                |

## Echoes and replies (conversations)

See [`conversations/`](conversations/README.md):

| #  | Title                                              |
|----|----------------------------------------------------|
| 00 | Design + UX model (echo count, one-level drill-down) |
| 01 | Verify publish payload (form fields); normalize `replying` ref |
| 02 | Echo/reply index tables + list/count APIs          |
| 03 | Echo count + conversation section on reed detail     |
| 04 | Mentions (`@` → `web+syrinx` links + `reed_mentions` index) |
| 05 | Recursive reply counts: thread total (`threadId`, live WS stat) + per-reed subtree count |

## Reed network coverage

See [`coverage/`](coverage/README.md):

| #  | Title                                              |
|----|----------------------------------------------------|
| 00 | Design + UX + formula                              |
| 01 | Denormalized counters                              |
| 02 | WS subscribe snapshot ACK + live echoes/coverage   |

## Publish ready (fanout gate)

See [`publish/`](publish/README.md):

| #  | Title                                              |
|----|----------------------------------------------------|
| 00 | Design + publish/relay race + locked model         |
| 01 | HTTP SignReed + WS `PUBLISH_READY` + SPA           |
| 02 | Real `RELAY_MISS` (drop allocation + retry)        |

## Custom avatars (hash + processed PNG)

See [`avatars/`](avatars/README.md):

| #  | Title                                      |
|----|--------------------------------------------|
| 00 | Design + locked model                      |
| 01 | `avatars` table + `avatarHash` in identity |
| 02 | Authenticated process endpoint             |
| 03 | Profile PUT: set / keep / clear            |
| 04 | `GET /avatars/<hash>`                      |
| 05 | SPA crop, IndexedDB, fetch/GC, Avatar      |

## Pipes (live hashtags)

See [`pipes/`](pipes/README.md). Ephemeral server-side tag listening;
local reeds with that tag remain on device.

| #  | Title                                     |
|----|-------------------------------------------|
| 00 | Design + naming (**pipe**) + locked model |
| 01 | Tag index at SignReed (`reed_tags`)       |
| 02 | WS subscribe + READY fanout               |
| 03 | SPA links + `/pipe/[tag]` page            |

## Account recovery (key-only restore)

See [`account_recovery/`](account_recovery/README.md). Distinct from server
`RECOVERY_MODE` ([`recovery/`](recovery/README.md)): the user reconstitutes a
client from private keys while the server still holds the account; peers
relay the user’s own reed bodies back.

| #  | Title                                              |
|----|----------------------------------------------------|
| 00 | Design + tip approaches + restore fork             |
| 01 | Key export format + profile Export key             |
| 02 | Challenge + bootstrap API + rehydration row        |
| 03 | Server-orchestrated own-reed relay + complete      |
| 04 | SPA keys-only `/import` fork + session             |
| 05 | SPA rehydration + tip `previousID` + UX            |
| 06 | Device binding on bootstrap (takeover)             |

## Protobuf wire (HTTP + WebSocket)

See [`protobuf/`](protobuf/README.md). Blank-slate cutover of all
client↔server bodies and WS frames to Protocol Buffers; `BytesToSign`
unchanged.

| #  | Title                             |
|----|-----------------------------------|
| 00 | Design + locked model             |
| 01 | Shared resource protos + codegen  |
| 02 | WebSocket envelope + event protos |
| 03 | HTTP encode/decode + content type |
| 04 | Switch every HTTP handler/client  |
| 05 | Binary WS only; SPA + realtime    |
| 06 | SPA consumes generated types      |

## Signed deletions (reeds + accounts)

See [`deletion/`](deletion/README.md):

| #  | Title                                        |
|----|----------------------------------------------|
| 00 | Design + trust model                         |
| 01 | Reed-removal schema                          |
| 02 | Reed-removal canonical payload + countersign |
| 03 | Reed-removal API (idempotent)                |
| 04 | Reed-removal realtime fanout + sync catch-up |
| 05 | SPA author queue (`pendingRemoval`)          |
| 06 | SPA holders: verify cert → drop reed         |
| 07 | Account-removal schema + store               |
| 08 | Account-removal API, 410 bodies, fanout      |
| 09 | SPA account removal (author + peers)         |

## Signature storage (`user_signatures` / `server_signatures`)

See [`signatures/`](signatures/README.md). **Blank slate — no migration,
no dual-write, no backwards compatibility** (hard cutover; recreate DB).

| #  | Title                                              |
|----|----------------------------------------------------|
| 00 | Design + table shapes                              |
| 01 | DDL for `user_signatures` + `server_signatures`    |
| 02 | Store helpers                                      |
| 03 | Switch `users` to signature FKs                    |
| 04 | Switch `user_keys` to server signature FK          |
| 05 | Switch `user_key_revocations` to signature FKs     |
| 06 | Switch `reed_removals` (and account later)         |
| 07 | Drop legacy columns *(cancelled — absorbed into 03–06)* |
| 08 | Nested `userSignature` / `serverSignature` wire |
| 09 | Verify every signed resource before store |

## Observability (request + DB query tracing)

See [`observability/`](observability/README.md). Closes the gap between the
existing (unused) OTEL SDK scaffolding in `observability.go` and an actual
per-request trace with nested DB query spans, landing in the same
OpenObserve stack that already receives logs and host metrics.

| #  | Title                                                        |
|----|---------------------------------------------------------------|
| 00 | Design + architecture + locked decisions                    |
| 01 | OTLP trace receiver on the app-host collector (`rpi` repo)   |
| 02 | Wire `SetupObservability` + HTTP request spans               |
| 03 | DB query spans via `otelsql`                                 |
| 04 | Thread `context.Context` so DB spans nest under the request |

## Parallelism

- **Remaining open (prerequisites):** 09 (revocation fanout), 11.
- **After 01 lands**: 03, 04, 05, 06, 07 unblocked on `BytesToSign`
  (most of these are already shipped).
- **After 06+10 land**: 09 (revocation fanout) unblocks.
- **Signatures 09** (verify-before-store) is implemented; attested
  possession cancelled.
- **Recovery feature steps** land only after the prerequisites they need;
  within `recovery/`, follow that directory's depends-on column (00→07).
  Step 00 (keychain passphrase) can land independently and unblocks 01/02.
- **Invites feature steps** are independent of recovery; within `invites/`,
  follow that directory's depends-on column (00→05). Step 00 can land alone.
- **Conversations feature steps** are independent of recovery; within
  `conversations/`, follow that directory's depends-on column (00→05). Step
  01 (publish verify) is valuable security hardening on its own.
- **Deletion feature steps** are independent of recovery; within
  `deletion/`, follow that directory's depends-on column. After 00, account
  schema (07) may parallel the reed track; 08 needs 02+04; 09 needs 06+08.
  Tip check ([recovery 16](recovery/16_reed_tip_check.md)) should exclude
  removed reeds (and block account-removed authors) once deletion lands.
- **Signature storage steps** are deferred relative to deletion and are
  **blank slate** (no migration / dual-write / client compat); within
  `signatures/`, follow that directory's depends-on column (00→06, 08;
  07 cancelled; 09 proposed after wire). Steps 03–06 may parallel after
  02. Deletion may keep
  inlined columns until signatures 06.
- **Account recovery** ([`account_recovery/`](account_recovery/README.md))
  is independent of server `RECOVERY_MODE` steps; it extends the unified
  restore entry (backup vs keys-only fork). Within `account_recovery/`,
  follow that directory's depends-on column (00→06). Step 01 (export) may
  parallel 02 (API). Step 06 waits on
  [recovery 17](recovery/17_device_binding.md).
- **Protobuf wire** ([`protobuf/`](protobuf/README.md)) is independent of
  recovery/invites feature work but should land as a coordinated server+SPA
  cutover; within `protobuf/`, follow that directory's depends-on column
  (00→06). Steps 01–02 (schema) may proceed before flipping traffic;
  04 and 05 are the hard cutovers.
- **Pipes** ([`pipes/`](pipes/README.md)) need publish READY fanout and the
  SignReed content path (for tag extract); within `pipes/`, follow 00→03.
  Mentions tag-index pattern ([conversations 04](conversations/04_mentions.md))
  is the closest cousin.
- **Observability** ([`observability/`](observability/README.md)) is
  independent of every other track above — pure infra/plumbing, no schema or
  wire changes. Step 01 lives in the `rpi` ops repo and can land any time;
  02→03 can be developed against a local OTLP endpoint before 01 reaches the
  Pi; 04 is the large one and is designed to land incrementally (package by
  package) rather than as a single change.

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
