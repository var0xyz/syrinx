# Content privacy 08 — Flatten redundant ids off relay/removal payloads

## Status

Implemented.

## Depends on

[06](06_event_id_at_root.md)

## Context

Two remaining redundancies in the relay/removal wire shapes, both
noticed by inspection of live traffic:

- `RelayResponseData{Ciphertext string}` was a single-field struct —
  `RELAY_RESPONSE`'s `data` was always exactly `{"ciphertext": "..."}`,
  never anything else. The wrapper object added nothing; unlike
  `DataResponseData` (genuinely shared between ciphertext-carrying
  messages and plaintext `REED_REMOVED`/`ACCOUNT_REMOVED` certs, which is
  why *that* struct keeps a named field), `RelayResponseData` has no
  sharing concern — the reason that justified keeping `DataResponseData`'s
  field named didn't apply here.
- `DataResponseData.ReedID`/`UserID` duplicated an id already present
  inside the payload itself: for the six ciphertext-carrying message
  types, the real reed id only exists after decrypting (`reed.id` on the
  decrypted object) — the outer `reed_id` was never read by any client
  handler. For `REED_REMOVED`/`ACCOUNT_REMOVED`, the cert object
  (`ReedRemovalWire`/`AccountRemovalWire`) already carries its own
  `reedID`/`userID`, and the client reads `cert.reedID`/`cert.userID`
  directly — the outer field was equally dead.

## Wire changes

`RELAY_RESPONSE`'s `data` is now a bare ciphertext string, not an object:

```json
{"type": "RELAY_RESPONSE", "id": "...", "data": "-----BEGIN PGP MESSAGE-----..."}
```

`DataResponseData` drops `ReedID`/`UserID` entirely — no message type
that shares this struct carries an outer id anymore:

```go
type DataResponseData struct {
    RequestID  string          `json:"request_id,omitempty"`
    Data       json.RawMessage `json:"data,omitempty"`       // REED_REMOVED/ACCOUNT_REMOVED cert
    Ciphertext string          `json:"ciphertext,omitempty"` // everything else
    Username   string          `json:"username,omitempty"`   // BROADCAST_REED only
}
```

Affects `DATA_RESPONSE`, `PIPE_REED`, `FOLLOW_REED`, `ARCHIVE_REED`,
`REED_REPLY`, `BROADCAST_REED` (drop `reed_id`) and `REED_REMOVED`,
`ACCOUNT_REMOVED` (drop `reed_id`/`user_id` — the cert's own id is what
every reader already used).

## Go implementation

`RelayResponseData` type deleted; `handleRelayResponse` unmarshals `data`
directly into a `string`. Every `NewXxxMsg` constructor for the eight
affected message types drops its `reedID`/`removedUserID` parameter —
callers that only ever used it to populate the now-removed field pass one
fewer argument; `pe.ReedID` is still used for logging in the same
functions, just no longer threaded into the wire payload.

## SPA implementation

`sendRelayResponse` sends `data: ciphertext` (bare string) instead of
`data: { ciphertext }`. No inbound `RELAY_RESPONSE` handler exists
client-side (holder → server only), so nothing to update on the receive
side for that message. No `DATA_RESPONSE`-family handler ever read
`data.reed_id`/`data.user_id` in the first place — confirmed by grep
before removing the fields, not assumed — so no client-side change was
needed beyond the send-side flattening.

## Acceptance

- `RELAY_RESPONSE.data` is a bare string on the wire, not `{ciphertext}`.
- No `DATA_RESPONSE`/`PIPE_REED`/`FOLLOW_REED`/`ARCHIVE_REED`/`REED_REPLY`/
  `BROADCAST_REED`/`REED_REMOVED`/`ACCOUNT_REMOVED` payload carries a
  `reed_id` or `user_id` field alongside its ciphertext/cert.
- `go build`, `go vet`, `go test ./...`, and `svelte-check` all pass.
