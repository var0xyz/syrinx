# Recovery, operational

A server rebuilt from its users' devices should get back to normal operation
without waiting for every user to report in, should accept every user who
comes back later, and should come back into its federation mesh with what
its users hold of their peers' content.

Builds on [`recovery/`](../recovery/README.md) (the rebuild itself) and
[`federation/`](../federation/README.md) (peering, relay legs, durable
delivery).

## Status

**In progress.**

| #  | Step                                                                  | Status      |
|----|-----------------------------------------------------------------------|-------------|
| 01 | Backup restore writes records as-is                                   | Implemented |
| 02 | Refuse to boot without a current identity backup                     | Implemented |
| 03 | `servers` / `server_mesh` / `server_self`; drop established handshakes | Implemented |
| 04 | Signed peer records in `/server/info`                                 | Implemented |
| 05 | Recovery always available; `RECOVERY_MODE=off\|lockdown\|operational`  | Implemented |
| 06 | Delivery: `503` suspends; receive-side fixes                          | Implemented |
| 07 | Recovery reports servers, then users, then reeds, local and foreign   | Proposed    |
| 08 | Username collision certificate                                        | Proposed    |
| 09 | SPA: import decides restore or recover; unclaimed profiles; `/logout` | In progress |
| 10 | Recovery metrics and dashboard                                        | Proposed    |
| 11 | Refuse removed accounts                                               | Implemented |
| 12 | Keys-only restore of a recorded account                               | Proposed    |
| 13 | Signed follows                                                        | Proposed    |

## What went wrong

A two-instance test (A and B peered, follows and reeds both ways, a reply
from B to A's reed, A's database wiped, identity imported, recovery mode
on) showed:

1. **The full-backup restore dropped the user's own profile**, so the claim
   failed and recovery could not start. The restore re-verified every record
   and stumbled on records the backup had compacted (`users.permissions`) and
   on anything countersigned by a peer, whose keys need a signed request and
   a peering A no longer had.
2. **A forgot its peers.** `servers` peer rows are not reconstructed, so A
   sent no boot notice and B kept A connected and marked down indefinitely.
   A could not even tell which servers it had been peered with.
3. **Peer content was refused by design.** `ReportReed` rejects a reed not
   authored here, so B's reed, B's reply to A's reed, the mention and holder
   rows, and blocks by B's users against A's users did not come back, though
   A's users held all of them.
4. **A follow of B's user stuck in `pending_follows`**, which only drains when
   a local user is claimed.
5. **Inbound delivery depends on local state.** Arriving while A hydrates, a
   mention of an unrecovered user is skipped with a `2xx` (lost); a reply to
   or echo of an unrecovered reed is a `500` that stalls the author's whole
   stream. The reply stall also happens in normal operation: `ReedExists`
   treats a removed reed as missing, contrary to
   [federation 09](../federation/09_reed_delivery.md)'s "upsert the parent".
6. **Signups stay blocked** for the whole window, which for a large community
   may never end.
7. **A user who comes back after the window is turned away**, even when
   their account was recorded from someone else's cache: the status probe
   answers `unknown` for an unclaimed account, and the claim only exists in
   recovery mode.
8. **A device that is still signed in gets no route to recovery**, and
   `/logout` called an `authService.logout()` that doesn't exist.

## Locked decisions

