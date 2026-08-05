# Account recovery 07 — Root user bootstrap (`id = 1`)

## Status

Proposed.

## Depends on

[01](01_key_export.md) (`.sxi.gpg` identity backup shape), [04](04_spa_keys_only_restore.md)
(keys-only `/import` fork). Server bootstrap can land before 04.

## Context

Closed / invite-only communities still need a first human operator account.
Today the practical path is briefly opening `SIGNUP_MODE=open`, signing up,
then locking the door — which invites race windows and never yields a
stable, recognizable “this is the root account” identity.

Account recovery already teaches the SPA to install a session from **keys
only** while the server already holds the profile. On a **fresh empty
database**, the server must mint one reserved root account, write a
**`.sxi.gpg`** identity backup to disk, and **never persist the root private
key**. The admin imports that file via keys-only `/import` without opening
signup for everyone.

## Scope

- On first healthy boot (`SELECT COUNT(*) FROM users = 0`, not
  `RECOVERY_MODE`), **require** a file-export passphrase and run a
  **one-shot root bootstrap**: mint `id = "1"`, countersign profile + key
  (same trust path as signup), write identity `.sxi.gpg`, discard private
  material, **exit** (do not serve HTTP on that boot).
- Export artifact **must** match [01](01_key_export.md) — same encrypted
  `BackupPayload` shape the SPA produces from Backup Keys (identity subset,
  gzip + OpenPGP). Filename `syrinx-1-<timestamp>.sxi.gpg`.
- Reserve `id = "1"` forever: `GenerateUserID` / signup must never mint or
  accept it for a normal account.
- SPA: treat `user.id === "1"` as **root** in the UI (stable badge / chrome —
  not spoofable by username).

## Non-goals

- Opening `SIGNUP_MODE` or special-casing invite consume for the first user.
- Multi-root / transfer of `id = "1"` to another person (root is permanent).
- Escrowing arbitrary user private keys (only this one bootstrap key).
- Auto-installing root into a browser; export + import stay operator-driven.
- Changing random user-id generation for everyone else (`ids.New()` stays).
- Filling root with reeds, follows, or invites at mint time (empty account).
- A separate `ops export-root-key` command or staged private key on the
  server (bootstrap is startup-only, one shot).

## Design

### Reserved identity

| Field | Value |
|-------|--------|
| `users.id` | `"1"` (string PK; not a random `ids.New()` value) |
| Default username | `root` (operator may rename later via normal signed profile update) |
| Bio / avatar | empty |
| Reeds / follows | none |
| `invitedBy` | null (omitted from profile payload) |

`GenerateUserID` and signup must **reject** `userID == "1"` so nobody else
can squat the reserved id. Document `"1"` as reserved in `ids` / signup
validation.

### Startup decision table

After `InitDB` and server identity are ready, **before** binding HTTP:

| `RECOVERY_MODE` | Users | `ROOT_KEY_EXPORT_PASSPHRASE` | Outcome |
|-----------------|-------|------------------------------|---------|
| on | any | any | Skip root bootstrap; normal recovery boot |
| off | ≥ 1 | set | **Panic** — remove env var; root already exists |
| off | 0 | unset, no TTY | **Panic** — empty DB requires bootstrap passphrase |
| off | 0 | unset, TTY | Prompt for file passphrase (confirm); bootstrap → exit |
| off | 0 | set | Bootstrap with env passphrase → exit |
| off | ≥ 1 | unset | Normal server start |

**Empty database without a passphrase is a fatal configuration error.** A
Syrinx instance cannot run with zero users; root creation is mandatory and
explicit.

### One-shot bootstrap (empty DB, passphrase available)

When `RECOVERY_MODE` is off and the user table is empty:

1. Resolve the **file passphrase** from `ROOT_KEY_EXPORT_PASSPHRASE` or an
   interactive prompt (confirm; reject empty).
2. Generate an OpenPGP key pair whose identity embeds `1@<serverID>` (same
   convention as client signup).
3. Use the **same string** as the private-key unlock passphrase when
   armoring the key (automation-friendly; import may still ask for file +
   unlock separately — operator enters the same value twice).
4. In one transaction:
   - Insert `user_keys` + server countersignature (same attestation as
     signup / `AddPublicKey`).
   - Insert `users` row `id = '1'` with user-signed profile + server
     countersignature, `user_fingerprint` = new key, `memberSince` =
     countersign timestamp.
