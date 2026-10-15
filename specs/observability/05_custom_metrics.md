# Observability 05 — Custom business metrics

## Status

Implemented.

## Depends on

[02](02_app_bootstrap.md) (needs a live `MeterProvider`; `observability.Setup` already
registers one when `OTEL_COLLECTOR_HOST` is set).

## Context

Steps 02–04 answer **how long** requests and queries take. Operators also need
**what happened** counters and distributions: signups, publishes, echoes,
replies, deletions (reeds and accounts), key revocations, WebSocket traffic,
per-reed network reach, admin requests and actions, and the refusals that
show abuse (failed auth, suspended or blocked callers, rejected signups,
cooldowns) — keyed by raw user id so an operator can name who did it, never
by username, bio, tag text, or content.

The OTEL SDK already exports **DB pool metrics** via `RegisterDBStatsMetrics`
when observability is enabled. This step adds **domain metrics** on the same
OTLP HTTP pipeline (`localhost:4318` → app-host collector → OpenObserve).

## Scope

### 5.1 Metric recorder API

`observability/metrics.Recorder` is a typed interface with one method per
event (see `metrics.go` for the full list); `Noop` is the inert
implementation and `OTEL` the SDK-backed one.

When observability is disabled, return a no-op implementation (same pattern as
`Manager.Middleware` / `OpenDB`).

Wire the recorder once at boot in `main.go` and inject into `Handlers`,
`DataService` (or only handlers — see instrumentation table), and
`RealtimeService`.

### 5.2 Instrument names and shapes

All instruments use the `syrinx.` prefix and land in the same OpenObserve
stream as host metrics today.