| Topic | Decision |
|---|---|
| Restoring a backup | The user's own encrypted export is trusted as-is. Only the server verifies signatures. |
| Recovery availability | Always on. `RECOVERY_MODE` only controls signups and peers. A user the server hasn't seen claimed reports back whenever they show up. |
| Peer records | One record per peer, countersigned by the server and re-signed whenever it changes. Clients keep the version they last saw and report it back; when reports disagree, the newest `signedAt` is kept, revoked or not: a connection can be deleted and re-established. |
| Handshake rows | `federation_invitation` and `federation_attempt` exist only while a handshake runs. Established: deleted, logs with them. Rejected or canceled invitations are kept. None of it is reconstructed. |
| Report order | Own claim, then servers, then users, then reeds, then follows and records. |
| Peer content in recovery | Verified against the peer's server key the client reports, checked against the signed peer record. A key the client doesn't hold, such as one the peer rotated away, is fetched from the peer and verified like any rotated key: a rotation doesn't invalidate earlier signatures. |
| Content nobody held | Not recovered. A reply on B that no user of A ever loaded never existed on A. |
| Pre-wipe history | Not replayed. What B delivered before the wipe comes back only from A's users' devices. |
| Peers in lockdown | Every inbound relay call answers `503`; the peer suspends delivery until A's boot notice. |
| Delivery mechanism | The existing cursor streams and per-leg queues. No outbox, no backoff. |
| Recovery boundary | `server_self.first_boot_at`: when this database was initialized, by a first boot or an identity import. It never changes. |
| Precedence | Older users always win. A user from before the wipe takes a username from anyone who signed up after `first_boot_at`, whenever they come back, however long the newer user has held it. |
| Username collisions | The loser keeps their identity; the username is nulled and a signed certificate forces a rename. A username collision never deletes an account. |
| User ID collisions | A claim landing on an ID a signup took after `first_boot_at` deletes the newer identity. |
| Removed accounts | Never come back through recovery: the probe and the claim refuse them. Another user's report restores only their keys. |
| Follows | Signed by the follower and countersigned. Both ends report them in recovery, newest wins. The server keeps only active follows and at most one undelivered certificate per pair. |
| Identity backups | One timestamp, the last export. Every bundle carries every key, so it covers them all. |
| `ops` passwords | Prompted on a TTY, read from stdin otherwise. Never an environment variable. |
| Migrations | None. `InitDB` changes; recreate the database. |

## 01 — Backup restore writes records as-is

`writeBackup` / `restoreItem` (`backupRestore.ts`) put every store with
`allowUnsigned`, except `privateKeys` (the repository wraps them) and user
tombstones. `storeReed`'s network work is skipped; `reedReplies` and `tags`
come back from their own tables. Identity-backup public keys go through the
same path.

`users` reads default `permissions` to `[]` (`STORE_DEFAULTS` in `db.ts`):
`put` compacts an empty list away and `verifyUser` and `admin.ts` spread or
query it.

## 02 — Refuse to boot without a current identity backup

The server identity is the one thing recovery cannot rebuild.

`server_self.identity_backup_at` (replacing `servers.identity_backup_at`)
is the last `ops export-identity`. Every bundle carries every signing key,
rotated ones included, so one timestamp covers them all; `ops
import-identity` restores it from the bundle.

Normal boot exits with an instruction to run `ops export-identity` unless
`identity_backup_at` is later than the newest private key's `created_at`,
so a rotation without a re-export also stops the server. The first boot
already mints root and exits; the operator exports before the real start.

`ops` reads the bundle password from stdin when stdin isn't a terminal, one
line, no confirmation:

```
echo "$PASSPHRASE" | syrinx-ops import-identity ./bundle.sxi.gpg
```

On a terminal it prompts as today. `federation-dev-up.sh` exports this way.

The deploy scripts build and install the `ops` binary too. On every
`setup` and `update`, after the root bootstrap, `ops check-identity-backup`
decides whether to export; when it does, the bundle and a generated
password go next to the root credentials, and `cp-root-creds.sh` fetches
both.

## 03 — `servers` / `server_mesh` / `server_self`

`servers` keeps what a server is:

| Column | Notes |
|---|---|
| `id` | primary key (today `name` is the primary key and `id` only unique) |
| `name` | |
| `self` | about fifty queries filter on it; not worth replacing with a join |
| `key_id` | the current signing key; replaces `signing_key` and the peer rows' `key_id` |
| `base_url`, `frontend_url` | |
| `created_at` | |

`server_self` holds what only applies to this server, in one row (a
constant primary key, `one BOOLEAN PRIMARY KEY DEFAULT TRUE CHECK (one)`):

| Column | Notes |
|---|---|
| `identity_backup_at` | step 02 |
| `first_boot_at` | set once, when the database is initialized: the first boot, or `ops import-identity` into an empty database (step 05) |

`server_mesh` holds what a peering is, one row per peer, from our approval
on: `server_id` (PK, references `servers`), `connected`,
`invited_by`/`invited_at` (when we sent the invitation),
`approved_by`/`approved_at`, `peer_approved_at`, `peer_key_ack`,
`revoked_at/by/reason`, `disconnect_requested_at/by`, `disconnect_reason`,
`down_at`, `purged_at`. `federation_server_log` hangs off it. A peer's
delivery streams start at `approved_at`.

