# Recovery 02 — Key bundle import (`ops` CLI)

## Status

Proposed.

## Depends on

[00](00_server_key_passphrase_keychain.md),
[01](01_key_bundle_export_ops_cli.md)

## Context

After a wipe, the operator restores identity with **`ops import-identity`**
before starting the server in recovery mode. This CLI is the **only** path that
decrypts a bundle and prompts for the **bundle password** — the long-running
server never does. The **server key passphrase** is resolved via the keychain
helper (proposal 00). See [README](README.md) *Phase 0* and *Key bundle*.

## Scope

- Reuse step-01 encrypt/decrypt, `ValidateShape`, `ValidateDecrypt`.
- Add `ImportIntoDB` (and ensure-schema) in `syrinx/recovery`.
- `ops import-identity <infile>`:
  - **Prompt for bundle password**, decrypt → validate → `ValidateDecrypt`
    with the server key passphrase from the keychain helper.
  - **Ensure** identity tables exist (`servers`, `private_keys`, `public_keys`,
    `identity_backup_at`) — same definitions the server uses, or a shared
    migrate helper.
  - Populate self `servers` row + full key history; set `identity_backup_at`
    from `exportedAt`.
  - Then **prompt to delete `<infile>`** (clear yes/no; recommend deleting once
    the DB holds the identity).
  - Wrong password or validation failure → no DB writes, no delete prompt.
  - Self identity already exists and **matches** the bundle → success / no-op.
  - Self identity **mismatches** → abort with no writes.
- Makefile target `import-identity`; document in `.env.example` that identity
  restore is via `ops import-identity` (no `RECOVERY_KEY_BUNDLE`).

## Non-goals

- No `RECOVERY_MODE` server wiring (step [03](03_bookkeeping_and_gates.md)).
- No HTTP routes, no recovery bookkeeping tables.
- Do **not** store the bundle password anywhere.
- Do **not** delete the bundle without an affirmative operator answer.

## Design

Private armors are stored **verbatim** (still wrapped with the server key
passphrase). Server **name** is not in the bundle (`SERVER_NAME` at runtime).

**Why CLI (not server boot):** a long-running process cannot reliably prompt;
ops is interactive by design. After a successful import, deleting the file
reduces the chance of leaving decryptable ciphertext on the host.

## Test plan

- [ ] Empty DB + `import-identity` + correct passwords → tables + self row + key history
- [ ] Import sets `identity_backup_at` from bundle `exportedAt`
- [ ] After successful import, affirmative answer deletes the bundle file; decline leaves it
- [ ] Wrong bundle password → no DB writes, no delete
- [ ] Wrong server key passphrase after decrypt → fail closed
- [ ] Matching existing identity → idempotent success
- [ ] Mismatched existing identity → abort, DB unchanged
