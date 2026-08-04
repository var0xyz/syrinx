# RISKS.md — Security audit findings

A defect-first security review of the Syrinx codebase (server + SPA). Findings
are located by `file:line` where possible and grouped by severity. Each entry
states the concrete attack or defect and a suggested fix.

**Trust model recap** (so severities make sense): the server is a
metadata/relay tracker. Reed *content* lives on peers; the server never sees or
validates it. Trust is cryptographic: records carry an author (user) detached
PGP signature and a server countersignature (with a server-authoritative
timestamp) that binds identity fields. Clients are supposed to verify signatures
before storing. Because content lives off-server, "the server can't forge X" is
often the *only* guarantee — so gaps in signature coverage/verification matter
more than in a typical web app.

> Scope & confidence: findings were derived by reading the actual code (core
> auth/crypto/handlers/db/signing audited directly; `recovery/`, `invites/`,
> `deletion/`, `realtime/`, `secret/`, `coverage/`, and the whole `spa/`
> audited with assistance). Where a claim could not be fully proven from
> source it is marked **(unconfirmed)**. This is a point-in-time review, not a
> guarantee of completeness.

## Severity summary

| #      | Severity | Area       | Title                                                                  |
|--------|----------|------------|------------------------------------------------------------------------|
| C1     | Critical | SPA        | Server response `Signature` header is never verified in the live app   |
| C2     | Critical | SPA        | Decrypted-key passphrase persisted in `localStorage`                   |
| C3     | Critical | SPA        | Private key + passphrase material logged to console                    |
| H1     | High     | server     | WebSocket auth signature is replayable and unbound to user/server      |
| H2     | High     | server     | Unauthenticated HTTP responses are not signed (MITM tampering)         |
| H3     | High     | server     | Response signing fails open                                            |
| H4     | High     | SPA        | `userId` in `localStorage` alone unlocks the app                       |
| H5     | High     | SPA        | Service worker `SIGN_TEXT` is an origin-unchecked signing oracle       |
| M1     | Medium   | server/SPA | `BytesToSign` no-escaping + a reed envelope that *is* parsed back      |
| M2     | Medium   | server     | Recovery claim challenge is a predictable, untracked timestamp         |
| M3     | Medium   | server     | Recovery claim can succeed with a revoked "active" key                 |
| M4     | Medium   | server     | WS `DATA_ACK`/relay handlers change state with no caller authorization |
| M5     | Medium   | server     | Unbounded WebSocket read frames → memory-exhaustion DoS                |
| M6     | Medium   | server     | Per-user invite quota is a check-then-insert race                      |
| M7     | Medium   | SPA        | Verification clock advanced by attacker-controlled timestamp           |
| M8     | Medium   | SPA        | `verifySignature` silently falls back binary→text mode                 |
| M9     | Medium   | SPA        | Server-provided counts/hints consumed for trust decisions unsigned     |
| L1     | Low      | server     | Reed author signature never verified on recovery ingest                |
| L2     | Low      | server     | `FollowUser`/`UnfollowUser` unsigned + no target validation            |
| L3     | Low      | SPA        | `verifyInvite` binds to local `userId`, not a signed issuer            |
| I1..I6 | Info     | mixed      | Residual trust assumptions & positives                                 |

---

## Critical

### C1 — SPA never verifies the server response `Signature` header
**Where:** `spa/src/lib/services/api.ts` (`request()`); dead code in
`spa/src/lib/services/signature-verifier.ts` (`verifyResponseSignature`,
`secureApiRequest`).
The production `apiService.request()` does a plain `fetch()` and only checks
`res.ok`. The response-signature verifier exists but is referenced only by its
own module, the README, and an example file — never by live code. For every
endpoint whose body is not *itself* an individually PGP-signed record
(`/server/info`, `/server/keys`, `getReedEchoCount`, `invites/check`,
`getInviteStatus`, `whoami`, follower/following counts, `users/status`, recovery
challenge), a MITM or malicious relay can forge arbitrary responses. This
defeats the entire point of the server-side response signer.
**Fix:** route all `request()` traffic through response-signature verification
against the pinned server key; fail closed when the header is missing/invalid.

### C2 — Decrypted-key passphrase persisted in `localStorage`
**Where:** `spa/src/lib/services/auth.ts:112-121` (write); read in
`request-signer.ts:169`, `serverConnection.ts:69`, `reedRemoval.ts:48`,
`NewReedModal.svelte:156`, etc.
The passphrase that unlocks the user's PGP private key is stored in
`localStorage`, and the private key armor is in IndexedDB. Any XSS, malicious
dependency, or browser extension can read both and fully impersonate the user
(sign reeds, rotate keys, delete the account).
**Fix:** never persist the passphrase; hold it only in memory (or SW memory),
re-prompt on reload, or wrap the key with a non-extractable WebCrypto key.

