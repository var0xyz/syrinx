# Server Recovery / DB Reconstruction

## Status

**Partially implemented.** Normal-operation prerequisites for trustless recovery
are in place (see proposals 01–10). The recovery feature itself — mode toggle,
boot reconciliation, key-bundle export/import, claim / peer identity / holdings
endpoints, client sync, and recovery bookkeeping tables — is **not implemented
yet**.

**Implemented** (normal operation):

- **Identity countersignatures** at signup / profile update / key rotation.
- **Signed, server-countersigned, server-timestamped profile records.**
- **Signed revocations**, replicated to followers.
- **Reed `server` block** binding `reedID`, `authorID`, and the server signing-key
  **fingerprint**, with one canonical form shared by signer and verifier.
- **Random, server-scoped user IDs** (replacing the Sqids counter).

**Not implemented yet** (recovery feature):

- **`RECOVERY_MODE`**, boot-time key-bundle **import**, operator key-bundle
  **export**.
- Own-identity **claim** (`GET`/`POST /api/recovery/identity/claim`) with a
  short-lived challenge.
- Authenticated peer **identity** report (`POST /api/recovery/identity`),
  one-at-a-time reed holdings, batched follows, and **`/complete`**.
- Recovery-only tables: **`unclaimed_accounts`**, **`ongoing_recoveries`**.

**Deferred** (not required for the first recovery cut):

- **Per-user system notifications** for username-collision renames. Losers are
  renamed in place during recovery; there is no persisted notification for now
  (see Proposal 11).

## Code organization

**All server-side recovery code lives in the `recovery/` Go package**
(`syrinx/recovery`). That includes boot reconciliation, key-bundle
export/import helpers, nested key-chain types, persistence for
`unclaimed_accounts` / `ongoing_recoveries`, signature verification for claim
and peer report-back, HTTP handlers, and route registration.

The main package only **wires** recovery in: env flags, calling
`recovery.ReconcileBoot` at startup, `recovery.RegisterRoutes` when
`RECOVERY_MODE` is on, and middleware checks such as `recovery.IsOngoing`.
Do not add recovery endpoints, recovery SQL, or recovery verification logic
under `handlers.go` / `services.go` or other root files.

Shared normal-operation helpers used by both live traffic and recovery (e.g.
canonical identity/reed payload builders) live in `identity/`, not in
`recovery/`. Operator CLI for the identity bundle lives under `cmd/ops/`.

## Motivation

Syrinx stores very little authoritative content server-side. Reed *content*,
user *signatures*, and the server's *countersignature* live only on user
devices (IndexedDB / localStorage). The server DB mostly holds metadata,
identity bindings, and a handful of keys. This is by design — but it means that
if the server DB is lost, the shared state must be reconstructed from what
users still hold locally.

Because every recoverable record is signed by its author **and** countersigned
by the server, we can rebuild the DB from client submissions and
cryptographically verify both that the data is authentic (author signature) and
that it legitimately existed on this server (server countersignature). We
deliberately do **not** keep a conventional DB backup: the system must be able
to *self-heal from the users' own data* after a hostile takeover, keeping as
little authoritative state server-side as possible.

## Actors

- **Operator** — the person redeploying the server. Preserves the server
  identity (ID + the **full** signing-key history) across the redeploy.
- **User** — an end user whose device holds their own profile, keys, reeds,
  cached copies of other users' profiles/reeds, and their own follow list.

### Who we authenticate

**Own identity** cannot use normal signature-auth yet (the key is not on
record). The owner **claims** via a challenge-response:

1. `GET /api/recovery/identity/claim` returns a server unix-second timestamp.
2. `POST /api/recovery/identity/claim` sends that challenge, a detached
   signature over it (active private key), the countersigned **profile**, and
   the **full nested public-key chain** (active key → … → signup key), each
   node optionally carrying a revocation.

The server checks the challenge is ≤ 60 seconds old and that the signature
matches the outermost (active) key, then verifies profile + every key /
revocation / predecessor link. Only the private-key holder can claim; a peer
who merely holds a cached profile cannot.

