# Invites

Invites are how closed communities grow without email verification, phone numbers, or a central approval queue. A signed-in member creates a **single-use link** and shares it; whoever opens that link can register on an invite-gated server. After a successful signup, the new account carries a countersigned **`invitedBy`** binding—the durable record of who brought them in. The row in the `invites` table is just operational state (pending, claimed, or revoked).

For signup modes and recovery, see [Identity, invites & recovery](/identity). This page walks through how invites work and why the redeem secret never lives on the server.

## Who can sign up

Operators set `SIGNUP_MODE`:

| Mode     | Sign up without a link | Redeem an invite |
|----------|------------------------|------------------|
| `open`   | Yes                    | Optional; sets `invitedBy` when present |
| `invite` | No (home CTA hidden)   | Required |
| `closed` | No                     | No new signups |

This table assumes the server is not in `RECOVERY_MODE`. **`RECOVERY_MODE` overrides every row above**: no new signups and no invite redemption at signup time, regardless of `SIGNUP_MODE`, until recovery ends — see [Identity, invites & recovery](/identity).

`MAX_INVITES_PER_USER` limits how many invites each user may create over the lifetime of their account (used and revoked invites count toward the cap). `/api/server/info` exposes the mode and quota so the client can show the right buttons without guessing.

The **first account** on a fresh instance is created while the server is in `open` mode; the operator then switches to `invite` or `closed`.

## How an invite travels

```mermaid
sequenceDiagram
  participant Inviter
  participant Browser
  participant Server
  participant Invitee
  Inviter->>Browser: Create invite
  Browser->>Browser: Mint id + secret; hash secret
  Browser->>Server: POST /api/invites (id, tokenHash, signature)
  Server-->>Browser: Countersignature; store hash only
  Browser->>Browser: Keep secret in IndexedDB
  Inviter->>Invitee: Share link (id in query, secret in fragment)
  Invitee->>Browser: Open /signup?iid=id&uid=creator#secret
  Browser->>Server: GET /api/invites/check
  Invitee->>Browser: Complete PGP signup
  Browser->>Server: POST /api/users/signup (inviteID + inviteSecret)
  Server->>Server: Validate, create user, mark invite claimed
```

### Create

1. The inviter’s browser mints an **id** (same format as user ids) and a **secret** (32 random bytes, URL-safe base64).
2. It computes `tokenHash = SHA-256(secret)` and signs `invite-user` over `serverID`, `userID`, `inviteID`, `tokenHash`, and `createdAt`.
3. `POST /api/invites` sends the id, hash, timestamp, and signature — **not** the secret.
4. The server verifies the signature, enforces quota and signup mode, stores `token_hash`, and countersigns `invite-server`.
5. The browser saves the full record (including the secret) to **IndexedDB**. There is no server list API.

### Share

1. The inviter copies a link: `{origin}/signup?iid={id}&uid={creatorId}#{secret}`.
2. The **id** is in the query string; the **secret** is in the fragment (`#…`).
3. Browsers do not send fragments on navigation, so the secret does not appear in normal HTTP access logs.

### Check (preflight)

When the signup page loads with both `?invite=` and `#secret` present:

1. The client calls `GET /api/invites/check?uid=&iid=&secret=`.
2. The server hashes the secret, loads the row, and returns `{ valid: true }` only if the invite is **pending** and the id matches.
3. Claimed and revoked invites return `valid: false` with no further detail.

This is a **preflight** so the UI can reject a bad link before key generation. **Signup is authoritative** — it performs the same validation again. If the check fails to reach the server, signup can still run; an invalid invite is rejected there.

### Redeem (signup)

The signup request includes `inviteID`, `inviteCreatorID` (from query), and `inviteSecret` (fragment), plus the usual identity fields. The server then:

1. Hashes the secret and loads the invite by composite primary key `(created_by, id)` plus `token_hash`.
2. Rejects if the row is missing, not pending, or the id does not match.
3. Inserts the new user with `invited_by` set to the inviter.
4. Countersigns the profile, including an `invitedBy` header.
5. Marks the invite claimed.

