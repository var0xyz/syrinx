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

## Status at a glance

Each table below has a **Status** column per step. Values:

- **Implemented** — landed in server and/or SPA.
- **In progress** — some steps landed, others still open (see per-step column).
- **Proposed** — specified, not yet implemented.
- **Cancelled** — dropped or absorbed into another step.

**What's left to build** (everything not fully Implemented):

| Track            | Status      | Remaining                                            |
|------------------|-------------|------------------------------------------------------|
| Prerequisites    | In progress | 09 (revocation fanout), 11 (notifications, deferred) |
| Recovery feature | In progress | 16 (reed tip check), 17 (device binding)             |
| Conversations    | Proposed    | 00–05                                                |
| Publish ready    | Proposed    | 00–02                                                |
| Avatars          | Proposed    | 00–05                                                |
| Pipes            | Implemented | —                                                    |
| Account recovery | Proposed    | 00–06                                                |
| Protobuf wire    | In progress | 01, 03, 04, 06 (HTTP + shared protos + SPA types)    |
| Observability    | Proposed    | 00–04                                                |

**Already done:** Invites, Coverage, Deletion, Signature storage, and the
prerequisites other than 09/11.

## Prerequisite proposals

| #  | Title                                              | Depends on     | Status      |
|----|----------------------------------------------------|----------------|-------------|
| 01 | Fix reed countersignature signer/verifier mismatch | —              | Implemented |
| 02 | Random, server-scoped user IDs                     | —              | Implemented |
| 03 | reed `server` block (bind reedID/authorID/fp)      | 01             | Implemented |
| 04 | Signed identity records at signup / rotation       | 01; 02         | Implemented |
| 05 | Signed profile updates                             | 01, 04         | Implemented |
| 06 | Signed key revocations                             | 01             | Implemented |
| 07 | Server-signed client keys on distribution          | 01             | Implemented |
| 08 | Client signature validation                        | 01, 03, 07, 09 | Implemented |
| 09 | Revocation realtime fanout + catch-up              | 06, 10         | Proposed    |
| 10 | Revocations as a separate signed resource          | 01             | Implemented |
| 11 | Per-user system-notification store                 | —              | Deferred    |

## Recovery feature steps

See [`recovery/`](recovery/README.md):

| #  | Title                                                 | Status      |
|----|-------------------------------------------------------|-------------|
| 00 | Server key passphrase (keychain + optional HA env)    | Implemented |
| 01 | Key bundle export (`ops` CLI)                         | Implemented |
| 02 | Key bundle import (`ops` CLI)                         | Implemented |
| 03 | `RECOVERY_MODE` boot, bookkeeping, import gate, flags | Implemented |
| 04 | Own identity claim                                    | Implemented |
| 05 | Peer identity report-back                             | Implemented |
| 06 | Reeds, follows, complete                              | Implemented |
| 07 | SPA recover client                                    | Implemented |

**Track status: In progress.** The server recovery API and SPA restore flow
(sub-directory steps 00–15, backup-first unified restore) are implemented; the
reed tip check ([recovery 16](recovery/16_reed_tip_check.md)) and device
binding ([recovery 17](recovery/17_device_binding.md)) remain **Proposed**.

## Invites / signup modes

See [`invites/`](invites/README.md):

| #  | Title                                             | Status      |
|----|---------------------------------------------------|-------------|
| 00 | `SIGNUP_MODE` + `MAX_INVITES_PER_USER`, info gate | Implemented |
| 01 | `invites` table, `users.invited_by`, store        | Implemented |
| 02 | Create / list / revoke / check APIs + quota       | Implemented |
| 03 | Consume at signup, identity, `invitedBy`          | Implemented |
| 04 | Home CTA + invite-link signup path                | Implemented |
| 05 | Toolbar Invites tab + management UI               | Implemented |

## Echoes and replies (conversations)

See [`conversations/`](conversations/README.md):

| #  | Title                                                          | Status   |
|----|----------------------------------------------------------------|----------|
| 00 | Design + UX model (echo count, one-level drill-down)           | Proposed |
| 01 | Verify publish payload (form fields); normalize `replying` ref | Proposed |
| 02 | Echo/reply index tables + list/count APIs                      | Proposed |
| 03 | Echo count + conversation section on reed detail               | Proposed |
| 04 | Mentions (`@` → `web+syrinx` links + `reed_mentions` index)    | Proposed |
| 05 | Recursive reply counts: thread total + per-reed subtree count  | Proposed |

## Reed network coverage

See [`coverage/`](coverage/README.md):

| #  | Title                                            | Status      |
|----|--------------------------------------------------|-------------|
| 00 | Design + UX + formula                            | Implemented |
| 01 | Denormalized counters                            | Implemented |
| 02 | WS subscribe snapshot ACK + live echoes/coverage | Implemented |

## Publish ready (fanout gate)

See [`publish/`](publish/README.md):

| #  | Title                                       | Status   |
|----|---------------------------------------------|----------|
| 00 | Design + publish/relay race + locked model  | Proposed |
| 01 | HTTP SignReed + WS `PUBLISH_READY` + SPA    | Proposed |
| 02 | Real `RELAY_MISS` (drop allocation + retry) | Proposed |

## Custom avatars (hash + processed PNG)

See [`avatars/`](avatars/README.md):