**Everything afterwards** — peer identities, held reeds, follows, import
complete — uses the **normal signature-auth middleware**. Peer-seeded accounts
sit in `unclaimed_accounts` until *their* owner claims.

## Server identity continuity (operator responsibility)

The server ID **and the full server signing-key history** must survive the
redeploy. The operator exports them from the old instance and injects them into
the new one.

- **All server signing keys**, not just the current one. The server rotates its
  signing key over its lifetime (`ProcessRevocations` revokes a key; the next
  `InitServerKey` mints a fresh one). Every reed and identity record is
  countersigned by whichever key was active at the time, and
  `reeds.private_key_fingerprint` is a `NOT NULL` foreign key into
  `private_keys`. A rotated-away key must therefore be restored too, or old
  countersignatures cannot be verified and the FK cannot be satisfied. Restore
  the whole of `private_keys` (`fingerprint`, `armor`, `revoked_at`,
  `revoke_reason`); derive `public_keys` from each.
- **The server ID.** Countersigned payloads embed `serverID`, so a new random ID
  would invalidate every countersignature.

All recovery verification is performed **server-side** using the restored keys,
selecting the key **by fingerprint** for each record.

### Required work (`recovery/` package)

Server identity import/export and recovery endpoints are implemented in
`syrinx/recovery` (see *Code organization*). Boot-time import restores the
operator-provided `serverID` and full signing-key history from
`RECOVERY_KEY_BUNDLE`; the operator CLI is `cmd/ops` (`export-identity`,
`rotate-passphrase`).

### Key bundle (export / import format)

The server identity is the **only** state that is not self-healing: it cannot be
reconstructed from users, so it must be exported and backed up **proactively,
while the server is healthy** — after the DB is gone it is too late. Losing the
bundle means every countersignature becomes unverifiable and recovery is
impossible.

The system provides an operator command to export the identity as a single JSON
file (e.g. `server-identity.json`):

```json
{
  "version": 1,
  "serverID": "Ab3xY9pQ",
  "signingKeyFingerprint": "<hex fingerprint of the currently-active key>",
  "keys": [
    {
      "fingerprint": "<hex>",
      "privateKeyArmor": "-----BEGIN PGP PRIVATE KEY BLOCK----- ... (still encrypted with SERVER_KEY_PASSPHRASE)",
      "publicKeyArmor": "-----BEGIN PGP PUBLIC KEY BLOCK----- ...",
      "createdAt": "2026-01-02T03:04:05Z",
      "revokedAt": null,
      "revokeReason": null
    }
  ]
}
```

- Private keys are exported **still encrypted** (verbatim from
  `private_keys.armor`); the export never decrypts them. `SERVER_KEY_PASSPHRASE`
  is supplied separately at boot (as today), so the bundle is safe at rest to the
  same degree the DB was. The passphrase must be the **same** one that encrypted
  the keys (see Open questions).
- The bundle carries the full key history (active + rotated/revoked) so every
  historical countersignature verifies and `reeds.private_key_fingerprint` FKs
  resolve.
- `serverID` is restored verbatim; the server **name** is not part of the bundle
  (it comes from `SERVER_NAME` and may change — countersignatures bind `serverID`
  only).
- **Delivery**: mount the file and point to it with an env var, e.g.
  `RECOVERY_KEY_BUNDLE=/run/secrets/server-identity.json`. A whole-bundle env var
  is unwieldy; a mounted file (or a secret store that materializes a file) is the
  intended mechanism.

**Restoring requires the same `SERVER_KEY_PASSPHRASE`** the bundle was encrypted
with. Two distinct rotations must not be confused:

- **Signing-key rotation** — minting a *new* server keypair (via `.rvk`
  revocation + `InitServerKey`). Produces additional entries in the key history;
  the old key stays in the bundle so its past countersignatures keep verifying.
- **Passphrase rotation** — re-wrapping the *same* keys under a new passphrase. A
  dedicated operator command decrypts every `private_keys.armor` with the old
  passphrase and re-encrypts with the new one; afterwards the operator **re-runs
  the export** so the backed-up bundle matches the new passphrase. A bundle and a
  passphrase are always a matched pair: keep them in sync, and a bundle taken
  before a passphrase change needs the *old* passphrase to import.

