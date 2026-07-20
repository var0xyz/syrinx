# Server Recovery / DB Reconstruction

This directory is the recovery **feature** proposal set. The root README is
the full protocol; numbered files below are independently reviewable
implementation steps. Land them in order unless a step's "Depends on" says
otherwise.

**Code organization:** all server-side recovery logic must live in the
`syrinx/recovery` Go package. The main package only wires boot, routes, and
middleware. Shared payload builders stay in `syrinx/identity`. Operator CLI
lives in root [`ops.go`](../../ops.go) (`//go:build ops`; build with
`go build -tags ops -o bin/ops .`).

| #                                          | Title                                                 | Depends on |
|--------------------------------------------|-------------------------------------------------------|------------|
| [00](00_server_key_passphrase_keychain.md) | Server key passphrase (keychain + optional env var)   | —          |
| [01](01_key_bundle_export_ops_cli.md)      | Key bundle export (`ops` CLI)                         | 00         |
| [02](02_key_bundle_import_ops_cli.md)      | Key bundle import (`ops` CLI)                         | 00, 01     |
| [03](03_bookkeeping_and_gates.md)          | `RECOVERY_MODE` boot, bookkeeping, import gate        | 02         |
| [04](04_own_identity_claim.md)             | Own-identity claim (challenge + nested key chain)     | 03         |
| [05](05_peer_identity_report.md)           | Peer identity report-back (`POST /recovery/identity`) | 04         |
| [06](06_reeds_follows_complete.md)         | Reed holdings, batched follows, `/complete`           | 04         |
| [07](07_spa_recover_client.md)             | SPA recover path + sync ledger                        | 04–06      |

Prerequisite normal-operation work is under
[`../`](../README.md) (proposals 01–11).

---

## Status

**Spec complete; implementation proceeds via the numbered steps above.**
Normal-operation prerequisites (proposals 01–10 under `docs/proposals/`) are
in place. Notifications (proposal 11) remain deferred.

**Implemented** (normal operation — not part of this directory):

- **Identity countersignatures** at signup / profile update / key rotation.
- **Signed, server-countersigned, server-timestamped profile records.**
- **Signed revocations**, replicated to followers.
- **Reed `server` block** binding `reedID`, `authorID`, and the server signing-key
  **fingerprint**, with one canonical form shared by signer and verifier.
- **Random, server-scoped user IDs** (replacing the Sqids counter).
- Shared **`syrinx/identity`** payload builders.

**Deferred** (not required for the first recovery cut):

- **Per-user system notifications** for username-collision renames. Losers are
  renamed in place during recovery; there is no persisted notification for now
  (see Proposal 11).

## Code organization

**All server-side recovery logic lives in the `recovery/` Go package**
(`syrinx/recovery`). That includes key-bundle export/import helpers, nested
key-chain types, store helpers for `unclaimed_accounts` / `ongoing_recoveries`,
signature verification for claim and peer report-back, HTTP handlers, route
registration, and import-gate helpers (`IsOngoing`, path allowlist).

Bookkeeping **table DDL** lives in root `InitDB` (`db.go`) with the rest of
the schema (always created). The main package only **wires** recovery in:
`RECOVERY_MODE` env, passing it into `InitServer` (mint vs fatal on missing
self), mode-on unclaimed count log, `recovery.RegisterRoutes` when
`RECOVERY_MODE` is on, and mode-on middleware that calls `recovery.IsOngoing`.
Do not add recovery endpoints or recovery verification logic under
`handlers.go` or other root files.

Shared normal-operation helpers used by both live traffic and recovery (e.g.
canonical identity/reed payload builders) live in `identity/`, not in
`recovery/`. Operator CLI for the identity bundle lives in root `ops.go`
(`go build -tags ops -o bin/ops .`).

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
`syrinx/recovery` (see *Code organization*). Identity is restored **only** via
`ops import-identity` (calls full `InitDB`, prompts for **bundle password** and
resolves **server key passphrase** separately, populate `servers` /
`private_keys` / `public_keys`, remind operator to enable `RECOVERY_MODE`,
optional delete of the encrypted bundle file). Boot with `RECOVERY_MODE`
assumes that import already happened — it does **not** prompt for the bundle
password. Operator CLI: root `ops.go` (`export-identity`, `import-identity`,
`rotate-passphrase`).

### Key bundle (export / import format)

The server identity is the **only** state that is not self-healing: it cannot be
reconstructed from users, so it must be exported and backed up **proactively,
while the server is healthy** — after the DB is gone it is too late. Losing the
bundle means every countersignature becomes unverifiable and recovery is
impossible.

