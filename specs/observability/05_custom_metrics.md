# Observability 05 — Custom business metrics (anonymized)

## Status

Implemented.

## Depends on

[02](02_app_bootstrap.md) (needs a live `MeterProvider`; `observability.Setup` already
registers one when `OTEL_COLLECTOR_HOST` is set).

## Context

Steps 02–04 answer **how long** requests and queries take. Operators also need
**what happened** counters and distributions: signups, publishes, echoes,
replies, deletions (reeds and accounts), key revocations, WebSocket traffic,
per-reed network reach, and reed shape (length, tags) — without usernames,
bios, tag text, or content.

The OTEL SDK already exports **DB pool metrics** via `RegisterDBStatsMetrics`
when observability is enabled. This step adds **domain metrics** on the same
OTLP HTTP pipeline (`localhost:4318` → app-host collector → OpenObserve).

## Scope

### 5.1 Metric recorder API

Add `observability/metrics` (or methods on `observability.Manager`) with a
small typed recorder:

```go
type Recorder interface {
    UserCreated(ctx context.Context, signupMode string)
    UserDeleted(ctx context.Context, userID string, noteHas bool)
    ReedPublished(ctx context.Context, p ReedPublishedAttrs)
    ReedDeleted(ctx context.Context, authorID, reedID string)
    EchoTargeted(ctx context.Context, targetAuthorID, targetReedID string)
    ReedRejectedLength(ctx context.Context, rawChars, visibleChars int)
    KeyRevoked(ctx context.Context, userID string)
    ReedCoverage(ctx context.Context, authorID, reedID string, holders, coveragePercent int)
    WSMessage(ctx context.Context, direction Direction, msgType string)
}
```

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
| `syrinx.users.created` | Counter | Successful `Signup` / import-signup commit | `signup.mode` (`open` \| `invite` \| `closed`) |
| `syrinx.users.deleted` | Counter | Successful account removal (`DeleteMe`, new cert) | `user.id`, `note.has` (bool — whether a non-empty goodbye note was supplied; never the note text) |
| `syrinx.reeds.published` | Counter | Successful `SignReed` (new reed, not replay) | `reed.kind` (`plain` \| `echo` \| `reply`), `tags.has` (bool), `tags.count` (int 0–4+, see bucketing), `author.id`, `reed.id` |
| `syrinx.reeds.deleted` | Counter | Successful reed removal (`DeleteReed`, new cert) | `author.id`, `reed.id` |
| `syrinx.echoes.targeted` | Counter | Echo indexed on a target reed (`CreateReedWithEcho`) | `target.author.id`, `target.reed.id` — the **echoed** reed, not the echoing one |
| `syrinx.reeds.rejected.length` | Counter | `SignReed` rejected for length limits | `raw.exceeds_max`, `visible.exceeds_max` (bool) |
| `syrinx.reed.content.raw_chars` | Histogram | Successful publish | `reed.kind`, `author.id`, `reed.id` — value = `len(body)` (JS string length) |
| `syrinx.reed.content.visible_chars` | Histogram | Successful publish | same — value = `CountMarkdownCharacters(body)` |
| `syrinx.keys.revoked` | Counter | Successful key rotation (`AddPublicKey` after predecessor verified revoked) | `user.id` |
| `syrinx.users.backup` | Counter | SPA reports a successful local export via `POST /users/me/backup` | `user.id_hash` (SHA-256 hex of `user.id`), `backup.kind` (`identity` \| `full`) |
| `syrinx.reed.holders` | Histogram | Whenever holder count changes for a reed | `author.id`, `reed.id` — value = `allocation_count` |
| `syrinx.reed.coverage_percent` | Histogram | Same hook as holders | `author.id`, `reed.id` — value = `coveragePercent` (0–100) |
| `syrinx.ws.messages` | Counter | Every WS frame handled (see 5.4) | `ws.direction` (`in` \| `out`), `ws.message.type` (normalized type name) |

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

**Length abuse:** histograms show the distribution of accepted reeds; the
rejected counter fires on HTTP 400 from `ReedContentWithinLimits` failure.
If a reed ever passes the handler check with `raw_chars > MaxReedRawChars` or
`visible_chars > MaxReedVisibleChars`, also set a log-only `[WARN]` — that
would indicate a validation bug, not a normal metric path.

Constants today: `MaxReedRawChars = 1400`, `MaxReedVisibleChars = 140`
(`services.go`).

### 5.3 Instrumentation call sites

