# Recovery 00 — Server key passphrase via OS keychain

## Status

Implemented.

## Depends on

— (lands before key-bundle export/import; normal boot benefits immediately)

## Context

Today the server private-key passphrase comes only from the
`SERVER_KEY_PASSPHRASE` environment variable (required, ≥16 characters). That
pushes a long-lived secret into `.env` files and encourages copying it into
default local configs.

Prefer the **OS user keychain / secret store** for single-host / long-running
operators: on start (and in `ops` commands that need the passphrase), if the
env var is unset or empty, look up the keychain; if that misses, **prompt once**
(hidden input). A non-empty answer must be ≥16 characters; an **empty** answer
**auto-generates** a 24-character passphrase, **prints it to stdout** (so the
operator can save it), and stores it in the keychain for later boots.

Keep `SERVER_KEY_PASSPHRASE` as an **optional HA escape hatch** (inject via
orchestrator / sealed secret into the process environment). Do **not** document
it in `.env.example` — local and tutorial setups should use prompt + keychain
(or empty-prompt auto-generation).

The passphrase still wraps `private_keys.armor` exactly as today; only the
delivery mechanism changes.

## Scope

- Keep parsing `SERVER_KEY_PASSPHRASE` in config, but treat empty / unset as
  “not provided” (no longer a hard required env at process start).
- **Remove** `SERVER_KEY_PASSPHRASE` from `.env.example` (and any “you must set
  this” onboarding copy). HA operators discover it from this proposal / ops
  help / deployment docs if needed.
- Add a small helper under `syrinx/secret` (used by `main` and later
  `cmd/ops`) that resolves the passphrase in order:
  1. **Env** — if `SERVER_KEY_PASSPHRASE` is non-empty, use it (do **not** write
     it into the keychain).
  2. **Keychain** — look up the fixed service/account key; if found, use it.
  3. **Prompt** — if stdin is a TTY, prompt (hidden):
     - non-empty → enforce ≥16 characters; store in keychain
     - empty → auto-generate a 24-character passphrase, print it to **stdout**,
       store in keychain
     Return the value for the process lifetime.
  4. If not a TTY and neither env nor keychain provided a value → fail closed.
- Stable service/account identity (e.g. service `syrinx`, account
  `server-key-passphrase`, optionally scoped by `SERVER_NAME`).
- Wire `main` boot and `InitServerKey` through the helper.
- Wire `ops` (`export-identity`, `import-identity`, `rotate-passphrase`) through
  the same helper.
- `rotate-passphrase`: after successfully re-wrapping keys, **update** the
  keychain entry when the process is using keychain-backed resolution; if the
  operator relies on env for HA, they must update the injected secret themselves
  (remind in command output). Also remind to re-export the identity bundle.
- Prefer a maintained cross-platform library (e.g. `zalando/go-keyring` or
  equivalent) covering macOS Keychain, Windows Credential Manager, and Linux
  Secret Service / libsecret where available.

## Non-goals

- No change to how OpenPGP armors are encrypted (still passphrase-based).
- No storage of the **bundle password** (identity file) in the keychain —
  that remains prompt-only on export/import and is never persisted by Syrinx.
- No full HA secret distribution product (Vault plugins, etc.) — env injection
  is enough of an escape hatch for this cut.
- Do not advertise the env var in `.env.example` or default local setup docs.

## Design

### Resolution order

```
non-empty SERVER_KEY_PASSPHRASE?
  → use env (HA path; skip keychain write)
else keychain hit?
  → use keychain
else TTY?
  → prompt (hidden)
       non-empty (≥16)? → store in keychain → use
       empty? → generate 24-char → print to stdout → store in keychain → use
else
  → fail closed
```

The passphrase lives in process memory after resolve; it is never written to
the DB, log lines, or the identity bundle JSON (private armors remain
ciphertext). The one intentional print is the auto-generated passphrase on
stdout when the operator accepts an empty prompt — that is the only chance to
copy it before it lives solely in the keychain.

Generated passphrases are 24 characters from a URL-/shell-safe alphabet
(`A–Z`, `a–z`, `0–9`, `-`, `_`), drawn via `crypto/rand`.

### Relation to the identity bundle

| Secret | Where it lives |
|--------|----------------|
| Server key passphrase | Env (HA) **or** OS keychain after first prompt / auto-generate; unwraps `privateKeyArmor` |
| Bundle password | Operator memory / password manager; encrypts the `.sxi.gpg` file; never in keychain |

`ops import-identity` still needs the server key passphrase (env / keychain /
prompt) to `ValidateDecrypt` after the bundle password decrypts the file.

### Deployment shapes

- **Single-host / long-running:** leave env unset; first interactive boot or
  `ops` run prompts (or empty → auto-generate + print) and fills the keychain;
  later restarts are unattended.
- **HA / headless:** set `SERVER_KEY_PASSPHRASE` via the orchestrator’s secret
  mechanism on each replica. No TTY required. Not listed in `.env.example`.

## Test plan

- [ ] Env unset + empty keychain + TTY → prompt with passphrase → key stored → `InitServerKey` works
- [ ] Env unset + empty keychain + TTY → empty prompt → 24-char passphrase printed to stdout → stored in keychain
- [ ] Second boot with env still unset → no prompt; key loaded from keychain
- [ ] Non-empty `SERVER_KEY_PASSPHRASE` → used; keychain not written
- [ ] Env unset + empty keychain + non-TTY → fail closed; no hang
- [ ] `.env.example` does not mention `SERVER_KEY_PASSPHRASE`
- [ ] Wrong passphrase → decrypt failure is explicit (same class of error as today)
- [ ] `ops rotate-passphrase` updates keychain when not using env; subsequent boot works
- [ ] Bundle password path unchanged (still not stored)

## Depends on this (downstream)

Recovery steps [01](01_key_bundle_export_ops_cli.md) and
[02](02_key_bundle_import_ops_cli.md) should resolve the server key passphrase
via this helper.