## Trust model

Every recoverable identity/content record carries **two** signatures:

1. **Author signature** — the user's detached signature over the canonical
   record. Note for reeds: the server never validates this signature over the
   content — neither at `SignReed` time (it only countersigns the opaque
   signature string and never even receives the content) nor at recovery.
   **Content authenticity is a peer-side check.** The server's trust in a reed
   comes from its own countersignature.
2. **Server countersignature** — the server's detached signature over the
   record, **including a server-authoritative timestamp** and the record's
   identity (`reedID` + `authorID` for reeds; `userID` + `fingerprint` +
   `username` for identity). Verifiable against the restored server key of the
   matching fingerprint. Proves the record legitimately existed on this server
   and establishes its position in time.

### Why the server timestamp is mandatory

A timestamp signed by the *user's own key* is trustworthy for choosing between
versions of that same user's record, but it is **attacker-chosen** and cannot
arbitrate anything globally contended. Two concrete failures if we trusted a
user-supplied timestamp:

- **Username squatting.** Usernames are unique. In normal operation the server
  rejects a duplicate at write time (`UsernameExists`). Recovery replays signed
  profiles *without* that mediation. An attacker signs
  `{userID: B, username: "alice", ts: <far future>}` with their own key — a
  valid signature over a value they picked — and "newest wins" hands them a name
  held by someone else.
- **Revocation replay → account takeover.** A user rotated from compromised
  `K1` to `K2`; the revocation lived only in the wiped DB. `K1`'s old records
  are still validly signed (and, if we countersigned the binding, still validly
  countersigned — the binding genuinely existed). An attacker holding `K1`
  resubmits `K1`'s identity, omits the revocation, and reinstates `K1` as an
  active key. Post-recovery they authenticate as the victim.

Both close with the **same mechanism**: the server countersigns each identity
record with **its own clock**, and recovery resolves all "which is current"
questions by that server timestamp — never the submitter's.

### The three rules

1. **Server-countersigned identity records.** For each user, the server issues
   (at signup / profile update / key rotation) a countersigned identity record
   carrying, at minimum:
   - `userID`
   - `username`
   - the currently-active key fingerprint
   - profile fields (avatarURL, bio)
   - the immutable binding `(userID, fingerprint, createdAt)` for each key
   - a server-authoritative **account creation date** (`accountCreatedAt` /
     wire `memberSince`), set once at signup and carried unchanged through
     every later record
   - a **server timestamp** and the `serverID`

   The latest such record (by server timestamp) is authoritative.

2. **Monotonic revocation.** Revocations must be signed and, once any party
   presents a valid revocation for a key, "revoked" wins permanently. A revoked
   key can never be overridden back to active by replaying an older "valid"
   record. **To keep revocations available after a wipe**, a signed revocation
   (and the old→new rotation proof) is **replicated to the revoker's followers**
   in normal operation, the same way reeds propagate — so it survives as widely
   as any cached profile and any one holder can resubmit it during recovery.

3. **Conflict resolution by server timestamp.** When multiple submissions
   describe the same entity, the record with the newest **server** timestamp
   wins; revocation state is applied on top and is sticky.

   **Username contention** across *different* `userID`s is resolved **online,
   the instant a collision is detected** (no batch step): the newest
   `server_signed_at` keeps the name; the loser is renamed with a permanent
   suffix (e.g. `alice#a1b2`, from their `userID`). They can change it later
   via the normal flow. A persisted system notification explaining the rename
   is **deferred** (Proposal 11).

   To keep live signups from racing restored owners for names during recovery,
   operators should set **`SIGNUPS_ENABLED=false`** for the window.

### Prerequisites (status)

**Done** — shipped in normal operation (proposals 01–10):

- Identity countersignatures at signup / profile update / key rotation.
- Signed, server-countersigned, server-timestamped profile records.
- Signed, replicated revocations.
- Reed countersignature with bound `reedID` / `authorID` / server key
  `fingerprint` and one canonical signed form.