### C3 — Private key + passphrase material logged to console
**Where:** `spa/src/lib/services/crypto.ts:67-71` (`console.log` dumps the
armored **private key** on every signup); `request-signer.ts:211`.
Console logs are captured by crash/telemetry tooling and readable by extensions.
**Fix:** delete these log statements; add a lint rule against logging key
material.

---

## High

### H1 — WebSocket auth signature is replayable and unbound
**Where:** `realtime/auth.go:114` (verifies a signature over *only* the
`timestamp` string); window is ±5 min (`crypto/crypto.go:291-310`).
The WS handshake signs just a decimal timestamp — no nonce, no binding to
`userID` or `serverID`. Anyone who captures one handshake query string
(`?userID=&fingerprint=&signature=&timestamp=`) from a proxy/log/referrer can
replay it for up to 5 minutes to open a socket *as that user* and receive their
fanout/relay traffic. A handshake captured against one server is also valid on
any other server that trusts the same key.
**Fix:** sign a server-issued single-use nonce (or a
`BytesToSign` payload binding `serverID`+`userID`+`timestamp`); track/expire
nonces; shrink the window.

### H2 — Unauthenticated HTTP responses are not signed
**Where:** `middlewares.go:562-590` (`responseSignerMiddleware` wraps the writer
only when a `userID` is in context).
Responses on the unauthenticated allowlist (`/server/info`, `/server/keys/*`,
`/users/signup`, `/users/status`, `/check-username`, `/invites/check`,
`/recovery/identity/claim`) are sent **unsigned**. A network attacker can tamper
with, e.g., `/server/keys/{fp}` (the server public key used to verify *every*
countersignature) or `/server/info` (`recoveryMode`, `signupMode`) with no
detection. This undercuts the "all API responses are signed" claim in
`RESPONSE_SIGNER.md`.
**Fix:** sign all responses (the signing key is loaded at startup and doesn't
need a user context); clients must pin/verify the server key out-of-band.

### H3 — Response signing fails open
**Where:** `middlewares.go:88-111,141-147` (`Flush`/`signCompleteResponse`): if
the key is empty or signing errors, the body is still written, just unsigned
(and only the status is flipped to 500 on hard error, but a missing key logs a
warning and returns `nil`).
A transient signing failure or a misconfigured/missing key silently degrades to
unsigned responses that a non-verifying client (see C1) accepts.
**Fix:** fail closed — refuse to emit an unsigned body on the authenticated
path; treat a missing signing key as fatal at startup (it already is for boot,
but the runtime path should not tolerate `privateKey == ""`).

### H4 — `userId` in `localStorage` alone unlocks the app
**Where:** `spa/src/lib/services/auth.ts:28-42` (`hasLocalIdentity`/
`isLoggedIn` read only `localStorage.userId`, "no network").
Anyone able to write `localStorage.userId` (shared machine, XSS, sibling tab) is
treated as logged in; combined with C2 this is a full session. There is no check
that a decryptable private key matching the account's active-key fingerprint is
present.
**Fix:** gate "logged in" on possession of a private key whose fingerprint
matches the account's active key.

### H5 — Service worker `SIGN_TEXT` is an origin-unchecked signing oracle
**Where:** `spa/src/service-worker.ts:126-163` (`message` handler; second
listener signs arbitrary `SIGN_TEXT` with no `event.origin`/`event.source`
check).
Any context that can `postMessage` to the SW can get arbitrary bytes signed by
the user's key — forging request signatures, reeds, or removal certs.
**Fix:** validate `event.origin`/`event.source` against the app origin;
restrict to same-origin clients.

---

## Medium

### M1 — `BytesToSign` "never parsed back" is violated by the reed envelope
**Where:** `signing/signing.go` (no-escaping envelope, documented as never
re-parsed); **but** `services.go:1591` `ExtractReedHeader` *does* parse it back,
splitting on `\n` and `strings.HasPrefix(line, "id:")`; envelope built by
`ReedAsMarkdown` (`services.go:1546`) with user-controlled `content`.
The whole "no escaping is safe" argument rests on the envelope never being
parsed into fields. `ExtractReedHeader` breaks that invariant. Reed `content` is
user-controlled and unescaped, so a body crafted with leading
`\nid: <other>\n---\n` can influence what a header-extractor reads, and more
generally lets two logically different reeds/records produce confusable bytes.
The SPA mirror (`spa/src/lib/types/reed.ts:24-41`, `signing.ts:59-73`) has the
same shape. Free-text fields placed in *headers* (notably `username` in identity
payloads) are the higher-risk sink; note that `username` cannot contain `\n`
(`trimInvisibleChars` in `utils.go:9` drops non-printable runes) but **can**
contain `:` and other printables.
**Fix:** either (a) stop parsing envelopes back (remove/replace
`ExtractReedHeader` with structured fields), or (b) escape/length-prefix header
values and reed content. At minimum, add an explicit invariant test that no
signed field can inject a header line.