Approving an attempt deletes it and, on the side that sent it, its
invitation, with their log lines; who invited and when moves to
`server_mesh`, so the peer page reads everything from the server record.
The peer's key lives only in `public_keys`. Rejected attempts and
canceled invitations are kept.

Purging a revoked peer saves its `servers` record and revocation, deletes
the `servers` row so every reference to it cascades as before (its users
and reeds included), and puts the record back with `purged_at`, so its
revoked record (step 04) stays published. `rejectKnownFederationPeer`
refuses a server with a pending attempt, or with a `server_mesh` row that
isn't both revoked and purged. A rejected attempt doesn't block a new
one, and a purged peer may reuse its key. A new connection replaces the
revoked record.

## 04 — Signed peer records in `/server/info`

`ServerInfo.federation` lists every peer, revoked and disconnected included,
each with a record this server countersigns:

```
serverID, peerID, name, keyID, baseURL, frontendURL,
status: connected | revoked | purged, signedAt
```

A peer's row is never deleted, so a purged peer keeps being listed: its
`purged` record is what certifies that nothing of it is held anymore.
A new record is signed whenever one of those fields changes: approval,
revocation, a purge, a peer key rotation, a URL or name change. `/server/info`
compares what the stored record covers (`server_mesh.record_fields`) with
the peer's current state and signs again only on a difference, so every
change is covered without a hook at each one; the replaced signature row is
deleted. Payload builder in `identity.go`, mirrored in `signing.ts`, through
`canonicalJSON`, with golden bytes in `peer_record_payload_test.go` and
`test:peer-record-payload`.

Next to the record go two unsigned fields: `established` (approved on both
sides), and the peer's server key armor, which the SPA keeps only when its
fingerprint is the signed `keyID`'s.

The SPA verifies each record before storing it in `federatedServers` and
keeps the newest per peer. It never deletes one: a rebuilt server lists
nothing until devices report back. Picking where a verification link opens
filters to `connected` and `established`.

A revoked record is never removed, purge included, until a new connection
with that peer replaces it. There is no record for "no peer", so this keeps
something to report: a device that saw the revocation reports it, and it
beats the older `connected` most devices hold.

When no device saw it, recovery restores the peer as connected. That is
harmless: the revocation was sent to the peer, which revoked back
(`disconnect-notify`), so it refuses A's boot notice and nothing flows. A's
admins see the peer refusing and revoke it again.

## 05 — Recovery always available; modes

The recovery endpoints are always registered. Whether a user restores or
recovers depends on what the server knows about them, not on a mode:

| Status probe | Means | Import does |
|---|---|---|
| `complete` | claimed, device key on record | restore, bind the device; nothing reported |
| `ongoing` | claimed, mid-import | resume the recovery run |
| `unclaimed` | recorded from someone's cache, never claimed | recovery run |
| `unknown` | no account | recovery run |

The import gate (`ongoing_recoveries`) works per user, as today, in every
mode. This undoes [`recovery/`](../recovery/README.md)'s "routes exist only
while `RECOVERY_MODE` is on": every recovery route requires a signature by
the user's key or data countersigned by this server, and step 11 keeps
removed accounts out.

`RECOVERY_MODE` takes `off` (the default when unset), `lockdown` or
`operational`, and only decides signups and peers:

| | `lockdown` | `operational` | `off` |
|---|---|---|---|
| Recovery | yes | yes | yes |
| Signups | blocked | blocked | per `SIGNUP_MODE` |
| Inbound `/api/federation/relay/*` | `503`, before peer auth | normal | normal |
| Boot notice to peers | no | yes | yes |

The `503` comes before peer authentication on purpose: A may not know the
calling peer yet, and a `401` is a `4xx`, which the peer's delivery treats as
permanent. Switching out of `lockdown` is a restart; on that boot the boot
notice goes to every connected peer in `server_mesh`, which by then holds
the peers clients reported (step 07).

Recovery compares records against `server_self.first_boot_at`, the moment
this database was initialized; for a rebuilt database, the moment it was
rebuilt from nothing. Only a new import into a wiped database moves it.