5. Build the same identity `BackupPayload` the SPA uses
   (`buildKeyBackupPayload` equivalent: keys, profile, identity localStorage).
6. Gzip + symmetric encrypt with the file passphrase.
7. Write `syrinx-1-<timestamp>.sxi.gpg` to `ROOT_KEY_EXPORT_PATH` if set,
   else a sensible default under the process working directory (document for
   Docker: mount a writable volume).
8. Discard root private key and unlock secret from memory. **Do not** write
   private material to the database, filesystem, or keychain.
9. Log where the file was written and that the operator must secure it,
   **remove `ROOT_KEY_EXPORT_PASSPHRASE` from the environment**, and
   restart the server before serving traffic.
10. **Exit 0** — this boot does not start the HTTP server.

Second bootstrap attempt on an empty DB after a failed partial write is out
of scope for v1 (operator recreates the DB). Once `id = "1"` exists, bootstrap
does not run again.

### Environment variables

| Variable | Required | Meaning |
|----------|----------|---------|
| `ROOT_KEY_EXPORT_PASSPHRASE` | For non-interactive first boot | Encrypts the `.sxi.gpg` file. **Must be unset after bootstrap succeeds.** If still set when user `"1"` exists → panic. |
| `ROOT_KEY_EXPORT_PATH` | No | Output file path; default `syrinx-1-<timestamp>.sxi.gpg` in cwd |

Do not reuse `SERVER_KEY_PASSPHRASE`. Document in `.env.example` (commented)
and [`docs/operators.md`](../../docs/operators.md).

### Admin recover loop

1. Operator prepares empty DB + env (or interactive shell), starts server
   once → bootstrap writes `.sxi.gpg` and exits.
2. Operator secures the file offline, **removes** `ROOT_KEY_EXPORT_PASSPHRASE`,
   restarts server → normal HTTP, root `id = 1` exists (empty profile aside
   from default `root` username).
3. Operator opens SPA → “Already a user” → `/import` with that
   file ([04](04_spa_keys_only_restore.md) when fork exists; today the
   standard import path accepts `.sxi.gpg`).
4. Challenge / bootstrap / session install as for any account recovery
   (profile fetched from server, not from the file).
5. Operator updates profile / issues invites under `SIGNUP_MODE=invite|closed`.

No signup open required.

### UI: root signal

Anywhere a username / avatar chrome is shown for `user.id === "1"` (profile
card, reed author row, feeds — product judgment on density):

- Show a durable **root** affordance keyed off **id**, never username
  (renaming to `root` must not grant the badge; renaming away from `root`
  must not remove it).
- Optional: copy that this account was minted by the server at bootstrap.

Do not special-case trust / verification — root is still verified like any
signed profile; the badge is UX only.

### Trust / security notes

- Root private key **never persists** on the server — only the public key,
  countersigned profile, and operator-held `.sxi.gpg`.
- Losing the `.sxi.gpg` (or its file passphrase) without a later full
  backup loses admin control of `id = 1` the same way any user loses an
  account without keys — there is no second mint.
- Leaving `ROOT_KEY_EXPORT_PASSPHRASE` set after bootstrap is a
  misconfiguration; the server refuses to start (panic) until it is removed.
- Server signing key ≠ root user key. Bootstrap never dumps server identity
  material (that remains `ops export-identity`).

## Test plan

- [ ] Empty DB + no passphrase + no TTY → panic; no HTTP listener.
- [ ] Empty DB + env passphrase → user `id = 1` + attested profile; `.sxi.gpg`
      on disk; no private key in DB; process exits 0.
- [ ] Second start with user `1` present + env still set → panic.
- [ ] Second start with user `1` present + env unset → normal server.
- [ ] `RECOVERY_MODE` boot does **not** run root bootstrap.
- [ ] `GenerateUserID` / signup never returns or accepts `"1"`.
- [ ] Written `.sxi.gpg` decrypts to identity backup payload matching 01.
- [ ] Keys-only import of that file against the same `serverID` installs
      session as user `1` (04).
- [ ] SPA shows root affordance iff `id === "1"`.
- [ ] `SIGNUP_MODE=closed` still allows the recover loop (no open signup).

## Done when

- Operators can stand up a closed server on an empty DB (passphrase via env
  or prompt), receive a one-time `.sxi.gpg`, restart without the env var,
  and recover into `id = 1` via keys-only import — without opening signup
  — and the UI can reliably mark that account as root by id.
