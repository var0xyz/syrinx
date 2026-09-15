# Content privacy 04 — SPA

## Status

Partial — relay encrypt/decrypt and tag-claim verification implemented;
mention-inbox consumption not yet wired (see [03](03_mention_inbox.md)).

## Depends on

[01](01_signreed_contentless.md), [02](02_relay_encryption.md)

## Files

- `types/reed.ts` — `extractTags`/`extractMentions` promoted from private
  `Reed` methods to standalone exported functions (reused by the pipe-tag
  verifier, which operates on a plain decrypted `ReedType`, not a live
  `Reed` instance). `ReedType`/`Reed.asObject()` carry `mentions: string[]`
  alongside the existing `tags`.
- `services/api.ts` — `createReed` drops `content`, keeps `signature`,
  adds `tags`/`mentions` as repeated form fields.
- `repositories/reeds.ts` — `countersignReed` passes `reed.tags`/
  `reed.mentions` instead of `reed.content`; local signing is unchanged
  (the client still signs content locally, it's just never transmitted).
- `services/crypto.ts` — `encryptToRecipient(plaintext, armor)`, the
  client-side counterpart of the server's `crypto.Service.Encrypt`, same
  armored-PGP format.
- `services/relayDecrypt.ts` (new) — `encryptReedForRequester` (holder
  side: resolve the requester's active key, encrypt) and
  `decryptRelayPayload` (requester side: decrypt with the caller's own
  active key, mirroring `mailboxReceipt.ts`'s key/passphrase lookup).
- `services/serverConnection.ts` — `sendRelayError`, `activePipeTag`
  getter (the currently-subscribed pipe tag, used for tag-claim
  verification), `sendContentRejected` gains an optional `reason`.
- `verifiers/index.ts` — `resolvePublicKeyArmor` exported for reuse by the
  relay handler; `verifyClaimedTags(reed, deliveryTag)` re-derives tags
  from decrypted content and confirms the delivery tag is really present.
- `routes/+layout.svelte` — the actual holder/requester relay logic:
  - `RelayRequest` handler: encrypt for the requester or send
    `RELAY_ERROR` (not `RELAY_MISS`) on key-resolution failure.
  - `DataResponse`/`FollowReed`/`PipeReed`/`ReedReply` handlers: decrypt
    before `storeReed`; report `CONTENT_REJECTED` with `decrypt_failed` on
    failure. `PipeReed` additionally runs `verifyClaimedTags` against
    `activePipeTag` before storing, reporting `tag_claim_mismatch` on
    failure.

## `verifyClaimedTags`'s known limitation

Verification is against the client's own currently-subscribed pipe tag,
not literally "the exact tag the server matched to trigger this specific
delivery" — the server's pipe fan-out (`PipeListenerUserIDs`) collapses
multiple claimed tags into one flat listener set before dispatch, and the
wire payload doesn't carry which tag caused a given delivery. Extending it
to per-tag attribution would need a `PIPE_REED` wire field and a bigger
fan-out restructure; out of scope here. What's implemented still catches
the meaningful case: a reed delivered to a pipe watcher whose real content
doesn't contain any tag that watcher cares about.

## Bugs fixed along the way

- `mailboxReceipt.ts` called `authService.getActiveKeyFingerprint()`,
  which doesn't exist (only `getActiveKeyId()` does) — a pre-existing,
  unrelated build-breaking error, fixed since it blocked verifying this
  change's own `svelte-check` pass.
- See [01](01_signreed_contentless.md) for the server-side self-mention
  comparison bug fixed in the same pass.

## Acceptance

- `svelte-check` passes with 0 errors.
- `POST /reeds` requests from the SPA never include `content`.
- A relay round trip between two local clients delivers ciphertext on the
  wire and the receiving client displays the correctly decrypted reed.
