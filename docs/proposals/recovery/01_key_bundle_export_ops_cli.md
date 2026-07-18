# Recovery 01 — Key bundle export (`ops` CLI)

## Status

Implemented.

## Depends on

[00](00_server_key_passphrase_keychain.md)

## Context

The server identity (ID + full signing-key history) is the only state that
cannot be reconstructed from users. Operators must export it **while the
server is healthy**. The **bundle password** is prompted by this CLI — never by
the long-running server process. The **server key passphrase** comes from the
OS keychain helper (proposal 00), or `SERVER_KEY_PASSPHRASE` when set for HA.
See [README](README.md) *Key bundle* and *Code organization*.

## Scope

- Add `syrinx/recovery` package with bundle types, `ExportFromDB`,
  `ValidateShape`, `ValidateDecrypt` (server-key armors), and
  **symmetric encrypt/decrypt** of the JSON file (OpenPGP armored message).
- Schema: nullable `servers.identity_backup_at` on the self row (see
  [README](README.md) *Backup freshness*).
- Add `cmd/ops` with:
  - `export-identity [outfile]` — build JSON, **prompt for bundle password**
    (and confirmation), write encrypted file, then set
    `identity_backup_at` to that export’s `exportedAt`. Default outfile name:
    `syrinx-<serverID>-<timestamp>.json.gpg` with `<timestamp>` =
    `YYYYMMDDTHHMMSSZ` from `exportedAt`. Optional `[outfile]` overrides the
    path. Failed encrypt/write must **not** update the timestamp.
  - `rotate-passphrase` — re-wrap `private_keys` under a new server key
    passphrase (prompt; update keychain per proposal 00); remind operator to
    re-export (bundle password prompted again at export).
- Startup helper (e.g. `WarnStaleIdentityBackup`): if a self identity exists and
  `identity_backup_at` is NULL or `< MAX(private_keys.created_at)`, log a
  **non-fatal** warning. Wire from `main` on normal boot.
- Makefile targets (`ops`, `export-identity`); build the binary to `bin/ops`
  and ignore `bin/`; document export in `.env.example` / ops help.

## Non-goals

- No `import-identity` (step [02](02_key_bundle_import_ops_cli.md)).
- No `RECOVERY_MODE`, no HTTP routes, no recovery bookkeeping tables.
- Do **not** store the bundle password anywhere (file, DB, `.env`, bundle
  contents). `identity_backup_at` is not a secret.

## Design

Plaintext JSON shape as in [README](README.md) (`version`, `exportedAt`,
`serverID`, `serverName`, `signingKeyFingerprint`, `keys[]`). Private armors remain
wrapped with the server key passphrase (env or keychain); export never decrypts
them.

**At-rest protection:** the file on disk is only the OpenPGP symmetrically
encrypted ciphertext of that JSON. Export uses a hidden prompt
(`term.ReadPassword` or equivalent) twice; mismatch aborts with no write.

**Bundle password vs server key passphrase:** independent. Losing the bundle
password makes the backup unreadable even if the keychain passphrase is known;
knowing the bundle password without the server key passphrase still cannot
unwrap the private keys inside.

**Freshness:** compare `identity_backup_at` to the newest signing key’s
`created_at` (covers rotate-away + new key). Does not detect passphrase-only
re-wrap; the rotate command’s reminder covers that.

All new code under `recovery/` and `cmd/ops/` only (plus the one column /
startup call from `main`). Prefer a small encrypt/decrypt helper for arbitrary
bytes so import (step 02) can reuse it.

## Test plan

- [ ] Default export path matches `syrinx-<serverID>-<YYYYMMDDTHHMMSSZ>.json.gpg`
- [ ] Export produces armored ciphertext, not plaintext JSON
- [ ] Decrypt with correct password yields valid bundle JSON
- [ ] Wrong password fails; no partial plaintext leak in errors
- [ ] `ValidateDecrypt` (server keys) still fails on wrong server key passphrase
- [ ] Password is not written to the outfile or process env by the tool
- [ ] Successful export sets `servers.identity_backup_at`; failed export leaves it unchanged
- [ ] Startup with NULL `identity_backup_at` logs a non-fatal warning
- [ ] Startup after a newer `private_keys.created_at` than `identity_backup_at` warns
- [ ] Startup with backup at/after newest key does not warn
