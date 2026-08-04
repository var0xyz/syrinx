# Loadtest 01 — Extract `performSignup` / `performPublish` into reusable SPA services

## Status

Proposed.

## Depends on

[00](00_design.md)

## Context

The script-driven driver needs to trigger signup and publish exactly the way
a real user's browser does — same key generation, same signed payloads, same
IndexedDB writes — without duplicating that sequence in test code (a second
copy is exactly the kind of drift `AGENTS.md` warns about for signed
envelopes). Today both sequences are inlined in Svelte components:

- **Signup** — [spa/src/routes/signup/+page.svelte](../../spa/src/routes/signup/+page.svelte)
  `onSubmit`: reserve a server-signed `userID`
  (`apiService.getUserID()`) → generate a PGP keypair
  (`cryptoService.generateKeyPair()`) → store the private key
  (`privateKeyRepository.put()`) → sign the public key upload
  (`cryptoService.signMessage()`) → build and sign the identity payload
  (`buildNewUserIdentityPayload` from
  [signing.ts](../../spa/src/lib/services/signing.ts)) → `authService.signup()`
  → `requestSigner.initializeWorker()` → fetch + cache the attested public
  key (`apiService.getPublicKey()`, `publicKeyRepository.put()`) →
  `authService.saveUserToStorage()` → `serverConnection.connect()`.
- **Publish** — [spa/src/lib/components/NewReedModal.svelte](../../spa/src/lib/components/NewReedModal.svelte):
  construct a `Reed`, set `content`/`replying`/`echoing`, sign
  `reed.asMarkdown()` (`cryptoService.signMessage`), attach the signature
  (`reed.setUserSignature`), then `reedsService.createReed(reed)`.

**Follow does not need extraction** — [spa/src/lib/repositories/following.ts](../../spa/src/lib/repositories/following.ts)
already exposes `following.follow(userId)` / `following.unfollow(userId)` as
standalone, already-reusable functions (local IndexedDB write +
`apiService.followUser`/`unfollowUser`, with pending-sync retry). The driver
can call these directly.

## Scope

- Extract the signup sequence into `performSignup(username, email?)` in a
  new `spa/src/lib/services/signupFlow.ts`, returning the created
  `api.User` (or throwing on failure, same as today).
- Extract the publish sequence into `performPublish(content, { replying?,
  echoing? })` in a new `spa/src/lib/services/publishFlow.ts`, returning
  `{ reed, publish }` (mirroring `reedsService.createReed`'s existing
  return shape).
- Update `+page.svelte` and `NewReedModal.svelte` to call these functions
  instead of inlining the steps — behavior must be unchanged (same
  `currentStep` progress reporting from signup, same error handling).
- No change to `following.ts` — it is already the reusable shape.

## Non-goals

- Any change to the signing/BytesToSign envelopes themselves.
- Extracting every flow used by the driver — subscribe/unsubscribe and reads
  are already single calls on `serverConnection` / the repositories and need
  no extraction (see [00](00_design.md) "Virtual user = one BrowserContext").
- Error-message/UX changes in the real signup or composer flows.

## Design

Both new modules are thin: same steps, same call order, just lifted out of
component scope so they have no dependency on Svelte component state
(`currentStep`, `loading`, etc.). The page/modal keeps its own progress
tracking by passing an optional `onStep` callback, e.g.:

```ts
// signupFlow.ts
export async function performSignup(
  username: string,
  email: string,
  onStep?: (step: number) => void,
): Promise<api.User> {
  onStep?.(1);
  const reserved = await apiService.getUserID();
  onStep?.(2);
  const keyPair = await cryptoService.generateKeyPair({ ... });
  // ...same remaining steps as today's onSubmit...
  return user;
}
```

The real signup page becomes:

```ts
const user = await performSignup(username, email, (step) => (currentStep = step));
```

Same pattern for `performPublish`. The load-test driver calls these with no
`onStep` callback and instead wraps the whole call in its own timing (see
[02](02_driver.md)).

## Work

1. Add `spa/src/lib/services/signupFlow.ts` with `performSignup`; update
   `signup/+page.svelte` to call it.
2. Add `spa/src/lib/services/publishFlow.ts` with `performPublish`; update
   `NewReedModal.svelte` to call it.
3. Run `npm run check` and the existing Playwright e2e suite
   (`spa/tests/signup-and-publish.spec.ts` and friends) to confirm no
   behavior change.

## Acceptance

- Signup and publish through the real UI behave identically to before
  (same steps, same error messages, same IndexedDB/localStorage writes).
- `performSignup` / `performPublish` are callable from a `page.evaluate`
  dynamic import against the Vite dev server, with no Svelte component
  instance required.
- Existing e2e specs pass unmodified.