| Instrument | OTEL type | When recorded | Attributes |
|---|---|---|---|
| `syrinx.users.created` | Counter | Successful signup or recovery import (new `users` row) | `signup.mode` (`open` \| `invite` \| `closed` \| `import`), `user.id`, `inviter.id` (empty without an invite) |
| `syrinx.users.deleted` | Counter | Account removal: `DeleteMe` (new cert) or an approved kick | `user.id`, `removal.kind` (`self` \| `kick`), `note.has` (bool; never the note text) |
| `syrinx.users.updated` | Counter | Successful `UpdateUser` | `user.id`, `renamed` (bool) |
| `syrinx.reeds.published` | Counter | Successful `SignReed` (new reed, not replay), and once per part of a new thread | `reed.kind` (`plain` \| `echo` \| `reply` \| `thread`), `tags.has`, `tags.count` (0–4+), `author.id`, `reed.id` |
| `syrinx.reeds.deleted` | Counter | Successful reed removal (new cert) | `author.id`, `reed.id` |
| `syrinx.threads.published` | Counter | Successful `CreateThread` (new thread, not replay) | `author.id`, `thread.id` |
| `syrinx.thread.size` | Histogram | Same hook as `syrinx.threads.published` | `author.id`, `thread.id` — value = number of parts |
| `syrinx.echoes.targeted` | Counter | Echo indexed on a target reed | `author.id` (who echoed), `target.author.id`, `target.reed.id` |
| `syrinx.keys.revoked` | Counter | Key rotation, or an approved rebind | `user.id`, `revocation.kind` (`rotation` \| `rebind`) |
| `syrinx.users.backup` | Counter | SPA reports a local export via `POST /users/me/backup` | `user.id`, `backup.kind` (`identity` \| `full`) |
| `syrinx.reed.holders` | Histogram | Whenever holder count changes for a reed | `author.id`, `reed.id` — value = `allocation_count` |
| `syrinx.reed.coverage_percent` | Histogram | Same hook as holders | `author.id`, `reed.id` — value = 0–100 |
| `syrinx.ws.messages` | Counter | Every WS frame handled (see 5.4) | `ws.direction` (`in` \| `out`), `ws.message.type` |
| `syrinx.relay.event` | Counter | Every `pending_events` row created/fulfilled/deleted | `relay.lifecycle`, `event.kind`, `event.id`, `requester.id` (on `created` only) |
| `syrinx.federation.relay` | Counter | Every federation HTTP call, either side (relay legs, proxied reads, follows, key fetches, connect) | `federation.direction`, `peer.server_id`, `federation.leg`, `ok` (2xx) |
| `syrinx.federation.lifecycle` | Counter | Invitation created/revoked/redeemed, attempt approved/rejected, disconnect requested/confirmed/cancelled, purge | `event`, `peer.server_id` (empty when not yet known), `admin.id` |
| `syrinx.federation.delivery.backlog` | Gauge | Each retry of behind peer streams | `peer.server_id` — value = author streams behind |
| `syrinx.keys.fetch_errors` | Counter | Client-reported `KEY_FETCH_ERROR` | `reporter.id`, `target.id`, `key.id` |
| `syrinx.keys.revoked_used` | Counter | Client-reported `REVOKED_KEY_USED` | `reporter.id`, `target.id`, `key.id` |
| `syrinx.content.rejected` | Counter | Client-reported `CONTENT_REJECTED` | `reporter.id`, `store.name`, `reason` |
| `syrinx.mentions.claim_rejected` | Counter | `DeleteMention` | `author.id`, `reporter.id`, `reason` |
| `syrinx.admin.requests.opened` | Counter | `OpenAdminRequest` stores a request | `action`, `requester.id`, `requester.role`, `targets.count` |
| `syrinx.admin.requests.refused` | Counter | An admin request or decision refused | `action`, `actor.id`, `stage` (`open` \| `decide`), `reason` (fixed code on each `refuse`) |
| `syrinx.admin.requests.decided` | Counter | `DecideAdminRequest` closes a request | `action`, `decision`, `requester.id`, `decider.id` |
| `syrinx.admin.request.decision_seconds` | Histogram | Approvals and rejections | `action` — value = open-to-decision seconds |
| `syrinx.admin.requests.moot` | Counter | A pending request loses every target | `action` |
| `syrinx.admin.actions.executed` | Counter | An approved action runs, once per target | `action`, `target.id`, `requester.id`, `approver.id`, `ok` |
| `syrinx.admin.suspended_refusals` | Counter | A suspended user is refused | `user.id`, `surface` (`http` \| `ws`) |
| `syrinx.admin.mail.sent` | Counter | Broadcast or private admin mail delivered | `action`, `sender.id`, `recipients.count`, `failed.count` |
| `syrinx.admin.invites.revoked` | Counter (by count) | Kick, rebind or demote revokes open invites | `action`, `target.id` |
| `syrinx.admin.reserved_usernames.changed` | Counter | Root reserves or releases a username | `op` (`add` \| `remove`), `root.id` |
| `syrinx.admin.reserved_usernames.hits` | Counter | A reserved name is refused | `surface` (`signup` \| `rename`), `user.id` |
| `syrinx.admin.foreign_kicks` | Counter | A peer's kick of its user is verified | `peer.server_id`, `ok` |
| `syrinx.flags.created` | Counter | A flag is stored, from a member or a peer | `reporter.id`, `author.id`, `reed.id`, `origin` (`local` \| `peer`), `note.has` |
| `syrinx.flags.dismissed` | Counter | An admin dismisses a reed's flags | `admin.id`, `reed.id`, `flags.count` |
| `syrinx.ledger.appended` | Counter | A ledger entry commits | `entry.kind` |
| `syrinx.server_key.revocations` | Gauge | Boot | `compromised` (bool) — a step between boots is a rotation |
| `syrinx.auth.rejected` | Counter | Signature-auth, server-key-proof or device middleware refuses a request | `key.id` (as claimed), `user.id` (empty when the key is unknown), `reason`, `route` (mux template) |
| `syrinx.ws.handshake.rejected` | Counter | A WebSocket handshake is refused | `key.id`, `user.id`, `reason` |
| `syrinx.ws.connections` | UpDownCounter | WebSocket connect/disconnect | — |
| `syrinx.ws.sessions` | Counter | WebSocket connect/disconnect | `user.id`, `event` |
| `syrinx.signup.rejected` | Counter | `Signup` refused | `signup.mode`, `reason` |
| `syrinx.username.checks` | Counter | Username availability checks | `user.id` (rename only), `available` |
| `syrinx.cooldown.hits` | Counter | Key-rotation or vouch cooldown refuses | `kind`, `user.id` |
| `syrinx.blocks.refusals` | Counter | A block refuses a read or request | `viewer.id`, `author.id`, `surface` |
| `syrinx.blocks` | Counter | Block or unblock, local or from a peer | `op`, `user.id`, `blocked.id`, `origin` |
| `syrinx.invites.created` / `.claimed` / `.deleted` | Counter | Invite lifecycle | `inviter.id`, `invitee.id` (claim), `admin_invite` |
| `syrinx.follows` | Counter | Follow or unfollow | `op`, `follower.id`, `followee.id`, `remote` |
| `syrinx.likes` | Counter | Like or unlike stored on the home server | `op`, `user.id`, `author.id`, `reed.id` |
| `syrinx.ripples` | Counter | Ripple posted or deleted | `op`, `user.id`, `author.id`, `reed.id` |
| `syrinx.pins` | Counter | Pin or unpin | `op`, `user.id` |
| `syrinx.vouches` | Counter | Vouch created or withdrawn | `op`, `voucher.id`, `subject.id`, `ok`, `reason` |
| `syrinx.verification_windows` | Counter | Window opened, pinged or closed | `op`, `user.id` |
| `syrinx.devices.bound` | Counter | `BindDevice` or account-recovery bootstrap | `user.id` |
| `syrinx.account_recovery` | Counter | Challenge issued, bootstrap attempted | `stage`, `user.id`, `ok`, `reason` |
| `syrinx.server_recovery` | Counter | `/recovery/*` handlers (`RECOVERY_MODE`) | `stage`, `user.id`, `ok` |
| `syrinx.mailbox` | Counter | Mailbox message sent or acked | `op`, `kind` (send only), `user.id` |
| `syrinx.search.users` | Counter | Local search, or a peer's fan-out | `user.id` (local only), `remote` |