| Event | Call site | Notes |
|---|---|---|
| User created | `DataService.Signup` after commit (or `Handlers.Signup` after success) | Include resolved `signup.mode`; do **not** record failed/duplicate attempts |
| User deleted | `Handlers.DeleteMe` after `InsertAccountRemoval` succeeds (not replay) | `user.id` + `note.has` (`note != ""` after trim); no note text |
| Reed published | `Handlers.SignReed` after successful create (not replay path) | Pass `kind`, tag slice length, `userID`, `reedID`, body lengths |
| Reed deleted | `Handlers.DeleteReed` after `InsertReedRemoval` succeeds (not replay) | `author.id`, `reed.id` |
| Echo targeted | `Handlers.SignReed` when `echoIndexed && echoRef != nil` | Target = `echoRef.AuthorID`, `echoRef.ReedID` |
| Reed rejected (length) | `Handlers.SignReed` before 400 on limit check | Raw + visible counts only |
| Key revoked | `Handlers.AddPublicKey` after successful rotation | `user.id` only — predecessor must already be revoked |
| User backup | `Handlers.RecordBackup` after authenticated POST | `user.id_hash`, `backup.kind`; no DB — client queues in IndexedDB `pendingBackups` and retries on startup |
| Reed holders + coverage | `RealtimeService.notifyReedCoverage` | Read `allocation_count` + percent already computed for WS; record both histograms |
| Initial coverage at publish | Same hook after author allocation on publish | Ensures every reed gets at least one coverage sample |
| WS inbound | Start of `handleJSONMessage` / `handleProtobufMessage` | Normalize `msgType` / `pb.MessageType_*` to the same string enum |
| WS outbound | `Client.writeMessage` (single choke point) | Parse JSON `type` field or protobuf message type from payload when cheap; else `ws.message.type=binary_unknown` / `json_unknown` |

Recovery-mode signup/import paths that create users should call `UserCreated`
with the active `signup.mode`.

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

Allowed on metric attributes:

- Server-scoped **user IDs** and **reed IDs** (opaque identifiers, not
  usernames), except where **`user.id_hash`** is used instead (backup
  telemetry — SHA-256 hex digest of the user id).
- Structural enums (`reed.kind`, `signup.mode`, `ws.message.type`, booleans,
  small integers).
- Content **lengths** and **tag counts**, never tag text or body text.

Forbidden:

- Usernames, bios, display names.
- Account-removal **note** text (goodbye messages).
- Hashtag / pipe tag strings.
- Key fingerprints, armored keys, signatures, tokens, invite secrets.
- Query arguments, request/response bodies, WS payload fields beyond the
  message type.
- IP addresses or device ids.

Per-reed series (`author.id` + `reed.id`) are intentional — the operator
asked for per-reed coverage and length/tag shape. They are not usernames.

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
- Deletion rate: `sum(syrinx_reeds_deleted)`, `sum(syrinx_users_deleted)`.
- Key rotations completed: `sum(syrinx_keys_revoked)` over time.
- Backup adoption: `sum(syrinx_users_backup)` by `backup_kind`; group by
  `user_id_hash` to see repeat exporters without raw user ids.
- WS health: `sum(syrinx_ws_messages)` by `ws_direction`, `ws_message_type`.
- Reed reach: `syrinx_reed_holders` / `syrinx_reed_coverage_percent` for a
  given `reed_id` + `author_id`.
- Length abuse: `syrinx_reeds_rejected_length` > 0, or
  `syrinx_reed_content_raw_chars` p99 near 1400.

Cardinality grows with total reeds (holder/coverage histograms). Start without
sampling; add explicit aggregation views or drop `reed.id` from long-retention
rollups only if storage becomes an issue.

**OpenObserve histogram queries:** OTLP histogram instruments (`syrinx.reed.*`
length/holders/coverage) decompose into `_sum`, `_count`, and `_bucket`
metric streams — not a single stream named after the instrument. Dashboards
should use `_sum / _count` for mean values (see
`rpi/telemetry/dashboards/generate_syrinx_dashboard.py` `histogram_avg()`).
Querying the bare instrument name returns no data.

## Non-goals

- Client-side (SPA) metrics — server-side only in v1.
- Per-holder identity (who holds a reed) — aggregate holder count only.
- Cross-instance federation metrics.
- Alerting rules — follow-up once dashboards exist.
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
8. Rotate key → `syrinx.keys.revoked`; export backup → `syrinx.users.backup` with `user.id_hash`.
9. Connect WS client; send `REQUEST_REED` → inbound counter; observe outbound
   counters for server responses.
10. `DATA_ACK` from a second user → holder histogram increases for that reed.
11. Attempt over-limit reed → `syrinx.reeds.rejected.length` +1, no publish
    counter.
12. Confirm exported series contain no username/tag-text/note fields
    (spot-check attribute keys in OpenObserve).

## Open points

- Whether import-signup (`Handlers.ImportSignup`) should use
  `signup.mode=closed` or a dedicated `import` label — lean toward `closed`
  unless operators need them split.
