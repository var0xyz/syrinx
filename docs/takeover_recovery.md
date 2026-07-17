# Server Recovery / DB Reconstruction

## Status

**Partially implemented.** Normal-operation prerequisites for trustless recovery
are in place (see proposals 01–10). The recovery feature itself — mode toggle,
boot reconciliation, key-bundle export/import, report-back, client sync, and
unclaimed-account bookkeeping — is **not implemented yet**.

**Implemented** (normal operation):

- **Identity countersignatures** at signup / profile update / key rotation.
- **Signed, server-countersigned, server-timestamped profile records.**
- **Signed revocations**, replicated to followers.
- **Reed `server` block** binding `reedID`, `authorID`, and the server signing-key
  **fingerprint**, with one canonical form shared by signer and verifier.
- **Random, server-scoped user IDs** (replacing the Sqids counter).

**Not implemented yet** (recovery feature):

- **`RECOVERY_MODE`**, boot-time key-bundle **import**, operator key-bundle
  **export**, and the unauthenticated identity **report-back** endpoint.
- **`unclaimed_accounts`** recovery-only table and the authenticated presence
  ("claim") call — gauge of restored-but-unclaimed accounts and basis for the
  incomplete-recovery startup warning.

**Deferred** (not required for the first recovery cut):

- **Per-user system notifications** for username-collision renames. Losers are
  renamed in place during recovery; there is no persisted notification for now
  (see Proposal 11).

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

The **one** unauthenticated step is the identity **report-back** — a user (or a
peer holding a cached copy) submits a countersigned profile + public key to put
that key back on record. It *cannot* be authenticated: the key is not on record
yet. It is verified purely by the author signature + server countersignature, so
it does not matter *who* uploads it. This is the sole recovery-specific DoS
surface — the same weakness every public endpoint already has (see Threat
model).

**Everything a user self-reports afterwards** — the reeds they *hold*, who they
*follow* — rides the **normal signature-auth middleware**, exactly as in normal
operation: their key is back on record, so they sign requests as usual. No
separate recovery session and no per-account auth gate — a request signed by the
user's key is proof enough, and an attacker who merely resubmitted the user's
(public) identity cannot sign as them.

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

### Required work (`services.go`) — not yet built

- `InitServerKey` currently only *generates* a keypair. Add a path to *import*
  operator-provided armored private keys (decryptable with
  `SERVER_KEY_PASSPHRASE`) and to restore the **entire** key history.
- `generateServerID` / `InitServer` currently always mint a fresh ID. Add a path
  to restore an operator-provided ID.

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
   - a server-authoritative **account creation date** (`accountCreatedAt`), set
     once at signup and carried unchanged through every later record — used for
     username sniper-detection (rule 3)
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

   **Username contention** across *different* `userID`s is a cross-row decision
   resolved **online, the instant a collision is detected** (no batch step),
   using the countersigned `accountCreatedAt` to rule out snipers:
   - A claimant whose account was created **during the recovery window**
     (`accountCreatedAt >= recoveryStartedAt`) loses to any claimant that
     **predates** the window. This is the sniper filter: a live signup/rename
     cannot displace a genuine pre-outage owner who simply hasn't been restored
     yet. (`accountCreatedAt` is server-set and countersigned, so it cannot be
     backdated.)
   - Among the surviving claimants (all pre-outage, e.g. a name freed by a rename
     then reused, where the rename record was never resubmitted), the newest
     server timestamp keeps the name.
   - Every loser is renamed with a permanent suffix (e.g. `alice#a1b2`, from
     their `userID`). They can change it later via the normal flow. A persisted
     system notification explaining the rename is **deferred** (Proposal 11) —
     recovery does not write one for now.

### Prerequisites (status)

**Done** — shipped in normal operation (proposals 01–10):

- Identity countersignatures at signup / profile update / key rotation.
- Signed, server-countersigned, server-timestamped profile records.
- Signed, replicated revocations.
- Reed countersignature with bound `reedID` / `authorID` / server key
  `fingerprint` and one canonical signed form.