- Random, server-scoped user IDs.

**Remaining** — recovery feature (all of it under `syrinx/recovery`):

- **`RECOVERY_MODE`** and boot reconciliation against `RECOVERY_KEY_BUNDLE`.
- Operator key-bundle **export** / matching boot-time **import** of the full
  server signing-key history and `serverID` (see *Required work* / `cmd/ops`).
- Own-identity **claim** (challenge + nested key chain) and authenticated peer
  **`POST /api/recovery/identity`**.
- **`unclaimed_accounts`**, **`ongoing_recoveries`**, reed/follow/`complete`
  endpoints, and the import gate.

**Deferred**:

- Per-user system notification store for collision renames (Proposal 11).
  Recovery renames losers silently for now.

## What is reconstructed

User-recoverable:

| Table                  | Source on device                          | Trust anchor |
|------------------------|-------------------------------------------|--------------|
| `users`                | latest signed identity record             | server countersig (server ts) |
| `user_keys`            | identity record active key + bindings     | server countersig over `(userID, fingerprint, createdAt)` |
| `user_key_revocations` | signed revocations (replicated)           | signed + monotonic |
| `reeds`                | reed `server` block (`reedID`, `author`, server `fingerprint`, `ts`) | server countersig bound to `reedID`+`author`, verified against the matching restored server key |
| `reed_allocations`     | holder's own report                       | normal signature-auth (holder signs the request) |
| `user_following`       | user's own follow list                    | normal signature-auth (no per-edge signature) |

Operator-restored (not from users): `servers` (preserved ID + name),
`private_keys` (**full history**), `public_keys` (derived).

Recovery-internal bookkeeping (not reconstructed from users):

- `unclaimed_accounts` — restored (peer-seeded) accounts whose owner has not
  yet claimed.
- `ongoing_recoveries` — claimants who have not finished their import ledger;
  while `RECOVERY_MODE` is on, these users are barred from non-recovery API use.

Not reconstructed (ephemeral/realtime, repopulate on reconnect):
`online_users`, `broadcast_subscriptions`, `pending_events`,
`pending_reed_requests`, `profile_subscriptions`.

## Recovery flow

All recovery routes exist only while `RECOVERY_MODE` is on. When off, they are
not registered (no attack surface).

| Endpoint | Auth | Cardinality |
|----------|------|-------------|
| `GET /api/recovery/identity/claim` | none | — |
| `POST /api/recovery/identity/claim` | challenge + sig | own identity |
| `POST /api/recovery/identity` | signature-auth | **one** peer |
| `POST /api/recovery/reeds` | signature-auth | **one** reed |
| `POST /api/recovery/following` | signature-auth | **≤100** userIDs |
| `POST /api/recovery/complete` | signature-auth | — |

**Normal use stays enabled during recovery** for anyone who has finished
claim/import (or never entered `ongoing_recoveries`). Live writes carry the
newest server timestamps and win newest-wins over restored records. Admins
should set **`SIGNUPS_ENABLED=false`** while recovery is underway so new
accounts cannot race restored users for usernames; that does **not** freeze
writes for recovered users.

### Nested key chain (claim and peer identity)

Identity submissions use a recursive nest (not a fingerprint map):

```json
{
  "profile": { "...User wire..." },
  "key": {
    "key": { "...active Key wire..." },
    "revocation": null,
    "predecessor": {
      "signature": "<old key's detached sig over this key's armor>",
      "key": { "...Key wire..." },
      "revocation": { "...or null..." },
      "predecessor": null
    }
  }
}
```

- Outermost node is the **active** key; nest walks back to the signup key
  (`predecessor: null`).
- **Full chain required.** Incomplete nests are rejected with no partial write.
  If a reporter lacks every key in a peer's rotation history, they **skip** that
  peer rather than submit a partial chain.
- Insert **oldest → newest** so `user_keys.predecessor_fingerprint` →
  `user_keys(fingerprint)` remains satisfied (no dangling predecessor FKs).
- A bad predecessor link aborts the whole request.

### Client responsibilities

Recovery is **client-driven**: the server exposes the steps; the client owns
the sync ledger and progress UI.