**Export** (`ops export-identity`): build the identity JSON and **encrypt the
whole file** with a password the operator enters at the prompt (and confirms).
That password is **never stored** — not in the file, not in the DB, not in env
files checked into the repo.

**Import** (`ops import-identity <file>`): the **only** path that decrypts a
bundle. Run **before** first server boot on an empty Postgres. Calls full
`InitDB`, prompts for the bundle password, resolves the server key passphrase
(not in the bundle), validates and populates identity, reminds the operator to
start with **`RECOVERY_MODE`**, and may optionally delete the encrypted file
(default no). Non-interactive use may read passwords from a TTY-less stdin only
where explicitly supported later; do not put secrets in env or committed config.

On-disk artifact: an OpenPGP **symmetrically encrypted**, ASCII-armored
message whose plaintext is the JSON below. Default export filename:

`syrinx-<serverID>-<timestamp>.json.gpg`

where `<timestamp>` is the same instant as `exportedAt`, formatted for
filenames as UTC `YYYYMMDDTHHMMSSZ` (e.g.
`syrinx-Ab3xY9pQ-20260717T150405Z.json.gpg`). The operator may pass another
path on export or import.

Plaintext JSON (only after decrypt):

```json
{
  "version": 1,
  "exportedAt": "2026-07-17T15:04:05Z",
  "serverID": "Ab3xY9pQ",
  "serverName": "syrinx.example",
  "signingKeyFingerprint": "<hex fingerprint of the currently-active key>",
  "keys": [
    {
      "fingerprint": "<hex>",
      "privateKeyArmor": "-----BEGIN PGP PRIVATE KEY BLOCK----- ... (still encrypted with the server key passphrase)",
      "publicKeyArmor": "-----BEGIN PGP PUBLIC KEY BLOCK----- ...",
      "createdAt": "2026-01-02T03:04:05Z",
      "revokedAt": null,
      "revokeReason": null
    }
  ]
}
```

Two independent secrets (do not conflate):

| Secret | Role |
|--------|------|
| **Bundle password** | Encrypts the exported file at rest. Prompted on `export-identity` and `import-identity` only. Never persisted by Syrinx. Empty rejected; weak passwords (under 16 characters, or missing upper/lower/digit/symbol) are accepted with a stderr warning. |
| **Server key passphrase** | Unwraps each `privateKeyArmor` inside the JSON (same as live server boot). Resolved via env (HA) **or** OS keychain (prompt + store when env unset/empty; empty prompt auto-generates a 24-char passphrase and prints it to stdout). See [00](00_server_key_passphrase_keychain.md). Not listed in `.env.example`. |

- `exportedAt` is the UTC time the plaintext bundle was built (RFC3339, second
  precision). Operator metadata; not used for trust on import.
- Private keys inside the JSON are exported **still encrypted** with the
  server key passphrase (verbatim from `private_keys.armor`); export never
  decrypts them.
- The bundle carries the full key history (active + rotated/revoked) so every
  historical countersignature verifies and `reeds.private_key_fingerprint` FKs
  resolve.
- `serverID` and `serverName` are restored verbatim from the bundle. Countersignatures
  bind `serverID` only; the name is operator-facing identity restored so the
  self `servers` row matches the pre-wipe instance. After import, a different
  `SERVER_NAME` at boot may still rename the row (existing `InitServer` behavior).
- **Delivery**: keep the encrypted file offline; pass its path to
  `ops import-identity`, which prompts for the bundle password.

**After decrypting the file**, restoring keys still needs the same server key
passphrase that wrapped `private_keys.armor`. Keep these distinct:

- **Signing-key rotation** — minting a *new* server keypair (via `.rvk`
  revocation + `InitServerKey`). Produces additional entries in the key history;
  the old key stays in the bundle so its past countersignatures keep verifying.
- **Server key passphrase rotation** — re-wrapping the *same* keys under a new
  passphrase (`ops rotate-passphrase`, which updates the keychain). Afterwards
  re-export the identity bundle (you will be prompted for a **bundle** password
  again — may be new or the same).
- **Bundle password change** — re-export (or a dedicated re-encrypt command) and
  choose a new password at the prompt; old encrypted files need the old password.

**Backup freshness (DB + startup warn).** After a successful `export-identity`,
the server records `exportedAt` on the self `servers` row as
`identity_backup_at`. That column is **operator metadata only** (not a trust
input on import). On every normal startup (identity already present), if
`identity_backup_at` is NULL **or** it is strictly older than the newest
`private_keys.created_at`, log a **non-fatal** warning that the identity
bundle is missing or stale and the operator should run `ops export-identity`.
`ops import-identity` sets `identity_backup_at` from the bundle’s `exportedAt`
so a just-restored instance does not spuriously warn until the next key
rotation. Passphrase-only re-wrap does not change key `created_at`; re-export
after `rotate-passphrase` remains an operator checklist item (this check does
not detect it).

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

