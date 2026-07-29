# Identity, invites & recovery

## Identities

User ids are **random and server-scoped**—not guessable counters. Profiles, keys, and related records are **user-signed** and **server-countersigned** so peers can verify both authorship and “this instance witnessed it.”

Username collisions during recovery are resolved by newest server-signed timestamp; losers may be renamed in place. Comfort notifications for that case are a deferred nicety, not a blocker for recovery itself.

## Signup modes

Operators set `SIGNUP_MODE`:

| Value    | Behavior                              |
|----------|---------------------------------------|
| `open`   | Anyone may sign up                    |
| `invite` | Valid invite required                 |
| `closed` | Signup and username checks return 403 |

`MAX_INVITES_PER_USER` caps how many invites a user may mint (`-1` / unset = infinite). `/api/server/info` exposes mode and quota so the SPA can hide Sign Up and disable minting without guessing.





## Auth day-to-day

After signup or restore, the client holds key material locally. Authenticated HTTP and WebSocket use **signatures**, not a session password checked like a website login form. See [Trust](/trust) and [Cryptography](/cryptography).

## Unified restore (import ⊂ recovery)

From the user’s point of view, “I have a backup” and “the server forgot me during recovery” feel almost the same: both start by bringing data onto the device. Recovery is a **superset**—it also reports evidence back to the server.

The client therefore offers **one restore path**:

1. Pick an encrypted backup and its password.
2. Ask `POST /api/users/status` with the countersigned profile: “do you know this user?”
3. Branch automatically:
   - Server knows them → write locally, normal session (import only).
   - Server doesn’t, but recovery mode is on → write locally, **claim** identity and report peers/holdings.
   - Ongoing recovery → resume.
   - Unknown and not recovering → do not write (avoid trapping the user in a broken state).

Users are not asked to pick “Import” vs “Recover.”

## Server recovery (hostile takeover / DB loss)

When an instance must be rebuilt:

### Operator

1. Stand up a new empty database.
2. Ensure the **server key passphrase** is available (keychain or env)—same passphrase that wrapped keys in the backup bundle.
3. Run **`ops import-identity`** on the encrypted identity bundle (`.sxi.gpg`). Bundle password decrypts the file; server passphrase unwraps keys. Restores `serverID`, name, and key history.
4. Start the server with **`RECOVERY_MODE`**. Without a prior successful identity import, boot should refuse.

### Clients

While recovery mode is on:

1. **Own-identity claim** — prove possession of the key chain the old server countersigned (challenge + nested keys).
2. **Peer identities** — report profiles/keys held locally, one at a time; server upserts with newest-wins on server-signed time.
3. **Reeds & follows** — report holdings and follow edges; mark complete.
4. **Import gate** — until complete, the user is barred from normal API use (server middleware + client mirror) so half-restored accounts don’t act like finished logins.

The server never learns private keys. It reconstructs **public, signed** state from the community.

Operators end the window by turning `RECOVERY_MODE` off when the community has reported in sufficiently.

## Device binding

Until multi-device sync exists, concurrent devices can fork history. Device binding aims for **one active device** per user: migrate by binding a new device and retiring the old (wipe app data on the old device—Syrinx does not remotely brick phones). Device ids are **not** part of the signed public profile; they are session metadata, not identity.

## What users should know

- Install / use the PWA thoughtfully: browser extensions can read page data; migrating between “loose tab” and installed app can strand data under browser storage rules.
- Export backups before you need them.
- Invite links are how closed communities grow without harvesting emails.
