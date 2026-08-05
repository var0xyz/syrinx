# Account recovery 01 — Identity export (`.sxi.gpg`)

## Status

Implemented (keys-only payload; profile from bootstrap on import).

## Depends on

[00](00_design.md)

## Context

Account recovery consumes a **keys-only** identity export from profile
**Backup Keys**: same encrypted backup pipeline as full export, minimal
payload. The file must **not** include a countersigned profile — there is
no guarantee the export snapshot is current. The server returns the
authoritative profile on bootstrap ([02](02_challenge_bootstrap.md)).

## Scope

- **Identity backup** extension **`.sxi.gpg`**, distinct from full
  **`.sxb.gpg`** and operator server bundle **`.sxi.gpg`** (ops
  `export-identity` — different plaintext; distinguished after decrypt).
- Filename: `syrinx-<userID>-<timestamp>.sxi.gpg`.
- Payload: `BackupPayload` **keys only** — see below. **No `users` table.**
- Crypto: JSON → gzip → OpenPGP symmetric encrypt.
- Profile UI: existing **Backup Keys** control (unchanged placement).
- Save-as behavior per browser quirks (single `.gpg` suffix; normalize on
  import).

## Non-goals

- Profile, following, reeds, or other server-held state in the file.
- `/import` account-recovery fork ([04](04_spa_keys_only_restore.md)).
- Server endpoints ([02](02_challenge_bootstrap.md)–[03](03_rehydration_relay.md)).
- Changing full **Export Data** (`.sxb.gpg` — still includes full local snapshot).

## Design

### File naming

| Kind | Extension | Example |
|------|-----------|---------|
| Full backup | `.sxb.gpg` | `syrinx-<userID>-<timestamp>.sxb.gpg` |
| Identity backup | `.sxi.gpg` | `syrinx-<userID>-<timestamp>.sxi.gpg` |

Gzip is internal; extension is a single `.gpg` suffix.

### Payload (after decrypt + gzip decompress)

Minimal `BackupPayload`:

- `localStorage`: `userId`, `keyFingerprint`, `keyPassphrase`, `serverId`,
  `serverName` (session markers needed before authenticated fetch).
- `indexedDB.tables`:
  - `privateKeys` — active private key only
  - `publicKeys` — matching active public key only
- **Must not** include `users`, `reeds`, `following`, or other tables.

Rationale: profile, following, and tip metadata are **server-authoritative**
at bootstrap time. A stale profile in the file would be wrong after renames,
key rotation, or edits on another device.

### Profile UI

**Backup Keys** → passphrase modal → build keys-only payload → encrypt →
`.sxi.gpg`. Disabled when key revoked or pending revocation.

Copy: this file is **account control** (keys); prefer **Export Data** for a
full local snapshot including profile and content.

### Helpers

`spa/src/lib/services/backupRestore.ts`:

- `buildKeyBackupPayload()` — keys-only payload (no profile)
- `assertIdentityBackupKeys()` — validates keys + localStorage; does **not**
  require profile in file
- `isIdentityBackupFilename()` / `normalizeExportFilename()`
- `encryptAndSaveBackup(..., kind)` / `decryptBackupFile()`

Full backup helpers (`extractProfile`, `assertBackupIdentity`) remain for
`.sxb.gpg` only.

## Test plan

- [x] Backup Keys produces `syrinx-<userID>-<timestamp>.sxi.gpg`
- [x] Decrypted identity payload has `privateKeys` + `publicKeys` only (no `users`)
- [x] Wrong file passphrase → decrypt fails
- [x] Full backup (`.sxb.gpg`) unchanged
- [x] Revoked / pending-revocation → Backup Keys disabled