### Prerequisites (status)

**Done** — shipped in normal operation (proposals 01–10):

- Identity countersignatures at signup / profile update / key rotation.
- Signed, server-countersigned, server-timestamped profile records.
- Signed, replicated revocations.
- Reed countersignature with bound `reedID` / `authorID` / server key
  `fingerprint` and one canonical signed form.
- Random, server-scoped user IDs.

**Remaining** — land numbered step [07](07_spa_recover_client.md)
in this directory:

- ~~**Server key passphrase** via OS keychain / optional HA env~~ **Done**
  ([00](00_server_key_passphrase_keychain.md), `syrinx/secret`).
- ~~Operator key-bundle **export** (`ops export-identity`), `identity_backup_at`,
  stale-backup startup warning, `rotate-passphrase`~~ **Done**
  ([01](01_key_bundle_export_ops_cli.md)).
- ~~Operator key-bundle **import** (`ops import-identity`)~~ **Done**
  ([02](02_key_bundle_import_ops_cli.md)).
- ~~**`RECOVERY_MODE`** boot that requires a prior `ops import-identity`, plus
  bookkeeping / import gate / `recoveryMode` on `/server/info`~~ **Done**
  ([03](03_bookkeeping_and_gates.md)).
- ~~Own-identity **claim** (challenge + nested key chain)~~ **Done**
  ([04](04_own_identity_claim.md)).
- ~~Authenticated peer **`POST /api/recovery/identity`**~~ **Done**
  ([05](05_peer_identity_report.md)).
- ~~Reed/follow/`complete` endpoints + `pending_follows`~~ **Done**
  ([06](06_reeds_follows_complete.md)).
- SPA recover path + sync ledger ([07](07_spa_recover_client.md)).

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
| `user_following`       | user's own follow list                    | normal signature-auth (no per-edge signature); missing targets held in `pending_follows` until the target is restored |

Operator-restored (not from users): `servers` (preserved ID + name),
`private_keys` (**full history**), `public_keys` (derived).

Recovery-internal bookkeeping (not reconstructed from users):

- `unclaimed_accounts` — restored (peer-seeded) accounts whose owner has not
  yet claimed.
- `ongoing_recoveries` — claimants who have not finished their import ledger;
  while `RECOVERY_MODE` is on, these users are barred from non-recovery API use.
- `pending_follows` — follow edges whose target user is not yet in `users`;
  drained when that user is claimed or peer-reported.

Not reconstructed (ephemeral/realtime, repopulate on reconnect):
`online_users`, `broadcast_subscriptions`, `pending_events`,
`pending_reed_requests`, `profile_subscriptions`.

## Recovery flow

All recovery routes exist only while `RECOVERY_MODE` is on. When off, they are
not registered (no attack surface).

| Endpoint                            | Auth            | Cardinality      |
|-------------------------------------|-----------------|------------------|
| `GET /api/recovery/identity/claim`  | none            | —                |
| `POST /api/recovery/identity/claim` | challenge + sig | own identity     |
| `POST /api/recovery/identity`       | signature-auth  | **one** peer     |
| `POST /api/recovery/reeds`          | signature-auth  | **one** reed     |
| `POST /api/recovery/following`      | signature-auth  | **≤100** userIDs |
| `POST /api/recovery/complete`       | signature-auth  | —                |

**Normal use stays enabled during recovery** for anyone who has finished
claim/import (or never entered `ongoing_recoveries`). Live writes carry the
newest server timestamps and win newest-wins over restored records. Signups
stay enabled; username collisions use the same newest-`server_signed_at` rule.

### Nested key chain (claim and peer identity)

Identity submissions use a recursive nest (not a fingerprint map):

```json
{
  "profile": { "...User wire..." },
  "key": {
    "fingerprint": "...",
    "armor": "<active public key armor>",
    "userID": "...",
    "createdAt": "...",
    "server": { "...ServerSignature..." },
    "revocation": null,
    "predecessor": {
      "signature": "<old key's detached sig over the parent key's armor>",
      "fingerprint": "...",
      "armor": "<older public key armor>",
      "userID": "...",
      "createdAt": "...",
      "server": { "...ServerSignature..." },
      "revocation": { "...or null..." },
      "predecessor": null
    }
  }
}
```