- Random, server-scoped user IDs.

**Remaining** — recovery feature:

- **`RECOVERY_MODE`** and boot reconciliation against `RECOVERY_KEY_BUNDLE`.
- Operator key-bundle **export** / matching boot-time **import** of the full
  server signing-key history and `serverID` (see *Required work* below).
- Unauthenticated identity **report-back** endpoint.
- **`unclaimed_accounts`** table + authenticated presence ("claim") call.

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
`unclaimed_accounts` — the gauge of restored accounts whose owner has not yet
proven presence.

Not reconstructed (ephemeral/realtime, repopulate on reconnect):
`online_users`, `broadcast_subscriptions`, `pending_events`,
`pending_reed_requests`, `profile_subscriptions`.

## Recovery flow

Recovery adds exactly one endpoint gated behind an env toggle (`RECOVERY_MODE`):
the unauthenticated identity **report-back** (Phase 1). When off, it does not
exist and there is no associated attack surface. It has to bypass the normal
signature-auth middleware because the user's key is not on record yet — that is
*why* it is the sole unauthenticated recovery step. Everything a restored user
re-reports afterwards (holdings, follows) goes through the **normal
signature-auth middleware** unchanged.

**Normal writes stay enabled during recovery** (signup, reeds, profile updates,
etc.), served with the restored active signing key. Live writes naturally carry
the newest server timestamps, so they win newest-wins arbitration over restored
(older) records — correct for genuine live activity. The one hazard this creates,
*username sniping*, is neutralized by the `accountCreatedAt` sniper filter (rule
3, see the caveat under *Ending recovery*). An operator flag to **freeze** writes
during recovery remains available but is not required for username safety.

### Client responsibilities

Recovery is **client-driven**: the server exposes the steps, the client
orchestrates them and owns progress.

- **Sync ledger.** The client tracks everything it still needs to push
  (identity, then each held reed, then each follow edge) and moves an item into
  its own IndexedDB only after the server confirms the write. This drives a
  progress indicator and a "restoration complete" notification.
- **App is blocked while restoring.** Normal use is gated client-side until the
  ledger drains — a half-synced client should not be interacting with the
  network.
- **A live account forfeits recovery.** If the device already has a logged-in
  account (a fresh signup, or a completed prior recovery), the client does **not**
  offer recovery there — you cannot both start over and reclaim the old identity
  on the same device.

### Phase 0 — Operator redeploy

At boot with `RECOVERY_MODE` on, the server reconciles the DB against the bundle:

- **No existing server identity** (no `self = TRUE` row in `servers`): fresh
  import. Initialize the schema, load the bundle from `RECOVERY_KEY_BUNDLE`
  (decrypting each private key with `SERVER_KEY_PASSPHRASE`), write the full
  `private_keys` / `public_keys` history, restore `serverID` verbatim, set the
  active signing key from `signingKeyFingerprint`, and record
  `recoveryStartedAt = now` (used by the username sniper filter).
- **Existing identity that matches the bundle** (same `serverID` and key set): a
  previous recovery was interrupted — **resume**. Keep the already-restored data
  (including any live writes) and re-open the report-back endpoint; do not reset
  `recoveryStartedAt`.
- **Existing identity that does *not* match the bundle**: **abort startup.** This
  prevents recovery from clobbering an unrelated/live instance.

Because the identity import creates the `self` row, the entry condition is
"no identity **or** matching identity" — this is what makes recovery restartable
after a crash. Normal endpoints are open too (live writes allowed); the client
learns recovery is active from `/server/info` (which also reports whether signups
are enabled) and renders the recovery banner + "import your data" / signup
buttons accordingly.

### Phase 1 — Identity restoration (report-back)

Recovery is **streaming, not batched**: each submission is validated and applied
immediately. Order matters only in that users are restored before reeds (a reed's
`user_id` FK requires the author to exist).

**Report-back (unauthenticated — any submitter, own or cached third-party
record).** For each identity record:

