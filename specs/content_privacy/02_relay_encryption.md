# Content privacy 02 — Encrypted relay

## Status

Implemented.

## Depends on

[00](00_design.md)

## Context

Reed bodies moved holder → server (in transit) → requester in plaintext.
The server never stored them, but it could read them in transit. The
relay plumbing already treated the payload as an opaque blob server-side
(`json.RawMessage`), so swapping plaintext for ciphertext needed no
routing/schema change — only that holder and requester clients encrypt
and decrypt it themselves.

## Wire changes

`RELAY_REQUEST` (server → holder) gains the requester's canonical id:

```go
type RelayRequestData struct {
    ReedID      string `json:"reed_id"`
    RequesterID string `json:"requester_id"` // new
}
```

No separate author field: `ReedID` is already the canonical id
(`userID@serverID/uuid`), globally unique and already embedding the
author, so nothing else is needed to disambiguate it — an earlier
`AuthorID` field was dropped as redundant once this was confirmed.

(The event id itself lives on the message root as `id`, not inside this
struct — see [06](06_event_id_at_root.md).)

`RELAY_RESPONSE` (holder → server) carries the ciphertext under its own
name — `RelayResponseData.Ciphertext` (`json:"ciphertext"`). `DATA_RESPONSE`
/ `FOLLOW_REED` / `PIPE_REED` / `ARCHIVE_REED` / `REED_REPLY` /
`BROADCAST_REED` also carry ciphertext under their own dedicated
`DataResponseData.Ciphertext` field (`json:"ciphertext"`), separate from
`DataResponseData.Data` (`json:"data"`), which stays reserved for the
unrelated plaintext `REED_REMOVED`/`ACCOUNT_REMOVED` cert payloads — the
two used to share one field, which meant client code had to know which
message type it was to interpret `data` correctly; they no longer do.

## Holder → requester flow

1. Server dispatches `RELAY_REQUEST` with the requester's id (already
   available from `pending_events.requester_user_id` at dispatch time —
   no DB change needed).
2. The holder resolves the requester's active key: `GET
   /users/{id}/info` → `activeKeyID` (already a canonical key id, not a
   bare fingerprint — see [[project_keys_use_key_ids_not_fingerprints]]),
   then the cached-or-fetch path (`resolvePublicKeyArmor`, reused as-is).
3. The holder encrypts (`cryptoService.encryptToRecipient`, PGP,
   asymmetric — no signing; the plaintext already carries the author's
   own signature) and sends `RELAY_RESPONSE` with the ciphertext string.
4. The requester decrypts with their own active key
   (`decryptRelayPayload`, mirroring the mailbox decrypt precedent), then
   runs the existing `verifyReed` pipeline unchanged.

## `RELAY_ERROR`: holder has it but can't relay it

A holder that can't resolve the requester's key (fetch failure) has the
content but can't complete the relay — this is **not** a `RELAY_MISS`
("I don't have this content"), and must not delete the holder's
`reed_allocations` row. `handleRelayMiss`/`handleRelayError` (Go) are thin
wrappers over a shared `handleFailedRelay(holderUserID, eventID,
deleteAllocation bool)`: `true` for a miss, `false` for an error — same
retry shape either way (reset dispatch, requery `GetOnlineHolders` with no
exclusion list, redispatch or give up), only the allocation-delete step
differs.

## Federation note

`relayRequestPayload.RequesterUserID` (`federation_relay.go`) already
forwards the full canonical identity peer-to-peer — no schema change
needed for the cross-server case. Verify manually during testing that a
holder on server A correctly resolves server B's real requester (not the
intermediating peer server's own identity) via the transparent
`GET /api/keys/{id}` peer-proxy.

## Acceptance

- `RELAY_RESPONSE` payloads observed on the wire are armored PGP
  ciphertext, never plaintext reed JSON.
- A holder that can't fetch the requester's key sends `RELAY_ERROR`, not
  `RELAY_MISS`, and keeps its allocation.
- The server never decrypts or inspects relay payloads at any point.
