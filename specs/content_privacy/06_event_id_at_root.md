# Content privacy 06 — Event id moves to the message root

## Status

Implemented.

## Depends on

[02](02_relay_encryption.md)

## Context

Every relay-adjacent message previously nested its event id inside the
`data` object, alongside — on `RELAY_RESPONSE`, `DATA_RESPONSE`, and
friends — the reed payload itself. Once [02](02_relay_encryption.md)
flattened `RelayEnvelope{ciphertext}` down to a bare ciphertext string,
some of those payloads ended up read as `data.data`, which reads as a
mistake even though it was structurally correct (outer `data` = the
message envelope's field; inner `data` = the payload). The event id sat
one level too deep for a field every relay-lifecycle message needs.

## Wire change

The event id moves to the message root as `id`, sibling to `type`, for
every message that replies to or drives one specific dispatched event.
`data` keeps carrying whatever payload-specific fields that message type
has — nothing else moved.

| Message | Before | After |
|---|---|---|
| `RELAY_REQUEST` | `data.event_id` | root `id` |
| `REQUEST_ACK` | `data.event_id` | root `id` |
| `RELAY_RESPONSE` | `data.event_id` | root `id` |
| `RELAY_MISS` / `RELAY_ERROR` | `data.event_id` | root `id` (no `data` needed at all — see below) |
| `DATA_ACK` / `DATA_INVALID` | `data.event_id` | root `id` (no `data` needed at all) |
| `DATA_RESPONSE` / `PIPE_REED` / `FOLLOW_REED` / `REED_REPLY` / `REED_REMOVED` / `ACCOUNT_REMOVED` | `data.event_id` | root `id` |
| `BROADCAST_REED` | never had one | unchanged — no event to ack, ephemeral delivery |

## What didn't move

`DataResponseData.Data` (`json:"data"`) — at the time of this change, the
field carrying either the reed ciphertext (as a JSON-encoded string) or,
for `REED_REMOVED`/`ACCOUNT_REMOVED`, the removal certificate — was
unrelated to this change and stayed exactly where it was.
[08](08_flatten_redundant_ids.md) later split the ciphertext case onto its
own `Ciphertext` field; `Data` now carries only the cert case. Also
unrelated: `pending_events.event_id`, the Postgres column — this was a
wire-shape change only, no schema migration.

## Go implementation

- `RelayMissData`, `RelayErrorData`, `DataAckData`, `DataInvalidData`
  deleted outright — once their only field (`EventID`) moved to the
  shared `InboundJSONMsg.ID`, they carried nothing. `handleRelayMiss`,
  `handleRelayError`, `handleDataAck`, `handleDataInvalid` now take a
  plain `eventID string` instead of a data struct.
- `InboundJSONMsg` (the shared envelope every inbound JSON frame
  unmarshals into) gained `ID string \`json:"id"\`` alongside its
  existing `Type`/`Data`/`ReedID` fields — the same pattern `ReedID`
  already established for a root-level field outside `data`.
- Every outbound `NewXxxMsg` constructor's message struct gained its own
  `ID string \`json:"id"\`` (or `id,omitempty` where the id can be
  legitimately absent, e.g. `DataResponseMsg` for `BROADCAST_REED`); the
  corresponding `Data` struct's `EventID` field was removed.
- `handleRelayResponse`'s signature changed from `(client, data
  json.RawMessage)` to `(client, eventID string, data json.RawMessage)`
  — it no longer parses the id out of `data` itself.

## SPA implementation

- `serverConnection.ts`'s inbound `onmessage` handler now merges the
  root-level `id` into the object handed to `emit()`'s listeners, so
  existing handler code reading a payload's fields continues to work
  with one added `id` field rather than needing a second parameter.
- Every `send*` helper (`sendRelayResponse`, `sendRelayMiss`,
  `sendRelayError`, `sendDataAck`, `sendDataInvalid`) sends `id` at the
  message root instead of nesting `event_id` in `data`.
- `+layout.svelte`'s relay handlers read `data.id` (or destructure `{
  id: eventId, ... }` directly off the `RELAY_REQUEST` payload) instead
  of `data.event_id`.

## Bug found and fixed along the way

`+layout.svelte`'s four decrypt call sites (`DataResponse`, `FollowReed`,
`PipeReed`, `ReedReply`) had drifted to reading `data.ciphertext` from a
prior pass, but `DataResponseData`'s field is (and stays) `data`, not
`ciphertext` — only `RelayResponseData` (a different struct, for
`RELAY_RESPONSE` specifically) uses `ciphertext`. Fixed back to
`data.data`, confirmed against the actual Go struct rather than assumed.

## Known follow-up, not fixed here

`serverConnection.ts`'s `requestReedContent()` resolves its promise with
the raw `DATA_RESPONSE` payload directly — no decrypt, no `verifyReed`,
no store — unlike `+layout.svelte`'s `ServerEvent.DataResponse` listener,
which does all three correctly. Callers (`ConversationSection.svelte`,
the reed detail page) currently receive an opaque ciphertext string
where they expect a real, verified reed. Predates this change (it never
verified signatures either, before encryption). Tracked as a separate
follow-up commit, not bundled into this rename.

## Acceptance

- No message anywhere on the wire carries `event_id` nested inside `data`.
- A payload capture never reads as `data.data` for the ciphertext-carrying
  message types.
- `go build`, `go vet`, `go test ./...`, and `svelte-check` all pass.