1. Verify the author signature and the server countersignature (server ts +
   `serverID` + `userID` + `fingerprint`) against the matching restored server
   key.
2. Upsert the user keyed by `userID` (accepted **verbatim** — recovery never
   mints a new ID, or every reed's author would change). Keep the newest record
   via an **atomic conditional upsert** on a stored server-timestamp scalar
   (`... WHERE excluded.server_signed_at > server_signed_at`) and apply
   revocation state (sticky). Only that timestamp is stored — it is server
   metadata already covered by the user-held countersignature — never the
   countersignature itself.
3. Resolve any **username collision on the spot** (rule 3): apply the sniper
   filter (`accountCreatedAt` vs `recoveryStartedAt`), then newest
   `server_signed_at`; rename the loser with a permanent suffix. The unique
   index therefore holds continuously — nothing is deferred. (No system
   notification is written; that is deferred — see Proposal 11.)
4. **If this report-back *created* the `users` row** (a first-time restoration,
   not an update to an already-restored account), insert `userID` into
   `unclaimed_accounts` (`ON CONFLICT DO NOTHING`) — "restored, owner not yet
   seen." Updates to existing accounts and live signups never touch the table, so
   a late cached copy of an already-claimed account cannot resurrect its row.
5. On `200`, the submitting owner's key is now on record, so the client copies
   the accepted state into its own IndexedDB and, from here on, authenticates
   with that key like any normal client — no separate session. (A *cacher*
   restoring someone else's identity simply gets the `200`; only the real owner,
   holding the private key, can go on to re-report that account's holdings and
   follows.)

**Claiming — the owner proves presence (gauge only, not an auth gate).** Once the
owner's key is on record, the client makes one **authenticated** request — an
explicit lightweight presence/`claim` call (a first holdings/follows report
counts too) — verified by the **normal signature-auth middleware**. On that first
authenticated request during recovery the server **deletes** the account's
`unclaimed_accounts` row (a no-op if absent — already claimed, or a live-signup
account that was never listed). A *cacher* can never trigger this: only the
private-key holder can produce a valid signed request. This drains the unclaimed
gauge and nothing else — it gates no functionality (see *Ending recovery*).

### Phase 2 — Reeds and holdings (authenticated)

A restored user, **authenticated by the normal signature-auth middleware**,
re-reports the reeds they hold. For each held reed the client submits
`{ reedID, authorID, userSignature, serverSignature, serverFingerprint }`:

1. The author must already be restored (Phase 1).
2. Verify the `serverSignature` — which binds this `reedID` + `authorID` + server
   ts + `serverID` — against the restored server key of `serverFingerprint`;
   require `server.id == serverID`.
3. Upsert the `reeds` metadata row (`signed_at` = countersignature timestamp)
   idempotently, and record the allocation
   `reed_allocations(reedID, reportingUserID)`. **The server stores no reed
   content**; content authenticity remains a peer-side check.

Because the request is signed by the reporter's key, the allocation is
trustworthy — this is why allocations are not reconstructed from anonymous
transport. The `reeds` row is created as a side effect (its author comes from
the countersignature, not from who reported it), so a reed is restored by
whoever holds it. The client moves each reed into its IndexedDB only after the
server confirms the write.

### Phase 3 — Follows (authenticated, unsigned)

A restored user, **authenticated by the normal signature-auth middleware**,
reports the list of users **they themselves follow** to populate `user_following`
(and the inverse `user_followers`). No per-edge signature is needed — the request
is already signed by the user's key, and a user only ever asserts their own
follow list. Inserts use `ON CONFLICT DO NOTHING`; edges toward not-yet-restored
users are skipped, and the client advances its sync progress as each edge is
confirmed.

### Ending recovery

There is **no finalize step and no automatic completion signal** — the server
cannot know whether every user has reported in (data on a lost device never
arrives). Everything that used to be "finalize" happens continuously: usernames
are resolved on collision (rule 3, Phase 1). The one piece of recovery
bookkeeping is the `unclaimed_accounts` gauge (Phase 1) — a recovery-only count
of restored accounts whose owner has not yet proven presence. It is **not** a
`users` column and **not** an auth gate; trust is still decided per request by
cryptographic validity, not by any flag.

Ending recovery is a single **operator action**: turn `RECOVERY_MODE` off. That
closes the only recovery-specific endpoint — the unauthenticated report-back —
and with it the server's cooperation in putting an as-yet-unrestored key back on
record. Holdings/follows re-reporting keeps working for anyone already restored
(it is just normal authenticated traffic); what a not-yet-recovered user loses is
the ability to get their key back on record at all.

Trust comes from cryptographic validity, never from having "recovered": a user
who still holds valid, server-signed data is legitimate and may use the network
freely — the system does not fight that. The practical effect of not recovering
before the cutoff is only that, without report-back, the user cannot get the
server to re-anchor their identity, and the server (holding no reed content, only
server-signed metadata) has nothing unsigned to leak.

`SELECT count(*) FROM unclaimed_accounts` is the admin "still unclaimed" gauge (it
counts only *restored* accounts awaiting their owner — users no one restored at
all are invisible to it, so it is a floor on what is missing, never a completeness
proof). The table **persists across the `RECOVERY_MODE`-off boundary** (so the
signal survives restarts) until the operator drops it, accepting the loss. If
`RECOVERY_MODE` is off **and** `unclaimed_accounts` is non-empty, the server
**warns at startup** (non-fatal, does not interrupt boot) with the residual count,
so the operator notices an incomplete recovery.

> **Cutoff caveat.** A user whose data no one restored has no account at all —
> reappearing means a fresh signup with a *new* random ID, orphaning their old
> reeds (author FK) and invalidating their countersigned bindings. And once
> recovery is off there is no cooperative re-import path. Weigh this when choosing
> when to close.

> **Username sniping (resolved).** Live writes are enabled, so a live
> signup/rename could otherwise beat a not-yet-recovered owner on newest-wins. The
> **sniper filter** (rule 3) prevents this: an account created during the recovery
> window (`accountCreatedAt >= recoveryStartedAt`) can never displace a pre-outage
> owner — on collision the sniper is the one renamed. A sniper only keeps a name
> if its legitimate owner never recovers at all — in which case that owner has no
> restored account anyway. The residual is the accepted rollback limitation (a
> genuine stale record for a since-renamed name).

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
  revocation is resubmitted by someone**. Normal-op replication of revocations to
  followers (rule 2) makes this very likely — a revocation is then as available
  as any cached profile. But if *no* honest party holds the revocation, an
  attacker holding the old key can report-back the old identity, reinstate it,
  and then authenticate as the victim. Accepted for the intended high-trust,
  small-community deployments.

Inherent limitations (accept and document):

- **Rollback to a real prior state.** An attacker can withhold newer records and
  submit an older, genuinely-signed one. This also covers username squatting via
  a *genuine* stale record that reclaims a freed name. We prevent forgery, not
  selective omission.
- **Deletion is not provable.** A reed the author deleted is still validly
  signed; any holder can resubmit it.
- **Completeness cannot be forced.** Data held only on a lost device is
  unrecoverable.
- **Unauthenticated identity report-back during the window.** Report-back needs
  no prior key on record, so an attacker can force expensive signature
  verifications (DoS) and selectively omit. This is the same DoS surface every
  public endpoint has. Rate/size limiting is deferred (see Open questions).

## Schema changes (summary)

Already in the base schema (normal operation):

- **`users`**: `server_signed_at`, identity signature columns, random
  server-scoped `id`. Username uniqueness is **enforced continuously** —
  including during recovery — with collisions resolved by immediate rename
  (rule 3), so the unique index never has to be dropped. A username-collision
  loser is renamed in place (permanent suffix); there is **no reselection
  flag** and **no notification** for now (Proposal 11 deferred). **No `claimed`
  column** is added to `users`: the only recovery-time per-account bookkeeping
  lives in the separate `unclaimed_accounts` gauge table (below), which gates
  nothing — normal operation trusts cryptographic validity per request, not a
  flag.
- **`reeds`**: `private_key_fingerprint` references `private_keys`; `signed_at`
  = server countersignature timestamp.
- **`user_key_revocations`**: signed revocation statements in normal operation.
  At recovery they are verified then discarded — no signature is stored
  server-side.
- **Reed `server` block (client + wire)**: `fingerprint` (server signing key),
  `reedID`, and `author` in the countersigned payload; one canonical form and
  pinned base64 layering.
- **Removed**: the `user_count` table (Proposal 02).

Still to add with the recovery feature:

- **`servers`**: `recovery_started_at` (set when recovery is first entered;
  the boundary the sniper filter compares `accountCreatedAt` against).
- **`unclaimed_accounts`** (recovery-only; **created on entering recovery**,
  not part of the base schema): one `user_id` per account restored via
  report-back whose owner has not yet proven presence. A row is inserted when a
  report-back **creates** the `users` row and deleted on that account's first
  authenticated request during recovery. `count(*)` is the admin "still
  unclaimed" gauge and drives the non-fatal startup warning; normal operation
  never touches it and it is not an auth gate. Persists after recovery ends until
  the operator drops it.

Deferred:

- **`user_notifications`** (Proposal 11): per-user system-message store for
  collision renames and other operator/system messages. Not part of the first
  recovery cut.

## Resolved (decisions)

- **Activation**: `RECOVERY_MODE` env flag. Fresh import against a DB with no
  server identity; **resume** if the existing identity matches the bundle;
  **abort** on mismatch. Identity/keys supplied via the `RECOVERY_KEY_BUNDLE`
  mounted file + `SERVER_KEY_PASSPHRASE`.
- **Client detection**: `/server/info` reports recovery status (and whether
  signups are enabled); the UI shows a recovery banner + "import your data" and
  (if enabled) signup buttons.
- **Authentication**: only the identity **report-back** is unauthenticated
  (verified by signatures alone); once a key is restored, holdings/follows
  re-reporting uses the **normal signature-auth middleware**. No
  challenge-response session and no `claimed` flag — both were dropped as
  redundant.
- **Unclaimed gauge**: a recovery-only `unclaimed_accounts` table (not a `users`
  column, not an auth gate) counts restored accounts whose owner has not yet
  proven presence — a row is added when report-back creates the account and
  removed on the owner's first authenticated request. Feeds the admin count and
  revives the non-fatal startup warning when `RECOVERY_MODE` is off but rows
  remain.
- **Client-driven progress**: the client tracks everything it still needs to
  sync (identity → holdings → follows), shows progress, and notifies on success.
  It **blocks normal app use while a restoration is in progress**, and does
  **not** offer recovery on a device that already has a logged-in account
  (creating/using a new account forfeits recovery on that device).
- **Normal writes during recovery**: allowed (optional operator freeze flag).
- **Username sniping**: resolved by the `accountCreatedAt` sniper filter (rule 3)
  — accounts created during the window cannot displace pre-outage owners.
- **Completion**: no finalize step and no automatic criteria; recovery is
  streaming (collisions resolved online) and the operator ends it by turning
  `RECOVERY_MODE` off; completeness is inherently unknowable.
- **Passphrase**: the same `SERVER_KEY_PASSPHRASE` is required to import; a
  separate passphrase-rotation command re-wraps keys and re-exports the bundle.
- **Migration**: not applicable — pre-launch, blank-slate assumption (no
  legacy reed blocks or IDs exist).
- **Abuse mitigation**: deferred.
- **Collision-rename notifications**: deferred (Proposal 11). Recovery renames
  losers in place without writing a persisted message.

## Open questions

1. **Residual abuse mitigation** for the unauthenticated identity report-back
   (verification-cost DoS, selective omission) — rate/size limits — deferred.
   Same class of weakness as every other public endpoint.
