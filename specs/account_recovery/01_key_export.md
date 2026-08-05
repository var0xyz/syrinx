# Account recovery 01 — Identity export (`.sxi.gpg`)

## Status

Implemented.

## Depends on

[00](00_design.md)

## Context

Account recovery consumes a **small identity backup**, not a dedicated
keys-only JSON format. The SPA already exports this from profile **Backup
Keys** using the same encrypted backup pipeline as full export, with a
smaller payload (`buildKeyBackupPayload` in `backupRestore.ts`).

## Scope

- **Identity backup** file extension **`.sxi.gpg`** (identity), distinct
  from full **`.sxb.gpg`** (backup) and operator server bundle
  **`.sxi.gpg`** (ops `export-identity` — different plaintext shape;
  distinguished after decrypt).
- Filename: `syrinx-<userID>-<timestamp>.sxi.gpg` (no `-keys-` segment).
- Payload: existing `BackupPayload` — active private key, public key, own
  countersigned profile, and identity `localStorage` subset (same as
  `buildKeyBackupPayload` today).
- Crypto: JSON → gzip → OpenPGP symmetric encrypt (same as full backup).
- Profile UI: existing **Backup Keys** control (unchanged placement).
- Save-as / download behavior that tolerates browsers mangling uncommon
  multi-dot extensions (see below).

## Non-goals

- A separate `.sxk` / keys-only JSON artifact.
- `/import` account-recovery fork ([04](04_spa_keys_only_restore.md)).
- Server endpoints ([02](02_challenge_bootstrap.md)–[03](03_rehydration_relay.md)).
- Changing full **Export Data** naming (now also `.sxb.gpg`; gzip remains internal).

## Design

### File naming

| Kind | Extension | Example |
|------|-----------|---------|
| Full backup | `.sxb.gpg` | `syrinx-<userID>-<timestamp>.sxb.gpg` |
| Identity backup | `.sxi.gpg` | `syrinx-<userID>-<timestamp>.sxi.gpg` |

Both use a **single** trailing extension; gzip is applied before encryption
internally.

### Browser save quirks

Some engines append spurious suffixes (`.com`, `.download`) when the declared
extension is not in their allowlist. Mitigations:

- Register **`application/octet-stream`** with accept **`['.gpg', '.sxi.gpg']`**
  (and `['.gpg', '.sxb.gpg']` for full export where applicable).
- On **import**, normalize the chosen filename before validation (strip
  trailing `.com` / `.download`).

### Payload (after decrypt + gzip decompress)

Same `BackupPayload` as full backup, minimal tables:

- `localStorage`: `userId`, `keyFingerprint`, `keyPassphrase`, `serverId`,
  `serverName`
- `indexedDB.tables`: `privateKeys`, `publicKeys`, `users` (own profile only)

### Profile UI

Existing **Backup Keys** button → passphrase modal →
`buildKeyBackupPayload()` → encrypt → save as `.sxi.gpg`.
Disabled when key is revoked or pending revocation.

Copy should stress this file carries **account identity + keys**; prefer
**Export Data** for a full backup when possible.

### Helpers

Implemented in `spa/src/lib/services/backupRestore.ts`:

- `buildKeyBackupPayload()` — identity payload
- `isIdentityBackupFilename()` / `normalizeExportFilename()` — import sniffing
- `encryptAndSaveBackup(..., kind)` — save picker extensions per kind
- `decryptBackupFile()` — shared decrypt path for full and identity files

## Test plan

- [x] Backup Keys produces `syrinx-<userID>-<timestamp>.sxi.gpg`
- [x] Wrong file passphrase → decrypt fails; nothing written
- [x] Decrypted payload has identity tables + localStorage subset
- [x] Full backup export uses `syrinx-<userID>-<timestamp>.sxb.gpg`
- [x] Import accepts normalized identity filename (including browser `.com` suffix)
- [x] Revoked / pending-revocation key → Backup Keys disabled
