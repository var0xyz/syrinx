# Account recovery 07 — Root user bootstrap (`id = "1`)

## Status

Implemented.

## Depends on

[01](01_key_export.md) (keys-only `.sxi.gpg`), [02](02_challenge_bootstrap.md),
[04](04_spa_keys_only_restore.md).

## Context

Closed / invite-only communities need a first operator account without opening
signup. When `ROOT_KEY_EXPORT_PASSPHRASE` is set, the server mints the reserved
root identity once: keys, countersigned profile, and public key on the server;
writes a keys-only `.sxi.gpg`; never persists the private key; exits. The
operator imports that file via `/import` like any other keys-only restore.

## Scope

- One-shot root mint when `ROOT_KEY_EXPORT_PASSPHRASE` is set (not
  `RECOVERY_MODE`): generate keys for `id = "1"`, countersign and persist
  profile + `user_keys`, write keys-only `.sxi.gpg`, discard private material,
  **exit**.
- Export matches [01](01_key_export.md) (keys only — no profile in file).
- Reserve `id = "1"` forever; signup rejects it (`GenerateUserID` never returns
  `"1"`).
- SPA: italic username on profile when `user.id === "1"`.

## Non-goals

- Reeds, follows, invites at mint.
- Staged private key or separate ops export command.
- Multi-root or transfer of `id = "1"`.
- TTY prompt when passphrase unset (env var required).

## Design

### Reserved identity

| Field | Value |
|-------|--------|
| `users.id` | `"1"` (reserved) |
| Default username | `root` (at mint) |
| At mint | Countersigned profile + public key on server; private key only in `.sxi.gpg` |

Signup rejects `userID == "1"`.

### Startup (`root.go`)

After `InitDB`, server identity, and signing key — **before** HTTP:

| `RECOVERY_MODE` | `ROOT_KEY_EXPORT_PASSPHRASE` | Root `"1"` exists | Outcome |
|-----------------|------------------------------|-------------------|---------|
| on | any | any | Skip mint; normal start |
| off | unset | any | Normal start |
| off | set | yes | **Fatal** — remove env var |
| off | set | no | Mint → write `.sxi.gpg` → **exit 0** |

### One-shot mint

1. `ROOT_KEY_EXPORT_PASSPHRASE` encrypts the export file; same string unlocks
   the armored private key in the file.
2. Generate OpenPGP key pair (`1@<serverID>`).
3. Sign identity payload (username `root`); countersign profile and public key;
   persist via normal signup path (`SignupInput`, open mode, no invite).
4. Build keys-only `BackupPayload` ([01](01_key_export.md)): `privateKeys`,
   `publicKeys`, identity `localStorage` subset.
5. Gzip + encrypt (SPA-compatible binary OpenPGP) →
   `syrinx-1-<timestamp>.sxi.gpg` in cwd, or under `ROOT_KEY_EXPORT_PATH`
   (directory only; filename fixed).
6. Discard private key from memory; never persist on server.
7. Log path; remind operator to unset env var and restart.

### Operator loop

1. Set `ROOT_KEY_EXPORT_PASSPHRASE`; start server once → `.sxi.gpg` on disk →
   exit.
2. Unset env var; restart → normal HTTP (root account already on server).
3. SPA `/import` → **I only have my keys** → `.sxi.gpg` → account recovery
   bootstrap + session ([04](04_spa_keys_only_restore.md)).
4. Operator updates profile / invites under closed or invite signup mode.

### Environment variables

| Variable | Meaning |
|----------|---------|
| `ROOT_KEY_EXPORT_PASSPHRASE` | Triggers one-shot mint; encrypts `.sxi.gpg`; unset after mint |
| `ROOT_KEY_EXPORT_PATH` | Optional output **directory** (filename is always `syrinx-1-<timestamp>.sxi.gpg`) |

### Trust / security notes

- Root private key never persists on server.
- Losing `.sxi.gpg` loses control of `id = "1"` — no second mint while root
  row exists.

## Test plan

- [x] Passphrase set + no root → `.sxi.gpg` on disk; profile + key on server;
      no private key persisted; exit 0 (`root.go`)
- [x] `/import` keys-only → normal account-recovery bootstrap for existing
      root profile
- [x] Env still set + root exists → fatal error
- [x] `RECOVERY_MODE` skips mint
- [x] Signup rejects `"1"`
- [x] Italic username when `user.id === "1"` (`UserProfileCard.svelte`)

## Done when

- Operators can bootstrap a closed server, import keys-only `.sxi.gpg`, and
  use the app without opening signup.