**Precedence never expires.** A user from before the wipe who comes back
any time later, with signups long open, wins a username collision against
anyone who signed up after `first_boot_at` (step 08), and on a user ID
collision the newer account is deleted. Operators choose when to open
signups (step 10 shows when claims stop); `docs/operators.md` says what
opening them early costs.

## 06 — Delivery

**`503` suspends.** A sender that gets a `503` marked
`X-Syrinx-Recovering: lockdown` from a peer sets that peer's `down_at` and
stops: events keep being written, nothing is sent. Any other `503` (an
overloaded or unreachable server) is an ordinary failure, retried as before.
The peer's boot notice clears `down_at` and delivers everything behind: reed
streams, block notices, key revocations and account removals. Account
removals were fire-and-forget; they now wait in `account_removal_peer_notices`
like block notices until the peer accepts them.

**Receive-side fixes:**

- `new-reed`: a reply to or echo of a local reed that is missing or removed
  upserts its `reed_identities` row instead of failing. No stream stalls on
  a parent.
- `acceptForeignBlock`: a verification failure is `400`, anything else
  `500`. Today every error is `400`.

## 07 — Recovery reports, in order

The runner reports, per client:

1. **Own claim** (unchanged, plus step 11's checks).
2. **Servers.** `POST /api/recovery/servers`, every peer record the device
   holds, with each peer's server key armor. A verifies each record with its
   own key by fingerprint, keeps the newest per peer, and creates the
   `servers` and `server_mesh` rows. The armor is accepted only when its
   fingerprint is the record's `keyID`.
3. **Users.** Peer identity reports accept users of a known, non-revoked
   peer, verified against that peer's server key.
4. **Reeds.** `ReportReed` accepts a reed of a known, non-revoked peer,
   verified the same way. Restores its `reed_identities`, the reporter's
   holding, and its reply, echo, mention and thread links.
5. **Follows.** Follow certificates from both ends (step 13). A target on a
   known peer gets its identity and the follow directly, never
   `pending_follows`.
6. **Records.** Adds `blockedBy`: a block by a peer's user against the
   reporting user.

A revoked peer's users and reeds are refused (`4xx`; the client skips them).
A record countersigned by a peer key A doesn't have is verified after
fetching that key from the peer, as for any rotated key. A is the one in
lockdown, so it can still call its peers.

## 08 — Username collision certificate

Collisions compare against `first_boot_at` (step 05):

- a record countersigned before `first_boot_at` beats one after it, no
  matter how long the newer user has held the name;
- among records from before it, the newest wins;
- a live signup cannot take a name already held, as today.

The loser keeps their identity and everything attached to it. Their
`users.username` becomes `NULL` (an account removal already nulls it, so
the column allows it) and the server signs a certificate:

```
username, winnerID, winnerSignedAt, loserID, loserSignedAt, signedAt
```

It is returned with the loser's profile and pushed to them. Their client
blocks every action until they sign a new profile with a new username
through the normal update flow, which peers receive like any profile update.

This replaces `claimRecoveryUsername`'s cascade `DELETE FROM identities` and
the claim's `409`, both of which lose a legitimate account (a stale cached
profile can win a name, then be deleted along with everything restored for
its owner).

A claim or peer report for a user ID that a signup took after
`first_boot_at`, with a different key, deletes the newer identity and
restores the reported one. IDs are 8 base62 characters, so this should be
rare.

Mention, search and display paths handle a missing username.

## 09 — SPA

- **Import decides.** The import page probes, then restores or runs
  recovery per step 05's table, in every mode. A signed-in device whose
  probe answers `unclaimed` or `unknown` goes straight to the recovery run
  with what is already on the device. No export and re-import.
- **Unclaimed profiles.** An unclaimed account's profile shows, with a
  notice that its owner hasn't come back to this server yet. Its follower
  and following counts show `0`, and there is no Follow button. The server
  says so in `/users/{id}/info` and refuses new follows of an unclaimed
  account. Follows reported before the claim are kept and show once it
  claims.
- **`/logout` is removed.** It called an `authService.logout()` that doesn't
  exist, so it cleared nothing, and nothing linked to it. Done.

## 10 — Recovery metrics

Operators open signups when claims stop coming in, so the telemetry has to
show when they stop.

