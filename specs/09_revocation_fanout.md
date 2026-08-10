# Proposal 09 — Revocation: on-demand check, not fanout

## Status

Implemented.

## Depends on

[06](06_signed_replicated_revocations.md) (signed revoke),
[10](10_revocation_resource.md) (resource / wire shape).

## Context

An earlier draft of this proposal specified push fanout for revocations,
mirroring [deletion 04](deletion/04_reed_fanout.md) (broadcast +
`pending_events` + `SYNC_REQUEST` catch-up). That model was **rejected**:
broadcasting every revocation to every follower spends bandwidth on
something with no effect for the vast majority of followers at the moment
it happens, and this project already has a lazy, on-demand trust model that
solves the same problem reactively — a client only needs a key the instant
it verifies something signed by it, and if the server can't produce a valid
key, the content is discarded. Pushing revocations preemptively duplicates
what the lazy-fetch path already does.

The lazy on-demand model was, on investigation, **already fully built**
before this proposal: on a local cache miss, `resolvePublicKey` /
`resolvePublicKeyArmor` (`spa/src/lib/verifiers/index.ts`) fetch
`GET /users/{userID}/keys/{fingerprint}` and verify+store; if the fetch
fails, verification returns `false` and the content is discarded
(`dbService.put` refuses to store on a failed verifier). Two real gaps
remained, both closed by this proposal:

1. **`.revoked` was never checked at verify time.** A key that was revoked
   but otherwise validly attested was accepted regardless.
2. **Cached keys were never re-checked once cached.** A key cached before
   revocation stayed trusted forever afterward — nothing re-asked the
   server.

## Scope

- **Time-relative revocation validity.** A key remains valid for content it
  signed *before* its own revocation — revoking a key does not retroactively
  invalidate everything it ever signed. Reject only if the signed content's
  trustworthy timestamp is at or after the revocation's timestamp. Both
  timestamps are server-attested (`serverSignature.timestamp` on the
  content, and on the fetched `KeyRevocation`), so the comparison is
  tamper-resistant: a compromised key cannot be used to backdate a forged
  reed past its own revocation.
- **Rate-limited re-check.** To avoid re-fetching the same fingerprint on
  every item in a large batch (e.g. a profile with thousands of reeds from
  one author), stamp each fingerprint in `sessionStorage` on check and skip
  re-checking for 60 seconds. `sessionStorage` needs no cleanup — tab-scoped,
  evaporates on close.
- **Anomaly telemetry, not fanout.** Content arriving over an
  already-authenticated connection is itself proof the server was reachable.
  So if a subsequent key/revocation fetch needed to verify that content
  fails for any reason other than a legitimate 404, that's an anomaly worth
  telling the server about (`KEY_FETCH_ERROR`), even though the content is
  discarded either way. If the fetch succeeds and shows genuine revoked-key
  abuse (content timestamped at/after revocation), report that too
  (`REVOKED_KEY_USED`, with the fingerprint) so it can be logged for later
  security analysis. Neither message is tied to a `pending_events` row —
  they are the client self-reporting, not acking a delivery.
- Do **not** change `GetPublicKey`'s response shape (stays a `revoked`
  boolean) — the revocation timestamp is fetched separately via
  `GetKeyRevocation`, and only when `revoked === true`.

## Non-goals

- No WebSocket push of revocations, no new `BroadcastType`, no
  `pending_events` involvement, no `SYNC_REQUEST` catch-up query for
  revocations. Superseded by the on-demand model above.
- Changing how revokes are created or signed ([06](06_signed_replicated_revocations.md),
  [10](10_revocation_resource.md)).
- Recovery report-back ingestion of revocations (recovery feature).
- Changing auth middleware (server still decides revoke from
  `user_key_revocations` rows).

## Design

### Time-relative check (`isKeyValidAt`)

`spa/src/lib/verifiers/index.ts`: `isKeyValidAt(userID, fingerprint,
revoked, atISO)` — if the key isn't revoked, valid. If revoked, fetches the
revocation (`apiService.getKeyRevocation`) and compares `atISO` against
`revocation.serverSignature.timestamp`; fetch failure fails closed (invalid).

Applied in every verifier that checks a peer's timestamped signed content
against a key that could have since been revoked, **after** that content's
own server signature has been verified (so the timestamp being compared is
itself proven, not just claimed):

- `verifyReed` — `reed.serverSignature.timestamp` vs. the author key's
  revocation time.