| #  | Title                                      | Status   |
|----|--------------------------------------------|----------|
| 00 | Design + locked model                      | Proposed |
| 01 | `avatars` table + `avatarHash` in identity | Proposed |
| 02 | Authenticated process endpoint             | Proposed |
| 03 | Profile PUT: set / keep / clear            | Proposed |
| 04 | `GET /avatars/<hash>`                      | Proposed |
| 05 | SPA crop, IndexedDB, fetch/GC, Avatar      | Proposed |

## Pipes (live hashtags)

See [`pipes/`](pipes/README.md). Ephemeral server-side tag listening;
local reeds with that tag remain on device.

| #  | Title                                     | Status   |
|----|-------------------------------------------|----------|
| 00 | Design + naming (**pipe**) + locked model | Implemented |
| 01 | Extract tags; stash on `pending_fanout` until READY | Implemented |
| 02 | WS subscribe + READY fanout               | Implemented |
| 03 | SPA links + `/pipe/[tag]` page            | Implemented |

## Account recovery (key-only restore)

See [`account_recovery/`](account_recovery/README.md). Distinct from server
`RECOVERY_MODE` ([`recovery/`](recovery/README.md)): the user reconstitutes a
client from private keys while the server still holds the account; peers
relay the user’s own reed bodies back.

| #  | Title                                         | Status   |
|----|-----------------------------------------------|----------|
| 00 | Design + tip approaches + restore fork        | Proposed |
| 01 | Key export format + profile Export key        | Proposed |
| 02 | Challenge + bootstrap API + rehydration row   | Proposed |
| 03 | Server-orchestrated own-reed relay + complete | Proposed |
| 04 | SPA keys-only `/import` fork + session        | Proposed |
| 05 | SPA rehydration + tip `previousID` + UX       | Proposed |
| 06 | Device binding on bootstrap (takeover)        | Proposed |

## Protobuf wire (HTTP + WebSocket)

See [`protobuf/`](protobuf/README.md). Blank-slate cutover of all
client↔server bodies and WS frames to Protocol Buffers; `BytesToSign`
unchanged.

| #  | Title                             | Status      |
|----|-----------------------------------|-------------|
| 00 | Design + locked model             | Proposed    |
| 01 | Shared resource protos + codegen  | Proposed    |
| 02 | WebSocket envelope + event protos | Implemented |
| 03 | HTTP encode/decode + content type | Proposed    |
| 04 | Switch every HTTP handler/client  | Proposed    |
| 05 | Binary WS only; SPA + realtime    | Implemented |
| 06 | SPA consumes generated types      | Proposed    |

**Track status: In progress.** The WebSocket side is done
(`proto/websocket.proto` + generated `websocket.pb.go`; realtime uses binary
protobuf frames). The shared resource protos and the HTTP codec/endpoint
cutover (01, 03, 04, 06) are still **Proposed**.

## Signed deletions (reeds + accounts)

See [`deletion/`](deletion/README.md):

| #  | Title                                        | Status      |
|----|----------------------------------------------|-------------|
| 00 | Design + trust model                         | Implemented |
| 01 | Reed-removal schema                          | Implemented |
| 02 | Reed-removal canonical payload + countersign | Implemented |
| 03 | Reed-removal API (idempotent)                | Implemented |
| 04 | Reed-removal realtime fanout + sync catch-up | Implemented |
| 05 | SPA author queue (`pendingRemoval`)          | Implemented |
| 06 | SPA holders: verify cert → drop reed         | Implemented |
| 07 | Account-removal schema + store               | Implemented |
| 08 | Account-removal API, 410 bodies, fanout      | Implemented |
| 09 | SPA account removal (author + peers)         | Implemented |

## Signature storage (`user_signatures` / `server_signatures`)

See [`signatures/`](signatures/README.md). **Blank slate — no migration,
no dual-write, no backwards compatibility** (hard cutover; recreate DB).

| #  | Title                                                   | Status      |
|----|---------------------------------------------------------|-------------|
| 00 | Design + table shapes                                   | Implemented |
| 01 | DDL for `user_signatures` + `server_signatures`         | Implemented |
| 02 | Store helpers                                           | Implemented |
| 03 | Switch `users` to signature FKs                         | Implemented |
| 04 | Switch `user_keys` to server signature FK               | Implemented |
| 05 | Switch `user_key_revocations` to signature FKs          | Implemented |
| 06 | Switch `reed_removals` (and account later)              | Implemented |
| 07 | Drop legacy columns *(cancelled — absorbed into 03–06)* | Cancelled   |
| 08 | Nested `userSignature` / `serverSignature` wire         | Implemented |
| 09 | Verify every signed resource before store               | Implemented |

## Observability (request + DB query tracing)

See [`observability/`](observability/README.md). Closes the gap between the
existing (unused) OTEL SDK scaffolding in `observability.go` and an actual
per-request trace with nested DB query spans, landing in the same
OpenObserve stack that already receives logs and host metrics.

| #  | Title                                                       | Status   |
|----|-------------------------------------------------------------|----------|
| 00 | Design + architecture + locked decisions                    | Proposed |
| 01 | OTLP trace receiver on the app-host collector (`rpi` repo)  | Proposed |
| 02 | Wire `SetupObservability` + HTTP request spans              | Proposed |
| 03 | DB query spans via `otelsql`                                | Proposed |
| 04 | Thread `context.Context` so DB spans nest under the request | Proposed |

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
- **Pipes** ([`pipes/`](pipes/README.md)) — Implemented (00–03).
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