### M2 — Recovery claim challenge is a predictable, untracked timestamp
**Where:** `recovery/identity.go:32-36` (`IssueChallenge` = `now().Unix()`);
accepted if ≤60s old (`identity.go:46-50`, `nest.go:248-257`).
The "challenge" is neither random nor server-stored nor single-use — the client
picks any in-window value, and a captured claim request replays for 60s. It
provides no real anti-replay property.
**Fix:** issue and persist a random single-use nonce; require the signature to
cover it; delete on use.

### M3 — Recovery claim can succeed with a revoked active key
**Where:** `recovery/nest.go:89` (`active = newestFirst[0]`, unconditionally);
challenge verified against `active.Key.Armor` (`identity.go:58`). No check that
the tip node lacks a `Revocation`.
An attacker holding a compromised key that was *later revoked* — but is still
the chain tip in the submitted nest — can satisfy the claim and take over the
account during recovery. This partially defeats the "monotonic revocation"
protection the recovery design relies on.
**Fix:** reject when `newestFirst[0].Revocation != nil`; require the claim to be
signed by the newest *unrevoked* key.

### M4 — WS `DATA_ACK`/relay handlers change state with no caller authorization
**Where:** `realtime/service.go:990-1033` (`handleDataAck` allocates
`(pe.ReedID, client.userID, pe.UserID)` looked up only by `eventID`, never
checking `client.userID == pe.RequesterUserID`); same gap in `handleRelayResponse`
(`service.go:903-944`) and `handleRelayMiss` (`service.go:947`).
Any authenticated client that learns an `eventID` can self-assert a reed
allocation (rigging coverage stats / positioning as a relay source) or inject
reed-body content toward the requester.
**Mitigation:** `eventID`s are random UUIDs, so blind guessing is impractical,
and content injection is caught client-side via `DATA_INVALID`. Still, a
security-relevant state change is keyed only on a bearer id.
**Fix:** verify `client.userID == pe.RequesterUserID` in ACK/INVALID handlers,
and that `client.userID` is an actual online holder before relay allocation.

### M5 — Unbounded WebSocket read frames → memory-exhaustion DoS
**Where:** `realtime/service.go:367-373` (upgrader) never calls
`conn.SetReadLimit`; `handleClientMessages` (`service.go:482`) unmarshals whole
frames.
One authenticated client can send arbitrarily large frames and exhaust memory.
**Fix:** `conn.SetReadLimit(maxFrameBytes)` after upgrade; reject oversized
frames.

### M6 — Per-user invite quota is a check-then-insert race
**Where:** `invites/handlers.go:152-193` (`CountByCreator` then `Insert`, no
atomic guard).
Concurrent `POST /api/invites` all pass the count check before any insert
commits, exceeding `MAX_INVITES_PER_USER`.
**Fix:** enforce in DB (conditional insert on a subquery count, partial
constraint, or `FOR UPDATE`/advisory lock on `created_by`).

### M7 — SPA verification clock advanced by attacker-controlled timestamp
**Where:** `spa/src/lib/services/crypto.ts:24-38,142` — verification reference
time is `max(now, serverTimestamp) + 5min`, where `serverTimestamp` is the
server-supplied countersignature time.
A malicious server can set a far-future `timestamp`, pushing the verification
clock forward and causing OpenPGP.js to accept signatures from keys that should
be **expired**, defeating key-expiry.
**Fix:** cap the reference time at `now + skew`; never let an attacker-controlled
timestamp advance the verification clock.

### M8 — SPA `verifySignature` silently falls back binary→text mode
**Where:** `spa/src/lib/services/crypto.ts:141-169`. The Go signer uses binary
detached signatures (`crypto/crypto.go:215` `openpgp.DetachSign`), so accepting
text mode (with CR/LF canonicalization) broadens the set of byte sequences that
verify for a given signature.
**Fix:** pin binary mode; remove the text fallback.

### M9 — Unsigned server counts/hints consumed for trust decisions
**Where:** `GET /users/{userID}/info` (`UserInfo`: `followersCount`,
`followingCount`, `hasReeds`, `activeKeyFingerprint`, `profileTimestamp`) and
SPA `usersInfo` IndexedDB (`spa/src/lib/repositories/userInfo.ts`). The signed
profile is `GET /users/{userID}/profile` only (`verifyUser` covers
username/fingerprint/avatarURL/invitedBy.id/bio/memberSince).
`hasReeds` gates content display (`profile/[userId]/+page.svelte`),
`activeKeyFingerprint` steers key-rotation/removal resolution
(`verifiers/index.ts`, recovery nest assembly). A malicious server can suppress
content or steer which key is treated as authoritative.
**Fix:** treat these strictly as untrusted hints; never let
`activeKeyFingerprint` alone select a signing key without an attested chain.
Clients invalidate cached profiles when `profileTimestamp` is newer than the
stored `serverSignature.timestamp`.