- Outermost node is the **active** key (`key.armor`); nest walks back to the
  signup key (`predecessor: null`). Key material sits on the node itself
  (`armor`, `fingerprint`, …) — not under a nested `key` object.
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

1. Stand up a new instance (empty DB). On first interactive start (or via
   `ops`), provide the **same server key passphrase** that wrapped the keys in
   the bundle so it can be stored in that host’s keychain; set `SERVER_NAME` as
   desired.
2. Run **`ops import-identity <path-to-bundle.json.gpg>`** (before first server
   boot): call full **`InitDB`**, prompt for the **bundle password**, resolve the
   **server key passphrase** (env / keychain / prompt — not in the bundle),
   decrypt/validate, write the full `private_keys` / `public_keys` history,
   restore `serverID` and `serverName` verbatim, set the active signing key from
   `signingKeyFingerprint`, set `identity_backup_at` from `exportedAt`. Remind
   the operator to start with **`RECOVERY_MODE`**. Optionally prompt whether to
   delete the encrypted bundle file (default no). If a self identity already
   exists and **matches** the bundle, treat as success (idempotent). If it
   **does not match**, abort with no identity writes.
3. Start the server with **`RECOVERY_MODE`**. `InitServer(recoveryMode)` loads
   `servers WHERE self = TRUE`:
   - **Self identity present** (from step 2, or a previous interrupted recovery):
     **resume** — keep already-restored data (including any live writes), open
     recovery endpoints, log the current `unclaimed_accounts` count, install
     the import-gate middleware.
   - **No self identity**: **abort startup** with a clear message to run
     `ops import-identity` first.

Bookkeeping tables (`unclaimed_accounts`, `ongoing_recoveries`) are created by
`InitDB` on every boot. The unclaimed gauge and import gate run while
`RECOVERY_MODE` is on.

The client learns recovery is active from `/server/info` (`recoveryMode`) and
shows the recovery banner + "import your data" accordingly.

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

```json
{
  "reedID": "...",
  "authorID": "...",
  "userSignature": "<base64 armored>",
  "server": {
    "id": "<serverID>",
    "fingerprint": "<server signing key>",
    "timestamp": "...",
    "signature": "<base64 armored countersig>"
  }
}
```

1. Author must already exist.
2. Verify the server countersignature (binds `reedID` + `authorID` + server ts +
   `serverID`) against the restored key of `server.fingerprint`.
3. Upsert `reeds` metadata idempotently; insert
   `reed_allocations(reedID, reportingUserID)`. No reed content is stored.
   Quiet — no realtime broadcast. Re-POST of the same metadata succeeds;
   conflicting metadata for an existing `reedID` → **409** + error log.

### Phase 3 — Follows (authenticated)

**`POST /api/recovery/following`** — body `{ "userIDs": [ ... ] }`, **at most
100** IDs per request (reject larger bodies). Self-follow → **400**.

Caller asserts their own follow list. Existing targets get
`user_following` + `user_followers` (`ON CONFLICT DO NOTHING`). Targets not
yet in `users` go into `pending_follows` (no FK on the target ID). When that
user later appears via own claim or peer identity report, pending edges are
drained into the real follow tables in the same transaction. Live signup does
not drain (new random IDs never match). Rows left behind after
`RECOVERY_MODE` turns off are left for the operator.

### Ending recovery

There is **no global finalize** — the server cannot know whether every user has
reported in. Operator ends the window by turning `RECOVERY_MODE` off, which
unregisters recovery endpoints (including claim). Already-claimed users who
still have `ongoing_recoveries` are no longer API-gated by that table once mode
is off; they should have called `complete` while mode was on.

`unclaimed_accounts` is the admin “still unclaimed” gauge while
`RECOVERY_MODE` is on (startup log of `count(*)`). The table persists after
mode-off until the operator drops it.

> **Cutoff caveat.** A user whose data no one restored has no account at all —
> reappearing means a fresh signup with a *new* random ID. Once recovery is off
> there is no cooperative claim path.

> **Username collisions.** Resolved by newest `server_signed_at`; loser renamed
> with a permanent suffix. A live signup during recovery can keep a name against
> a later claim with an older `server_signed_at`.

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

Added with the recovery feature:

- **`servers.identity_backup_at`** (nullable timestamp on the self row): last
  successful identity-bundle export (`exportedAt`). NULL means never exported
  (or not yet recorded). Drives the non-fatal startup warning when missing or
  older than the newest server signing key. Set by `ops export-identity` and by
  `ops import-identity` from the bundle.
