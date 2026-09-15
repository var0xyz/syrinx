# Content privacy 07 — `requestReedContent` resolves a verified reed

## Status

Implemented.

## Depends on

[06](06_event_id_at_root.md)

## Context

`serverConnection.ts`'s `requestReedContent()` — used by the reed detail
page and reply-thread fetching to pull a reed the client doesn't hold
locally — resolved its returned promise with the raw `DATA_RESPONSE`
payload directly. It never decrypted, never ran `verifyReed`, never
stored. This predates end-to-end encryption: even when the payload was
plaintext reed JSON, this path handed it back unverified. Once relay
became encrypted ([02](02_relay_encryption.md)), it got worse — callers
received an opaque ciphertext string where they expected a reed object.

Meanwhile `+layout.svelte`'s `ServerEvent.DataResponse` listener already
runs the correct sequence (decrypt → `storeReed`, which verifies before
persisting → ack) on the exact same `DATA_RESPONSE` message. Both
consumers ran independently and uncoordinated on one delivery.

## Fix

Rather than add a second, duplicate decrypt/verify path inside
`serverConnection.ts` (real circular-import risk too: `relayDecrypt.ts`
already imports `serverConnection` for `reportDecryptFailure`), the
`DataResponse` listener is now the single source of truth. It calls two
new public methods after it has already decrypted, verified, and stored:

- `resolvePendingReedRequest(requestId, reed)` — called on success, with
  the real, verified `ReedType` `storeReed` just persisted.
- `rejectPendingReedRequest(requestId, err)` — called on decrypt failure
  or verification/store failure, so a waiting caller's `catch` actually
  fires instead of silently resolving with garbage.

`serverConnection.ts`'s own inline `DATA_RESPONSE` handling in
`onmessage` no longer touches `pendingRequests` at all — it only clears
`dispatchedReedRequests` bookkeeping, which is unrelated to content.

`requestReedContent`'s return type tightens from `Promise<any>` to
`Promise<ReedType>`, and `PendingRequest.resolve` from `(data: any) =>
void` to `(reed: ReedType) => void`.

## Callers affected

`ConversationSection.svelte` and the reed detail page
(`routes/reed/[...reedID]/+page.svelte`) already had `try`/`catch`
around their `requestReedContent()` calls and already treated the
resolved value as a reed object — they were written for the correct
contract, just never actually got it. No caller-side changes needed;
they now behave as their existing code already assumed. The two
fire-and-forget calls in `ReedsList.svelte` (`.catch(() => {})`,
discarding the resolved value) are unaffected either way.

## Acceptance

- A successful on-demand reed fetch resolves with the same verified
  `ReedType` object that ends up in IndexedDB, not raw ciphertext.
- A decrypt or verification failure during an on-demand fetch rejects
  the caller's promise instead of resolving with unusable data.
- `svelte-check` passes with 0 errors.
