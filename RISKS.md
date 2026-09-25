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
> `deletion/`, `realtime/`, `secret/`, `coverage/`, and the whole `src/frontend/`
> audited with assistance). Where a claim could not be fully proven from
> source it is marked **(unconfirmed)**. This is a point-in-time review, not a
> guarantee of completeness.

## Severity summary

| #      | Severity | Area       | Title                                                                  |
|--------|----------|------------|------------------------------------------------------------------------|
| H1     | High     | server     | WebSocket auth signature is replayable and unbound to user/server      |
| H4     | High     | SPA        | `userId` in `localStorage` alone unlocks the app                       |
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
| A1     | Fixed    | server     | In-memory subscriber state blocks running >1 replica (no HA)           |

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

### H4 — `userId` in `localStorage` alone unlocks the app
**Where:** `src/frontend/src/lib/services/auth.ts:29-43` (`hasLocalIdentity`/
`isLoggedIn` read only `localStorage.userId`, "no network").
Anyone able to write `localStorage.userId` (shared machine, XSS, sibling tab) is
treated as logged in. There is no check that a usable private key matching the
account's active-key fingerprint is present.
**Fix:** gate "logged in" on possession of a private key whose fingerprint
matches the account's active key.

---

## Medium

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
**Where:** `src/frontend/src/lib/services/crypto.ts:22-25,141` — verification reference
time is `max(now, serverTimestamp) + 5min`, where `serverTimestamp` is the
server-supplied countersignature time.
A malicious server can set a far-future `timestamp`, pushing the verification
clock forward and causing OpenPGP.js to accept signatures from keys that should
be **expired**, defeating key-expiry.
**Fix:** cap the reference time at `now + skew`; never let an attacker-controlled
timestamp advance the verification clock.

### M8 — SPA `verifySignature` silently falls back binary→text mode
**Where:** `src/frontend/src/lib/services/crypto.ts:144-160`. The Go signer uses binary
detached signatures (`crypto/crypto.go:215` `openpgp.DetachSign`), so accepting
text mode (with CR/LF canonicalization) broadens the set of byte sequences that
verify for a given signature.
**Fix:** pin binary mode; remove the text fallback.

### M9 — Unsigned server counts/hints consumed for trust decisions
**Where:** `GET /users/{userID}/info` (`UserInfo`: `followersCount`,
`followingCount`, `firstReedId`, `activeKeyFingerprint`, `profileTimestamp`) and
SPA `usersInfo` IndexedDB (`src/frontend/src/lib/repositories/userInfo.ts`). The signed
profile is `GET /users/{userID}/profile` only (`verifyUser` covers
username/fingerprint/invitedBy.id/bio/memberSince).
`firstReedId` gates content display and end-of-feed
(`profile/[userId]/+page.svelte`, `ReedsList.svelte`),
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
**Where:** `src/frontend/src/lib/verifiers/index.ts:684-700` — the payload `userID` is
`localStorage.getItem('userId')`, so verification proves "matches my local
userId," not the real issuer. Fine for own-invite display; not a trustworthy
issuer binding.

---

## Info / positives / residual trust

- **I1 — SQL injection: none found.** Core, recovery, invites, deletion, and
  coverage queries use `$N` placeholders; the only string concatenation is
  static `FOR UPDATE`-style suffixes with parameterized values.
- **I2 — Markdown rendering is XSS-safe.** `src/frontend/src/lib/components/MarkdownParser.svelte`
  / `MarkdownInline.svelte` render an AST via Svelte templating with no `@html`/
  `innerHTML` (grep-confirmed none in `src/frontend/src`). `resolveLinkHref`
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
- **Residual server-trust:** `verifyPublicKey` (`src/frontend/src/lib/verifiers/index.ts:140-220`)
  trusts the server's binding of a key to a `userID` (the server countersigns
  it). A malicious/compromised server can bind a key to the wrong userID; peers
  cannot. This is inherent to the server-attestation model, not a client bug.