---

## Low

### L1 — Reed author signature never verified on recovery ingest
**Where:** `recovery/handlers.go:111-140` (`verifyReedCountersig` checks only the
server countersignature); `recovery/reeds_follows.go:60` stores caller-supplied
`req.UserSignature.Fingerprint`; `identity/identity.go:154-162`
(`ReedCountersignHeaders` does not bind the author fingerprint).
The countersignature transitively vouches for the reed body, but the author key
fingerprint isn't bound, so a caller can attach a bogus `userSignature.Fingerprint`
to a genuinely-countersigned reed — mislabeling the stored fingerprint (not a
forged reed).
**Fix:** bind the author fingerprint into the reed countersign header set, or
verify the user signature against the resolved author key on ingest.

### L2 — `FollowUser`/`UnfollowUser` are unsigned and don't validate the target
**Where:** `handlers.go:591-622`. Follows carry no per-edge user signature (a
documented recovery limitation) and the target `userID` isn't checked for
existence before the DB call (a non-existent target hits an FK error → 500).
Follow edges cannot be cryptographically re-attributed after a wipe, and the
handler leaks a 500-vs-204 oracle for user existence / allows junk edge attempts.
**Fix:** validate the target exists and return a clean 404; consider signing
follow edges if recovery fidelity matters.

### L3 — `verifyInvite` binds to local `userId`, not a signed issuer
**Where:** `spa/src/lib/verifiers/index.ts:377-408` — the payload `userID` is
`localStorage.getItem('userId')`, so verification proves "matches my local
userId," not the real issuer. Fine for own-invite display; not a trustworthy
issuer binding.

---

## Info / positives / residual trust

- **I1 — SQL injection: none found.** Core, recovery, invites, deletion, and
  coverage queries use `$N` placeholders; the only string concatenation is
  static `FOR UPDATE`-style suffixes with parameterized values.
- **I2 — Markdown rendering is XSS-safe.** `spa/src/lib/components/MarkdownParser.svelte`
  / `MarkdownInline.svelte` render an AST via Svelte templating with no `@html`/
  `innerHTML` (grep-confirmed none in `spa/src`). `resolveLinkHref`
  (`reedMarkdown.ts:41-54`) allowlists schemes (http/https/mailto/web+syrinx);
  identicons are numeric SVG from a hash. Keep it AST-based.
- **I3 — `spaHandler` path traversal: none.** `spa_handler.go:17` uses
  `path.Clean` and `os.Stat` before serving.
- **I4 — Passphrase generation is sound.** `secret/passphrase.go:205-215` uses
  `crypto/rand`; the 64-char alphabet divides 256 evenly, so `% 64` has no
  modulo bias. Generated passphrase is printed once by design; env passphrases
  are never written to the keychain.
- **I5 — Deletion store trusts its caller (latent footgun).**
  `deletion/store.go:30`, `deletion/account.go:37` persist certs without
  verifying; current callers (`handlers.go:696,1694`) do verify author-only and
  compare on idempotent replay. Add a guard/comment so a future caller can't
  skip verification.
- **I6 — Reed/profile subscriptions are open to any authenticated user**
  (`realtime/service.go:824,1047,1108`) — consistent with a public content
  platform, but confirm reed bodies are meant to be readable by non-followers.
- **Residual server-trust:** `verifyPublicKey` (`spa/src/lib/verifiers/index.ts:68-92`)
  trusts the server's binding of a key to a `userID` (the server countersigns
  it). A malicious/compromised server can bind a key to the wrong userID; peers
  cannot. This is inherent to the server-attestation model, not a client bug.

---

## Recommended priority order

1. **C1 / H2 / H3** — make response-signature verification real and fail-closed
   end to end. Without this, several server-side protections are cosmetic.
2. **C2 / C3 / H4 / H5** — stop persisting/logging key material; require key
   possession for "logged in"; lock down the SW signing oracle.
3. **H1** — bind and nonce the WebSocket handshake.
4. **M2 / M3** — fix recovery claim replay and revoked-tip acceptance before
   relying on `RECOVERY_MODE` in anger.
5. **M1** — remove the "envelope is parsed back" contradiction or add escaping.
6. **M4 / M5 / M6 / M7 / M8 / M9** — realtime authorization, WS read limit,
   invite-quota atomicity, and SPA verification hardening.