- **Order:** claim self → each peer identity (complete nest only) → each reed →
  follows in chunks of 100 → `POST /complete`.
- **Import gate:** after claim, the server inserts `ongoing_recoveries`. While
  `RECOVERY_MODE` is on **and** that row exists, non-recovery API routes return
  `403`. The client also blocks normal UI until `complete`.
- **A live account forfeits recovery.** If the device already has a logged-in
  account, the client does **not** offer recovery on that device.

### Phase 0 — Operator redeploy

At boot with `RECOVERY_MODE` on, the server reconciles the DB against the bundle:

- **No existing server identity** (no `self = TRUE` row in `servers`): fresh
  import. Initialize the schema, load the bundle from `RECOVERY_KEY_BUNDLE`
  (decrypting each private key with `SERVER_KEY_PASSPHRASE`), write the full
  `private_keys` / `public_keys` history, restore `serverID` verbatim, set the
  active signing key from `signingKeyFingerprint`, and create recovery-only
  tables (`unclaimed_accounts`, `ongoing_recoveries`).
- **Existing identity that matches the bundle** (same `serverID` and key set): a
  previous recovery was interrupted — **resume**. Keep the already-restored data
  (including any live writes) and re-open recovery endpoints.
- **Existing identity that does *not* match the bundle**: **abort startup.**

The client learns recovery is active from `/server/info` (`recoveryMode`,
`signupsEnabled`) and shows the recovery banner + "import your data" / signup
buttons accordingly.

### Phase 1a — Own identity claim

**`GET /api/recovery/identity/claim`** → `{ "challenge": <unix seconds> }`.

**`POST /api/recovery/identity/claim`** body:

```json
{
  "challenge": 1710000000,
  "signature": "<base64 detached PGP sig over the decimal challenge>",
  "profile": { "...User..." },
  "key": { "...nested KeyNode..." }
}
```

Steps:

1. Reject if challenge is in the future or older than **60 seconds**.
2. Verify the nested key chain (full chain, server countersigs, predecessor
   links, optional revocations); require `server.id == serverID`.
3. Verify `signature` over the challenge with the **outermost** public key.
4. Upsert `users` by verbatim `userID` with atomic newest-wins on
   `server_signed_at`; resolve username collisions (rule 3: newest
   `server_signed_at` keeps the name, loser renamed). No system notification
   (Proposal 11 deferred).
5. Insert keys oldest→newest (FK-safe) and sticky revocations; set active
   fingerprint on the user.
6. **If the user row was created by this claim:** do **not** add
   `unclaimed_accounts` (owner just proved presence).
7. **If the user already existed** (e.g. peer-seeded): apply newest-wins +
   keys, then `DELETE FROM unclaimed_accounts WHERE user_id = $1`.
8. `INSERT INTO ongoing_recoveries` (`ON CONFLICT DO NOTHING`) — import gate
   starts.

Idempotent re-claim is allowed. After `200`, the client initializes normal
request signing with the restored private key.

### Phase 1b — Peer identity (authenticated)

**`POST /api/recovery/identity`** — **one peer per request**, same
`{ profile, key }` shape as claim (no challenge fields). Caller must already
have claimed (key on record).

1. Reject if `profile.id == caller` (own identity must use claim).
2. Same full-chain verification and oldest-first key insert as Phase 1a.
3. Conditional upsert by `server_signed_at`; rename on username collision as
   needed (rule 3).
4. **If this request created the `users` row** →
   `INSERT INTO unclaimed_accounts ON CONFLICT DO NOTHING`.
5. If the user already existed → newest-wins only; never resurrect
   `unclaimed_accounts` for an already-claimed account.

Peers the client cannot fully nest are skipped in the ledger.

### Import gate (`ongoing_recoveries`)

While `RECOVERY_MODE` **and** the caller’s `user_id` is in `ongoing_recoveries`,
signature-auth middleware allows only `/api/recovery/*`, `/api/server/info`, and
`/api/server/keys/`. All other API routes return **403**. When `RECOVERY_MODE`
is off, the gate does not apply even if rows remain.