---

## Architectural / operational risks (non-security)

Findings below aren't security defects — they're limitations of the current
design worth tracking separately.

### A1 — Reed/pipe subscription state was in-process memory, blocking HA (>1 replica) — **fixed**
**Was:** `realtime.go` held `reedSubscribers` and `pipeSubscribers` as plain
in-memory maps guarded by a `sync.RWMutex`, so live fanout decisions depended on
which websocket connections happened to be held by *this* process. A viewer on
replica A was invisible to replica B's fanout, making `REED_REPLY`/stats-push
delivery partial and instance-dependent.

(The original entry cited `realtime/connection_manager.go` and a
`profileSubscribers` map; neither existed. The backend is a flat `package main`
in `src/backend/`, profile subscriptions were already in Postgres, and the
genuinely memory-only state was `pipeSubscribers`.)

**Fixed in two parts:**

1. *Subscription state moved to Postgres.* `pipe_subscriptions` is a new UNLOGGED
   table and `reed_subscriptions` now FKs to `online_users(user_id) ON DELETE
   CASCADE`, so disconnecting clears subscriptions through the cascade rather
   than explicit teardown — the pattern `broadcast_subscriptions` already used.
   Fanout reads go through `DataService` (`GetPipeListeners`,
   `GetReedSubscriberUserIDs`, `GetTagsWithListeners`), so every replica computes
   the same recipient set. Only `userConnections` remains in memory, since a
   `*websocket.Conn` is inherently process-local.

   This also fixed a live single-replica bug: `handleSubscribeReed`'s foreign-reed
   branch returned before ever touching the in-memory map, so foreign reed
   subscriptions were invisible to every map-based fanout read.

2. *Cross-replica delivery.* `realtime_bus.go` forwards deliveries between
   replicas over Postgres `LISTEN`/`NOTIFY` (no new infrastructure). `SendToUser`
   writes locally when it holds the socket and otherwise publishes to the bus, so
   call sites are unchanged — but note a nil return now means "delivered *or*
   handed off". Frames over the 8000-byte `NOTIFY` ceiling publish as a bare
   wake-up; the durable `pending_events` + `catchUp` path supplies the payload.
   Enabled with `REALTIME_BUS_ENABLED` (off by default; a single replica needs no
   bus). Relay-dispatch gates that previously asked the local map
   (`dispatchNextIfConnected`, `dispatchNIfConnected`, `notifyParentAuthorOfReply`)
   now check `online_users`, which is true across replicas.

**Also fixed — stale presence.** `online_users` had no heartbeat, TTL or sweep,
so a crashed Go process (Postgres surviving) left every row behind and the server
booted believing everyone was online. This became load-bearing once subscriptions
cascade off presence. Clients now send `PONG` every minute, refreshing
`online_users.last_pong`; `startPeriodicCleanup` — previously an empty TODO
already running in a goroutine — evicts rows idle for two minutes and disconnects
any local socket still held. `CLEAR_PRESENCE_ON_BOOT` (default on) also clears
presence at startup; turn it off when running >1 replica, where a booting
instance would otherwise wipe its peers' presence, and rely on the reaper.

**Residual:** `profile_subscriptions` still FKs to `identities` rather than
`online_users`. Retargeting it would be consistent, but
`pending_events.subscription_id` cascades from it, so disconnect would start
dropping pending events by a second path. Its explicit teardown on disconnect is
therefore still required.

---

## Recommended priority order

1. **H4** — require possession of a private key matching the account's
   active key for "logged in".
2. **H1** — bind and nonce the WebSocket handshake.
3. **M2 / M3** — fix recovery claim replay and revoked-tip acceptance before
   relying on `RECOVERY_MODE` in anger.
4. **M4 / M5 / M6 / M7 / M8 / M9** — realtime authorization, WS read limit,
   invite-quota atomicity, and SPA verification hardening.