**Echoes and replies** use two complementary counters:

- **`syrinx.reeds.published`** with `reed.kind=echo` / `reed.kind=reply` —
  one increment per echo/reply **sent** (the new reed).
- **`syrinx.echoes.targeted`** — one increment on the **target** reed each
  time an echo is indexed (`reed_echoes` row inserted). Answers “how many
  echoes did this original receive?” without double-counting the echoing reed.

Dashboards that only care about echo volume can filter `reed.kind=echo` on
publish; dashboards about target reach use `syrinx.echoes.targeted` grouped by
`target.author.id` / `target.reed.id`.

**Tag bucketing on publish:** store exact `tags.count` when ≤ 3; use `4` as
“4 or more” on the attribute to cap cardinality from pathological tag spam.
Never emit individual tag names.

**Relay lifecycle and wasted bandwidth:** `syrinx.relay.event` records one
sample per `pending_events` row transition — `created` when a relay request
is registered, `fulfilled` when a `RELAY_RESPONSE` is actually delivered for
it, `deleted` when it's removed without ever recording a `fulfilled` (a
cancellation, a `DATA_ACK`/`DATA_INVALID` cleanup, or the requester going
offline). `event.id` is stable per event, so dashboards can correlate
the three stages for the same request:

- **Leaked requests**: `created` with no matching `fulfilled` or `deleted`
  for the same `event.id` — the request never got an answer and never
  got cleaned up either (there is no timeout/expiry on `pending_events`
  today, so this is currently unbounded until the process restarts).
