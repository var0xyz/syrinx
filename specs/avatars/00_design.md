# Avatars 00 — Design + locked model

## Status

Proposed.

## Depends on

—

## Context

Product display uses a client-side identicon from user ID. The identity
wire and `users` row still carry `avatarURL` (often empty), and profile
update historically treated that field as a remote URL.

Custom avatars should travel as **device-friendly image bytes** with a
**hash in the signed profile**, processed and attested by the home server,
without putting image bytes into `BytesToSign`.

## Scope

- Lock upload → process → profile PUT → store/fetch/GC.
- Lock identity field `avatarHash`, `avatars` table, and APIs.
- Keep identicon as fallback when there is no usable custom avatar.

## Non-goals

- Peer/relay distribution of avatar bytes (hash-as-id is the hook; server
  `GET` is the distributor for now).
- Deduplicating identical PNGs across users (two users may store the same
  bytes under two `user_id` rows).
- Hosting arbitrary remote URLs or `http.Head` of user-supplied hosts.
- Putting `data:` URIs into the signed identity.

## Design

### Display

```
if profile.avatarHash and local (or fetched) PNG for that hash
  → show PNG (object URL / img)
else
  → GeneratedAvatar(userID)
```

### End-to-end flow (set or replace)

```mermaid
sequenceDiagram
  participant SPA
  participant Process as POST process
  participant Profile as PUT profile
  participant DB

  SPA->>SPA: crop square (client)
  SPA->>Process: image bytes
  Process->>Process: reject if non-square; 256×256; 256 colors; PNG
  Process-->>SPA: optimized PNG + server attestation
  SPA->>SPA: avatarHash = SHA-256(optimized PNG)
  SPA->>SPA: sign profile (username, fingerprint, avatarHash, bio)
  SPA->>Profile: profile + userSignature + PNG + attestation
  Profile->>Profile: verify user sig; SHA-256(PNG)==hash; verify attestation
  Profile->>DB: same TX: identity + upsert avatars(user_id)
  Profile-->>SPA: countersigned profile
```

Process is **stateless**: no avatar row until profile PUT succeeds. The
client holds optimized bytes + attestation in memory until PUT.

### Process attestation

Server detached signature over a canonical `BytesToSign` payload that
includes at least:

- `userID` — authenticated caller (bound into the attestation)
- `hash` — hex SHA-256 of the **optimized** PNG bytes returned

(Exact header `type` and any `signedAt` are fixed in [02](02_process_api.md).)

The **user does not sign the image**. The user signs the profile, which
includes `avatarHash`. On PUT the server checks the process attestation
so the PNG must have been minted for this user and this hash.

### Profile PUT cases

| Intent | `avatarHash` in signed profile | Image body | Server |
|--------|--------------------------------|------------|--------|
| Set / replace | New hash | Optimized PNG + attestation | Verify hash==SHA-256(PNG), verify attestation for `(userID, hash)`, upsert `avatars` in same TX as identity |
| Unchanged | Same as stored | Omitted | Require hash == stored hash; leave `avatars` row |
| Clear | Empty (omit from signed bytes) | Null / absent | Delete `avatars` for `user_id` in same TX |

Reject mixed cases (non-empty hash without bytes when hash changed; empty
hash with bytes; bytes whose digest ≠ profile hash; attestation that does
not match caller + hash).

Username/bio-only edits use the **unchanged** row: resend current
`avatarHash`, do not resend the image.

### Storage

```text
users          — identity fields include avatarHash (no avatar_url)
avatars        — user_id PK, hash (indexed), png BYTEA
```

One active avatar per user. Replace overwrites the row; previous hash
becomes unreachable via `GET /avatars/<oldHash>` (404). Clients treat
404 as missing → identicon / drop stale local blob.

### Fetch + client cache

- Opening a profile always loads the **latest** signed profile from the
  server (existing SPA behavior). That response carries `avatarHash`.
- Then: if `avatarHash` is empty → identicon; if IndexedDB already has
  that hash → use it; else `GET /avatars/<hash>`, store under
  `(userId, hash)`, delete other local rows for that `userId`.
- A failed or aborted avatar fetch is not retried in a loop on the same
  view: show identicon for now. The next profile open fetches the profile
  again and re-runs the cache check, so the avatar request is retried
  naturally.
- `GET /avatars/<hash>` returns the PNG (auth policy locked in
  [04](04_fetch_api.md)). Old hash after replace → 404 → identicon /
  drop stale local blob.
- Ignore in-flight fetches whose hash ≠ the profile’s current hash.

### Upload hygiene (process only)

- Authenticated.
- Max upload bytes / max decoded dimensions before accept.
- Reject non-square.
- Output: 256×256, ≤256-color paletted PNG, with a max encoded size
  (reject if still too large).

### Identity canonical form

Replace wire/DB `avatarURL` with `avatarHash` in
`BuildUserIdentityPayload` / `BuildProfilePayload` (and SPA mirrors).
Empty hash is omitted from signed headers (same absent==empty rule as
other optional headers).

### Future peer distribution

`avatarHash` is the stable content id. A later step can teach clients to
request bytes from peers; this design does not require server `GET` to
remain the only path.
