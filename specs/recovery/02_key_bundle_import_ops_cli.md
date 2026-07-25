# Recovery 02 — Key bundle import (`ops` CLI)

## Status

Implemented.

## Depends on

[00](00_server_key_passphrase_keychain.md),
[01](01_key_bundle_export_ops_cli.md)

## Context

After a wipe, the operator restores identity with **`ops import-identity`**
**before** the server’s first boot (so the server never mints a conflicting
identity). This CLI is the **only** path that decrypts a bundle and prompts for
the **bundle password** — the long-running server never does. The **server key
passphrase** is resolved separately via the keychain helper (proposal 00); it is
**not** in the bundle. See [README](README.md) *Phase 0* and *Key bundle*.

## Scope

- Reuse step-01 encrypt/decrypt, `ValidateShape`, `ValidateDecrypt`.
- Add `ImportIntoDB` in `syrinx/recovery` (identity match + insert only).
- Operator CLI lives in root **`ops.go`** (`//go:build ops`), same `package main`
  as the server so it can call **`InitDB`** directly. Server entry is
  `main.go` with `//go:build !ops`. Build: `go build -tags ops -o bin/ops .`.
- `ops import-identity <infile>`:
  1. Open DB → **`InitDB`** (full schema, not identity-only DDL).
  2. **Prompt for bundle password**, decrypt → `ValidateShape`.
  3. Resolve **server key passphrase** (env / keychain / prompt) →
     `ValidateDecrypt` (private armors still wrapped; passphrase not in bundle).
  4. Populate self `servers` row + full key history; set `identity_backup_at`
     from `exportedAt`.
  5. Print that the operator should start the server with **`RECOVERY_MODE`**.
  6. Optionally prompt to delete `<infile>` (default no; file is encrypted so
     this is hygiene, not required for safety).
  - Wrong password or validation failure → no identity writes.
  - Self identity already exists and **matches** the bundle → success / no-op.
  - Self identity **mismatches** → abort with no writes (operator must act).
- **Match rule:** self `id`, `name`, and `signing_key` equal bundle
  `serverID`, `serverName`, and `signingKeyFingerprint`, and every
  `private_keys` row’s fingerprint + armor equals the bundle keys (same set).
- Makefile target `import-identity`; document in ops help (no
  `RECOVERY_KEY_BUNDLE`).

## Non-goals

- No `RECOVERY_MODE` server wiring (step [03](03_bookkeeping_and_gates.md)).
- No HTTP routes, no recovery bookkeeping tables.
- Do **not** store the bundle password or put the server key passphrase in the
  bundle.
- Do **not** delete the bundle without an affirmative operator answer.

## Design

Private armors are stored **verbatim** (still wrapped with the server key
passphrase). `serverID` and `serverName` come from the bundle (restored onto
the self `servers` row).

**Why CLI before first boot:** calling full `InitDB` from ops means the operator
does not need a sacrificial server start to create schema. Importing after a
normal first boot would conflict with a freshly minted identity.

**Why CLI (not server boot):** a long-running process cannot reliably prompt for
two secrets; ops is interactive by design.

## Test plan

- [ ] Empty DB + `import-identity` + correct passwords → full schema + self row + key history
- [ ] Import sets `identity_backup_at` from bundle `exportedAt`
- [ ] Success message reminds operator to enable `RECOVERY_MODE`
- [ ] Optional delete: yes removes file; decline / default leaves it
- [ ] Wrong bundle password → no identity writes
- [ ] Wrong server key passphrase after decrypt → fail closed
- [ ] Matching existing identity → idempotent success
- [ ] Mismatched existing identity → abort, DB identity unchanged
