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

| Track            | Status      | Remaining                                         |
|------------------|-------------|---------------------------------------------------|
| Ripples          | Proposed    | 00–04                                             |
| Notifications    | Proposed    | 00–05                                             |
| Load testing     | Proposed    | 00–03                                             |
| Federation       | In progress | 00, 02–05 (depends on roles)                      |
| Protobuf wire    | In progress | 01, 03, 04, 06 (HTTP + shared protos + SPA types) |
| Publish ready    | Implemented | —                                                 |
| Pipes            | Implemented | —                                                 |
| Conversations    | Implemented | —                                                 |
| Recovery feature | Implemented | 00–17 implemented                                 |
| Account recovery | Implemented | 01–07 implemented                                 |
| Observability    | Implemented | Steps 01–05                                       |
| Roles            | Implemented | 00–02                                             |
| Prerequisites    | Implemented | 01–10 (11 superseded — see Notifications)         |
| Avatars          | Deferred    | 00–05                                             |
| Likes            | Proposed    | 00–06                                             |
| Pagination       | In progress | 02–05                                             |

**Already done:** Invites, Coverage, Deletion, Signature storage, Publish
ready, Conversations, Recovery feature, and all prerequisites 01–10 (11 is
superseded by [`notifications/`](notifications/README.md), which is its own track,
separate from the recovery prerequisites — see below).

## Prerequisite proposals

| #  | Title                                              | Depends on | Status      |
|----|----------------------------------------------------|------------|-------------|
| 01 | Fix reed countersignature signer/verifier mismatch | —          | Implemented |
| 02 | Random, server-scoped user IDs                     | —          | Implemented |
| 03 | reed `server` block (bind reedID/authorID/fp)      | 01         | Implemented |
| 04 | Signed identity records at signup / rotation       | 01; 02     | Implemented |
| 05 | Signed profile updates                             | 01, 04     | Implemented |
| 06 | Signed key revocations                             | 01         | Implemented |
| 07 | Server-signed client keys on distribution          | 01         | Implemented |
| 08 | Client signature validation                        | 01, 03, 07 | Implemented |
| 09 | Revocation: on-demand check, not fanout            | 06, 10     | Implemented |
| 10 | Revocations as a separate signed resource          | 01         | Implemented |
| 11 | Per-user system-notification store                 | —          | Superseded  |

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

**Track status: Implemented.** All sub-directory steps (00–17) have landed,
including backup-first unified restore (00–15), device binding
([recovery 17](recovery/17_device_binding.md)), and the reed tip check
history-fork safeguard ([recovery 16](recovery/16_reed_tip_check.md)).

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

| #  | Title                                                          | Status      |
|----|----------------------------------------------------------------|-------------|
| 00 | Design + UX model (echo count, one-level drill-down)           | Implemented |
| 01 | Verify publish payload (form fields); normalize `replying` ref | Implemented |
| 02 | Echo/reply index tables + list/count APIs                      | Implemented |
| 03 | Conversation section + local reply caches on reed detail       | Implemented |
| 04 | Mentions (`@` → `~userID@serverID` + `reed_mentions` index)    | Implemented |
| 05 | Recursive reply counts: thread total + per-reed subtree count  | Implemented |

## Reed network coverage

See [`coverage/`](coverage/README.md):

| #  | Title                                            | Status      |
|----|--------------------------------------------------|-------------|
| 00 | Design + UX + formula                            | Implemented |
| 01 | Denormalized counters                            | Implemented |
| 02 | WS subscribe snapshot ACK + live echoes/coverage | Implemented |

## Reed likes

See [`likes/`](likes/README.md). Like is signed and server-countersigned;
unlike is a plain unsigned `DELETE` (hard row delete, no cert). Both are
offline-first (`pendingLikes` / `pendingUnlike` outboxes), backed by a
denormalized `reeds.like_count`, with live count updates via the existing
per-reed WS subscription (`REED_LIKES` alongside
`REED_ECHOES`/`REED_COVERAGE`), and a "Liked reeds" feed entry point on
the profile page.

| #  | Title                                              | Status   |
|----|-----------------------------------------------------|----------|
| 00 | Design + locked model                              | Proposed |
| 01 | `reeds_liked` schema + denormalized like count     | Proposed |
| 02 | Like canonical payload + countersign               | Proposed |
| 03 | Like (signed) / unlike (unsigned) API (idempotent) | Proposed |
| 04 | `REED_LIKES` subscribe snapshot + live updates     | Proposed |
| 05 | SPA `pendingLikes`/`pendingUnlike` + like button   | Proposed |
| 06 | SPA "Liked reeds" list (profile entry point)       | Proposed |

## Publish ready (fanout gate)

See [`publish/`](publish/README.md):

| #  | Title                                       | Status      |
|----|---------------------------------------------|-------------|
| 00 | Design + publish/relay race + locked model  | Implemented |
| 01 | HTTP SignReed + WS `PUBLISH_READY` + SPA    | Implemented |
| 02 | Real `RELAY_MISS` (drop allocation + retry) | Implemented |

## Roles (root, admin, user)

