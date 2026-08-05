# Account recovery 04 — SPA keys-only restore

## Status

Proposed.

## Depends on

[01](01_key_export.md), [02](02_challenge_bootstrap.md)

## Context

`/import` today accepts full backups. Account recovery adds an **identity**
branch on the same route: decrypt `.sxi.gpg` (or fork after decrypt on the
existing file picker), prove possession via bootstrap, install a minimal
session, then hand off to background rehydration ([05](05_spa_rehydration_publish.md)).

## Scope

- Extend `/import` UI: toggle or secondary path **“I only have my keys”**
  vs backup file (do not ask the user to name “account recovery”).
- Accept `.sxi.gpg` identity backup ([01](01_key_export.md)) — keys-only
  `BackupPayload`; same decrypt path, **no profile in file**.
- Validate `serverID` matches this instance; fingerprint is active-looking
  locally.
- `GET /api/account-recovery/challenge` → sign →
  `POST /api/account-recovery/bootstrap`.
- On success: write private key, passphrase, `userId`, fingerprint,
  profile, following rows, **server tip id** for publish; seed
  `reedRequests` from bootstrap catalog and start paced drainer ([03](03_rehydration_relay.md));
  do **not** write a fake full backup.
- Warn that continuing **logs out other devices** (copy now; bind in
  [06](06_device_takeover.md)).
- On 404 unknown account: write nothing; tell user they need a full
  backup / server recovery.
- Resume: if `reedRequests` rows remain with identity present, drainer
  continues on reconnect rather than forcing re-upload.

## Non-goals

- Background relay consumption UI details beyond starting the run (05).
- Server recovery handoff from keys-only (out of scope per 00).
- Changing the backup branch of `/import`.

## Design

### Entry UX

Same page as import. Two modes (tabs or expandable section):

1. **Backup** — existing `.sxb.gpg` flow.
2. **Identity** — `.sxi.gpg` from Backup Keys; file passphrase → decrypt →
   account-recovery bootstrap ([02](02_challenge_bootstrap.md)).

Device-takeover confirmation before calling bootstrap (mirror import’s
future confirm-on-import from recovery 17).

### Client sequence

```text
decrypt .sxi.gpg
→ assertIdentityBackupKeys (userId, fingerprint, key armor — no profile)
→ GET challenge → sign → POST bootstrap
→ on 200:
     put privateKeys / localStorage session
     put profile from bootstrap response (verify-before-store)
     recordLocalFollow …
```

### Local markers

| Marker | Meaning |
|--------|---------|
| `accountRecoveryRun` started | Keys-only restore / rehydration in progress |
| `accountRecoveryRun` completed | User finished or dismissed rehydration (05) |

Do **not** conflate with `importRun` / `recoveryRun` (server recovery).
Backup import leaves those as today. Keys-only sets `accountRecoveryRun`
only.

`isLoggedIn`: treat keys-only like a finished import once bootstrap
succeeded and private key is present — mid-rehydration is **not** a
block on the app shell (unlike server-recovery import gate). Compose
requires tip id (or genesis) per 05.

### Tip id storage

Persist bootstrap `tipReedID` in localStorage (e.g. `publishTipReedID`)
or a tiny IndexedDB meta record until the user successfully creates a
new reed; then clear and use normal “newest local countersigned reed.”

### Failure copy

- Unknown account → need full backup; if the community is rebuilding,
  use backup under recovery mode.
- Revoked / bad key → cannot recover with this key; rotate from a device
  that still works or restore a backup taken while the key was active.

## Test plan

- [ ] Keys mode decrypt + bootstrap 200 → session + following + tip id
- [ ] Wrong serverID in file → abort before challenge
- [ ] 404 bootstrap → no local writes
- [ ] Backup mode unchanged
- [ ] Resume with accountRecoveryRun + keys present → app, not empty import
- [ ] Device-takeover warning shown before bootstrap
