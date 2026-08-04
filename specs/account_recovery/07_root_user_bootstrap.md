# Account recovery 07 — Root user bootstrap (`id = 1`)

## Status

Proposed.

## Depends on

[01](01_key_export.md) (`.sxk.gpg` shape), [04](04_spa_keys_only_restore.md)
(keys-only `/import` consumes the exported artifact). Server minting can
land before 04; operators cannot finish the loop until 04 exists.

## Context

Closed / invite-only communities still need a first human operator account.
Today the practical path is briefly opening `SIGNUP_MODE=open`, signing up,
then locking the door — which invites race windows and never yields a
stable, recognizable “this is the root account” identity.

Account recovery already teaches the SPA to install a session from **keys
only** while the server already holds the profile. If the server **mints**
one reserved account at bootstrap and operators **download** that key
pair (ops CLI), the admin can “recover” into an empty root user without
opening signup for everyone.

## Scope

- On first healthy boot (no `users` row yet, not `RECOVERY_MODE`),
  automatically create a **root** user with **`id = "1"`**, a server-generated
  OpenPGP key pair, and a fully countersigned identity record (same trust
  path as signup).
- Hold the root **private** key only long enough for operators to export it;
  expose export via **`ops`** (same binary family as identity bundle export).
- Export artifact **must** be a valid **`.sxk.gpg`** (version 1 plaintext from
  [01](01_key_export.md)) so `/import` keys-only ([04](04_spa_keys_only_restore.md))
  works unchanged.
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

### Boot mint (server)

When `InitDB` / identity boot completes successfully and **all** of:

- `RECOVERY_MODE` is off,
- `SELECT COUNT(*) FROM users` is `0`,
- no root private material already staged,

then mint root in one transaction:

1. Generate an OpenPGP key pair whose identity embeds `1@<serverID>` (same
   convention as client signup).
2. Choose an unlock passphrase for the private key (cryptographically
   random, high entropy — **not** the server key passphrase). Persist it
   only alongside the staged private armor for ops export (see below).
3. Insert `user_keys` + countersignature (same attestation as
   `AddPublicKey` / signup).
4. Insert `users` row `id = '1'` with signed profile (user signature over
   identity payload + server countersignature), `user_fingerprint` = new
   key, `memberSince` = countersign timestamp.
5. Stage private armor + unlock passphrase for ops (table or keychain —
   pick one in implementation; must not be world-readable on disk).

If users already exist, **do nothing** (no second root, no overwrite).

Log a clear **operator warning** until root private material has been
exported at least once (e.g. `root_key_exported_at` set).

### Ops: export root key

Add to `ops` (`//go:build ops`), e.g.:

```text
ops export-root-key [outfile]
```

Behavior:

- Load staged root private armor + unlock passphrase + fingerprint /
  `serverID`.
- Build plaintext JSON matching [01](01_key_export.md) (`version`,
  `exportedAt`, `userID: "1"`, `serverID`, `fingerprint`,
  `privateKeyArmor`).
- Prompt for a **file passphrase** (confirm; reject empty; same weakness
  warning style as identity-bundle export).
- Write `syrinx-1-<timestamp>.sxk.gpg` (or `[outfile]`).
- On success: set `root_key_exported_at`, then **wipe staged private armor
  and unlock passphrase** from the server (public key + profile remain).
- Second export without staged material → hard error: “root key already
  exported / not available; restore from the operator’s `.sxk.gpg` only”.

Makefile target e.g. `make export-root-key`. Document in ops help and
[`docs/operators.md`](../../docs/operators.md): prefer
`SIGNUP_MODE=closed|invite` from day one; bootstrap root via export +
keys-only import instead of briefly opening signup.

### Admin recover loop

1. Fresh server boots → root `id = 1` exists (empty).
2. Operator: `ops export-root-key` → secure the `.sxk.gpg` offline.
3. Operator opens SPA → “Already a user” → keys-only `/import` with that
   file ([04](04_spa_keys_only_restore.md)).
4. Challenge / bootstrap / session install as for any account recovery.
5. Profile is empty aside from default `root` username; operator updates
   profile / invites others under invite mode.

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

- Root private key **exists on the server only until first successful
  export**. Treat pre-export hosts as holding bootstrap escrow; rotate
  host access accordingly.
- Losing the `.sxk.gpg` (or its file passphrase) without a later full
  backup loses admin control of `id = 1` the same way any user loses an
  account without keys — there is no second mint.
- Server signing key ≠ root user key. Export-root never dumps server
  identity material (that remains `export-identity`).

## Test plan

- [ ] Empty DB boot creates exactly one user `id = 1` with attested key +
      profile; second boot does not duplicate.
- [ ] `RECOVERY_MODE` boot does **not** mint root.
- [ ] `GenerateUserID` / signup never returns or accepts `"1"`.
- [ ] `ops export-root-key` produces decryptable `.sxk.gpg` matching 01;
      wipes staged private material; second export fails closed.
- [ ] Keys-only import of that file against the same `serverID` installs
      session as user `1` (04).
- [ ] SPA shows root affordance iff `id === "1"`.
- [ ] `SIGNUP_MODE=closed` still allows the recover loop (no open signup).

## Done when

- Operators can stand up a closed server, export root once, and recover
  into `id = 1` via existing account-recovery import — without opening
  signup — and the UI can reliably mark that account as root by id.