See [`roles/`](roles/README.md). Local role tiers in code; first capability:
admins may invite other admins. Prerequisite for federation operator actions.

| #  | Title                                      | Status      |
|----|--------------------------------------------|-------------|
| 00 | Design + locked model                      | Proposed    |
| 01 | `users.role` column + code helpers         | Implemented |
| 02 | Admin-only admin invites (create + signup) | Implemented |
| 03 | Role on profile countersignature           | Implemented |

## Federation (explicit peering + cross-server content)

See [`federation/`](federation/README.md). Encrypted admin invite, server
`connect` callback, then trust/runtime-verify/content-relay — each doc's
own Status header has the precise shipped-vs-designed breakdown; this
table is just the rollup.

| #  | Title                                            | Status      |
|----|--------------------------------------------------|-------------|
| 00 | Design + handshake model                         | Superseded by shipped design (02) |
| 01 | Invitation create + `federation_invitation` + UI | Implemented |
| 02 | Connect handshake + `federation_attempt`         | Implemented (deviated — see doc for exact shape) |
| 03 | Second-admin approval + `federation_established` | Implemented as a single-admin gate on `federation_attempt` — no `federation_established` table, and the "second admin" check is never enforced (any admin, including the invite's creator, can approve) |
| 04 | Runtime verify + foreign ref display             | Implemented (trust store simplified, see 03) |
| 05 | Revoke peering + 401 incoming peer traffic       | **Gap: check exists, nothing ever sets `revoked = true`** — no way to actually revoke an approved peer today |
| 06 | Cross-instance content relay                     | Implemented, via a per-operation "leg" pattern (`federation_relay.go`) instead of this doc's generic relay endpoints |
| 07 | Server presence + durable event delivery         | **Gap: shipped fire-and-forget, no durability** — an unreachable peer at notify time silently loses the event, no backlog/retry |

Beyond this doc set's original scope, `federation_relay.go` also covers
mentions, federated user search, and reed-stats/like-count propagation —
none of which had a numbered spec when built. Two feature areas have zero
federation story at all: account/plain-reed deletion (no removal-notify
leg outside reply/echo) and key revocation (no propagation to peers
holding content signed by a revoked key).

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

| #  | Title                                     | Status      |
|----|-------------------------------------------|-------------|
| 00 | Design + naming (**pipe**) + locked model | Implemented |
| 01 | Extract tags                              | Implemented |
| 02 | WS subscribe + READY fanout               | Implemented |
| 03 | SPA links + `/pipe/[tag]` page            | Implemented |

## Notifications

See [`notifications/`](notifications/README.md). Supersedes
[prerequisite 11](11_user_notifications.md). Umbrella spec for three
notification mechanisms — **Alert** (existing client-only toasts,
unchanged), **Mailbox message** (encrypted, one-way, server→one user),
and **Admin mention** (`@everyone`, public, repliable, delivered as an
ordinary reed).

| #  | Title                                                             | Status   |
|----|-------------------------------------------------------------------|----------|
| 00 | Glossary + design + locked model                                  | Proposed |
| 01 | `@everyone` admin broadcast handle                                | Proposed |
| 02 | Mentions tab (list API + SPA)                                     | Proposed |
| 03 | `user_mailbox` schema + `SendMailboxMessage` + `ops mailbox-send` | Proposed |
| 04 | WS delivery + ACK-and-delete                                      | Proposed |
| 05 | SPA bell + `/mailbox/[id]` detail                                 | Proposed |

## Account recovery (key-only restore)

See [`account_recovery/`](account_recovery/README.md). Distinct from server
`RECOVERY_MODE` ([`recovery/`](recovery/README.md)): the user reconstitutes a
client from private keys while the server still holds the account; peers
relay the user’s own reed bodies back.

| #  | Title                                        | Status      |
|----|----------------------------------------------|-------------|
| 00 | Design + tip approaches + restore fork       | Implemented |
| 01 | Identity export `.sxi.gpg` (Backup Keys)     | Implemented |
| 02 | Challenge + bootstrap API                    | Implemented |
| 03 | Client `reedRequests` + paced `REQUEST_REED` | Implemented |
| 04 | SPA keys-only `/import` fork + session       | Implemented |
| 05 | SPA rehydration + tip `previousID` + UX      | Implemented |
| 06 | Device binding on bootstrap (takeover)       | Implemented |
| 07 | Root user `id=1` mint + `.sxi.gpg` export    | Implemented |

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

## Pagination (system-wide audit + convention)

See [`pagination/`](pagination/README.md):

| #  | Title                                          | Status      |
|----|-------------------------------------------------|-------------|
| 00 | Audit + locked convention                       | Implemented |
| 01 | Unify duplicated pagination logic               | Implemented |
| 02 | User search cursor pagination                   | Proposed    |
| 03 | Invites list endpoint                           | Proposed    |
| 04 | Federation admin list/log pagination            | Proposed    |
| 05 | `ReedsList` (profile feed) client-side cursor   | Proposed    |

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

## Observability (request + DB query tracing + business metrics)

See [`observability/`](observability/README.md). Closes the gap between the
OTEL SDK wiring in `observability/` and an actual per-request trace with
nested DB query spans, plus anonymized domain metrics (signups, publishes,
WS traffic, per-reed coverage), landing in the same OpenObserve stack that
already receives logs and host metrics.

| #  | Title                                                             | Status      |
|----|-------------------------------------------------------------------|-------------|
| 00 | Design + architecture + locked decisions                          | Reference   |
| 01 | OTLP trace receiver on the app-host collector (`rpi` repo)        | Implemented |
| 02 | Wire observability bootstrap + HTTP request spans                 | Implemented |
| 03 | DB query spans via `otelsql`                                      | Implemented |
| 04 | Thread `context.Context` so DB spans nest under the request       | Implemented |
| 05 | Custom business metrics (signups, reeds, deletions, WS, coverage) | Implemented |

## Load testing (real browsers, script-driven)

See [`loadtest/`](loadtest/README.md). Drives the real SPA in many isolated
Playwright browser contexts (script-driven via real service/repository
calls, not click simulation) pointed at a target server through Vite's
existing `API_HOST` dev-proxy — no signing/WS-framing code is reimplemented.

| #  | Title                                                             | Status   |
|----|-------------------------------------------------------------------|----------|
| 00 | Design + `API_HOST` proxy trick + locked model                    | Proposed |
| 01 | Extract `performSignup` / `performPublish` into reusable services | Proposed |
| 02 | Playwright driver: virtual users, scenario mix, config            | Proposed |
| 03 | Publish → delivery fanout-latency correlation                     | Proposed |

## Parallelism

- **Prerequisites 01–10 are all Implemented; 11 is Superseded** by
  [`notifications/`](notifications/README.md) — nothing remains open in the
  prerequisites track itself.
- **After 01 lands**: 03, 04, 05, 06, 07 unblocked on `BytesToSign`
  (most of these are already shipped).
- **09** (revocation: on-demand check, not fanout) is implemented; landed
  after 06+10 as an SPA verify-path + throttle change, not a WS fanout.
- **Signatures 09** (verify-before-store) is implemented; attested
  possession cancelled.
- **Recovery feature steps** land only after the prerequisites they need;
  within `recovery/`, follow that directory's depends-on column (00→07).
  Step 00 (keychain passphrase) can land independently and unblocks 01/02.
- **Invites feature steps** are independent of recovery; within `invites/`,
  follow that directory's depends-on column (00→05). Step 00 can land alone.
- **Conversations** ([`conversations/`](conversations/README.md)) —
  Implemented (00–05).
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
- **Likes** ([`likes/`](likes/README.md)) — independent of every other
  track; a straightforward extension of the existing coverage/echo
  subscription plumbing and the reed-removal offline-first pattern, not a
  new mechanism. Within `likes/`, follow 00→06: schema (01) and payload
  (02) can proceed in parallel after 00; 03 needs both; 04 (realtime)
  needs 03; 05 (SPA outbox + button) needs 03 and 04; 06 (liked feed)
  needs 05.
- **Notifications** ([`notifications/`](notifications/README.md)) —
  independent of every other track; supersedes prerequisite 11. Within
  `notifications/`, follow 00→05: 01 (`@everyone`) and 03 (mailbox message
  schema/producers) both only need 00 and can proceed in parallel; 02
  (mentions tab) needs 01; 04 (WS delivery) needs 03; 05 (SPA bell) needs
  04.
- **Roles** ([`roles/`](roles/README.md)) — independent of federation
  implementation but federation admin actions assume roles 01; within
  `roles/`, follow 00→03. Step 01 (column + helpers) unblocks 02 (admin
  invites); 02 extends [`invites`](invites/README.md) create + signup.
- **Federation** ([`federation/`](federation/README.md)) — depends on
  [roles 01](roles/01_role_store.md); within `federation/`, follow 00→04.
  Handshake (01–02) before approval (03) before runtime verify (04); revoke (05).
- **Observability** ([`observability/`](observability/README.md)) is
  independent of every other track above — pure infra/plumbing, no schema or
  wire changes. Step 01 lives in the `rpi` ops repo; 02–05 are implemented in
  syrinx (02/03 spec markdown still references old names — code is current).
  Remaining optional follow-ups: trace sampling policy, alerting, `contextcheck` lint.
- **Load testing** ([`loadtest/`](loadtest/README.md)) is independent of
  every other track — pure test tooling, no server/SPA wire changes beyond
  the small [01](loadtest/01_shared_flow_helpers.md) extraction. Within
  `loadtest/`, follow the depends-on column (00→03); 01 (extracting
  `performSignup`/`performPublish`) can land and be verified against the
  existing e2e suite independently of 02/03.
- **Pagination** ([`pagination/`](pagination/README.md)) — independent of
  every other track; 00 (audit) and 01 (deduplication) are Implemented.
  02–05 are each independent of each other and may land in any order.
- **Ripples** ([`ripples/`](ripples/README.md)) — unsigned, server-only,
  ephemeral reed comments; independent of every other track (new package,
  no shared schema). Within `ripples/`, follow 00→04 in order (schema
  before API before realtime before SPA). Implements
  `docs/planned.md` § Ripples (ephemeral comments).

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
