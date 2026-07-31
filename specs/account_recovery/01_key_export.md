# Account recovery 01 — Key export format + profile Export key

## Status

Proposed.

## Depends on

[00](00_design.md)

## Context

Keys-only restore needs a small, intentional artifact — not a full `.sxb`
backup. Users export the **active** private key from the profile encryption
section and later feed that file into `/import`.

## Scope

- Define the **`.sxk.gpg`** key-export file format (plaintext JSON → gzip
  optional → OpenPGP symmetric encrypt, same crypto helpers as backups).
- Profile UI: **Export key** button directly **below** Revoke Key.
- Passphrase prompt at export (user-chosen; may differ from key unlock
  passphrase).
- Download / save-as with filename
  `syrinx-<userID>-<timestamp>.sxk.gpg`.
- Shared SPA helpers to build and later decrypt/parse the artifact (used
  by [04](04_spa_keys_only_restore.md)).
- Copy: this file is account control; prefer a full backup when possible.

## Non-goals

- `/import` keys-only branch (04).
- Server endpoints (02–03).
- Exporting revoked / historical private keys.
- Changing full-backup Export Data behavior.

## Design

### Plaintext JSON (after decrypt)

```json
{
  "version": 1,
  "exportedAt": "2026-07-31T21:00:00Z",
  "userID": "<user id>",
  "serverID": "<server id>",
  "fingerprint": "<active key fingerprint hex>",
  "privateKeyArmor": "-----BEGIN PGP PRIVATE KEY BLOCK-----\n..."
}
```

Rules:

- `version` must be `1` for this cut; unknown version → reject on import.
- `privateKeyArmor` is the **active** key only (still passphrase-protected
  as stored in IndexedDB — the file passphrase wraps the whole JSON).
- Two secrets: **file passphrase** (encrypts `.sxk.gpg`) and **key unlock
  passphrase** (unwraps `privateKeyArmor` for signing). Import must ask
  for both when they differ; if the user uses the same string for both,
  that is fine but not assumed.
- Do **not** put `deviceId`, following, reeds, or other IndexedDB tables
  in this file.

### Encryption

Mirror backup export: JSON bytes → (optional gzip) →
`cryptoService.encryptBackup` / equivalent symmetric OpenPGP encrypt with
the file passphrase. Extension **`.sxk.gpg`** so `/import` can distinguish
from `.sxb.gz.gpg` by filename (and by sniffing `version` + fields after
decrypt).

First cut: gzip the JSON the same way as backups for one code path, or
skip gzip if the payload is tiny — pick one in implementation and keep
decrypt tolerant if versioned later. Prefer **gzip + encrypt** for parity
with backup helpers.

### Profile UI

In the encryption-key card, when the key is **not** revoked:

1. Existing Revoke Key button.
2. New **Export key** button immediately below it (secondary styling).

Flow: click → passphrase modal (confirm passphrase) → build file →
download. On revoked / pending-revocation key: hide or disable Export key
(only the active key is exportable; after rotation the new active key is
what exports).

### Helpers

e.g. `spa/src/lib/services/keyExport.ts`:

- `buildKeyExportPayload(...)` → plaintext object
- `encryptKeyExport(payload, filePassphrase)` → `Uint8Array` / Blob
- `decryptKeyExportFile(file, filePassphrase)` → payload
- `assertKeyExportPayload(payload)` → validates fields + `serverID`
  match when used on import

## Test plan

- [ ] Export key appears below Revoke; hidden/disabled when key revoked
- [ ] Wrong file passphrase → decrypt fails; nothing written
- [ ] Decrypted payload has version, userID, serverID, fingerprint, armor
- [ ] Filename matches `syrinx-<userID>-<timestamp>.sxk.gpg`
- [ ] Full backup export path unchanged
