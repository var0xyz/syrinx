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
more than in a typical web app. That guarantee has a floor, though: the server
is also the only directory mapping keys to people, so it can forge X by
answering with a key of its own choosing (see [H1](#h1--server-is-the-sole-authority-binding-keys-to-identities)).

> Scope & confidence: findings were derived by reading the actual code (core
> auth/crypto/handlers/db/signing audited directly; recovery, invites,
> deletion, realtime, secret, coverage, and the whole `src/frontend/` audited
> with assistance). The backend is a flat `package main` in `src/backend/`;
> earlier subpackage paths in this file have been resolved to their real
> locations. Where a claim could not be fully proven from source it is marked
> **(unconfirmed)**. This is a point-in-time review, not a guarantee of
> completeness.

## Severity summary

| #      | Severity | Area       | Title                                                                  |
|--------|----------|------------|------------------------------------------------------------------------|
| H1     | High     | design     | Server is the sole authority binding keys to identities               |
| H6     | High     | server     | WebSocket auth signature is replayable and unbound to user/server      |
| M2     | Medium   | server     | Recovery claim challenge is a predictable, untracked timestamp         |
| M9     | Medium   | SPA        | Server-provided counts/hints consumed for trust decisions unsigned     |
| L1     | Low      | server     | Reed author signature never verified on recovery ingest                |
| L2     | Low      | server     | Follow edges carry no user signature                                   |
| L3     | Low      | SPA        | `verifyInvite` binds to local `userId`, not a signed issuer            |
| A2     | Arch     | server     | `profile_subscriptions` needs explicit teardown on disconnect          |

---

## High

### H1 — Server is the sole authority binding keys to identities
**Where:** `verifyPublicKey` (`src/frontend/src/lib/verifiers/index.ts:145-185`)
accepts a key↔`userID` binding on the strength of the server's countersignature
alone; `resolvePublicKeyArmor` (`:78`) and `getUserInfo().activeKeyID` decide
which key is current.
Clients verify that a key is *internally* consistent — the armor matches the
labelled fingerprint, the id is prefixed by the owner's `userID`, the server
countersigned it — but nothing ties that key to the human it claims to
represent except the server's word. The server is the only directory.

A compromised server (or a coerced admin) can mint a keypair, countersign it as
Alice's active key, and serve it to everyone asking for Alice. It then sits in
the middle: content encrypted "to Alice" is encrypted to the server's key, and
it can decrypt, read, store, and re-encrypt to Alice's real key before
forwarding. Neither party sees anything unusual — signatures verify, because
they verify against the key the attacker supplied.

This bites hardest where content is supposed to be private from the server.
Reed relay encrypts to whatever key `getUserInfo` reports as the requester's
active key (`relayDecrypt.ts:25-38`), so key substitution silently defeats the
guarantee that the server never sees reed content. Key rotation is the natural
cover: a substituted key looks exactly like a legitimate rotation.

Note what is *not* the gap: the server's own key is pinned out-of-band on first
run (`serverKeyTrust.ts`) and never fetched from the server, so it cannot swap
its own identity. The gap is that a genuine server key can attest to counterfeit
*user* keys, and clients have no second opinion to consult.
**Fix:** remove the server's monopoly on key distribution. Options, roughly in
increasing order of cost:
- **Key transparency log.** Append-only, independently mirrored, with inclusion
  and consistency proofs (CONIKS/Key Transparency shape). Clients check that the
  key they were served is in the log and that their own key history has not been
  rewritten. Equivocation becomes detectable after the fact.
- **Third-party key directory**, outside admin control, as suggested — clients
  cross-check the server's answer against it. Simpler, but relocates trust
  rather than removing it, and needs its own operator and availability story.
- **Out-of-band verification between users.** Safety numbers / fingerprint
  comparison over another channel, plus a visible warning on key change. Cheap,
  no new infrastructure, and it makes substitution detectable by the people who
  care — but only for users who actually check.
Pinning a contact's key on first sight (TOFU) plus a loud change warning is the
minimum worth having, and composes with all three.

The out-of-band option is specified in
[`specs/attestations/`](specs/attestations/README.md): users verify each other's
keys face to face and publish signed vouches, so a substituted key contradicts
evidence the server never controlled.

### H6 — WebSocket auth signature is replayable and unbound
**Where:** `realtime.go:1039` (verifies a signature over *only* the
`timestamp` string); window is ±5 min (`crypto.go:457-471`,
`validateTimestamp`).
The WS handshake signs just a decimal timestamp — no nonce, no binding to
`userID` or `serverID`. Anyone who captures one handshake query string
(`?userID=&fingerprint=&signature=&timestamp=`) from a proxy/log/referrer can
replay it for up to 5 minutes to open a socket *as that user* and receive their
fanout/relay traffic. A handshake captured against one server is also valid on
any other server that trusts the same key.
**Fix:** sign a server-issued single-use nonce (or a
`BytesToSign` payload binding `serverID`+`userID`+`timestamp`); track/expire
nonces; shrink the window.

---

## Medium

### M2 — Recovery claim challenge is a predictable, untracked timestamp
**Where:** `handlers.go:3102` (`IssueChallenge` = `now().Unix()`); accepted if
≤60s old (`recovery.go:39` `challengeMaxAge`, `recovery.go:1045`
`validateChallengeAge`).
The "challenge" is neither random nor server-stored nor single-use — the client
picks any in-window value, and a captured claim request replays for 60s. It
provides no real anti-replay property.
**Fix:** issue and persist a random single-use nonce; require the signature to
cover it; delete on use.

### M9 — Unsigned server counts/hints consumed for trust decisions
**Where:** `GET /users/{userID}/info` (`UserInfo`: `followersCount`,
`followingCount`, `firstReedId`, `activeKeyID`, `profileTimestamp`) and
SPA `usersInfo` IndexedDB (`src/frontend/src/lib/repositories/userInfo.ts`). The signed
profile is `GET /users/{userID}/profile` only (`verifyUser` covers
username/fingerprint/invitedBy.id/bio/memberSince).
`firstReedId` gates content display and end-of-feed
(`profile/[userId]/+page.svelte`, `ReedsList.svelte`),
`activeKeyID` steers key-rotation/removal resolution
(`src/frontend/src/lib/verifiers/index.ts`, recovery nest assembly). A malicious
server can suppress
content or steer which key is treated as authoritative.
**Fix:** treat these strictly as untrusted hints; never let `activeKeyID`
alone select a signing key without an attested chain.
Clients invalidate cached profiles when `profileTimestamp` is newer than the
stored `serverSignature.timestamp`.

---

## Low

### L1 — Reed author signature never verified on recovery ingest
**Where:** `recovery.go` (`verifyReedCountersig` checks only the server
countersignature) and its reed/follow ingest, which stores the caller-supplied
user signature; `identity.go`
(`ReedCountersignHeaders` does not bind the author fingerprint).
The countersignature transitively vouches for the reed body, but the author key
fingerprint isn't bound, so a caller can attach a bogus `userSignature.Fingerprint`
to a genuinely-countersigned reed — mislabeling the stored fingerprint (not a
forged reed).
**Fix:** bind the author fingerprint into the reed countersign header set, or
verify the user signature against the resolved author key on ingest.

### L2 — Follow edges carry no user signature
**Where:** `FollowUser`/`UnfollowUser` (`handlers.go`). Follows carry no
per-edge user signature (a documented recovery limitation), so follow edges
cannot be cryptographically re-attributed after a wipe.
**Fix:** sign follow edges if recovery fidelity matters.

### L3 — `verifyInvite` binds to local `userId`, not a signed issuer
**Where:** `src/frontend/src/lib/verifiers/index.ts:684-700` — the payload `userID` is
`localStorage.getItem('userId')`, so verification proves "matches my local
userId," not the real issuer. Fine for own-invite display; not a trustworthy
issuer binding.

---

## Architectural / operational risks (non-security)

Findings below aren't security defects — they're limitations of the current
design worth tracking separately.

### A2 — Profile and reed subscriptions need explicit teardown
**Where:** `db.go` — both tables' `viewer_user_id` FK to `identities(id)`,
not `online_users(user_id)`; `teardownUser` in `realtime.go`.
Only pipe subscriptions cascade off presence. Profile and reed subscriptions
can't: a foreign viewer has no presence row here, and
`pending_events.subscription_id` cascades from `profile_subscriptions`.
`teardownUser` clears them on every path that removes presence (socket close,
the reaper, boot), so a new path that deletes `online_users` without calling
it leaks rows.

### A3 — Realtime federation assumes a single replica
**Where:** `CLEAR_PRESENCE_ON_BOOT` in `main.go`; `reapStalePresence` and
`handlePong` in `realtime.go`; `specs/federation/08_server_reset.md`.
The boot clear, the `realtime-reset` notices and the reaper's "is the socket
here" check all assume this process is the whole server. With more than one
replica, one restarting would wipe state its siblings still serve and tell
peers to forget the whole server, so the notices are off whenever
`CLEAR_PRESENCE_ON_BOOT` is. Running several replicas needs presence and
reset semantics per replica first.

### A4 — Federation delivery is best-effort at the edges
**Where:** `federation_delivery.go`, `openForeignEvent` in `realtime.go`;
`specs/federation/09_reed_delivery.md`.
- The cap of three copies crossing per reed per peer is checked without a
  lock, so requests arriving at the same instant can send a few more.
  Accepted: rare, and never unbounded.
- A stream only advances on a trigger (a publish or removal by that author,
  any client SYNC, the peer's boot notice). A peer that crashed without a
  shutdown notice keeps failing deliveries until it boots; nothing marks it
  down on failure.
- Streams are per author, so a reply can reach a peer before its parent.
  The receiver tolerates it (the parent's identity is upserted), but a
  peer only dispatches a foreign reply to its viewers of the immediate
  parent, not of ancestors further up the thread.
- Every reed goes to every peer, and opening a foreign profile or reed is
  a round trip to its server. The mesh page tells admins to compare load
  before connecting.

---

## Recommended priority order

1. **H1** — break the server's monopoly on key distribution. A design change,
   not a patch; TOFU pinning plus a key-change warning is the cheap first step
   and composes with whatever comes after.
2. **H6** — bind and nonce the WebSocket handshake.
3. **M2** — fix recovery claim replay before relying on `RECOVERY_MODE` in
   anger.
4. **M9** — SPA verification hardening. M9 is H1's near neighbour: `activeKeyID` is an unsigned hint
   that steers key selection.
5. **L1 / L2 / L3** — recovery ingest signature checks, follow-edge signing,
   and invite issuer binding.