- **Wasted/duplicate responses**: more than one `fulfilled` for the same
  `event.id` — a holder retransmitted a `RELAY_RESPONSE` after the
  first one already satisfied the request (only possible for event types
  whose row isn't deleted immediately on delivery; see 5.3).

`syrinx.federation.relay` is the same idea for cross-server traffic: one
sample per federation relay HTTP leg, either a peer calling one of this
server's 16 `/federation/relay/*` endpoints (`federation.direction=in`) or
this server calling out to a peer (`federation.direction=out`), plus the
proxied reads, follows, key fetches and connect calls that go through
`doPeerRequest`.
`peer.server_id` (this server's own DB id for the peer, never a raw
domain/URL) lets an operator see how much relay traffic a specific foreign
server is generating.

### 5.3 Instrumentation call sites

| Event | Call site | Notes |
|---|---|---|
| User created (signup) | `Handlers.Signup` after success | `signup.mode` from server config, `user.id` |
| User created (import) | `recovery.ClaimIdentity` / `recovery.ReportPeerIdentity` when `SaveIdentityResult.Created` | `signup.mode=import`, `user.id` |
| User deleted | `Handlers.DeleteMe` after `InsertAccountRemoval` succeeds (not replay) | `user.id` + `note.has` (`note != ""` after trim); no note text |
| Reed published | `Handlers.SignReed` after successful create (not replay path) | Pass `kind`, tag slice length, `userID`, `reedID` |
| Reed deleted | `Handlers.DeleteReed` after `InsertReedRemoval` succeeds (not replay) | `author.id`, `reed.id` |
| Echo targeted | `Handlers.SignReed` when `echoIndexed && echoRef != nil` | Target = `target.author.id`, `target.reed.id` |
| Key revoked | `Handlers.AddPublicKey` rotation, or the rebind executor | `user.id`, `revocation.kind` |
| User backup | `Handlers.RecordBackup` after authenticated POST | `user.id`, `backup.kind`; no DB — client queues in IndexedDB `pendingBackups` and retries on startup |
| Reed holders + coverage | `RealtimeService.notifyReedCoverage` | Read `allocation_count` + percent already computed for WS; record both histograms with `author.id` |
| Initial coverage at publish | Same hook after author allocation on publish | Ensures every reed gets at least one coverage sample |
| WS inbound | Start of `handleJSONMessage` / `handleProtobufMessage` | Normalize `msgType` / `pb.MessageType_*` to the same string enum |
| WS outbound | `Client.writeMessage` (single choke point) | Parse JSON `type` field or protobuf message type from payload when cheap; else `ws.message.type=binary_unknown` / `json_unknown` |
| Relay event created | `RealtimeService.createPendingReedEvent` / `createPendingAccountEvent` / `createProfileSubscriptionEvent` (wrap the `DBService` inserts) | Every `service.go` call site goes through these wrappers instead of `dbService` directly, so no creation path can skip the metric |
| Relay event fulfilled | `RealtimeService.handleRelayResponse`, right after the `pe == nil` (already-resolved) check | Recorded once per processed `RELAY_RESPONSE`; a same-holder retransmit for a not-yet-deleted event type records a second `fulfilled` for the same `event.id` — the duplicate-response signal |
| Relay event deleted | `RealtimeService.deletePendingEvent` (wraps `DBService.DeletePendingEvent`) / `DeletePendingEventsByUser`'s `RETURNING` rows on disconnect | `DeletePendingEvent` now returns the deleted row's `event_name` so the wrapper can record it; skipped if the row was already gone |
| Federation relay inbound | `main.go` route registration via `Handlers.withFederationRelayMetric` wrapping each of the 16 `/federation/relay/*` handlers | `peer.server_id` from `authenticateAsPeer`'s context value; not recorded if auth rejected the request before reaching the handler |
| Federation relay outbound | `Handlers.callPeerRelayEndpoint` (single choke point for all 16 outbound legs) | `federation.leg` derived from the request path's last segment; `peer.server_id` passed in by each leg's caller (already resolved from the reed/author identity) |

Recovery-mode import paths call `UserCreated` with `signup.mode=import` when a
new `users` row is inserted (own claim or peer seed).

### 5.4 WebSocket type normalization

Maintain one canonical list aligned with
[`realtime/messages.go`](../../realtime/messages.go) and the JSON switch in
[`realtime/service.go`](../../realtime/service.go):

**Client → server (in):** `PING`, `SUBSCRIBE_USER`, `SUBSCRIBE_BROADCAST`,
`UNSUBSCRIBE_USER`, `UNSUBSCRIBE_BROADCAST`, `SYNC_REQUEST`, `REQUEST_REED`,
`RELAY_RESPONSE`, `RELAY_MISS`, `DATA_ACK`, `DATA_INVALID`, `SUBSCRIBE_PROFILE`,
`UNSUBSCRIBE_PROFILE`, `PUBLISH_READY`, `SUBSCRIBE_REED`, `UNSUBSCRIBE_REED`,
`SUBSCRIBE_PIPE`, `UNSUBSCRIBE_PIPE`, plus `unknown_json` / `unknown_protobuf`.

**Server → client (out):** `RELAY_REQUEST`, `REQUEST_ACK`, `DATA_RESPONSE`,
`BROADCAST_REED`, `PIPE_REED`, `FOLLOW_REED`, `REED_REMOVED`,
`ACCOUNT_REMOVED`, `REED_NOT_FOUND`, `REED_NOT_HELD`, `REED_COVERAGE`,
`REED_ECHOES`, `REED_REPLIES`, `subscribed`, `pong`, plus `unknown`.

Do not inspect relay payloads, usernames embedded in delivery messages, or reed
bodies for metrics.

### 5.5 Privacy rules (extends [00](00_design.md))

The server holds usernames and user ids, not content, and metrics exist to
find who is abusing the system. User ids therefore go on attributes as-is;
they were hashed once, but an unsalted hash of a public, enumerable id hid
nothing (and `reed.id` already carried the author in the clear).

Allowed on metric attributes:

- **User ids** (`user.id`, `author.id`, `requester.id`, …) and **key ids**
  (`key.id`), as the full canonical ids the server already stores.
- **Reed IDs** (`reed.id`, `target.reed.id`) and **event ids** (`event.id`).
- `peer.server_id` and the mux **route template** (never the raw URL).
- Structural enums (`reed.kind`, `signup.mode`, `action`, `reason`, `op`,
  `stage`, `surface`, `ws.message.type`), booleans and small integers.
  `reason` and `stage` values are Go constants, never error text.
- **Tag counts**, never tag text.

Forbidden:

- Usernames, bios, display names.
- Account-removal **note** text, admin request reasons, mailbox text, flag
  notes, dismissal reasons.
- Hashtag / pipe tag strings.
- Armored keys, signatures, tokens, invite secrets.
- Query arguments, request/response bodies, WS payload fields beyond the
  message type.
- IP addresses or device ids.

Per-reed series (`author.id` + `reed.id`) are intentional — the operator
asked for per-reed coverage. They are not usernames.

### 5.6 OpenObserve usage (operator notes)

Example questions this unlocks:

- Signups/day: `sum(syrinx_users_created)` by `signup_mode`.
- Account churn: `sum(syrinx_users_deleted)` vs `sum(syrinx_users_created)`;
  split by `note_has` to see annotated vs silent departures.
- Publish mix: `sum(syrinx_reeds_published)` by `reed_kind`.
- Echo rate (sent): filter `reed_kind = echo` on publish.
- Echo rate (received by originals): `sum(syrinx_echoes_targeted)` by
  `target_author_id` / `target_reed_id`.
- Reply rate: filter `reed_kind = reply`.
- Thread volume: `sum(syrinx_threads_published)`; parts per thread from
  `syrinx_thread_size`; thread parts among publishes via `reed_kind = thread`.
- Deletion rate: `sum(syrinx_reeds_deleted)`, `sum(syrinx_users_deleted)`.
- Key revocations: `sum(syrinx_keys_revoked)` over time.
- Backup adoption: `sum(syrinx_users_backup)` by `backup_kind`; group by
  `user_id` to see repeat exporters.
- WS health: `sum(syrinx_ws_messages)` by `ws_direction`, `ws_message_type`.
- Reed reach: `syrinx_reed_holders` / `syrinx_reed_coverage_percent` for a
  given `reed_id` + `author_id`.
- Who is probing auth: `sum(syrinx_auth_rejected)` by `user_id`, `key_id`,
  `reason`; the same for `syrinx_ws_handshake_rejected`.
- Admin key misuse: `sum(syrinx_admin_requests_refused)` by `actor_id`,
  `reason`.
- Flag pressure on an author: `sum(syrinx_flags_created)` by `author_id`.
- Leaked relay requests: per `event_id`, `created` with no matching
  `fulfilled`/`deleted` in a sufficiently wide window (see the "Relay" tab's
  `syrinx_relay_leaked` panel for the exact SQL).
- Wasted/duplicate responses: per `event_id`, more than one `fulfilled`
  (`syrinx_relay_duplicate_fulfilled` panel).
- Federation traffic by peer: `sum(syrinx_federation_relay)` by
  `peer_server_id`, `federation_direction`, `federation_leg`.
- Federation error rate: `sum(syrinx_federation_relay{ok="false"})`.

Cardinality grows with total reeds (holder/coverage histograms). Start without
sampling; add explicit aggregation views or drop `reed.id` from long-retention
rollups only if storage becomes an issue.

**OpenObserve histogram queries:** OTLP histogram instruments (`syrinx.reed.*`
holders/coverage, admin decision time) decompose into `_sum`, `_count`, and `_bucket`
metric streams — not a single stream named after the instrument. Dashboards
should use `_sum / _count` for mean values (see
`rpi/telemetry/dashboards/generate_syrinx_dashboard.py` `histogram_avg()`).
Querying the bare instrument name returns no data.

## Non-goals

- Client-side (SPA) metrics — server-side only in v1.
- Per-holder identity (who holds a reed) — aggregate holder count only.
- Cross-instance federation metrics.
- Alerting rules — **not planned**; dashboards only.
- Replacing structured logs; metrics complement zerolog, not replace it.
- Tag-name / pipe-name popularity analytics (count only).

## Verification

1. Boot with `OTEL_COLLECTOR_HOST` pointing at a local collector; confirm
   instruments appear in OpenObserve within one export interval (~10s).
2. Signup → `syrinx.users.created` +1.
3. Publish plain reed → `syrinx.reeds.published{kind=plain}`, length histograms,
   holder/coverage samples.
4. Publish echo → `kind=echo` on publish **and** `syrinx.echoes.targeted` on
   the original reed; target echo count updates over WS (existing behaviour).
5. Publish reply → `kind=reply`.
6. Delete reed → `syrinx.reeds.deleted`; replay with same signature → no
   second increment.
7. Delete account → `syrinx.users.deleted`; replay → no second increment.
8. Revoke key → `syrinx.keys.revoked`; export backup → `syrinx.users.backup` with `user.id`.
9. Recovery claim / peer seed (new user) → `syrinx.users.created{signup_mode=import}`.
10. Connect WS client; send `REQUEST_REED` → inbound counter; observe outbound
   counters for server responses.
11. `DATA_ACK` from a second user → holder histogram increases for that reed.
12. Attempt over-limit reed → `syrinx.reeds.rejected.length` +1, no publish
    counter.
13. Confirm exported series contain no username/tag-text/note fields or raw
    user ids (spot-check attribute keys in OpenObserve).