- `verifyUser` — `user.serverSignature.timestamp` vs. the profile's
  signing key's revocation time. (`resolvePublicKeyArmor` was replaced with
  `resolvePublicKey` here so `.revoked` is available.)

Deliberately **not** applied to:

- `verifyKeyRevocation` — resolves the key *being revoked* to confirm the
  revocation cert itself was signed by that key (self-consistency, not
  peer trust of ongoing content); a revocation is expected to be signed by
  an about-to-be-revoked key.
- `verifyInvite` — resolves the local logged-in user's **own** current key
  against their own locally-created content, not a peer trusting someone
  else's key post-revocation.

### Rate-limited re-check (`keyCheckThrottle`)

`spa/src/lib/utils/keyCheckThrottle.ts`: `shouldRecheck(fingerprint)` /
`markChecked(fingerprint)`, backed by `sessionStorage`, 60s window.
`resolvePublicKey` / `resolvePublicKeyArmor` (`verifiers/index.ts`) check
this on a cache **hit**: if the window has elapsed, re-fetch + re-verify +
re-store (picking up a revocation that happened after the key was first
cached) before returning; on a cache **miss**, fetch as before. A failed
re-check (network error, not 404) **fails closed** — it does not fall back
to the stale cached copy, since freshness could not be confirmed.

### Anomaly telemetry

New client→server WS messages, following the existing
`RELAY_MISS`/`DATA_ACK`/`DATA_INVALID` pattern (`realtime/messages.go`
struct + `realtime/service.go` dispatch case + handler):

- `KEY_FETCH_ERROR { user_id, fingerprint }` — sent when a key fetch needed
  for verification fails with anything other than 404. Server logs at warn
  level and records `Recorder.KeyFetchError`.
- `REVOKED_KEY_USED { user_id, fingerprint }` — sent when `isKeyValidAt`
  determines content was signed at/after its key's revocation. Server logs
  at warn level (visible for security analysis) and records
  `Recorder.RevokedKeyUsed`. The fingerprint is kept in the clear (not
  hashed) in metrics attributes, unlike the reporting/target user IDs, so it
  can be cross-referenced during analysis.

Neither handler touches `pending_events` — both are pure
logging/metrics, since they report an anomaly rather than ack a specific
delivered event.

## Work items

1. `realtime/messages.go`: `KeyFetchErrorData`, `RevokedKeyUsedData`.
2. `realtime/service.go`: dispatch cases + `handleKeyFetchError` /
   `handleRevokedKeyUsed`.
3. `observability/metrics`: `KeyFetchError` / `RevokedKeyUsed` on
   `Recorder` (+ `Noop`, `OTEL`).
4. `spa/src/lib/services/serverConnection.ts`: `sendKeyFetchError` /
   `sendRevokedKeyUsed`.
5. `spa/src/lib/utils/keyCheckThrottle.ts`: `shouldRecheck` / `markChecked`.
6. `spa/src/lib/verifiers/index.ts`: `isKeyValidAt`; wire into
   `verifyReed` / `verifyUser`; throttled re-check in `resolvePublicKey` /
   `resolvePublicKeyArmor`.

## Testing

- Go (`realtime/key_anomaly_test.go`): `handleKeyFetchError` /
  `handleRevokedKeyUsed` record exactly once with the right reporter/target/
  fingerprint; malformed/empty payloads are ignored.
- SPA (`spa/scripts/test-key-revocation.mjs`, standalone — no unit-test
  framework in this repo, see `test-signing.mjs`): throttle window edges
  (first check, within window, at/past window, independent per
  fingerprint); `isKeyValidAt` timestamp comparison (before/at/after
  revocation).

## Risks

- **Throttle window size** — 60s bounds re-check cost on large batches
  from one author but means a just-revoked key can still verify as valid
  for up to a minute from a peer's already-cached copy. Accepted trade-off
  (see Context).
- **Two round-trips for a revoked key** — `GetPublicKey` then
  `GetKeyRevocation` only when `revoked === true`, so the common
  (not-revoked) path stays single-round-trip.

## Dependencies

- Requires [06](06_signed_replicated_revocations.md) + [10](10_revocation_resource.md)
  (signed resource already on the server).
- Builds on [08](08_client_signature_validation.md) /
  [signatures 09](signatures/09_verify_server_countersignatures.md)
  verify-before-store gates (already landed) and the existing lazy
  `resolvePublicKey` fetch-on-miss path.

## Parallelism

- Independent of invites / tip check / device binding.
- Implemented; no further tracks block on it.