**`POST /api/recovery/complete`** deletes the caller’s `ongoing_recoveries` row
(idempotent), ending the import lock for that user.

### Phase 2 — Reeds and holdings (authenticated)

**`POST /api/recovery/reeds`** — **one reed per request** (no batches; keeps
verification bounded):

`{ reedID, authorID, userSignature, serverSignature, serverFingerprint, ... }`

1. Author must already exist.
2. Verify the server countersignature (binds `reedID` + `authorID` + server ts +
   `serverID`) against the restored key of `serverFingerprint`.
3. Upsert `reeds` metadata idempotently; insert
   `reed_allocations(reedID, reportingUserID)`. No reed content is stored.

### Phase 3 — Follows (authenticated)

**`POST /api/recovery/following`** — body `{ "userIDs": [ ... ] }`, **at most
100** IDs per request (reject larger bodies).

Caller asserts their own follow list. Inserts use `ON CONFLICT DO NOTHING`;
edges toward not-yet-restored users are skipped; the client retries or drops
them as the ledger advances.

### Ending recovery

There is **no global finalize** — the server cannot know whether every user has
reported in. Operator ends the window by turning `RECOVERY_MODE` off, which
unregisters recovery endpoints (including claim). Already-claimed users who
still have `ongoing_recoveries` are no longer API-gated by that table once mode
is off; they should have called `complete` while mode was on.

`unclaimed_accounts` remains the admin “still unclaimed” gauge (floor on what is
missing). It **persists** after mode-off until the operator drops it. If
`RECOVERY_MODE` is off and the table is non-empty, startup logs a **non-fatal**
warning with the residual count.

> **Cutoff caveat.** A user whose data no one restored has no account at all —
> reappearing means a fresh signup with a *new* random ID. Once recovery is off
> there is no cooperative claim path.

> **Username sniping.** Collisions resolve by newest `server_signed_at`; there
> is no recovery-window sniper filter. Operators should set
> **`SIGNUPS_ENABLED=false`** during recovery so live signups cannot race
> restored owners for names.

## Threat model & accepted limitations

Prevented by the design:

- **Content forgery** — impossible without the author's private key.
- **Fabricated server acceptance / reed misattribution** — impossible without
  the server private key; the countersignature binds `reedID` + `authorID`,
  so a genuine signature pair cannot be replayed onto a different reed.
- **Username squatting via forged timestamps** — resolved by server-timestamped
  identity records.

Conditionally prevented (requires the record to be **resubmitted by some
holder**):

- **Revocation replay / key reinstatement** — prevented **as long as a valid
  revocation is resubmitted** (by the owner in their claim nest, or by a peer
  nesting that key+revocation). If *no* honest party holds the revocation, an
  attacker holding a compromised old key can claim with it and authenticate as
  the victim. Accepted for the intended high-trust, small-community deployments.

Inherent limitations (accept and document):

- **Rollback to a real prior state.** An attacker can withhold newer records and
  submit an older, genuinely-signed one. This also covers username squatting via
  a *genuine* stale record that reclaims a freed name. We prevent forgery, not
  selective omission.
- **Deletion is not provable.** A reed the author deleted is still validly
  signed; any holder can resubmit it.
- **Completeness cannot be forced.** Data held only on a lost device is
  unrecoverable. Peers with incomplete key nests cannot seed that user at all.
- **Claim challenge endpoint during the window.** `GET`/`POST .../identity/claim`
  must work without a prior key on record, so an attacker can force expensive
  verifications (DoS). Same class of weakness as other public endpoints.
  Rate/size limiting is deferred (see Open questions).

## Schema changes (summary)

Already in the base schema (normal operation):

- **`users`**: `server_signed_at`, identity signature columns, random
  server-scoped `id`. Username uniqueness is **enforced continuously** —
  including during recovery — with collisions resolved by immediate rename
  (rule 3), so the unique index never has to be dropped. A username-collision
  loser is renamed in place (permanent suffix); there is **no reselection
  flag** and **no notification** for now (Proposal 11 deferred). **No `claimed`
  column** on `users`: claim state is “not in `unclaimed_accounts`” after own
  claim (or never listed, for claimants who created their own row).