- `syrinx.server_recovery` already counts every report by `stage`. The
  `claim` stage gains a `first` attribute: true when the claim takes the
  account out of `unclaimed_accounts`, false for a repeat. First claims
  flattening out for a few days is the signal.
- Gauges for recorded users, unclaimed accounts (`unclaimed_accounts`) and
  users mid-import (`ongoing_recoveries`). Today these are only logged at
  boot.
- A panel in `generate_syrinx_dashboard.py` for both, regenerated and
  committed with the JSON.

## 11 — Refuse removed accounts

With recovery always open, a removed account must not come back through it.
The claim doesn't check `account_removals` today. A removal keeps the
`users` row with the username and both signatures nulled, so a claim from
an old backup takes the no-signature branch, inserts the keys and starts an
import. The status probe and the claim refuse a user with an
`account_removals` row, kicks included, with `410` like
`BootstrapAccountRecovery` already does. Every other report is signed by
its reporter, and the auth middleware already refuses a removed reporter.

Another user's report of a removed account's profile is still accepted: on
the nulled row it only restores the account's keys, which verify what it
signed before it was removed, and never its profile or a claim.

## 12 — Keys-only restore of a recorded account

A user with only an identity export (`.sxi.gpg`: active key pair, no
profile, no data) restores through `BootstrapAccountRecovery` when the
server has their account, claimed or unclaimed, and the export's key is
the account's active key. It already refuses removed accounts.

It also takes the account out of `unclaimed_accounts`, so it counts as
claimed. From there it hydrates as a normal account: its followers, and its
own reeds from the users who reported holding them.

Not covered:

- **Who they followed** comes back only from the people they followed, as
  those report their followers' certificates (step 13). A followee who
  never reports back is lost; the user follows them again by hand.
- **An account nobody recorded.** There is no profile to restore, so the
  user asks an admin.
- **A key rotated after the account was recorded.** The export holds only
  the active key, without the rotation proof; the server answers "Key is
  not the active key for this account".

## 13 — Signed follows

A follow is a certificate, so either end can bring it back in recovery and
nobody can misstate who follows them.

**The certificate.** `followerID`, `followeeID`, `followerKeyID`, `follow`
(true to follow, false to unfollow), signed by the follower and
countersigned by the follower's home server with its timestamp. One payload
builder in `identity.go`, mirrored in `signing.ts`, through
`canonicalJSON`. It works the same across servers: a follow of a peer's
user is countersigned at home and delivered to the peer once.

**The follower** keeps every certificate they created, countersigned.

**The followee** keeps the latest follow certificate per follower, plus the
public key that signed it, locked: the user can't remove either, eviction
doesn't touch them, and a full export includes them. The key is the one
that signed the certificate, not the follower's current one: a later
rotation doesn't invalidate it. An unfollow, once verified, drops the
follow certificate and the lock, and is acknowledged; the followee doesn't
keep it. A block by the followee ends the follow the same way: the block
certificate already lives on both devices.

**The server** keeps only active follows, one certificate per edge, which
it serves when a followers list is opened. Third parties verify those and
don't store them. Unfollows are never stored beyond delivery: queued for
the followee, then dropped with the follow certificate and the edge once
the followee acknowledges. An offline followee gets their certificates in
order. The queue holds at most one undelivered certificate per pair: a
newer one replaces it. Each certificate states the whole state of the pair
at its time, so the newest is all the followee needs, and follow/unfollow
loops against an offline user can't grow the database.

**Recovery.** The follower sends the latest certificate per followee,
unfollows included, even for a followee who never saw the follow. The
followee sends the latest follow certificate per follower, with the
followers' keys in the users step before it (step 07). The newest
certificate per pair wins, so a follow the followee kept because the
unfollow never reached them loses to the follower's unfollow. Blocks come
back in the records step and end any follow they cover.

A follower restoring a backup older than an unfollow brings the follow
back. That misstates only their own choice, from their own backup; it
can't inflate anyone else's count.

**Device storage.** Each follower costs the followee a certificate and a
locked key, about 3 KB with RSA-2048 keys. Deferred until accounts get that
large.

Today's follows are unsigned: `user_following` / `user_followers` rows and
the client's `following` and `pendingFollows` stores carry no signature,
and `POST /api/recovery/following` takes a bare list of IDs. All of them
carry the certificate instead.

## Open questions

None.