- **`unclaimed_accounts`**: one `user_id` per account **created by peer**
  `POST /api/recovery/identity` whose owner has not yet claimed. Deleted on
  successful own claim. DDL in `InitDB`. `count(*)` is the admin gauge logged
  at startup while `RECOVERY_MODE` is on. Not an auth gate. Persists after
  recovery ends until the operator drops it.
- **`ongoing_recoveries`**: one `user_id` per claimant who has not finished
  import. DDL in `InitDB`. Inserted on successful claim; deleted by
  `POST /api/recovery/complete`. While `RECOVERY_MODE` is on, presence of a row
  blocks that user from non-recovery API routes (middleware installed in that
  mode).
- **`pending_follows`**: follow edges reported during recovery whose target
  `user_id` is not yet in `users`. DDL in `InitDB`.
  `(follower_user_id → users, following_user_id TEXT, PK)`; no FK on the
  target. Drained into `user_following` / `user_followers` when the target is
  saved via claim or peer identity report. Not cleaned up when recovery mode
  ends.

Deferred:

- **`user_notifications`** (Proposal 11): per-user system-message store for
  collision renames and other operator/system messages. Not part of the first
  recovery cut.

## Resolved (decisions)

- **Code organization**: recovery logic in `syrinx/recovery`; bookkeeping DDL
  in `InitDB`; main wires `RECOVERY_MODE`, `InitServer(recoveryMode)`,
  mode-on routes/middleware. Shared payload builders in `syrinx/identity`;
  ops CLI in root `ops.go` (`go build -tags ops -o bin/ops .`).
- **Activation**: `RECOVERY_MODE` env flag. Identity must already be in the DB
  via `ops import-identity`; `InitServer` **resumes** if a self identity exists
  and **aborts** if not. Bundle password is prompted only by
  `ops export-identity` / `ops import-identity`. Server key passphrase comes
  from proposal 00 (env if set, else keychain / prompt).
- **Client detection**: `/server/info` reports `recoveryMode`.
- **Own claim**: `GET`/`POST /api/recovery/identity/claim` with a ≤60s challenge
  signed by the active key; body carries profile + **full nested** key chain.
  Creates a claimed user or claims a peer-seeded one (deletes
  `unclaimed_accounts`) and inserts `ongoing_recoveries`.
- **Peer identity**: authenticated `POST /api/recovery/identity`, **one user**
  per request, same nest shape; creates `unclaimed_accounts` when the row is
  new. No list endpoint (verification cost).
- **Key chains**: incomplete nests rejected; insert oldest→newest to preserve
  `user_keys.predecessor_fingerprint` FK integrity.
- **Reeds**: authenticated, **one reed** per request; nested `server` block;
  quiet restore; conflicting metadata → 409.
- **Follows**: authenticated batches of **at most 100** userIDs; missing
  targets → `pending_follows`; drain on claim / peer report (not signup).
- **Import progress**: client-owned; no `GET /api/recovery/status`; finish via
  `POST /complete`.
- **Import gate**: middleware registered when `RECOVERY_MODE` is on;
  `ongoing_recoveries` → 403 on non-recovery routes until
  `POST /api/recovery/complete`. Client mirrors the block in the UI.
- **Unclaimed gauge**: peer-seeded accounts awaiting owner claim; startup
  `count(*)` log while `RECOVERY_MODE` is on.
- **Username collisions**: newest `server_signed_at` wins; loser renamed with
  a permanent suffix.
- **Completion**: no global finalize; operator turns `RECOVERY_MODE` off;
  per-user import ends via `complete`.
- **Secrets**: bundle password (prompted by `ops` only, encrypts the export
  file, never stored) is independent of the server key passphrase (env for HA,
  else OS keychain / prompt; empty prompt auto-generates 24 chars to stdout;
  unwraps private armors). Do not list `SERVER_KEY_PASSPHRASE` in
  `.env.example`. Re-export after passphrase rotate; bundle password may be
  changed by re-exporting. After `import-identity`, offer to delete the bundle
  file.
- **Keychain tradeoff**: preferred for long-running single-host services; HA
  fleets inject `SERVER_KEY_PASSPHRASE` via the orchestrator instead.
- **Backup freshness**: `servers.identity_backup_at` updated on successful
  export and on `import-identity`; non-fatal startup warning if never set or
  older than newest `private_keys.created_at`.
- **Migration**: not applicable — pre-launch blank slate.
- **Abuse mitigation**: deferred.
- **Collision-rename notifications**: deferred (Proposal 11).

## Open questions

1. **Residual abuse mitigation** for the claim challenge endpoints
   (verification-cost DoS) — rate/size limits — deferred. Same class of
   weakness as every other public endpoint.