- **`user_keys`**: `predecessor_fingerprint` FK to `user_keys(fingerprint)` —
  recovery must insert full chains oldest-first and never accept partial nests.
- **`reeds`**: `private_key_fingerprint` references `private_keys`; `signed_at`
  = server countersignature timestamp.
- **`user_key_revocations`**: signed revocation statements in normal operation;
  recovery verifies and stores them with the nest (same shape as live).
- **Reed `server` block (client + wire)**: `fingerprint` (server signing key),
  `reedID`, and `author` in the countersigned payload; one canonical form and
  pinned base64 layering.
- **Removed**: the `user_count` table (Proposal 02).

Still to add with the recovery feature:

- **`unclaimed_accounts`** (recovery-only): one `user_id` per account **created
  by peer** `POST /api/recovery/identity` whose owner has not yet claimed. Deleted
  on successful own claim. `count(*)` is the admin gauge and drives the
  non-fatal startup warning. Not an auth gate. Persists after recovery ends
  until the operator drops it.
- **`ongoing_recoveries`** (recovery-only): one `user_id` per claimant who has
  not finished import. Inserted on successful claim; deleted by
  `POST /api/recovery/complete`. While `RECOVERY_MODE` is on, presence of a row
  blocks that user from non-recovery API routes.

Deferred:

- **`user_notifications`** (Proposal 11): per-user system-message store for
  collision renames and other operator/system messages. Not part of the first
  recovery cut.

## Resolved (decisions)

- **Code organization**: all recovery logic in `syrinx/recovery`; main only
  wires boot/routes/middleware. Shared payload builders in `syrinx/identity`;
  ops CLI in `cmd/ops`.
- **Activation**: `RECOVERY_MODE` env flag. Fresh import against a DB with no
  server identity; **resume** if the existing identity matches the bundle;
  **abort** on mismatch. Identity/keys via `RECOVERY_KEY_BUNDLE` +
  `SERVER_KEY_PASSPHRASE`. Optional `SIGNUPS_ENABLED=false` to disable new
  signups only (recovered users keep full normal use).
- **Client detection**: `/server/info` reports `recoveryMode` and
  `signupsEnabled`.
- **Own claim**: `GET`/`POST /api/recovery/identity/claim` with a ≤60s challenge
  signed by the active key; body carries profile + **full nested** key chain.
  Creates a claimed user or claims a peer-seeded one (deletes
  `unclaimed_accounts`) and inserts `ongoing_recoveries`.
- **Peer identity**: authenticated `POST /api/recovery/identity`, **one user**
  per request, same nest shape; creates `unclaimed_accounts` when the row is
  new. No list endpoint (verification cost).
- **Key chains**: incomplete nests rejected; insert oldest→newest to preserve
  `user_keys.predecessor_fingerprint` FK integrity.
- **Reeds**: authenticated, **one reed** per request.
- **Follows**: authenticated batches of **at most 100** userIDs.
- **Import gate**: `ongoing_recoveries` + middleware 403 while
  `RECOVERY_MODE && importing`; cleared by `POST /api/recovery/complete`.
  Client mirrors the block in the UI.
- **Unclaimed gauge**: peer-seeded accounts awaiting owner claim; startup
  warning when mode is off but rows remain.
- **Username collisions**: newest `server_signed_at` wins; loser renamed with
  a permanent suffix. Prefer `SIGNUPS_ENABLED=false` during recovery to avoid
  live signup races for names.
- **Completion**: no global finalize; operator turns `RECOVERY_MODE` off;
  per-user import ends via `complete`.
- **Passphrase**: same `SERVER_KEY_PASSPHRASE` to import; separate rotate +
  re-export command.
- **Migration**: not applicable — pre-launch blank slate.
- **Abuse mitigation**: deferred.
- **Collision-rename notifications**: deferred (Proposal 11).

## Open questions

1. **Residual abuse mitigation** for the claim challenge endpoints
   (verification-cost DoS) — rate/size limits — deferred. Same class of
   weakness as every other public endpoint.
