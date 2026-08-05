# Account recovery 07 — Root user bootstrap (`id = "1`)

## Status

Proposed.

## Depends on

[01](01_key_export.md) (keys-only `.sxi.gpg`), [02](02_challenge_bootstrap.md),
[04](04_spa_keys_only_restore.md).

## Context

Closed / invite-only communities need a first operator account without opening
signup. On a **fresh empty database**, the server generates the reserved root
key pair, writes a **keys-only** `.sxi.gpg`, and **never persists the private
key**. The operator imports that file; **bootstrap creates the profile** on the
server ([02](02_challenge_bootstrap.md)) — no countersigned profile at mint.

## Scope

- One-shot root bootstrap on empty DB (not `RECOVERY_MODE`): generate keys
  for `id = "1"`, register **public key only** on server, write keys-only
  `.sxi.gpg`, discard private material, **exit**.
- Export matches [01](01_key_export.md) (keys only — no profile in file).
- Reserve `id = "1"` forever; signup / `GenerateUserID` reject it.
- SPA: root affordance when `user.id === "1"`.

## Non-goals

- Countersigned profile at mint time.
- Reeds, follows, invites at mint.
- Staged private key or separate ops export command.
- Multi-root or transfer of `id = "1"`.

## Design

### Reserved identity

| Field | Value |
|-------|--------|
| `users.id` | `"1"` (reserved; row created at **first bootstrap**, not at mint) |
| Default username | `root` (set when profile is created on bootstrap) |
| At mint | Public key registered; **no profile row** |

`GenerateUserID` and signup reject `userID == "1"`.

### Startup decision table

After `InitDB` and server identity are ready, **before** HTTP:

| `RECOVERY_MODE` | Users | `ROOT_KEY_EXPORT_PASSPHRASE` | Outcome |
|-----------------|-------|------------------------------|---------|
| on | any | any | Skip root bootstrap |
| off | ≥ 1 | set | **Panic** — remove env var |
| off | 0 | unset, no TTY | **Panic** — empty DB needs passphrase |
| off | 0 | unset, TTY | Prompt → bootstrap → exit |
| off | 0 | set | Bootstrap → exit |
| off | ≥ 1 | unset | Normal server start |

Empty DB without passphrase is fatal.

### One-shot bootstrap (empty DB)

1. Resolve file passphrase (`ROOT_KEY_EXPORT_PASSPHRASE` or prompt).
2. Generate OpenPGP key pair (`1@<serverID>`).
3. Same string for private-key unlock passphrase when armoring (automation).
4. Persist **public key attestation only** for owner `"1"` — register active
   key on server so bootstrap can verify possession later. **Do not** insert
   a countersigned `users` profile row at mint.
5. Build keys-only `BackupPayload` ([01](01_key_export.md)): `privateKeys`,
   `publicKeys`, identity `localStorage` subset (`userId`, `keyFingerprint`,
   `keyPassphrase`, `serverId`, `serverName`).
6. Gzip + encrypt → `syrinx-1-<timestamp>.sxi.gpg` in cwd, or under
   `ROOT_KEY_EXPORT_PATH` when set (directory only; filename is fixed).
7. Discard private key from memory; never persist it.
8. Log path; remind operator to remove env var and restart.
9. **Exit 0** — no HTTP on this boot.

**Implementation note:** `user_keys.owner` references `users(id)`. Mint may
use a minimal stub row, defer FK until bootstrap, or extend schema — pick one
in implementation; the **invariant** is no countersigned profile until
bootstrap.

### First bootstrap creates profile

When the operator imports `.sxi.gpg` and `POST /account-recovery/bootstrap`
succeeds ([02](02_challenge_bootstrap.md)):

- Server verifies active key for `userID = "1"`.
- Server **creates** countersigned profile (default username `root`, empty
  bio/avatar) if none exists yet.
- Returns bootstrap payload (profile, following, tip id, reed catalog).

Subsequent bootstraps for user `1` return the existing profile.

### Environment variables

| Variable | Meaning |
|----------|---------|
| `ROOT_KEY_EXPORT_PASSPHRASE` | Encrypts `.sxi.gpg`; **unset after mint**; panic if set when users exist |
| `ROOT_KEY_EXPORT_PATH` | Optional output **directory** (filename is always `syrinx-1-<timestamp>.sxi.gpg`) |

### Admin recover loop

1. Empty DB + passphrase → one-shot mint → `.sxi.gpg` on disk → exit.
2. Remove env var; restart → server has root **public key** registered, no
   profile yet (or stub only).
3. SPA `/import` with `.sxi.gpg` → challenge → bootstrap → profile created
   on server + session installed ([04](04_spa_keys_only_restore.md)).
4. Operator updates profile / invites under closed or invite signup mode.

### Trust / security notes

- Root private key never persists on server.
- Profile trust is always from server bootstrap, never from the export file.
- Losing `.sxi.gpg` loses control of `id = "1"` — no second mint.

## Test plan

- [ ] Empty DB + passphrase → `.sxi.gpg` on disk; public key registered; no
      countersigned profile; no private key persisted; exit 0
- [ ] First bootstrap → profile created for `id = "1"`
- [ ] Second start with env still set + user/key present → panic
- [ ] `RECOVERY_MODE` skips root bootstrap
- [ ] Signup never accepts `"1"`

## Done when

- Operators can bootstrap a closed server, import keys-only `.sxi.gpg`, and
  receive profile + session from bootstrap without ever opening signup.