All of the above succeeds or fails together — if signup fails, the invite stays pending. Two concurrent redeems of the same link race; only one can claim it.

### Revoke

1. The inviter calls `DELETE /api/invites/{id}` on an unused invite.
2. Claimed invites cannot be revoked.
3. The hash row stays in the database, but check and redeem treat it as invalid.

## Why the secret stays on the client

At create time the server only needs to record that a member opened a redeem slot and to countersign that fact. It never receives the secret itself.

- Create requests carry only the hash, so observers of mint-time traffic learn nothing redeemable.
- The inviter chooses how to share the link; the server is not an invite inbox.
- Pending invites live on the inviter’s device. Status refresh uses `GET /api/invites/{id}`; the secret stays local until the invite is claimed or revoked.

What the server keeps is `token_hash`, the creator, and claim/revoke timestamps — not the secret, and not anything reversible.

## Why we store a hash

The `invites` table stores `SHA-256(secret)` in a unique `token_hash` column. Redeem and check both hash the presented secret and look up that value.

| Reason                        | Impact                                                                                                             |
|-------------------------------|--------------------------------------------------------------------------------------------------------------------|
| **One-way**                   | A database dump has no usable invite links; recovering the secret from the hash is infeasible for a 256-bit token. |
| **Lookup without storage**    | The server can confirm a match without ever having stored the secret.                                              |
| **Same algorithm everywhere** | Web Crypto on the client, Go on the server — fixed length, easy to index.                                          |

The signed create payload includes `tokenHash` so the inviter’s attestation binds to a specific slot, without putting the redeem secret in signed bytes that might be logged or replicated.

## What a server attacker cannot do

Assume read access to Postgres (or backups), but no access to the inviter’s browser and no valid invite link:

- The attacker sees hashes, creators, and claim state — not pending secrets.
- They cannot recover a secret from `token_hash`.
- They cannot redeem a pending invite by tampering with the database alone.
- They cannot create an invite as another user without that user’s signing key.

Rows on the server are redeem **slots**; the capability is whoever holds the link. Write access is a separate threat (revoke, outage); hash-only storage addresses read-only compromise.

## What is signed and what is not

| Piece                                           | Signed?                | Role                                                            |
|-------------------------------------------------|------------------------|-----------------------------------------------------------------|
| Invite create (`invite-user` / `invite-server`) | Yes                    | Proves which member minted which id, when, bound to `tokenHash` |
| Redeem secret                                   | No                     | Opaque capability—possession plus hash lookup is the gate       |
| `invitedBy` on the new user                     | Server identity header | Durable social fact after signup                                |

Invite redeem tokens are not canonical signed envelopes like reeds or removal certificates. What lasts is `users.invited_by` inside the countersigned identity record. That binding survives even if the `invites` table is rebuilt during recovery.

## On the inviter’s device

- Inviters keep invite records in **IndexedDB**, including the secret while status is `pending`.
- Once an invite is claimed or revoked, the client drops the secret.
- On load, the SPA refreshes **pending** rows from the server; finished rows are left as-is.

## In the UI

- Home **Sign Up** — visible only when `signupMode` is `open` **and** the server is not in `RECOVERY_MODE`.
- Invite link — `/signup?iid={id}&uid={creatorId}#{secret}`.
- Toolbar **Invites** — always visible; create disabled when mode is `closed` or quota is exhausted.
- Profile **Invited by** — shown when `invitedBy` is set (signed binding is the inviter’s user id; username is joined at read time).

Restoring from a backup is not signup and works in every mode.

## Related

- [Trust model — Invites](/trust#invites) — threat framing
- [Identity, invites & recovery](/identity) — modes, `invitedBy`, recovery
- [Operators](/operators) — `SIGNUP_MODE`, `MAX_INVITES_PER_USER`
